// SPDX-License-Identifier: AGPL-3.0-or-later

package push

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/quip1/simple-apb/server/internal/settings"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/testdb"
)

type b64 struct{}

func (b64) Encrypt(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func (b64) Decrypt(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}

func TestValidateEndpoint(t *testing.T) {
	allowed := []string{"ntfy"}
	cases := map[string]bool{
		"https://ntfy.sh/upAbC123?up=1":  true,
		"http://ntfy:80/upXYZ":           true, // the instance's own ntfy, even on plain http
		"http://ntfy.sh/upAbC":           false,
		"https://127.0.0.1/x":            false,
		"https://10.0.0.5/x":             false,
		"https://[::1]/x":                false,
		"https://169.254.169.254/latest": false,
		"https://user:pw@ntfy.sh/x":      false,
		"ftp://ntfy.sh/x":                false,
		"not a url":                      false,
	}
	for raw, ok := range cases {
		err := ValidateEndpoint(raw, allowed)
		if (err == nil) != ok {
			t.Errorf("%s: err=%v, want ok=%v", raw, err, ok)
		}
	}
}

func TestSendToNtfyAndUnifiedPush(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		var (
			mu  sync.Mutex
			got = map[string]string{}
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			got[r.URL.Path] = string(b) + "|" + r.Header.Get("Authorization")
			mu.Unlock()
			if strings.Contains(r.URL.Path, "gone") {
				w.WriteHeader(http.StatusGone)
			}
		}))
		defer srv.Close()
		if err := (settings.Store{Q: db, Enc: b64{}}).Set(ctx, settings.KeyNtfy, settings.Ntfy{InternalURL: srv.URL, Token: "tk_secret"}); err != nil {
			t.Fatal(err)
		}
		admin, _ := store.CreateUser(ctx, db, store.NewUser{Username: "admin", Status: "active", Role: "admin"})
		alice, _ := store.CreateUser(ctx, db, store.NewUser{Username: "alice", Status: "active", Role: "user"})
		adminTopic, err := Register(ctx, db, admin, store.NewID(), "")
		if err != nil {
			t.Fatal(err)
		}
		if again, _ := Register(ctx, db, admin, mustDevice(t, db, admin), ""); again != adminTopic || len(adminTopic) != 36 {
			t.Fatalf("topic should be stable per device and 128-bit: %q %q", adminTopic, again)
		}
		dev := store.NewID()
		if _, err := Register(ctx, db, alice, dev, srv.URL+"/upAlice"); err != nil {
			t.Fatal(err)
		}
		_, _ = Register(ctx, db, alice, store.NewID(), srv.URL+"/gone")

		n := &Notifier{DB: db, Enc: b64{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTP: srv.Client()}
		res, err := n.Send(ctx, Recipients{Admins: true, Users: []string{alice}}, Event{Type: UploadDone, ID: "u1"})
		if err != nil || res.Sent != 2 || res.Removed != 1 {
			t.Fatalf("send: %+v %v", res, err)
		}
		var ev Event
		_ = json.Unmarshal([]byte(strings.Split(got["/"+adminTopic], "|")[0]), &ev)
		if ev.Type != UploadDone || ev.ID != "u1" || !strings.HasSuffix(got["/"+adminTopic], "|Bearer tk_secret") {
			t.Fatalf("ntfy publish: %q", got["/"+adminTopic])
		}
		if !strings.HasSuffix(got["/upAlice"], "|") {
			t.Fatalf("UnifiedPush endpoints must not get the ntfy token: %q", got["/upAlice"])
		}
		var subs int
		_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM push_subscriptions WHERE user_id = ?`, alice).Scan(&subs)
		if subs != 1 {
			t.Fatalf("gone endpoint not removed: %d", subs)
		}
		if err := Unregister(ctx, db, alice, dev); err != nil {
			t.Fatal(err)
		}
		if err := Unregister(ctx, db, alice, dev); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("double unregister should be not found")
		}
	})
}

func mustDevice(t *testing.T, db *store.DB, user string) string {
	var d string
	if err := db.QueryRow(context.Background(), `SELECT device_id FROM push_subscriptions WHERE user_id = ?`, user).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSafeDialerBlocksPrivateAddresses(t *testing.T) {
	db := testdb.SQLite(t)
	n := &Notifier{DB: db, Enc: b64{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	c := n.client(settings.Ntfy{})
	_, err := post(context.Background(), c, "https://localhost:1/x", "", nil)
	if err == nil || !strings.Contains(err.Error(), "push endpoint must be") {
		t.Fatalf("localhost should be refused at dial time: %v", err)
	}
}
