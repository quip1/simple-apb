-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Simple APB initial schema — PostgreSQL dialect.
-- MUST stay in lockstep with ../sqlite/00001_init.sql (same tables, same columns).
--
-- Conventions (see docs/DECISIONS.md ADR-0004):
--   ids         TEXT UUIDv7 (Postgres: UUID)
--   timestamps  INTEGER unix epoch milliseconds, UTC (Postgres: BIGINT)
--   booleans    INTEGER 0/1 (Postgres: BOOLEAN)
--   json        TEXT containing JSON (Postgres: JSONB)
--   lists       normalized join tables, never arrays

-- +goose Up

-- ───────────────────────────── Users and access ─────────────────────────────

CREATE TABLE users (
    id                     UUID   PRIMARY KEY,
    username               TEXT   NOT NULL,
    display_name           TEXT   NOT NULL,
    email                  TEXT,
    email_verified         BOOLEAN NOT NULL DEFAULT FALSE,
    password_hash          TEXT,                      -- argon2id PHC string; NULL for OIDC-only accounts
    totp_secret            TEXT,                      -- encrypted at rest; NULL = TOTP off
    totp_enabled_at        BIGINT,
    status                 TEXT   NOT NULL DEFAULT 'pending'
                           CHECK (status IN ('pending','active','restricted','suspended','banned','deleted')),
    status_reason          TEXT,
    status_until           BIGINT,                   -- suspension end, if any
    notice                 TEXT,                      -- moderator warning shown on next login
    role                   TEXT   NOT NULL DEFAULT 'user' CHECK (role IN ('admin','user')),
    quota_bytes            BIGINT NOT NULL DEFAULT 0, -- 0 = unlimited
    birthdate_confirmed_at BIGINT,
    terms_accepted_at      BIGINT,
    locale                 TEXT   NOT NULL DEFAULT 'en',
    prefs                  JSONB  NOT NULL DEFAULT '{}',
    listening_visible      BOOLEAN NOT NULL DEFAULT TRUE, -- "who's listening" opt-out
    conflict_threshold_ms  BIGINT NOT NULL DEFAULT 180000 CHECK (conflict_threshold_ms BETWEEN 60000 AND 1800000),
    failed_logins          BIGINT NOT NULL DEFAULT 0,
    locked_until           BIGINT,
    last_login_at          BIGINT,
    created_at             BIGINT NOT NULL,
    updated_at             BIGINT NOT NULL,
    deleted_at             BIGINT
);
CREATE UNIQUE INDEX users_username_uq ON users (lower(username));
CREATE UNIQUE INDEX users_email_uq ON users (lower(email)) WHERE email IS NOT NULL;
CREATE INDEX users_status_idx ON users (status);

-- Explicit per-user overrides. NULL = no override (fall through to tags, then instance defaults).
CREATE TABLE user_permissions (
    user_id           UUID   PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    can_upload        BOOLEAN,
    can_edit_metadata BOOLEAN,
    can_delete_own    BOOLEAN,
    can_share         BOOLEAN,
    can_review        BOOLEAN,
    can_post_public   BOOLEAN,
    created_at        BIGINT NOT NULL,
    updated_at        BIGINT NOT NULL
);

