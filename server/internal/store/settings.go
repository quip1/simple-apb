// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// Encrypter seals secret setting values (implemented by config.Secrets).
type Encrypter interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// GetSetting decodes the JSON value of key into dest. Secret values are
// decrypted with enc. Returns ErrNotFound if the key is unset.
func GetSetting(ctx context.Context, q Querier, enc Encrypter, key string, dest any) error {
	var (
		val    string
		secret bool
	)
	err := q.QueryRow(ctx, `SELECT value, secret FROM settings WHERE key = ?`, key).Scan(&val, &secret)
	if err != nil {
		return notFound(err)
	}
	if secret {
		// Secret values are stored as a JSON string holding the ciphertext.
		var ct string
		if err := json.Unmarshal([]byte(val), &ct); err != nil {
			return fmt.Errorf("setting %s: %w", key, err)
		}
		if val, err = enc.Decrypt(ct); err != nil {
			return fmt.Errorf("setting %s: %w", key, err)
		}
	}
	if err := json.Unmarshal([]byte(val), dest); err != nil {
		return fmt.Errorf("setting %s: %w", key, err)
	}
	return nil
}

// SetSetting stores value (JSON-encoded) under key, encrypting when secret.
func SetSetting(ctx context.Context, q Querier, enc Encrypter, key string, value any, secret bool) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	val := string(b)
	if secret {
		ct, err := enc.Encrypt(val)
		if err != nil {
			return err
		}
		quoted, _ := json.Marshal(ct)
		val = string(quoted)
	}
	now := NowMS()
	_, err = q.Exec(ctx, `
		INSERT INTO settings (key, value, secret, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, secret = excluded.secret, updated_at = excluded.updated_at`,
		key, val, secret, now, now)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}
