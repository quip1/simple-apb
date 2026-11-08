-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Reject reused TOTP codes (DECISIONS Q5): remember the last accepted 30 s step.
-- MUST stay in lockstep with ../sqlite/00002_totp_last_step.sql.

-- +goose Up
ALTER TABLE users ADD COLUMN totp_last_step BIGINT;

-- +goose Down
ALTER TABLE users DROP COLUMN totp_last_step;
