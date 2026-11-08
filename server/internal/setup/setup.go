// SPDX-License-Identifier: AGPL-3.0-or-later

// Package setup implements the first-run wizard (brief §6.2).
//
// Crash safety: everything the wizard creates in the database (admin, Public
// group, settings, the setup_completed_at marker) is written in ONE
// transaction. config.json (which says where the DB is) is written BEFORE
// that transaction. A power cut at any point leaves either "not set up"
// (wizard runs again, reusing the same DB) or "fully set up".
package setup

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/config"
	"github.com/quip1/simple-apb/server/internal/mail"
	"github.com/quip1/simple-apb/server/internal/settings"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Error is a validation failure shown to the person running the wizard.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func invalid(code, msg string) *Error { return &Error{Code: code, Message: msg} }

// ErrAlreadyComplete is returned once the instance is set up.
var ErrAlreadyComplete = errors.New("setup already complete")

// Service runs the wizard. Token is printed to the log at startup.
type Service struct {
	Paths   config.Paths
	Secrets *config.Secrets
	Token   string
	Now     func() time.Time
}

// NewToken creates a random one-time setup token.
func NewToken() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// CheckToken compares in constant time.
func (s *Service) CheckToken(t string) bool {
	return t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.Token)) == 1
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Database is the DB choice.
type Database struct{ Dialect, DSN string }

func (s *Service) open(ctx context.Context, d Database) (*store.DB, error) {
	dialect, err := store.ParseDialect(d.Dialect)
	if err != nil {
		return nil, invalid("invalid_dialect", err.Error())
	}
	dsn := d.DSN
	if dialect == store.SQLite {
		if err := os.MkdirAll(s.Paths.Config, 0o750); err != nil {
			return nil, err
		}
		dsn = config.SQLitePath(s.Paths.Config)
	} else if dsn == "" {
		return nil, invalid("dsn_required", "a Postgres connection string is required")
	}
	return store.OpenDB(ctx, dialect, dsn)
}

// TestDatabase opens (and migrates) the chosen DB, then closes it.
func (s *Service) TestDatabase(ctx context.Context, d Database) error {
	db, err := s.open(ctx, d)
	if err != nil {
		return err
	}
	return db.Close()
}

// LibraryCheck is the result of TestLibrary.
type LibraryCheck struct {
	Exists, Writable, Empty bool
	Message                 string
}

// TestLibrary checks the library folder.
func TestLibrary(path string) LibraryCheck {
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		return LibraryCheck{Message: "folder does not exist"}
	}
	r := LibraryCheck{Exists: true}
	probe, err := os.CreateTemp(path, ".simpleapb-write-test-*")
	if err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
		r.Writable = true
	} else {
		r.Message = "folder is not writable by the server"
	}
	entries, _ := os.ReadDir(path)
	r.Empty = true
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			r.Empty = false
			break
		}
	}
	if r.Writable && r.Empty {
		r.Message = "folder is empty — that's fine for a new library"
	}
	return r
}

// TestSMTP sends a test email.
func TestSMTP(ctx context.Context, cfg settings.SMTP, to string) error {
	return mail.Send(ctx, cfg, to, "Simple APB test email", "If you can read this, email from your Simple APB server works.\n")
}

// NewAdminTOTP returns a secret for the first admin.
func NewAdminTOTP(instanceName, username string) (secret, uri string, err error) {
	if instanceName == "" {
		instanceName = "Simple APB"
	}
	return auth.NewTOTPSecret(instanceName, username)
}

// Request is the full wizard submission.
type Request struct {
	AdminUsername, AdminPassword, AdminDisplayName, AdminEmail string
	TOTPSecret, TOTPCode                                       string
	Database                                                   Database
	LibraryPath, BaseURL                                       string
	SMTP                                                       *settings.SMTP
	Ntfy                                                       settings.Ntfy
	Instance                                                   settings.Instance
	EgressMbps                                                 int
	IP                                                         string
}

// Result is returned on success.
type Result struct {
	DB            *store.DB
	AdminID       string
	RecoveryCodes []string
}

