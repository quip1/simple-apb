-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Series order as a number (ADR-0051): series_index is free text ("3",
-- "Book 10", "2.5"), which sorts "10" before "2". The server fills
-- series_order from it (store.SeriesOrder), including for existing books at
-- start-up.
-- MUST stay in lockstep with ../sqlite/00004_series_order.sql.

-- +goose Up
ALTER TABLE books ADD COLUMN series_order DOUBLE PRECISION;
CREATE INDEX books_series_order_idx ON books (series_id, series_order);

-- +goose Down
DROP INDEX books_series_order_idx;
ALTER TABLE books DROP COLUMN series_order;
