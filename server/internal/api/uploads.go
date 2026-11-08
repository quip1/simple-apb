// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tus/tusd/v2/pkg/filelocker"
	"github.com/tus/tusd/v2/pkg/filestore"
	tusd "github.com/tus/tusd/v2/pkg/handler"
	expslog "golang.org/x/exp/slog"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/library"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/version"
)

// MaxUploadBytes is a hard ceiling per upload, independent of quota.
const MaxUploadBytes = 32 << 30

var hexSHA256 = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// uploadsBase is where the tus endpoint lives.
var uploadsBase = "/api/" + version.APIVersion + "/uploads/"

// newUploadHandler builds the resumable (tus) upload endpoint. Uploads are
// staged in /library/.incoming so the final move into the library is an
// atomic same-filesystem rename.
func newUploadHandler(a *App, trustForwarded bool) (http.Handler, error) {
	dir := a.Lib.IncomingPath()
	composer := tusd.NewStoreComposer()
	filestore.New(dir).UseIn(composer)
	filelocker.New(dir).UseIn(composer)
	h, err := tusd.NewHandler(tusd.Config{
		BasePath:                uploadsBase,
		StoreComposer:           composer,
		MaxSize:                 MaxUploadBytes,
		DisableDownload:         true,
		RespectForwardedHeaders: trustForwarded,
		// tusd logs every request at INFO; keep only warnings and errors.
		Logger: expslog.New(expslog.NewJSONHandler(os.Stdout, &expslog.HandlerOptions{Level: expslog.LevelWarn})),
		PreUploadCreateCallback: func(hook tusd.HookEvent) (tusd.HTTPResponse, tusd.FileInfoChanges, error) {
			return a.preUploadCreate(hook)
		},
		PreFinishResponseCallback: func(hook tusd.HookEvent) (tusd.HTTPResponse, error) {
			return tusd.HTTPResponse{}, a.uploadFinished(hook)
		},
	})
	if err != nil {
		return nil, err
	}
	return a.guardUploads(http.StripPrefix(strings.TrimSuffix(uploadsBase, "/"), h)), nil
}

func tusErr(status int, code, msg string) error { return tusd.NewError(code, msg, status) }

func (a *App) preUploadCreate(hook tusd.HookEvent) (tusd.HTTPResponse, tusd.FileInfoChanges, error) {
	ctx := hook.Context
	p := authFrom(ctx).principal
	if p == nil || !p.Can(authz.Upload) {
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, tusErr(http.StatusForbidden, "forbidden", "you are not allowed to upload")
	}
	name := strings.TrimSpace(hook.Upload.MetaData["filename"])
	if name == "" || !library.UploadAccepts(name) {
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, tusErr(http.StatusUnsupportedMediaType, "unsupported_file_type",
			"upload an .m4b, .m4a, .mp3 or a .zip of one book's mp3 files")
	}
	if hook.Upload.SizeIsDeferred || hook.Upload.Size <= 0 {
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, tusErr(http.StatusBadRequest, "length_required", "Upload-Length is required")
	}
	if p.User.QuotaBytes > 0 {
		used, err := store.UsedBytes(ctx, a.DB, p.User.ID)
		if err != nil {
			return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, err
		}
		pending, err := store.PendingUploadBytes(ctx, a.DB, p.User.ID)
		if err != nil {
			return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, err
		}
		if used+pending+hook.Upload.Size > p.User.QuotaBytes {
			return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, tusErr(http.StatusRequestEntityTooLarge, "quota_exceeded",
				"this upload would exceed your storage quota")
		}
	}
	id, now := store.NewID(), store.NowMS()
	if _, err := a.DB.Exec(ctx, `INSERT INTO uploads (id, user_id, filename, size_bytes, received, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, 'receiving', ?, ?)`, id, p.User.ID, filepath.Base(name), hook.Upload.Size, now, now); err != nil {
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, err
	}
	meta := tusd.MetaData{"filename": filepath.Base(name), "user_id": p.User.ID}
	if s := hook.Upload.MetaData["sha256"]; hexSHA256.MatchString(s) {
		meta["sha256"] = strings.ToLower(s)
	}
	return tusd.HTTPResponse{}, tusd.FileInfoChanges{ID: id, MetaData: meta}, nil
}

func (a *App) uploadFinished(hook tusd.HookEvent) error {
	ctx := context.WithoutCancel(hook.Context)
	id := hook.Upload.ID
	if _, err := a.DB.Exec(ctx, `UPDATE uploads SET status = 'processing', received = size_bytes, updated_at = ? WHERE id = ? AND status = 'receiving'`,
		store.NowMS(), id); err != nil {
		return err
	}
	if _, err := jobs.Enqueue(ctx, a.DB, library.JobImportUpload, library.UploadPayload{UploadID: id}, time.Time{}, hook.Upload.MetaData["user_id"]); err != nil {
		return err
	}
	a.Jobs.Wake()
	return nil
}

// guardUploads requires a signed-in user who may upload, and restricts access
// to an existing upload (HEAD/PATCH/DELETE) to its owner.
func (a *App) guardUploads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := requireCan(r.Context(), authz.Upload)
		if err != nil {
			writeAuthErr(w, err)
			return
		}
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, strings.TrimSuffix(uploadsBase, "/")), "/")
		if id != "" {
			var owner, status string
			err := a.DB.QueryRow(r.Context(), `SELECT user_id, status FROM uploads WHERE id = ?`, id).Scan(&owner, &status)
			if err != nil || (owner != p.User.ID && p.User.Role != "admin") {
				writeError(w, http.StatusNotFound, "not_found", "no such upload")
				return
			}
			if r.Method == http.MethodPatch && status != "receiving" {
				writeError(w, http.StatusConflict, "upload_closed", "this upload is already complete")
				return
			}
			if r.Method == http.MethodDelete && status == "receiving" {
				_, _ = a.DB.Exec(r.Context(), `UPDATE uploads SET status = 'cancelled', updated_at = ? WHERE id = ?`, store.NowMS(), id)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// writeAuthErr renders an auth/authz error outside the strict handlers.
func writeAuthErr(w http.ResponseWriter, err error) {
	var (
		he *httpError
		ae *auth.Error
	)
	switch {
	case errors.As(err, &he):
		writeError(w, he.status, he.code, he.msg)
	case errors.As(err, &ae):
		writeError(w, ae.Status, ae.Code, ae.Message)
	default:
		writeError(w, http.StatusInternalServerError, "internal", "internal server error")
	}
}

// receivedBytes reads how much of a receiving upload has arrived.
func (a *App) receivedBytes(id string) int64 {
	st, err := os.Stat(filepath.Join(a.Lib.IncomingPath(), id))
	if err != nil {
		return 0
	}
	return st.Size()
}
