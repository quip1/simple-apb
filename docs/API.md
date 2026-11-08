# Simple APB API

> **Generated** from `server/openapi.yaml` by `web/scripts/gen-api-docs.mjs`. Do not edit by hand.

Version **0.1.0** · base path `/api/v1`

REST API for Simple APB. All endpoints live under `/api/v1`.

**Conventions**
- Every response carries an `X-Server-Version` header.
- IDs are UUIDv7 strings. Positions and durations are integer milliseconds.
- Timestamps are RFC 3339 UTC.
- Errors use the `Error` schema with a stable machine-readable `code`.
- List endpoints use opaque cursor pagination (`cursor` in, `next_cursor` out).
- Clients authenticate with a Bearer access token (apps) or the
  `sapb_session` cookie plus `X-CSRF-Token` header (web).

## Endpoints

### instance

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/health` | Liveness check (used by container healthchecks) | public | 200 |
| GET | `/instance` | Public instance information | public | 200 |

### auth

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| POST | `/auth/signup` | Request an account (creates a pending user awaiting admin approval) | public | 202, 400, 403, 409, 429 |
| POST | `/auth/login` | Log in with username + password | public | 200, 401, 403, 429 |
| POST | `/auth/totp/verify` | Complete login with a TOTP code or a recovery code | public | 200, 401, 429 |
| POST | `/auth/totp/enroll` | Start TOTP enrollment (returns secret + otpauth URI) | yes | 200, 401, 409 |
| POST | `/auth/totp/confirm` | Confirm TOTP enrollment; returns the 10 one-time recovery codes | yes | 200, 400, 401 |
| POST | `/auth/refresh` | Exchange a refresh token for a new token pair (rotating) | public | 200, 401 |
| POST | `/auth/logout` | End the current device session | yes | 204, 401 |

### me

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/me` | The current user | yes | 200, 401 |
| GET | `/me/sessions` | My device sessions | yes | 200, 401 |
| DELETE | `/me/sessions/{sessionId}` | Revoke one of my sessions | yes | 204, 401, 404 |

### library

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/library` | My library (own uploads ∪ distributions, minus removed/hidden/filtered/taken down) | yes | 200, 400, 401 |
| GET | `/books/{bookId}` | Book detail (files, chapters, my progress) | yes | 200, 401, 404 |
| DELETE | `/me/library/{bookId}` | Remove a book from my view (restorable for 30 days; master copy is kept) | yes | 204, 401, 404 |
| POST | `/me/library/{bookId}/restore` | Restore a book from "Recently removed", or un-archive it | yes | 204, 401, 404, 410 |
| POST | `/me/library/{bookId}/hide` | Archive (hide) a book in my library | yes | 204, 401, 404 |
| GET | `/authors/{personId}` | Author page (books visible to me) | yes | 200, 401, 404 |
| GET | `/narrators/{personId}` | Narrator page (books visible to me) | yes | 200, 401, 404 |
| GET | `/series/{seriesId}` | Series page (books in order, visible to me) | yes | 200, 401, 404 |

### progress

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/me/progress/{bookId}` | My stored progress for a book | yes | 200, 401, 404 |
| PUT | `/me/progress/{bookId}` | Report playback position | yes | 200, 400, 401, 404, 409 |
| GET | `/me/sync` | Everything that changed for me since a cursor | yes | 200, 400, 401, 410 |