// Complete validates and applies the wizard. On success the returned DB is
// open and ready for the app; the caller owns it.
func (s *Service) Complete(ctx context.Context, r Request) (*Result, error) {
	if err := validate(&r); err != nil {
		return nil, err
	}
	totpStep, totpOK := auth.MatchTOTPStep(r.TOTPSecret, r.TOTPCode, s.now())
	if !totpOK {
		return nil, invalid("invalid_totp", "the authenticator code is wrong or expired")
	}
	if chk := TestLibrary(r.LibraryPath); !chk.Exists || !chk.Writable {
		return nil, invalid("library_unusable", "library path: "+chk.Message)
	}
	hash, err := auth.HashPassword(r.AdminPassword)
	if err != nil {
		return nil, err
	}
	totpEnc, err := s.Secrets.Encrypt(r.TOTPSecret)
	if err != nil {
		return nil, err
	}
	codes, codeHashes, err := auth.NewRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	if r.Ntfy.Token == "" {
		r.Ntfy.Token = "tk_" + NewToken()
	}

	db, err := s.open(ctx, r.Database)
	if err != nil {
		return nil, err
	}
	// config.json first: if we crash after the transaction, a restart reopens
	// this same DB and sees setup_completed_at, instead of starting over.
	boot := &config.Bootstrap{DBDialect: string(db.Dialect())}
	if db.Dialect() == store.Postgres {
		if boot.DBDSNEncrypted, err = s.Secrets.Encrypt(r.Database.DSN); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if err := config.SaveBootstrap(s.Paths.Config, boot); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("write config.json: %w", err)
	}

	var adminID string
	err = db.InTx(ctx, func(tx *store.Tx) error {
		st := settings.Store{Q: tx, Enc: s.Secrets}
		done, err := st.SetupCompleted(ctx)
		if err != nil {
			return err
		}
		if done {
			return ErrAlreadyComplete
		}
		adminID, err = store.CreateUser(ctx, tx, store.NewUser{
			Username: r.AdminUsername, DisplayName: r.AdminDisplayName, Email: r.AdminEmail, PasswordHash: hash,
			Status: "active", Role: "admin", TOTPSecretEnc: totpEnc, AgeConfirmed: true, TermsAccepted: true,
		})
		if errors.Is(err, store.ErrConflict) {
			return invalid("username_taken", "a user with that name already exists in this database")
		}
		if err != nil {
			return err
		}
		if err := store.ReplaceRecoveryCodes(ctx, tx, adminID, codeHashes); err != nil {
			return err
		}
		// The code used in the wizard can't be replayed for the first login.
		if _, err := store.ClaimTOTPStep(ctx, tx, adminID, totpStep); err != nil {
			return err
		}
		pub, err := store.EnsurePublicGroup(ctx, tx)
		if err != nil {
			return err
		}
		if err := store.AddGroupMember(ctx, tx, pub, adminID); err != nil {
			return err
		}
		r.Instance.BaseURL = r.BaseURL
		writes := []struct {
			key string
			val any
		}{
			{settings.KeyInstance, r.Instance},
			{settings.KeyLibrary, settings.Library{Path: r.LibraryPath}},
			{settings.KeyNtfy, r.Ntfy},
			{settings.KeyBandwidth, settings.Bandwidth{EgressMbps: r.EgressMbps}},
		}
		if r.SMTP != nil {
			writes = append(writes, struct {
				key string
				val any
			}{settings.KeySMTP, *r.SMTP})
		}
		for _, w := range writes {
			if err := st.Set(ctx, w.key, w.val); err != nil {
				return err
			}
		}
		if err := st.Set(ctx, settings.KeySetupDone, s.now().UnixMilli()); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: adminID, Action: "setup.complete", ObjectType: "instance",
			IP: r.IP, Details: map[string]any{"db": string(db.Dialect()), "base_url": r.BaseURL}})
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Result{DB: db, AdminID: adminID, RecoveryCodes: codes}, nil
}

func validate(r *Request) error {
	if p := auth.CheckPassword(r.AdminPassword, r.AdminUsername); p != auth.PasswordAcceptable {
		return invalid(string(p), "admin password does not meet the policy")
	}
	u, err := url.Parse(strings.TrimRight(r.BaseURL, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" && u.Path != "/" {
		return invalid("invalid_base_url", "base URL must look like https://books.example.com")
	}
	r.BaseURL = u.Scheme + "://" + u.Host
	if strings.TrimSpace(r.Instance.Name) == "" {
		return invalid("invalid_instance_name", "instance name is required")
	}
	if r.LibraryPath == "" {
		r.LibraryPath = "/library"
	}
	r.LibraryPath = filepath.Clean(r.LibraryPath)
	if r.EgressMbps <= 0 {
		r.EgressMbps = settings.DefaultBandwidth().EgressMbps
	}
	if r.SMTP != nil && r.SMTP.Host == "" {
		r.SMTP = nil
	}
	return nil
}

// LoadExisting opens the DB named by config.json, if the instance was set up
// (or a DB was chosen by a previous, interrupted wizard run).
func LoadExisting(ctx context.Context, paths config.Paths, secrets *config.Secrets) (*store.DB, bool, error) {
	boot, err := config.LoadBootstrap(paths.Config)
	if errors.Is(err, config.ErrNotConfigured) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	dialect, err := store.ParseDialect(boot.DBDialect)
	if err != nil {
		return nil, false, err
	}
	dsn := config.SQLitePath(paths.Config)
	if dialect == store.Postgres {
		if dsn, err = secrets.Decrypt(boot.DBDSNEncrypted); err != nil {
			return nil, false, fmt.Errorf("config.json database: %w", err)
		}
	}
	db, err := store.OpenDB(ctx, dialect, dsn)
	if err != nil {
		return nil, false, err
	}
	done, err := settings.Store{Q: db, Enc: secrets}.SetupCompleted(ctx)
	if err != nil {
		_ = db.Close()
		return nil, false, err
	}
	return db, done, nil
}
