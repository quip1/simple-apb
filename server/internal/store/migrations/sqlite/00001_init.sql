-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Simple APB initial schema — SQLite dialect.
-- MUST stay in lockstep with ../postgres/00001_init.sql (same tables, same columns).
--
-- Conventions (see docs/DECISIONS.md ADR-0004):
--   ids         TEXT UUIDv7 (Postgres: UUID)
--   timestamps  INTEGER unix epoch milliseconds, UTC (Postgres: BIGINT)
--   booleans    INTEGER 0/1 (Postgres: BOOLEAN)
--   json        TEXT containing JSON (Postgres: JSONB)
--   lists       normalized join tables, never arrays
--   STRICT tables so SQLite enforces column types.

-- +goose Up

-- ───────────────────────────── Users and access ─────────────────────────────

CREATE TABLE users (
    id                     TEXT    PRIMARY KEY,
    username               TEXT    NOT NULL,
    display_name           TEXT    NOT NULL,
    email                  TEXT,
    email_verified         INTEGER NOT NULL DEFAULT 0 CHECK (email_verified IN (0, 1)),
    password_hash          TEXT,                      -- argon2id PHC string; NULL for OIDC-only accounts
    totp_secret            TEXT,                      -- encrypted at rest; NULL = TOTP off
    totp_enabled_at        INTEGER,
    status                 TEXT    NOT NULL DEFAULT 'pending'
                           CHECK (status IN ('pending','active','restricted','suspended','banned','deleted')),
    status_reason          TEXT,
    status_until           INTEGER,                   -- suspension end, if any
    notice                 TEXT,                      -- moderator warning shown on next login
    role                   TEXT    NOT NULL DEFAULT 'user' CHECK (role IN ('admin','user')),
    quota_bytes            INTEGER NOT NULL DEFAULT 0, -- 0 = unlimited
    birthdate_confirmed_at INTEGER,
    terms_accepted_at      INTEGER,
    locale                 TEXT    NOT NULL DEFAULT 'en',
    prefs                  TEXT    NOT NULL DEFAULT '{}',
    listening_visible      INTEGER NOT NULL DEFAULT 1 CHECK (listening_visible IN (0, 1)), -- "who's listening" opt-out
    conflict_threshold_ms  INTEGER NOT NULL DEFAULT 180000 CHECK (conflict_threshold_ms BETWEEN 60000 AND 1800000),
    failed_logins          INTEGER NOT NULL DEFAULT 0,
    locked_until           INTEGER,
    last_login_at          INTEGER,
    created_at             INTEGER NOT NULL,
    updated_at             INTEGER NOT NULL,
    deleted_at             INTEGER
) STRICT;
CREATE UNIQUE INDEX users_username_uq ON users (lower(username));
CREATE UNIQUE INDEX users_email_uq ON users (lower(email)) WHERE email IS NOT NULL;
CREATE INDEX users_status_idx ON users (status);

-- Explicit per-user overrides. NULL = no override (fall through to tags, then instance defaults).
CREATE TABLE user_permissions (
    user_id           TEXT    PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    can_upload        INTEGER CHECK (can_upload IN (0, 1)),
    can_edit_metadata INTEGER CHECK (can_edit_metadata IN (0, 1)),
    can_delete_own    INTEGER CHECK (can_delete_own IN (0, 1)),
    can_share         INTEGER CHECK (can_share IN (0, 1)),
    can_review        INTEGER CHECK (can_review IN (0, 1)),
    can_post_public   INTEGER CHECK (can_post_public IN (0, 1)),
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
) STRICT;

