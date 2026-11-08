// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"testing"
)

// forEachDialect runs fn against a fresh, migrated SQLite DB and — when
// SIMPLEAPB_TEST_POSTGRES_DSN is set — a freshly reset Postgres DB.
func forEachDialect(t *testing.T, fn func(t *testing.T, db *DB)) {
	t.Run("sqlite", func(t *testing.T) {
		db, err := OpenDB(context.Background(), SQLite, filepath.Join(t.TempDir(), "t.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		fn(t, db)
	})
	t.Run("postgres", func(t *testing.T) {
		ctx := context.Background()
		raw, err := Open(ctx, Postgres, postgresDSN(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := MigrateDownAll(ctx, raw, Postgres); err != nil {
			t.Fatal(err)
		}
		if err := MigrateUp(ctx, raw, Postgres); err != nil {
			t.Fatal(err)
		}
		db := Wrap(raw, Postgres)
		t.Cleanup(func() { _ = db.Close() })
		fn(t, db)
	})
}

// plainEnc is a stand-in encrypter (base64) so tests can check values aren't stored in clear.
type plainEnc struct{}

func (plainEnc) Encrypt(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func (plainEnc) Decrypt(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}