### media

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/books/{bookId}/files/{fileId}` | Original audio file (supports HTTP Range) | yes | 200, 206, 401, 404, 416 |
| GET | `/stream/{bookId}/hls/{profile}/index.m3u8` | Low-bitrate HLS playlist for the whole book (generated on demand) | yes | 200, 401, 404 |
| GET | `/stream/{bookId}/hls/{profile}/{segment}` | One 10 s HLS segment | yes | 200, 401, 404 |
| POST | `/books/{bookId}/stream-url` | Signed, short-lived stream URLs for casting targets | yes | 200, 401, 404 |
| GET | `/books/{bookId}/cover` | Cover image (resized variant) | yes | 200, 401, 404 |

## Schemas

### Error

| Field | Type | Required | Notes |
|---|---|---|---|
| `code` | string | yes | Stable machine-readable code, e.g. `invalid_credentials`, `account_pending`, `rate_limited`. |
| `message` | string | yes | Human-readable English message (clients show their own translated text by `code`). |
| `details` | object |  |  |

### Health

| Field | Type | Required | Notes |
|---|---|---|---|
| `status` | string: ok \| degraded | yes |  |
| `version` | string | yes |  |

### Instance

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes |  |
| `version` | string | yes | Server semver |
| `api_version` | string | yes |  |
| `min_client_version` | `ClientVersionGuard` | yes |  |
| `signup_open` | boolean | yes |  |
| `setup_required` | boolean | yes | True until the first-run wizard is finished |
| `privacy_text` | string |  |  |
| `terms_text` | string |  |  |
| `copyright_contact` | string |  |  |
| `oidc_providers` | array of `OidcProvider` | yes |  |

### ClientVersionGuard

Clients below `required` must block with "Update required"; below `recommended` show a soft banner.

| Field | Type | Required | Notes |
|---|---|---|---|
| `android` | `VersionPair` |  |  |
| `wear` | `VersionPair` |  |  |
| `web` | `VersionPair` |  |  |
| `windows` | `VersionPair` |  |  |

### VersionPair

| Field | Type | Required | Notes |
|---|---|---|---|
| `required` | string | yes |  |
| `recommended` | string | yes |  |

### OidcProvider

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes |  |
| `display_name` | string | yes |  |

### Platform

Enum: `android`, `wear`, `web`, `windows`, `other`

### SignupRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `username` | string | yes |  |
| `password` | string (password) | yes |  |
| `email` | string (email) |  |  |
| `message` | string |  | Optional note to the admin |
| `age_confirmed` | boolean | yes |  |
| `terms_accepted` | boolean | yes |  |

### SignupAccepted

| Field | Type | Required | Notes |
|---|---|---|---|
| `status` | string: pending | yes |  |

### DeviceInfo

| Field | Type | Required | Notes |
|---|---|---|---|
| `device_id` | string (uuid) | yes | Generated once per install |
| `device_name` | string | yes |  |
| `platform` | `Platform` | yes |  |
| `app_version` | string | yes |  |

### LoginRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `username` | string | yes |  |
| `password` | string (password) | yes |  |
| `device` | `DeviceInfo` | yes |  |

### LoginResponse

| Field | Type | Required | Notes |
|---|---|---|---|
| `status` | string: ok \| totp_required | yes |  |
| `tokens` | `TokenPair` |  |  |
| `totp_challenge` | string |  | Present when `status = totp_required`; valid for 5 minutes. |
| `user` | `Me` |  |  |

### TotpVerifyRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `totp_challenge` | string | yes |  |
| `code` | string |  |  |
| `recovery_code` | string |  |  |

### TotpEnrollment

| Field | Type | Required | Notes |
|---|---|---|---|
| `secret` | string | yes | Base32 secret |
| `otpauth_uri` | string | yes |  |

### TotpConfirmRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `code` | string | yes |  |

### RecoveryCodes

| Field | Type | Required | Notes |
|---|---|---|---|
| `codes` | array of string | yes |  |

### RefreshRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `refresh_token` | string | yes |  |

### TokenPair

| Field | Type | Required | Notes |
|---|---|---|---|
| `access_token` | string | yes |  |
| `access_expires_at` | string (date-time) | yes |  |
| `refresh_token` | string | yes | Empty for web clients (cookie is set instead) |
| `refresh_expires_at` | string (date-time) | yes |  |

### UserRole

Enum: `admin`, `user`

### UserStatus

Enum: `pending`, `active`, `restricted`, `suspended`, `banned`, `deleted`

### Me

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `username` | string | yes |  |
| `display_name` | string | yes |  |
| `email` | string (email) |  |  |
| `role` | `UserRole` | yes |  |
| `status` | `UserStatus` | yes |  |
| `totp_enabled` | boolean | yes |  |
| `quota_bytes` | integer (int64) | yes | 0 = unlimited |
| `used_bytes` | integer (int64) | yes |  |
| `locale` | string | yes |  |
| `permissions` | `EffectivePermissions` | yes |  |
| `notice` | string |  | Moderator warning to show once on login, if any. |

### EffectivePermissions

Result of authz evaluation (status → role → user overrides → tags → instance defaults).

| Field | Type | Required | Notes |
|---|---|---|---|
| `can_upload` | boolean | yes |  |
| `can_edit_metadata` | boolean | yes |  |
| `can_delete_own` | boolean | yes |  |
| `can_share` | boolean | yes |  |
| `can_review` | boolean | yes |  |
| `can_post_public` | boolean | yes |  |

### Session

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `device_id` | string (uuid) | yes |  |
| `device_name` | string | yes |  |
| `platform` | `Platform` | yes |  |
| `app_version` | string | yes |  |
| `last_seen_at` | string (date-time) | yes |  |
| `created_at` | string (date-time) | yes |  |
| `current` | boolean | yes | This is the session making the request |

### PersonRef

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |

### SeriesRef

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `index` | string |  | Position in the series as text (e.g. "1", "2.5") |

### TagRef

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `kind` | string: genre \| custom | yes |  |

### LibrarySource

Enum: `own_upload`, `distribution`, `recommendation`

### BookSummary

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `title` | string | yes |  |
| `subtitle` | string |  |  |
| `authors` | array of `PersonRef` | yes |  |
| `narrators` | array of `PersonRef` | yes |  |
| `series` | `SeriesRef` |  |  |
| `duration_ms` | integer (int64) | yes |  |
| `cover_url` | string | yes | Relative URL; add `?size=` |
| `source` | `LibrarySource` | yes |  |
| `progress` | `Progress` |  |  |
| `added_at` | string (date-time) |  |  |
| `hidden` | boolean |  |  |
| `removed_at` | string (date-time) |  | Set on the recently_removed shelf |
| `updated_at` | string (date-time) | yes |  |

### BookDetail

| Field | Type | Required | Notes |
|---|---|---|---|
| _all fields of_ `BookSummary` | | | |
| `description` | string |  |  |
| `publisher` | string |  |  |
| `published_year` | integer |  |  |
| `language` | string |  |  |
| `isbn` | string |  |  |
| `asin` | string |  |  |
| `size_bytes` | integer (int64) | yes |  |
| `has_epub` | boolean | yes |  |
| `tags` | array of `TagRef` | yes |  |
| `files` | array of `BookFile` | yes |  |
| `chapters` | array of `Chapter` | yes |  |

### BookFile

One audio file. `offset_ms` maps it onto the global book timeline.

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `index` | integer | yes |  |
| `offset_ms` | integer (int64) | yes |  |
| `duration_ms` | integer (int64) | yes |  |
| `size_bytes` | integer (int64) | yes |  |
| `codec` | string | yes |  |
| `bitrate_kbps` | integer |  |  |
| `mime_type` | string | yes |  |
| `sha256` | string | yes |  |

### Chapter

Chapter on the global book timeline (across files).

| Field | Type | Required | Notes |
|---|---|---|---|
| `index` | integer | yes |  |
| `title` | string | yes |  |
| `start_ms` | integer (int64) | yes |  |
| `end_ms` | integer (int64) | yes |  |

### LibraryPage

| Field | Type | Required | Notes |
|---|---|---|---|
| `items` | array of `BookSummary` | yes |  |
| `next_cursor` | string |  | Absent on the last page |

### PersonPage

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `books` | array of `BookSummary` | yes |  |

### SeriesPage

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `books` | array of `BookSummary` | yes |  |

### Progress

| Field | Type | Required | Notes |
|---|---|---|---|
| `book_id` | string (uuid) | yes |  |
| `position_ms` | integer (int64) | yes |  |
| `device_id` | string (uuid) | yes |  |
| `device_name` | string |  |  |
| `client_ts` | string (date-time) | yes |  |
| `updated_at` | string (date-time) | yes | Server receive time |
| `speed` | number (float) | yes |  |
| `finished_at` | string (date-time) |  |  |

### ProgressUpdate

| Field | Type | Required | Notes |
|---|---|---|---|
| `position_ms` | integer (int64) | yes |  |
| `device_id` | string (uuid) | yes |  |
| `client_ts` | string (date-time) | yes |  |
| `speed` | number (float) | yes |  |
| `finished` | boolean |  |  |
| `last_synced_at` | string (date-time) |  | When this device last successfully synced progress for this book. Used to detect writes made in the same offline window. |
| `resolution` | string |  | Echo a `conflict_id` from a 409 to force this write after the user chose. |

### ProgressWriteResult

| Field | Type | Required | Notes |
|---|---|---|---|
| `applied` | boolean | yes | False when the write was older (by `client_ts`) than the stored record and was ignored. |
| `progress` | `Progress` | yes |  |

### ProgressConflict

| Field | Type | Required | Notes |
|---|---|---|---|
| `code` | string: progress_conflict | yes |  |
| `conflict_id` | string | yes |  |
| `server` | `Progress` | yes |  |
| `incoming` | `Progress` | yes |  |

### Tombstone

| Field | Type | Required | Notes |
|---|---|---|---|
| `entity` | string: library_entry \| progress \| bookmark \| collection | yes |  |
| `id` | string | yes | Entity id (for library_entry and progress: the book id) |
| `deleted_at` | string (date-time) | yes |  |

### SyncDelta

| Field | Type | Required | Notes |
|---|---|---|---|
| `cursor` | string | yes | Pass as `since` next time |
| `has_more` | boolean | yes |  |
| `full` | boolean |  | True when this is a full snapshot (no `since`) |
| `library` | array of `BookSummary` | yes | Books that became visible or whose metadata changed |
| `progress` | array of `Progress` | yes |  |
| `tombstones` | array of `Tombstone` | yes |  |

### HlsProfile

`low` = HE-AAC 32 kbps mono, `medium` = AAC 64 kbps stereo

Enum: `low`, `medium`

### StreamUrlRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `profile` | string: original \| low \| medium |  |  |

### StreamUrls

| Field | Type | Required | Notes |
|---|---|---|---|
| `expires_at` | string (date-time) | yes |  |
| `files` | array of object |  | Present for `original` |
| `hls_url` | string |  | Present for `low`/`medium` |
| `cover_url` | string |  |  |