CREATE TABLE recovery_codes (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT    NOT NULL,
    used_at    INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX recovery_codes_user_idx ON recovery_codes (user_id);

CREATE TABLE user_tags (
    id                   TEXT    PRIMARY KEY,
    name                 TEXT    NOT NULL,
    color                TEXT,
    capability_overrides TEXT    NOT NULL DEFAULT '{}', -- e.g. {"can_upload": false}
    require_totp         INTEGER NOT NULL DEFAULT 0 CHECK (require_totp IN (0, 1)),
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX user_tags_name_uq ON user_tags (lower(name));

CREATE TABLE user_tag_assignments (
    id          TEXT    PRIMARY KEY,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_tag_id TEXT    NOT NULL REFERENCES user_tags (id) ON DELETE CASCADE,
    assigned_by TEXT    REFERENCES users (id),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX user_tag_assignments_uq ON user_tag_assignments (user_id, user_tag_id);

CREATE TABLE groups (
    id            TEXT    PRIMARY KEY,
    name          TEXT    NOT NULL,
    builtin       INTEGER NOT NULL DEFAULT 0 CHECK (builtin IN (0, 1)),       -- 'Public' is builtin, undeletable
    high_priority INTEGER NOT NULL DEFAULT 0 CHECK (high_priority IN (0, 1)), -- bandwidth priority class
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX groups_name_uq ON groups (lower(name));

CREATE TABLE group_members (
    id         TEXT    PRIMARY KEY,
    group_id   TEXT    NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX group_members_uq ON group_members (group_id, user_id);
CREATE INDEX group_members_user_idx ON group_members (user_id);

CREATE TABLE sessions (
    id                 TEXT    PRIMARY KEY,
    user_id            TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_id          TEXT    NOT NULL,
    device_name        TEXT    NOT NULL,
    platform           TEXT    NOT NULL CHECK (platform IN ('android','wear','web','windows','other')),
    app_version        TEXT    NOT NULL,
    ip                 TEXT,
    refresh_token_hash TEXT    NOT NULL,
    prev_refresh_hash  TEXT,                -- previous token, to detect refresh-token reuse
    expires_at         INTEGER NOT NULL,
    last_seen_at       INTEGER NOT NULL,
    revoked_at         INTEGER,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
) STRICT;
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE UNIQUE INDEX sessions_refresh_uq ON sessions (refresh_token_hash);

CREATE TABLE oidc_links (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    issuer     TEXT    NOT NULL,
    subject    TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX oidc_links_uq ON oidc_links (issuer, subject);

-- Sign-up requests (brief: invites_or_requests). One row per pending user.
CREATE TABLE signup_requests (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    username   TEXT    NOT NULL,
    email      TEXT,
    message    TEXT,
    ip         TEXT,
    status     TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','denied')),
    decided_by TEXT    REFERENCES users (id),
    decided_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX signup_requests_status_idx ON signup_requests (status, created_at);

CREATE TABLE api_tokens (
    id           TEXT    PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    token_hash   TEXT    NOT NULL,
    scopes       TEXT    NOT NULL DEFAULT '["read"]', -- read | write | admin | opds
    last_used_at INTEGER,
    expires_at   INTEGER,
    revoked_at   INTEGER,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX api_tokens_hash_uq ON api_tokens (token_hash);

-- ───────────────────────────────── Library ──────────────────────────────────

CREATE TABLE series (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    sort_name  TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX series_name_uq ON series (lower(name));

CREATE TABLE authors (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    sort_name  TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX authors_name_uq ON authors (lower(name));

CREATE TABLE narrators (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    sort_name  TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX narrators_name_uq ON narrators (lower(name));

-- The master store. Kept generic enough to become a shared "media item" later.
CREATE TABLE books (
    id                TEXT    PRIMARY KEY,
    title             TEXT    NOT NULL,
    sort_title        TEXT    NOT NULL,
    subtitle          TEXT,
    series_id         TEXT    REFERENCES series (id) ON DELETE SET NULL,
    series_index      TEXT,
    description       TEXT,
    publisher         TEXT,
    published_year    INTEGER,
    language          TEXT,
    isbn              TEXT,
    asin              TEXT,
    duration_ms       INTEGER NOT NULL DEFAULT 0,
    size_bytes        INTEGER NOT NULL DEFAULT 0,
    cover_path        TEXT,
    uploader_id       TEXT    REFERENCES users (id) ON DELETE SET NULL, -- NULL = admin scan / former user
    origin            TEXT    NOT NULL CHECK (origin IN ('scan','upload','abs_import')),
    status            TEXT    NOT NULL DEFAULT 'processing'
                      CHECK (status IN ('processing','ready','quarantined','trashed')),
    status_reason     TEXT,
    trashed_at        INTEGER,               -- auto-purge 30 days after this
    checksum_sha256   TEXT,                  -- whole-book checksum (hash of ordered file hashes)
    folder_path       TEXT    NOT NULL,      -- relative to the library root
    epub_path         TEXT,
    locked_fields     TEXT    NOT NULL DEFAULT '[]', -- metadata fields edited by hand; providers never overwrite them
    metadata_review   INTEGER NOT NULL DEFAULT 0 CHECK (metadata_review IN (0, 1)), -- "needs review"
    metadata_score    REAL,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX books_folder_uq ON books (folder_path);
CREATE INDEX books_checksum_idx ON books (checksum_sha256);
CREATE INDEX books_status_idx ON books (status);
CREATE INDEX books_series_idx ON books (series_id);
CREATE INDEX books_uploader_idx ON books (uploader_id);
CREATE INDEX books_sort_title_idx ON books (sort_title);

CREATE TABLE book_authors (
    book_id   TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    author_id TEXT    NOT NULL REFERENCES authors (id) ON DELETE CASCADE,
    position  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, author_id)
) STRICT;
CREATE INDEX book_authors_author_idx ON book_authors (author_id);

CREATE TABLE book_narrators (
    book_id     TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    narrator_id TEXT    NOT NULL REFERENCES narrators (id) ON DELETE CASCADE,
    position    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, narrator_id)
) STRICT;
CREATE INDEX book_narrators_narrator_idx ON book_narrators (narrator_id);

CREATE TABLE book_files (
    id          TEXT    PRIMARY KEY,
    book_id     TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    path        TEXT    NOT NULL,            -- relative to the book folder
    idx         INTEGER NOT NULL,            -- play order
    offset_ms   INTEGER NOT NULL,            -- start on the global book timeline
    duration_ms INTEGER NOT NULL,
    size_bytes  INTEGER NOT NULL,
    codec       TEXT    NOT NULL,
    bitrate     INTEGER,                     -- kbps
    mime_type   TEXT    NOT NULL,
    checksum    TEXT    NOT NULL,            -- sha256 hex
    verified_at INTEGER,                     -- last successful scrub
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX book_files_order_uq ON book_files (book_id, idx);
CREATE INDEX book_files_checksum_idx ON book_files (checksum);

CREATE TABLE chapters (
    id         TEXT    PRIMARY KEY,
    book_id    TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    idx        INTEGER NOT NULL,
    title      TEXT    NOT NULL,
    start_ms   INTEGER NOT NULL,
    end_ms     INTEGER NOT NULL CHECK (end_ms >= start_ms),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX chapters_order_uq ON chapters (book_id, idx);

CREATE TABLE book_tags (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    kind       TEXT    NOT NULL DEFAULT 'custom' CHECK (kind IN ('genre','custom')),
    created_by TEXT    REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX book_tags_name_uq ON book_tags (kind, lower(name));

CREATE TABLE book_tag_assignments (
    id          TEXT    PRIMARY KEY,
    book_id     TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    book_tag_id TEXT    NOT NULL REFERENCES book_tags (id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX book_tag_assignments_uq ON book_tag_assignments (book_id, book_tag_id);
CREATE INDEX book_tag_assignments_tag_idx ON book_tag_assignments (book_tag_id);

-- Per-user content filter: books carrying these tags are excluded everywhere for that user.
CREATE TABLE content_filters (
    id          TEXT    PRIMARY KEY,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_tag_id TEXT    NOT NULL REFERENCES book_tags (id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX content_filters_uq ON content_filters (user_id, book_tag_id);

CREATE TABLE distributions (
    id             TEXT    PRIMARY KEY,
    book_id        TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    target_type    TEXT    NOT NULL CHECK (target_type IN ('group','user')),
    target_id      TEXT    NOT NULL,         -- groups.id or users.id
    distributed_by TEXT    REFERENCES users (id) ON DELETE SET NULL,
    note           TEXT,
    revoked_at     INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX distributions_uq ON distributions (book_id, target_type, target_id) WHERE revoked_at IS NULL;
CREATE INDEX distributions_target_idx ON distributions (target_type, target_id);

CREATE TABLE user_library_entries (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id    TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    source     TEXT    NOT NULL CHECK (source IN ('own_upload','distribution','recommendation')),
    removed_at INTEGER,   -- user removed it from their view; hard-deleted after 30 days
    hidden_at  INTEGER,   -- archived
    added_at   INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX user_library_entries_uq ON user_library_entries (user_id, book_id);
CREATE INDEX user_library_entries_book_idx ON user_library_entries (book_id);

CREATE TABLE collections (
    id          TEXT    PRIMARY KEY,
    owner_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    description TEXT,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    deleted_at  INTEGER   -- tombstone for sync
) STRICT;
CREATE INDEX collections_owner_idx ON collections (owner_id);

CREATE TABLE collection_books (
    id            TEXT    PRIMARY KEY,
    collection_id TEXT    NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    book_id       TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    position      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX collection_books_uq ON collection_books (collection_id, book_id);

CREATE TABLE collection_shares (
    id            TEXT    PRIMARY KEY,
    collection_id TEXT    NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    target_type   TEXT    NOT NULL CHECK (target_type IN ('group','user')),
    target_id     TEXT    NOT NULL,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX collection_shares_uq ON collection_shares (collection_id, target_type, target_id);

CREATE TABLE uploads (
    id          TEXT    PRIMARY KEY,         -- tus upload id
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    filename    TEXT    NOT NULL,
    size_bytes  INTEGER NOT NULL,
    received    INTEGER NOT NULL DEFAULT 0,
    status      TEXT    NOT NULL DEFAULT 'receiving'
                CHECK (status IN ('receiving','processing','done','failed','cancelled')),
    error       TEXT,
    book_id     TEXT    REFERENCES books (id) ON DELETE SET NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX uploads_user_idx ON uploads (user_id, status);

-- ──────────────────────────────── Listening ─────────────────────────────────

CREATE TABLE progress (
    id          TEXT    PRIMARY KEY,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id     TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    position_ms INTEGER NOT NULL CHECK (position_ms >= 0),
    device_id   TEXT    NOT NULL,
    client_ts   INTEGER NOT NULL,   -- client clock; "latest" is decided on this
    speed       REAL    NOT NULL DEFAULT 1.0,
    finished_at INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL    -- server receive time
) STRICT;
CREATE UNIQUE INDEX progress_uq ON progress (user_id, book_id);

-- Append-only listening history (conflict detection + stats).
CREATE TABLE progress_events (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id    TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    device_id  TEXT    NOT NULL,
    from_ms    INTEGER NOT NULL,
    to_ms      INTEGER NOT NULL,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX progress_events_user_time_idx ON progress_events (user_id, started_at);
CREATE INDEX progress_events_book_idx ON progress_events (book_id, user_id);

-- Conflicts returned as 409, and how the user resolved them (audit trail).
CREATE TABLE progress_conflicts (
    id                TEXT    PRIMARY KEY,
    user_id           TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id           TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    server_position   INTEGER NOT NULL,
    server_device_id  TEXT    NOT NULL,
    incoming_position INTEGER NOT NULL,
    incoming_device_id TEXT   NOT NULL,
    chosen_position   INTEGER,
    resolved_at       INTEGER,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
) STRICT;
CREATE INDEX progress_conflicts_user_idx ON progress_conflicts (user_id, book_id);

CREATE TABLE bookmarks (
    id                      TEXT    PRIMARY KEY,
    user_id                 TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id                 TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    position_ms             INTEGER NOT NULL CHECK (position_ms >= 0),
    note                    TEXT,
    shared_to_collection_id TEXT    REFERENCES collections (id) ON DELETE SET NULL,
    created_at              INTEGER NOT NULL,
    updated_at              INTEGER NOT NULL,
    deleted_at              INTEGER   -- tombstone for sync
) STRICT;
CREATE INDEX bookmarks_user_book_idx ON bookmarks (user_id, book_id);

CREATE TABLE ratings_reviews (
    id            TEXT    PRIMARY KEY,
    user_id       TEXT    REFERENCES users (id) ON DELETE SET NULL, -- NULL after anonymization
    book_id       TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    stars         INTEGER NOT NULL CHECK (stars BETWEEN 1 AND 5),
    text          TEXT    CHECK (text IS NULL OR length(text) <= 500),
    hidden_by_mod TEXT    REFERENCES users (id) ON DELETE SET NULL,
    hidden_reason TEXT,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    deleted_at    INTEGER
) STRICT;
CREATE UNIQUE INDEX ratings_reviews_uq ON ratings_reviews (user_id, book_id) WHERE user_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX ratings_reviews_book_idx ON ratings_reviews (book_id);

CREATE TABLE recommendations (
    id         TEXT    PRIMARY KEY,
    from_user  TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    to_user    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id    TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    note       TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX recommendations_to_idx ON recommendations (to_user, created_at);

-- ──────────────────────────────── Operations ────────────────────────────────

CREATE TABLE jobs (
    id         TEXT    PRIMARY KEY,
    type       TEXT    NOT NULL,
    payload    TEXT    NOT NULL DEFAULT '{}',
    status     TEXT    NOT NULL DEFAULT 'queued'
               CHECK (status IN ('queued','running','succeeded','failed','cancelled')),
    priority   INTEGER NOT NULL DEFAULT 0,
    attempts   INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    run_after  INTEGER NOT NULL,
    locked_at  INTEGER,
    last_error TEXT,
    result     TEXT,
    created_by TEXT    REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX jobs_ready_idx ON jobs (status, run_after, priority);

CREATE TABLE reports (
    id          TEXT    PRIMARY KEY,
    reporter_id TEXT    REFERENCES users (id) ON DELETE SET NULL,
    object_type TEXT    NOT NULL CHECK (object_type IN ('book','review','user')),
    object_id   TEXT    NOT NULL,
    reason      TEXT    NOT NULL,
    details     TEXT,
    status      TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved','dismissed')),
    resolution  TEXT,
    resolved_by TEXT    REFERENCES users (id) ON DELETE SET NULL,
    resolved_at INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX reports_status_idx ON reports (status, created_at);

CREATE TABLE review_queue (
    id           TEXT    PRIMARY KEY,
    book_id      TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    uploader_id  TEXT    REFERENCES users (id) ON DELETE SET NULL,
    target_group TEXT    NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    status       TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    reason       TEXT,
    decided_by   TEXT    REFERENCES users (id) ON DELETE SET NULL,
    decided_at   INTEGER,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
) STRICT;
CREATE INDEX review_queue_status_idx ON review_queue (status, created_at);

CREATE TABLE takedowns (
    id         TEXT    PRIMARY KEY,
    book_id    TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    reason     TEXT    NOT NULL,
    taken_by   TEXT    REFERENCES users (id) ON DELETE SET NULL,
    taken_at   INTEGER NOT NULL,
    lifted_at  INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX takedowns_book_idx ON takedowns (book_id) WHERE lifted_at IS NULL;

CREATE TABLE duplicate_candidates (
    id         TEXT    PRIMARY KEY,
    book_a_id  TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    book_b_id  TEXT    NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL CHECK (kind IN ('exact','probable')),
    score      REAL,
    status     TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open','merged','kept')),
    decided_by TEXT    REFERENCES users (id) ON DELETE SET NULL,
    decided_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX duplicate_candidates_uq ON duplicate_candidates (book_a_id, book_b_id);

-- Append-only. The application never UPDATEs or DELETEs rows here (except the optional pruning job).
CREATE TABLE audit_log (
    id          TEXT    PRIMARY KEY,
    actor_id    TEXT,               -- not a FK: the log must survive user anonymization
    action      TEXT    NOT NULL,
    object_type TEXT,
    object_id   TEXT,
    details     TEXT    NOT NULL DEFAULT '{}',
    ip          TEXT,
    at          INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX audit_log_at_idx ON audit_log (at);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_id, at);
CREATE INDEX audit_log_object_idx ON audit_log (object_type, object_id);

CREATE TABLE crash_reports (
    id         TEXT    PRIMARY KEY,
    device_id  TEXT    NOT NULL,
    user_id    TEXT    REFERENCES users (id) ON DELETE SET NULL,
    app        TEXT    NOT NULL,
    version    TEXT    NOT NULL,
    stacktrace TEXT    NOT NULL,
    at         INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX crash_reports_at_idx ON crash_reports (at);

CREATE TABLE push_subscriptions (
    id                   TEXT    PRIMARY KEY,
    device_id            TEXT    NOT NULL,
    user_id              TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    topic                TEXT    NOT NULL,   -- random 128-bit id
    unifiedpush_endpoint TEXT,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX push_subscriptions_device_uq ON push_subscriptions (user_id, device_id);
CREATE UNIQUE INDEX push_subscriptions_topic_uq ON push_subscriptions (topic);

CREATE TABLE settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,             -- JSON
    secret     INTEGER NOT NULL DEFAULT 0 CHECK (secret IN (0, 1)), -- value is encrypted with /config/secret.key
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- Change log that drives GET /me/sync. One row per change; seq is the sync cursor.
-- user_id NULL = change relevant to everyone who can see the object (e.g. book metadata).
CREATE TABLE sync_changes (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    TEXT,
    entity     TEXT    NOT NULL,   -- book | library_entry | progress | bookmark | collection
    entity_id  TEXT    NOT NULL,
    op         TEXT    NOT NULL CHECK (op IN ('upsert','delete')),
    at         INTEGER NOT NULL
) STRICT;
CREATE INDEX sync_changes_user_seq_idx ON sync_changes (user_id, seq);

-- +goose Down
DROP TABLE sync_changes;
DROP TABLE settings;
DROP TABLE push_subscriptions;
DROP TABLE crash_reports;
DROP TABLE audit_log;
DROP TABLE duplicate_candidates;
DROP TABLE takedowns;
DROP TABLE review_queue;
DROP TABLE reports;
DROP TABLE jobs;
DROP TABLE recommendations;
DROP TABLE ratings_reviews;
DROP TABLE bookmarks;
DROP TABLE progress_conflicts;
DROP TABLE progress_events;
DROP TABLE progress;
DROP TABLE uploads;
DROP TABLE collection_shares;
DROP TABLE collection_books;
DROP TABLE collections;
DROP TABLE user_library_entries;
DROP TABLE distributions;
DROP TABLE content_filters;
DROP TABLE book_tag_assignments;
DROP TABLE book_tags;
DROP TABLE chapters;
DROP TABLE book_files;
DROP TABLE book_narrators;
DROP TABLE book_authors;
DROP TABLE books;
DROP TABLE narrators;
DROP TABLE authors;
DROP TABLE series;
DROP TABLE api_tokens;
DROP TABLE signup_requests;
DROP TABLE oidc_links;
DROP TABLE sessions;
DROP TABLE group_members;
DROP TABLE groups;
DROP TABLE user_tag_assignments;
DROP TABLE user_tags;
DROP TABLE recovery_codes;
DROP TABLE user_permissions;
DROP TABLE users;