CREATE TABLE recovery_codes (
    id         UUID   PRIMARY KEY,
    user_id    UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT   NOT NULL,
    used_at    BIGINT,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX recovery_codes_user_idx ON recovery_codes (user_id);

CREATE TABLE user_tags (
    id                   UUID   PRIMARY KEY,
    name                 TEXT   NOT NULL,
    color                TEXT,
    capability_overrides JSONB  NOT NULL DEFAULT '{}', -- e.g. {"can_upload": false}
    require_totp         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           BIGINT NOT NULL,
    updated_at           BIGINT NOT NULL
);
CREATE UNIQUE INDEX user_tags_name_uq ON user_tags (lower(name));

CREATE TABLE user_tag_assignments (
    id          UUID   PRIMARY KEY,
    user_id     UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_tag_id UUID   NOT NULL REFERENCES user_tags (id) ON DELETE CASCADE,
    assigned_by UUID   REFERENCES users (id),
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE UNIQUE INDEX user_tag_assignments_uq ON user_tag_assignments (user_id, user_tag_id);

CREATE TABLE groups (
    id            UUID   PRIMARY KEY,
    name          TEXT   NOT NULL,
    builtin       BOOLEAN NOT NULL DEFAULT FALSE,       -- 'Public' is builtin, undeletable
    high_priority BOOLEAN NOT NULL DEFAULT FALSE, -- bandwidth priority class
    created_at    BIGINT NOT NULL,
    updated_at    BIGINT NOT NULL
);
CREATE UNIQUE INDEX groups_name_uq ON groups (lower(name));

CREATE TABLE group_members (
    id         UUID   PRIMARY KEY,
    group_id   UUID   NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    user_id    UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX group_members_uq ON group_members (group_id, user_id);
CREATE INDEX group_members_user_idx ON group_members (user_id);

CREATE TABLE sessions (
    id                 UUID   PRIMARY KEY,
    user_id            UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_id          TEXT   NOT NULL,
    device_name        TEXT   NOT NULL,
    platform           TEXT   NOT NULL CHECK (platform IN ('android','wear','web','windows','other')),
    app_version        TEXT   NOT NULL,
    ip                 TEXT,
    refresh_token_hash TEXT   NOT NULL,
    prev_refresh_hash  TEXT,                -- previous token, to detect refresh-token reuse
    expires_at         BIGINT NOT NULL,
    last_seen_at       BIGINT NOT NULL,
    revoked_at         BIGINT,
    created_at         BIGINT NOT NULL,
    updated_at         BIGINT NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE UNIQUE INDEX sessions_refresh_uq ON sessions (refresh_token_hash);

CREATE TABLE oidc_links (
    id         UUID   PRIMARY KEY,
    user_id    UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    issuer     TEXT   NOT NULL,
    subject    TEXT   NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX oidc_links_uq ON oidc_links (issuer, subject);

-- Sign-up requests (brief: invites_or_requests). One row per pending user.
CREATE TABLE signup_requests (
    id         UUID   PRIMARY KEY,
    user_id    UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    username   TEXT   NOT NULL,
    email      TEXT,
    message    TEXT,
    ip         TEXT,
    status     TEXT   NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','denied')),
    decided_by UUID   REFERENCES users (id),
    decided_at BIGINT,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX signup_requests_status_idx ON signup_requests (status, created_at);

CREATE TABLE api_tokens (
    id           UUID   PRIMARY KEY,
    user_id      UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT   NOT NULL,
    token_hash   TEXT   NOT NULL,
    scopes       JSONB  NOT NULL DEFAULT '["read"]', -- read | write | admin | opds
    last_used_at BIGINT,
    expires_at   BIGINT,
    revoked_at   BIGINT,
    created_at   BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL
);
CREATE UNIQUE INDEX api_tokens_hash_uq ON api_tokens (token_hash);

-- ───────────────────────────────── Library ──────────────────────────────────

CREATE TABLE series (
    id         UUID   PRIMARY KEY,
    name       TEXT   NOT NULL,
    sort_name  TEXT   NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX series_name_uq ON series (lower(name));

CREATE TABLE authors (
    id         UUID   PRIMARY KEY,
    name       TEXT   NOT NULL,
    sort_name  TEXT   NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX authors_name_uq ON authors (lower(name));

CREATE TABLE narrators (
    id         UUID   PRIMARY KEY,
    name       TEXT   NOT NULL,
    sort_name  TEXT   NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX narrators_name_uq ON narrators (lower(name));

-- The master store. Kept generic enough to become a shared "media item" later.
CREATE TABLE books (
    id                UUID   PRIMARY KEY,
    title             TEXT   NOT NULL,
    sort_title        TEXT   NOT NULL,
    subtitle          TEXT,
    series_id         UUID   REFERENCES series (id) ON DELETE SET NULL,
    series_index      TEXT,
    description       TEXT,
    publisher         TEXT,
    published_year    BIGINT,
    language          TEXT,
    isbn              TEXT,
    asin              TEXT,
    duration_ms       BIGINT NOT NULL DEFAULT 0,
    size_bytes        BIGINT NOT NULL DEFAULT 0,
    cover_path        TEXT,
    uploader_id       UUID   REFERENCES users (id) ON DELETE SET NULL, -- NULL = admin scan / former user
    origin            TEXT   NOT NULL CHECK (origin IN ('scan','upload','abs_import')),
    status            TEXT   NOT NULL DEFAULT 'processing'
                      CHECK (status IN ('processing','ready','quarantined','trashed')),
    status_reason     TEXT,
    trashed_at        BIGINT,               -- auto-purge 30 days after this
    checksum_sha256   TEXT,                  -- whole-book checksum (hash of ordered file hashes)
    folder_path       TEXT   NOT NULL,      -- relative to the library root
    epub_path         TEXT,
    locked_fields     JSONB  NOT NULL DEFAULT '[]', -- metadata fields edited by hand; providers never overwrite them
    metadata_review   BOOLEAN NOT NULL DEFAULT FALSE, -- "needs review"
    metadata_score    DOUBLE PRECISION,
    created_at        BIGINT NOT NULL,
    updated_at        BIGINT NOT NULL
);
CREATE UNIQUE INDEX books_folder_uq ON books (folder_path);
CREATE INDEX books_checksum_idx ON books (checksum_sha256);
CREATE INDEX books_status_idx ON books (status);
CREATE INDEX books_series_idx ON books (series_id);
CREATE INDEX books_uploader_idx ON books (uploader_id);
CREATE INDEX books_sort_title_idx ON books (sort_title);

CREATE TABLE book_authors (
    book_id   UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    author_id UUID   NOT NULL REFERENCES authors (id) ON DELETE CASCADE,
    position  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, author_id)
);
CREATE INDEX book_authors_author_idx ON book_authors (author_id);

CREATE TABLE book_narrators (
    book_id     UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    narrator_id UUID   NOT NULL REFERENCES narrators (id) ON DELETE CASCADE,
    position    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, narrator_id)
);
CREATE INDEX book_narrators_narrator_idx ON book_narrators (narrator_id);

CREATE TABLE book_files (
    id          UUID   PRIMARY KEY,
    book_id     UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    path        TEXT   NOT NULL,            -- relative to the book folder
    idx         BIGINT NOT NULL,            -- play order
    offset_ms   BIGINT NOT NULL,            -- start on the global book timeline
    duration_ms BIGINT NOT NULL,
    size_bytes  BIGINT NOT NULL,
    codec       TEXT   NOT NULL,
    bitrate     BIGINT,                     -- kbps
    mime_type   TEXT   NOT NULL,
    checksum    TEXT   NOT NULL,            -- sha256 hex
    verified_at BIGINT,                     -- last successful scrub
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE UNIQUE INDEX book_files_order_uq ON book_files (book_id, idx);
CREATE INDEX book_files_checksum_idx ON book_files (checksum);

CREATE TABLE chapters (
    id         UUID   PRIMARY KEY,
    book_id    UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    idx        BIGINT NOT NULL,
    title      TEXT   NOT NULL,
    start_ms   BIGINT NOT NULL,
    end_ms     BIGINT NOT NULL CHECK (end_ms >= start_ms),
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX chapters_order_uq ON chapters (book_id, idx);

CREATE TABLE book_tags (
    id         UUID   PRIMARY KEY,
    name       TEXT   NOT NULL,
    kind       TEXT   NOT NULL DEFAULT 'custom' CHECK (kind IN ('genre','custom')),
    created_by UUID   REFERENCES users (id) ON DELETE SET NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX book_tags_name_uq ON book_tags (kind, lower(name));

CREATE TABLE book_tag_assignments (
    id          UUID   PRIMARY KEY,
    book_id     UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    book_tag_id UUID   NOT NULL REFERENCES book_tags (id) ON DELETE CASCADE,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE UNIQUE INDEX book_tag_assignments_uq ON book_tag_assignments (book_id, book_tag_id);
CREATE INDEX book_tag_assignments_tag_idx ON book_tag_assignments (book_tag_id);

-- Per-user content filter: books carrying these tags are excluded everywhere for that user.
CREATE TABLE content_filters (
    id          UUID   PRIMARY KEY,
    user_id     UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_tag_id UUID   NOT NULL REFERENCES book_tags (id) ON DELETE CASCADE,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE UNIQUE INDEX content_filters_uq ON content_filters (user_id, book_tag_id);

CREATE TABLE distributions (
    id             UUID   PRIMARY KEY,
    book_id        UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    target_type    TEXT   NOT NULL CHECK (target_type IN ('group','user')),
    target_id      TEXT   NOT NULL,         -- groups.id or users.id
    distributed_by UUID   REFERENCES users (id) ON DELETE SET NULL,
    note           TEXT,
    revoked_at     BIGINT,
    created_at     BIGINT NOT NULL,
    updated_at     BIGINT NOT NULL
);
CREATE UNIQUE INDEX distributions_uq ON distributions (book_id, target_type, target_id) WHERE revoked_at IS NULL;
CREATE INDEX distributions_target_idx ON distributions (target_type, target_id);

CREATE TABLE user_library_entries (
    id         UUID   PRIMARY KEY,
    user_id    UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id    UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    source     TEXT   NOT NULL CHECK (source IN ('own_upload','distribution','recommendation')),
    removed_at BIGINT,   -- user removed it from their view; hard-deleted after 30 days
    hidden_at  BIGINT,   -- archived
    added_at   BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX user_library_entries_uq ON user_library_entries (user_id, book_id);
CREATE INDEX user_library_entries_book_idx ON user_library_entries (book_id);

CREATE TABLE collections (
    id          UUID   PRIMARY KEY,
    owner_id    UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT   NOT NULL,
    description TEXT,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL,
    deleted_at  BIGINT -- tombstone for sync
);
CREATE INDEX collections_owner_idx ON collections (owner_id);

CREATE TABLE collection_books (
    id            UUID   PRIMARY KEY,
    collection_id UUID   NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    book_id       UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    position      BIGINT NOT NULL DEFAULT 0,
    created_at    BIGINT NOT NULL,
    updated_at    BIGINT NOT NULL
);
CREATE UNIQUE INDEX collection_books_uq ON collection_books (collection_id, book_id);

CREATE TABLE collection_shares (
    id            UUID   PRIMARY KEY,
    collection_id UUID   NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    target_type   TEXT   NOT NULL CHECK (target_type IN ('group','user')),
    target_id     TEXT   NOT NULL,
    created_at    BIGINT NOT NULL,
    updated_at    BIGINT NOT NULL
);
CREATE UNIQUE INDEX collection_shares_uq ON collection_shares (collection_id, target_type, target_id);

CREATE TABLE uploads (
    id          TEXT    PRIMARY KEY,         -- tus upload id
    user_id     UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    filename    TEXT   NOT NULL,
    size_bytes  BIGINT NOT NULL,
    received    BIGINT NOT NULL DEFAULT 0,
    status      TEXT   NOT NULL DEFAULT 'receiving'
                CHECK (status IN ('receiving','processing','done','failed','cancelled')),
    error       TEXT,
    book_id     UUID   REFERENCES books (id) ON DELETE SET NULL,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE INDEX uploads_user_idx ON uploads (user_id, status);

-- ──────────────────────────────── Listening ─────────────────────────────────

CREATE TABLE progress (
    id          UUID   PRIMARY KEY,
    user_id     UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id     UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    position_ms BIGINT NOT NULL CHECK (position_ms >= 0),
    device_id   TEXT   NOT NULL,
    client_ts   BIGINT NOT NULL,   -- client clock; "latest" is decided on this
    speed       DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    finished_at BIGINT,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL    -- server receive time
);
CREATE UNIQUE INDEX progress_uq ON progress (user_id, book_id);

-- Append-only listening history (conflict detection + stats).
CREATE TABLE progress_events (
    id         UUID   PRIMARY KEY,
    user_id    UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id    UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    device_id  TEXT   NOT NULL,
    from_ms    BIGINT NOT NULL,
    to_ms      BIGINT NOT NULL,
    started_at BIGINT NOT NULL,
    ended_at   BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX progress_events_user_time_idx ON progress_events (user_id, started_at);
CREATE INDEX progress_events_book_idx ON progress_events (book_id, user_id);

-- Conflicts returned as 409, and how the user resolved them (audit trail).
CREATE TABLE progress_conflicts (
    id                UUID   PRIMARY KEY,
    user_id           UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id           UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    server_position   BIGINT NOT NULL,
    server_device_id  TEXT   NOT NULL,
    incoming_position BIGINT NOT NULL,
    incoming_device_id TEXT   NOT NULL,
    chosen_position   BIGINT,
    resolved_at       BIGINT,
    created_at        BIGINT NOT NULL,
    updated_at        BIGINT NOT NULL
);
CREATE INDEX progress_conflicts_user_idx ON progress_conflicts (user_id, book_id);

CREATE TABLE bookmarks (
    id                      UUID   PRIMARY KEY,
    user_id                 UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id                 UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    position_ms             BIGINT NOT NULL CHECK (position_ms >= 0),
    note                    TEXT,
    shared_to_collection_id UUID   REFERENCES collections (id) ON DELETE SET NULL,
    created_at              BIGINT NOT NULL,
    updated_at              BIGINT NOT NULL,
    deleted_at              BIGINT -- tombstone for sync
);
CREATE INDEX bookmarks_user_book_idx ON bookmarks (user_id, book_id);

CREATE TABLE ratings_reviews (
    id            UUID   PRIMARY KEY,
    user_id       UUID   REFERENCES users (id) ON DELETE SET NULL, -- NULL after anonymization
    book_id       UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    stars         BIGINT NOT NULL CHECK (stars BETWEEN 1 AND 5),
    text          TEXT   CHECK (text IS NULL OR length(text) <= 500),
    hidden_by_mod UUID   REFERENCES users (id) ON DELETE SET NULL,
    hidden_reason TEXT,
    created_at    BIGINT NOT NULL,
    updated_at    BIGINT NOT NULL,
    deleted_at    BIGINT
);
CREATE UNIQUE INDEX ratings_reviews_uq ON ratings_reviews (user_id, book_id) WHERE user_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX ratings_reviews_book_idx ON ratings_reviews (book_id);

CREATE TABLE recommendations (
    id         UUID   PRIMARY KEY,
    from_user  UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    to_user    UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id    UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    note       TEXT,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX recommendations_to_idx ON recommendations (to_user, created_at);

-- ──────────────────────────────── Operations ────────────────────────────────

CREATE TABLE jobs (
    id         UUID   PRIMARY KEY,
    type       TEXT   NOT NULL,
    payload    JSONB  NOT NULL DEFAULT '{}',
    status     TEXT   NOT NULL DEFAULT 'queued'
               CHECK (status IN ('queued','running','succeeded','failed','cancelled')),
    priority   BIGINT NOT NULL DEFAULT 0,
    attempts   BIGINT NOT NULL DEFAULT 0,
    max_attempts BIGINT NOT NULL DEFAULT 5,
    run_after  BIGINT NOT NULL,
    locked_at  BIGINT,
    last_error TEXT,
    result     JSONB,
    created_by UUID   REFERENCES users (id) ON DELETE SET NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX jobs_ready_idx ON jobs (status, run_after, priority);

CREATE TABLE reports (
    id          UUID   PRIMARY KEY,
    reporter_id UUID   REFERENCES users (id) ON DELETE SET NULL,
    object_type TEXT   NOT NULL CHECK (object_type IN ('book','review','user')),
    object_id   TEXT   NOT NULL,
    reason      TEXT   NOT NULL,
    details     TEXT,
    status      TEXT   NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved','dismissed')),
    resolution  TEXT,
    resolved_by UUID   REFERENCES users (id) ON DELETE SET NULL,
    resolved_at BIGINT,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE INDEX reports_status_idx ON reports (status, created_at);

CREATE TABLE review_queue (
    id           UUID   PRIMARY KEY,
    book_id      UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    uploader_id  UUID   REFERENCES users (id) ON DELETE SET NULL,
    target_group UUID   NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    status       TEXT   NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    reason       TEXT,
    decided_by   UUID   REFERENCES users (id) ON DELETE SET NULL,
    decided_at   BIGINT,
    created_at   BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL
);
CREATE INDEX review_queue_status_idx ON review_queue (status, created_at);

CREATE TABLE takedowns (
    id         UUID   PRIMARY KEY,
    book_id    UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    reason     TEXT   NOT NULL,
    taken_by   UUID   REFERENCES users (id) ON DELETE SET NULL,
    taken_at   BIGINT NOT NULL,
    lifted_at  BIGINT,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX takedowns_book_idx ON takedowns (book_id) WHERE lifted_at IS NULL;

CREATE TABLE duplicate_candidates (
    id         UUID   PRIMARY KEY,
    book_a_id  UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    book_b_id  UUID   NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    kind       TEXT   NOT NULL CHECK (kind IN ('exact','probable')),
    score      DOUBLE PRECISION,
    status     TEXT   NOT NULL DEFAULT 'open' CHECK (status IN ('open','merged','kept')),
    decided_by UUID   REFERENCES users (id) ON DELETE SET NULL,
    decided_at BIGINT,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX duplicate_candidates_uq ON duplicate_candidates (book_a_id, book_b_id);

-- Append-only. The application never UPDATEs or DELETEs rows here (except the optional pruning job).
CREATE TABLE audit_log (
    id          UUID   PRIMARY KEY,
    actor_id    TEXT,               -- not a FK: the log must survive user anonymization
    action      TEXT   NOT NULL,
    object_type TEXT,
    object_id   TEXT,
    details     JSONB  NOT NULL DEFAULT '{}',
    ip          TEXT,
    at          BIGINT NOT NULL,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE INDEX audit_log_at_idx ON audit_log (at);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_id, at);
CREATE INDEX audit_log_object_idx ON audit_log (object_type, object_id);

CREATE TABLE crash_reports (
    id         UUID   PRIMARY KEY,
    device_id  TEXT   NOT NULL,
    user_id    UUID   REFERENCES users (id) ON DELETE SET NULL,
    app        TEXT   NOT NULL,
    version    TEXT   NOT NULL,
    stacktrace TEXT   NOT NULL,
    at         BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX crash_reports_at_idx ON crash_reports (at);

CREATE TABLE push_subscriptions (
    id                   UUID   PRIMARY KEY,
    device_id            TEXT   NOT NULL,
    user_id              UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    topic                TEXT   NOT NULL,   -- random 128-bit id
    unifiedpush_endpoint TEXT,
    created_at           BIGINT NOT NULL,
    updated_at           BIGINT NOT NULL
);
CREATE UNIQUE INDEX push_subscriptions_device_uq ON push_subscriptions (user_id, device_id);
CREATE UNIQUE INDEX push_subscriptions_topic_uq ON push_subscriptions (topic);

CREATE TABLE settings (
    key        TEXT    PRIMARY KEY,
    value      JSONB  NOT NULL,             -- JSON
    secret     BOOLEAN NOT NULL DEFAULT FALSE, -- value is encrypted with /config/secret.key
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

-- Change log that drives GET /me/sync. One row per change; seq is the sync cursor.
-- user_id NULL = change relevant to everyone who can see the object (e.g. book metadata).
CREATE TABLE sync_changes (
    seq        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    UUID,
    entity     TEXT   NOT NULL,   -- book | library_entry | progress | bookmark | collection
    entity_id  TEXT   NOT NULL,
    op         TEXT   NOT NULL CHECK (op IN ('upsert','delete')),
    at         BIGINT NOT NULL
);
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
