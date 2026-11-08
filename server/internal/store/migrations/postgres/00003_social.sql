-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Moderation and social (phase 2 M5): report resolution notes, one
-- recommendation per sender/recipient/book, faster report lookups by object.
-- MUST stay in lockstep with ../sqlite/00003_social.sql.

-- +goose Up
ALTER TABLE reports ADD COLUMN resolution_note TEXT;
CREATE INDEX reports_object_idx ON reports (object_type, object_id, status);
CREATE UNIQUE INDEX recommendations_uq ON recommendations (from_user, to_user, book_id);
CREATE INDEX review_queue_book_idx ON review_queue (book_id, status);

-- +goose Down
DROP INDEX review_queue_book_idx;
DROP INDEX recommendations_uq;
DROP INDEX reports_object_idx;
ALTER TABLE reports DROP COLUMN resolution_note;
