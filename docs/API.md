# Simple APB API

> **Generated** from `server/openapi.yaml` by `web/scripts/gen-api-docs.mjs`. Do not edit by hand.

Version **0.10.0** · base path `/api/v1`

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
| POST | `/auth/password-reset` | Set a new password with a reset token (signs out all sessions) | public | 204, 400, 429 |

### me

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/me` | The current user | yes | 200, 401 |
| PATCH | `/me` | Edit my profile and preferences | yes | 200, 400, 401, 409 |
| POST | `/me/password` | Change my password (signs out my other devices) | yes | 204, 400, 401, 429 |
| POST | `/me/totp/disable` | Turn off two-factor authentication | yes | 204, 401, 403, 429 |
| POST | `/me/recovery-codes` | Replace my recovery codes (the old ones stop working) | yes | 200, 401, 409, 429 |
| GET | `/me/stats` | My listening stats for a year (charts and year in review) | yes | 200, 400, 401 |
| GET | `/me/sessions` | My device sessions | yes | 200, 401 |
| DELETE | `/me/sessions` | Sign out all my other devices | yes | 204, 401 |
| DELETE | `/me/sessions/{sessionId}` | Revoke one of my sessions | yes | 204, 401, 404 |
| POST | `/devices/push-subscription` | Register this device for push notifications | yes | 200, 400, 401 |
| DELETE | `/devices/push-subscription/{deviceId}` | Stop push notifications for a device | yes | 204, 401, 404 |

### library

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/library` | My library (own uploads ∪ distributions, minus removed/hidden/filtered/taken down) | yes | 200, 400, 401 |
| GET | `/library/facets` | Series, authors, narrators or tags in my library, with book counts | yes | 200, 400, 401 |
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
| GET | `/me/bookmarks` | My bookmarks (optionally for one book) | yes | 200, 401 |
| POST | `/me/bookmarks` | Add a bookmark | yes | 201, 400, 401, 404 |
| PATCH | `/me/bookmarks/{bookmarkId}` | Edit a bookmark (last write wins by client_ts) | yes | 200, 401, 404 |
| DELETE | `/me/bookmarks/{bookmarkId}` | Delete a bookmark (leaves a tombstone for other devices) | yes | 204, 401, 404 |
| GET | `/me/collections` | My collections | yes | 200, 401 |
| POST | `/me/collections` | Create a collection | yes | 201, 400, 401 |
| PATCH | `/me/collections/{collectionId}` | Rename or describe a collection (last write wins by client_ts) | yes | 200, 401, 404 |
| DELETE | `/me/collections/{collectionId}` | Delete a collection (books are not affected) | yes | 204, 401, 404 |
| PUT | `/me/collections/{collectionId}/books/{bookId}` | Add a book to a collection | yes | 204, 401, 404 |
| DELETE | `/me/collections/{collectionId}/books/{bookId}` | Remove a book from a collection | yes | 204, 401, 404 |

### media

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/books/{bookId}/files/{fileId}` | Original audio file (supports HTTP Range) | yes | 200, 206, 401, 404, 416 |
| GET | `/stream/{bookId}/hls/{profile}/index.m3u8` | Low-bitrate HLS playlist for the whole book (generated on demand) | yes | 200, 401, 404 |
| GET | `/stream/{bookId}/hls/{profile}/{segment}` | One 10 s HLS segment | yes | 200, 401, 404 |
| POST | `/books/{bookId}/stream-url` | Signed, short-lived stream URLs for casting targets | yes | 200, 401, 404 |
| GET | `/books/{bookId}/cover` | Cover image (resized variant) | yes | 200, 401, 404 |

### setup

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/setup/status` | Whether the first-run wizard still needs to run | public | 200 |
| POST | `/setup/database/test` | Check that a database can be opened | yes | 200, 401, 409 |
| POST | `/setup/library/test` | Check the library path exists and is writable (warns if empty) | yes | 200, 401, 409 |
| POST | `/setup/smtp/test` | Send a test email with the given SMTP settings | yes | 200, 401, 409 |
| POST | `/setup/totp` | Generate the first admin's TOTP secret (enrollment is mandatory) | yes | 200, 401, 409 |
| POST | `/setup/complete` | Finish the wizard (creates the admin, stores settings, switches the server to normal mode) | yes | 200, 400, 401, 409 |

### uploads

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/me/uploads` | My recent uploads and their processing status | yes | 200, 401 |
| GET | `/jobs/{jobId}` | Status of a background job you started (admins can see any) | yes | 200, 401, 404 |

### social

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/books/{bookId}/reviews` | Ratings and reviews of a book, newest first, with the average | yes | 200, 400, 401, 404 |
| PUT | `/books/{bookId}/reviews/mine` | Rate and review a book (creates or replaces your review; needs `can_review`) | yes | 200, 400, 401, 403, 404 |
| DELETE | `/books/{bookId}/reviews/mine` | Delete your review of a book | yes | 204, 401, 403, 404 |
| POST | `/books/{bookId}/recommend` | Recommend a book to other people | yes | 200, 400, 401, 403, 404 |
| GET | `/me/recommendations` | Books other people recommended to you (only books you can see) | yes | 200, 401 |
| DELETE | `/me/recommendations/{recommendationId}` | Dismiss a recommendation | yes | 204, 401, 404 |
| GET | `/books/{bookId}/listeners` | Who's listening to (or finished) a book | yes | 200, 401, 404 |
| POST | `/reports` | Report a book, a review or a person to the admins | yes | 201, 400, 401, 403, 404, 429 |
| GET | `/people` | Find people to recommend or share to (active users in your groups) | yes | 200, 400, 401, 403 |
| GET | `/me/groups` | The groups you belong to (share targets) | yes | 200, 401 |
| GET | `/books/{bookId}/shares` | Your shares of a book you uploaded, and your requests to share it publicly | yes | 200, 401, 403, 404 |
| POST | `/books/{bookId}/shares` | Share a book you uploaded with a person or one of your groups | yes | 201, 400, 401, 403, 404, 409 |
| DELETE | `/books/{bookId}/shares/{shareId}` | Stop sharing (or withdraw a pending public request) | yes | 204, 401, 404 |
| GET | `/me/collections/{collectionId}/shares` | Who a collection of yours is shared with | yes | 200, 401, 404 |
| POST | `/me/collections/{collectionId}/shares` | Share a collection with a person or one of your groups | yes | 201, 400, 401, 403, 404 |
| DELETE | `/me/collections/{collectionId}/shares/{shareId}` | Stop sharing a collection with someone | yes | 204, 401, 404 |
| GET | `/shared-collections` | Collections other people shared with you or your groups | yes | 200, 401 |

### admin

| Method | Path | Summary | Auth | Responses |
|---|---|---|---|---|
| GET | `/admin/signup-requests` | Sign-up requests (moderation inbox) | yes | 200, 401, 403 |
| POST | `/admin/signup-requests/{requestId}/approve` | Approve a sign-up (user becomes active and joins Public) | yes | 204, 401, 403, 404 |
| POST | `/admin/signup-requests/{requestId}/deny` | Deny a sign-up | yes | 204, 401, 403, 404 |
| GET | `/admin/users` | Users | yes | 200, 401, 403 |
| GET | `/admin/users/{userId}` | One user with tags, groups and permissions | yes | 200, 401, 403, 404 |
| PATCH | `/admin/users/{userId}` | Change a user's status, role, quota or notice (a reason is required) | yes | 200, 400, 401, 403, 404 |
| GET | `/admin/groups` | Groups | yes | 200, 401, 403 |
| POST | `/admin/groups` | Create a group | yes | 201, 400, 401, 403, 409 |
| PATCH | `/admin/groups/{groupId}` | Rename a group or change its bandwidth priority | yes | 200, 400, 401, 403, 404, 409 |
| DELETE | `/admin/groups/{groupId}` | Delete a group (the built-in Public group cannot be deleted) | yes | 204, 401, 403, 404 |
| PUT | `/admin/groups/{groupId}/members/{userId}` | Add a user to a group | yes | 204, 401, 403, 404 |
| DELETE | `/admin/groups/{groupId}/members/{userId}` | Remove a user from a group | yes | 204, 401, 403, 404 |
| DELETE | `/admin/sessions/{sessionId}` | Revoke any user's session | yes | 204, 401, 403, 404 |
| GET | `/admin/dashboard` | Live sessions, server health, moderation inbox counts (brief §6.23) | yes | 200, 401, 403 |
| GET | `/admin/growth` | Users, books, storage and listening per month | yes | 200, 400, 401, 403 |
| PUT | `/admin/users/{userId}/permissions` | Replace a user's permission overrides (a missing key = use tags/defaults) | yes | 204, 400, 401, 403, 404 |
| PUT | `/admin/users/{userId}/tags/{tagId}` | Give a user a tag | yes | 204, 401, 403, 404 |
| DELETE | `/admin/users/{userId}/tags/{tagId}` | Take a tag off a user | yes | 204, 401, 403, 404 |
| GET | `/admin/users/{userId}/sessions` | A user's signed-in devices | yes | 200, 401, 403, 404 |
| POST | `/admin/users/{userId}/reset-link` | A one-time password reset link for a user (valid 24 h; stops working once used) | yes | 200, 401, 403, 404 |
| GET | `/admin/user-tags` | User tags (capability overrides, required 2FA) | yes | 200, 401, 403 |
| POST | `/admin/user-tags` | Create a user tag | yes | 201, 400, 401, 403, 409 |
| PATCH | `/admin/user-tags/{tagId}` | Edit a user tag (fields present are changed) | yes | 200, 400, 401, 403, 404, 409 |
| DELETE | `/admin/user-tags/{tagId}` | Delete a user tag (removes it from everyone) | yes | 204, 401, 403, 404 |
| GET | `/admin/groups/{groupId}/members` | Members of a group | yes | 200, 401, 403, 404 |
| POST | `/admin/books/{bookId}/takedown` | Hide a book from everyone now (files are kept; brief §6.5 rule 8) | yes | 204, 400, 401, 403, 404, 409 |
| DELETE | `/admin/books/{bookId}/takedown` | Lift a takedown (the book is visible again) | yes | 204, 401, 403, 404 |
| GET | `/admin/audit` | The audit log, newest first | yes | 200, 400, 401, 403 |
| GET | `/admin/audit/export` | The filtered audit log as CSV (at most 100 000 rows) | yes | 200, 400, 401, 403 |
| GET | `/admin/settings` | Instance settings (secrets are never returned, only whether they're set) | yes | 200, 401, 403 |
| PATCH | `/admin/settings` | Change instance settings (groups and fields present are changed) | yes | 200, 400, 401, 403 |
| POST | `/admin/settings/smtp/test` | Send a test email with the saved SMTP settings | yes | 200, 401, 403 |
| GET | `/admin/books` | Browse the master store (every book the server holds) | yes | 200, 401, 403 |
| GET | `/admin/books/{bookId}` | One master-store book with its distributions | yes | 200, 401, 403, 404 |
| DELETE | `/admin/books/{bookId}` | Move a book to the Trash (hidden everywhere; purged after 30 days) | yes | 204, 401, 403, 404 |
| POST | `/admin/books/{bookId}/restore` | Restore a book from the Trash | yes | 204, 401, 403, 404, 409 |
| POST | `/admin/books/{bookId}/purge` | Permanently delete one trashed book and its files now | yes | 204, 401, 403, 404, 409 |
| POST | `/admin/trash/empty` | Permanently delete everything in the Trash | yes | 200, 400, 401, 403 |
| POST | `/admin/distributions` | Make a master-store book visible to a group or a user | yes | 201, 400, 401, 403, 404 |
| DELETE | `/admin/distributions/{distributionId}` | Revoke a distribution | yes | 204, 401, 403, 404 |
| POST | `/admin/library/scan` | Rescan the library folder now | yes | 202, 401, 403 |
| GET | `/admin/backups` | Database backups and the latest backup/scrub status | yes | 200, 401, 403 |
| POST | `/admin/backups` | Back up the database now | yes | 202, 401, 403 |
| POST | `/admin/integrity-check` | Re-hash every library file against its stored checksum now | yes | 202, 401, 403 |
| GET | `/admin/review-queue` | Requests to share books with Public, oldest first | yes | 200, 400, 401, 403 |
| POST | `/admin/review-queue/{itemId}/approve` | Approve a request (the book is shared with the group) | yes | 204, 401, 403, 404, 409 |
| POST | `/admin/review-queue/{itemId}/reject` | Reject a request (a reason is required and shown to the uploader) | yes | 204, 400, 401, 403, 404, 409 |
| GET | `/admin/reports` | Reports, oldest open first (or newest first for closed ones) | yes | 200, 400, 401, 403 |
| POST | `/admin/reports/{reportId}/resolve` | Close a report, and every other open report about the same thing | yes | 204, 400, 401, 403, 404, 409 |
| POST | `/admin/reviews/{reviewId}/hide` | Hide a review from everyone but its author (a reason is required) | yes | 204, 400, 401, 403, 404 |
| DELETE | `/admin/reviews/{reviewId}/hide` | Show a hidden review again | yes | 204, 401, 403, 404 |

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
| `totp_required` | boolean | yes | 2FA is required for this account (admin, a tag, or instance-wide) |
| `conflict_threshold_minutes` | integer | yes | Progress differences larger than this between devices ask which position to keep |
| `listening_visible` | boolean | yes | Show me in a book's "who's listening" |
| `prefs` | object | yes | Client preferences synced across devices (opaque to the server, at most 8 KB) |

### MePatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `display_name` | string |  |  |
| `email` | string |  | Empty string removes it |
| `locale` | string |  |  |
| `conflict_threshold_minutes` | integer |  |  |
| `listening_visible` | boolean |  |  |
| `prefs` | object |  |  |

### PasswordChange

| Field | Type | Required | Notes |
|---|---|---|---|
| `current_password` | string (password) | yes |  |
| `new_password` | string (password) | yes |  |

### TotpDisable

| Field | Type | Required | Notes |
|---|---|---|---|
| `password` | string (password) | yes |  |
| `code` | string | yes |  |

### TotpCode

| Field | Type | Required | Notes |
|---|---|---|---|
| `code` | string | yes |  |

### MyStats

| Field | Type | Required | Notes |
|---|---|---|---|
| `year` | integer | yes |  |
| `years` | array of integer | yes | Years with any listening, newest first |
| `total_ms` | integer (int64) | yes |  |
| `active_days` | integer | yes |  |
| `longest_streak_days` | integer | yes | Fun fact only (no streak features) |
| `books_finished` | integer | yes |  |
| `days` | array of `StatsDay` | yes | Days with listening, in order |
| `books` | array of `StatsBook` | yes | Time per book, most first (top 50) |
| `authors` | array of `StatsAuthor` | yes | Top authors by listening time (top 20) |
| `finished` | array of `StatsBook` | yes | Books finished this year, most recent first |

### StatsDay

| Field | Type | Required | Notes |
|---|---|---|---|
| `date` | string (date) | yes |  |
| `listened_ms` | integer (int64) | yes |  |

### StatsAuthor

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `listened_ms` | integer (int64) | yes |  |

### StatsBook

| Field | Type | Required | Notes |
|---|---|---|---|
| `book_id` | string (uuid) | yes |  |
| `title` | string | yes |  |
| `authors` | array of `PersonRef` | yes |  |
| `cover_url` | string | yes |  |
| `has_cover` | boolean | yes |  |
| `listened_ms` | integer (int64) | yes | This year |
| `finished_at` | string (date-time) |  |  |

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
| `has_cover` | boolean | yes | False when the book has no cover, so clients can skip the request |
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
| `uploaded_by_me` | boolean |  | You uploaded it (you may share it, given the permission) |
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

### LibraryFacet

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `count` | integer | yes | Visible books |
| `kind` | string: genre \| custom |  | Tags only |

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
| `bookmarks` | array of `Bookmark` | yes |  |
| `collections` | array of `Collection` | yes |  |
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

### SetupStatus

| Field | Type | Required | Notes |
|---|---|---|---|
| `setup_required` | boolean | yes |  |
| `database_configured` | boolean |  | A database is already chosen (config.json exists); the wizard will reuse it |

### SetupDatabase

| Field | Type | Required | Notes |
|---|---|---|---|
| `dialect` | string: sqlite \| postgres | yes |  |
| `dsn` | string |  | Postgres connection string. Ignored for SQLite (stored at /config/simpleapb.db). |

### SetupCheckResult

| Field | Type | Required | Notes |
|---|---|---|---|
| `ok` | boolean | yes |  |
| `message` | string |  |  |

### SetupLibrary

| Field | Type | Required | Notes |
|---|---|---|---|
| `path` | string | yes |  |

### LibraryCheck

| Field | Type | Required | Notes |
|---|---|---|---|
| `exists` | boolean | yes |  |
| `writable` | boolean | yes |  |
| `empty` | boolean | yes | True when the folder has no files yet (a warning, not an error) |
| `message` | string |  |  |

### SmtpSettings

| Field | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes |  |
| `port` | integer | yes |  |
| `tls` | string: starttls \| tls \| none | yes |  |
| `username` | string |  |  |
| `password` | string (password) |  |  |
| `from` | string (email) | yes |  |

### SmtpTestRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `smtp` | `SmtpSettings` | yes |  |
| `to` | string (email) | yes |  |

### NtfySettings

| Field | Type | Required | Notes |
|---|---|---|---|
| `internal_url` | string |  | How the server reaches ntfy, e.g. http://ntfy:80 |
| `public_url` | string |  | How devices reach ntfy |
| `token` | string |  | Access token; generated automatically when empty |

### SetupAdmin

| Field | Type | Required | Notes |
|---|---|---|---|
| `username` | string | yes |  |
| `password` | string (password) | yes |  |
| `display_name` | string |  |  |
| `email` | string (email) |  |  |
| `totp_secret` | string | yes | Secret from POST /setup/totp |
| `totp_code` | string | yes |  |

### SetupRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `admin` | `SetupAdmin` | yes |  |
| `database` | `SetupDatabase` | yes |  |
| `library_path` | string | yes |  |
| `base_url` | string | yes | Public base URL, e.g. https://books.example.com |
| `smtp` | `SmtpSettings` |  |  |
| `ntfy` | `NtfySettings` |  |  |
| `instance_name` | string | yes |  |
| `privacy_text` | string |  |  |
| `terms_text` | string |  |  |
| `signup_open` | boolean |  |  |
| `egress_mbps` | integer |  |  |

### SetupResult

| Field | Type | Required | Notes |
|---|---|---|---|
| `admin_id` | string (uuid) | yes |  |
| `recovery_codes` | array of string | yes | The admin's one-time recovery codes. Shown once. |

### SignupRequestItem

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `user_id` | string (uuid) | yes |  |
| `username` | string | yes |  |
| `email` | string |  |  |
| `message` | string |  |  |
| `status` | string: pending \| approved \| denied | yes |  |
| `created_at` | string (date-time) | yes |  |
| `decided_at` | string (date-time) |  |  |

### ReasonRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `reason` | string |  |  |

### AdminUser

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `username` | string | yes |  |
| `display_name` | string | yes |  |
| `email` | string |  |  |
| `role` | `UserRole` | yes |  |
| `status` | `UserStatus` | yes |  |
| `status_reason` | string |  |  |
| `status_until` | string (date-time) |  |  |
| `totp_enabled` | boolean | yes |  |
| `quota_bytes` | integer (int64) | yes |  |
| `created_at` | string (date-time) | yes |  |

### AdminUserPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `reason` | string | yes | Why; written to the audit log |
| `status` | string: active \| restricted \| suspended \| banned |  |  |
| `status_until` | string (date-time) |  | Suspension end (status = suspended) |
| `role` | `UserRole` |  |  |
| `quota_bytes` | integer (int64) |  |  |
| `notice` | string |  | Warning shown to the user on next login; empty clears it |

### Group

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `builtin` | boolean | yes |  |
| `high_priority` | boolean | yes | Bandwidth priority class |
| `members` | integer | yes |  |

### GroupCreate

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes |  |
| `high_priority` | boolean |  |  |

### UploadItem

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes |  |
| `filename` | string | yes |  |
| `size_bytes` | integer (int64) | yes |  |
| `received_bytes` | integer (int64) | yes |  |
| `status` | string: receiving \| processing \| done \| failed \| cancelled | yes |  |
| `error` | string |  | Why processing failed (shown to the user) |
| `book_id` | string (uuid) |  |  |
| `created_at` | string (date-time) | yes |  |
| `updated_at` | string (date-time) | yes |  |

### JobStatus

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `type` | string | yes |  |
| `status` | string: queued \| running \| succeeded \| failed \| cancelled | yes |  |
| `attempts` | integer | yes |  |
| `last_error` | string |  |  |
| `result` | object |  |  |
| `created_at` | string (date-time) | yes |  |
| `updated_at` | string (date-time) | yes |  |

### BookStatus

Enum: `processing`, `ready`, `quarantined`, `trashed`

### MasterBook

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `title` | string | yes |  |
| `authors` | array of `PersonRef` | yes |  |
| `series` | `SeriesRef` |  |  |
| `status` | `BookStatus` | yes |  |
| `status_reason` | string |  |  |
| `origin` | string: scan \| upload \| abs_import | yes |  |
| `uploader_id` | string (uuid) |  |  |
| `folder_path` | string | yes | Relative to the library root |
| `size_bytes` | integer (int64) | yes |  |
| `duration_ms` | integer (int64) | yes |  |
| `trashed_at` | string (date-time) |  |  |
| `purge_at` | string (date-time) |  | When the Trash will purge it automatically |
| `created_at` | string (date-time) | yes |  |
| `distributions` | array of `Distribution` | yes |  |
| `takedown` | `Takedown` |  |  |

### MasterBookPage

| Field | Type | Required | Notes |
|---|---|---|---|
| `items` | array of `MasterBook` | yes |  |
| `next_cursor` | string |  |  |

### DistributionCreate

| Field | Type | Required | Notes |
|---|---|---|---|
| `book_id` | string (uuid) | yes |  |
| `target_type` | string: group \| user | yes |  |
| `target_id` | string (uuid) | yes |  |
| `note` | string |  |  |

### Distribution

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `book_id` | string (uuid) | yes |  |
| `target_type` | string: group \| user | yes |  |
| `target_id` | string (uuid) | yes |  |
| `target_name` | string | yes |  |
| `note` | string |  |  |
| `created_at` | string (date-time) | yes |  |

### EmptyTrashRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `confirm_count` | integer | yes | Must equal the number of books in the Trash |
| `password` | string (password) | yes |  |
| `totp_code` | string | yes |  |

### AdminDashboard

| Field | Type | Required | Notes |
|---|---|---|---|
| `version` | string | yes |  |
| `started_at` | string (date-time) | yes |  |
| `uptime_s` | integer (int64) | yes |  |
| `egress` | `DashboardEgress` | yes |  |
| `live` | array of `LiveStream` | yes |  |
| `health` | `ServerHealth` | yes |  |
| `last_backup` | `OpStatus` |  |  |
| `last_scrub` | `OpStatus` |  |  |
| `inbox` | `InboxCounts` | yes |  |

### LiveStream

One open audio response (a stream or a download)

| Field | Type | Required | Notes |
|---|---|---|---|
| `user_id` | string (uuid) | yes |  |
| `username` | string | yes |  |
| `book_id` | string (uuid) | yes |  |
| `book_title` | string | yes |  |
| `kind` | string: live \| download | yes |  |
| `priority` | string: admin \| priority \| normal | yes |  |
| `bitrate_kbps` | integer |  |  |
| `rate_bps` | integer (int64) | yes |  |
| `sent_bytes` | integer (int64) | yes |  |
| `since` | string (date-time) | yes |  |

### ServerHealth

| Field | Type | Required | Notes |
|---|---|---|---|
| `db_bytes` | integer (int64) | yes |  |
| `disks` | array of `DiskUsage` | yes |  |
| `zfs` | string | yes | ZFS pool status, or "unknown" |
| `job_queue_depth` | integer | yes |  |
| `process_memory_bytes` | integer (int64) | yes |  |
| `memory_total_bytes` | integer (int64) |  | Linux only |
| `memory_available_bytes` | integer (int64) |  | Linux only |
| `load_average` | number |  | 1-minute load average (Linux only) |
| `cpus` | integer |  |  |

### GrowthPoint

| Field | Type | Required | Notes |
|---|---|---|---|
| `month` | string | yes |  |
| `users` | integer | yes | Active accounts at the end of the month |
| `books` | integer | yes | Books in the master store at the end of the month |
| `storage_bytes` | integer (int64) | yes |  |
| `listening_ms` | integer (int64) | yes | Listening during the month, all users |

### UserRef

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `username` | string | yes |  |
| `display_name` | string | yes |  |

### AdminUserDetail

| Field | Type | Required | Notes |
|---|---|---|---|
| _all fields of_ `AdminUser` | | | |
| `notice` | string |  |  |
| `used_bytes` | integer (int64) | yes |  |
| `last_login_at` | string (date-time) |  |  |
| `tags` | array of `UserTag` | yes |  |
| `groups` | array of `Group` | yes |  |
| `permissions` | `PermissionOverridesInput` | yes |  |
| `effective` | `EffectivePermissions` | yes |  |

### PermissionOverridesInput

Explicit values; a missing key means "not overridden"

| Field | Type | Required | Notes |
|---|---|---|---|
| `can_upload` | boolean |  |  |
| `can_edit_metadata` | boolean |  |  |
| `can_delete_own` | boolean |  |  |
| `can_share` | boolean |  |  |
| `can_review` | boolean |  |  |
| `can_post_public` | boolean |  |  |

### UserTag

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `color` | string |  |  |
| `require_totp` | boolean | yes |  |
| `capability_overrides` | `PermissionOverridesInput` | yes |  |
| `members` | integer | yes |  |

### UserTagInput

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes |  |
| `color` | string |  |  |
| `require_totp` | boolean |  |  |
| `capability_overrides` | `PermissionOverridesInput` |  |  |

### UserTagPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string |  |  |
| `color` | string |  | Empty clears it |
| `require_totp` | boolean |  |  |
| `capability_overrides` | `PermissionOverridesInput` |  |  |

### GroupPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string |  |  |
| `high_priority` | boolean |  |  |

### ResetLink

| Field | Type | Required | Notes |
|---|---|---|---|
| `url` | string | yes |  |
| `expires_at` | string (date-time) | yes |  |

### PasswordReset

| Field | Type | Required | Notes |
|---|---|---|---|
| `token` | string | yes |  |
| `new_password` | string (password) | yes |  |

### Takedown

| Field | Type | Required | Notes |
|---|---|---|---|
| `reason` | string | yes |  |
| `taken_at` | string (date-time) | yes |  |
| `taken_by` | string (uuid) |  |  |

### AuditEntry

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `at` | string (date-time) | yes |  |
| `actor_id` | string (uuid) |  |  |
| `actor_name` | string |  |  |
| `action` | string | yes |  |
| `object_type` | string |  |  |
| `object_id` | string |  |  |
| `details` | object | yes |  |
| `ip` | string |  |  |

### AuditPage

| Field | Type | Required | Notes |
|---|---|---|---|
| `items` | array of `AuditEntry` | yes |  |
| `next_cursor` | string |  |  |

### AdminSettings

| Field | Type | Required | Notes |
|---|---|---|---|
| `instance` | `InstanceSettings` | yes |  |
| `auth` | `AuthSettings` | yes |  |
| `bandwidth` | `BandwidthSettings` | yes |  |
| `quotas` | `QuotaSettings` | yes |  |
| `permission_defaults` | `EffectivePermissions` | yes |  |
| `smtp` | `SmtpSettingsView` | yes |  |
| `ntfy` | `NtfySettingsView` | yes |  |

### AdminSettingsPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `instance` | `InstanceSettingsPatch` |  |  |
| `auth` | `AuthSettingsPatch` |  |  |
| `bandwidth` | `BandwidthSettingsPatch` |  |  |
| `quotas` | `QuotaSettingsPatch` |  |  |
| `permission_defaults` | `PermissionOverridesInput` |  |  |
| `smtp` | `SmtpSettingsPatch` |  |  |
| `ntfy` | `NtfySettingsPatch` |  |  |

### DashboardEgress

| Field | Type | Required | Notes |
|---|---|---|---|
| `cap_bps` | integer (int64) | yes | Global egress cap |
| `used_bps` | integer (int64) | yes | Average rate of the open streams |

### InboxCounts

| Field | Type | Required | Notes |
|---|---|---|---|
| `signup_requests` | integer | yes |  |
| `review_queue` | integer | yes |  |
| `reports` | integer | yes |  |

### InstanceSettings

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes |  |
| `base_url` | string | yes |  |
| `signup_open` | boolean | yes |  |
| `privacy_text` | string | yes |  |
| `terms_text` | string | yes |  |
| `copyright_contact` | string | yes |  |

### AuthSettings

| Field | Type | Required | Notes |
|---|---|---|---|
| `require_totp_all` | boolean | yes |  |

### BandwidthSettings

| Field | Type | Required | Notes |
|---|---|---|---|
| `egress_mbps` | integer | yes |  |
| `hls_cache_gb` | integer | yes |  |

### QuotaSettings

| Field | Type | Required | Notes |
|---|---|---|---|
| `default_quota_bytes` | integer (int64) | yes | Given to newly approved accounts; 0 = unlimited |

### SmtpSettingsView

| Field | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes |  |
| `port` | integer | yes |  |
| `tls` | string: starttls \| tls \| none | yes |  |
| `username` | string | yes |  |
| `from` | string | yes |  |
| `password_set` | boolean | yes |  |

### NtfySettingsView

| Field | Type | Required | Notes |
|---|---|---|---|
| `internal_url` | string | yes |  |
| `public_url` | string | yes |  |
| `token_set` | boolean | yes |  |

### InstanceSettingsPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string |  |  |
| `base_url` | string |  |  |
| `signup_open` | boolean |  |  |
| `privacy_text` | string |  |  |
| `terms_text` | string |  |  |
| `copyright_contact` | string |  |  |

### AuthSettingsPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `require_totp_all` | boolean |  |  |

### BandwidthSettingsPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `egress_mbps` | integer |  |  |
| `hls_cache_gb` | integer |  |  |

### QuotaSettingsPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `default_quota_bytes` | integer (int64) |  |  |

### SmtpSettingsPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `host` | string |  |  |
| `port` | integer |  |  |
| `tls` | string: starttls \| tls \| none |  |  |
| `username` | string |  |  |
| `password` | string (password) |  | Write-only; empty clears it |
| `from` | string |  |  |

### NtfySettingsPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `internal_url` | string |  |  |
| `public_url` | string |  |  |
| `token` | string |  | Write-only; empty clears it |

### DiskUsage

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string: config \| library \| cache \| backups | yes |  |
| `free_bytes` | integer (int64) | yes |  |
| `total_bytes` | integer (int64) | yes |  |

### Bookmark

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `book_id` | string (uuid) | yes |  |
| `position_ms` | integer (int64) | yes |  |
| `note` | string |  |  |
| `updated_at` | string (date-time) | yes | Last-write-wins clock (client time, clamped) |

### BookmarkCreate

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) |  | Client-generated id for idempotent offline creation |
| `book_id` | string (uuid) | yes |  |
| `position_ms` | integer (int64) | yes |  |
| `note` | string |  |  |
| `client_ts` | string (date-time) |  |  |

### BookmarkPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `position_ms` | integer (int64) |  |  |
| `note` | string |  |  |
| `client_ts` | string (date-time) |  |  |

### Collection

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `description` | string |  |  |
| `book_ids` | array of string | yes | Books in order (only books you can currently see) |
| `updated_at` | string (date-time) | yes |  |

### CollectionCreate

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) |  |  |
| `name` | string | yes |  |
| `description` | string |  |  |
| `client_ts` | string (date-time) |  |  |

### CollectionPatch

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string |  |  |
| `description` | string |  |  |
| `client_ts` | string (date-time) |  |  |

### PushRegistration

| Field | Type | Required | Notes |
|---|---|---|---|
| `device_id` | string (uuid) | yes |  |
| `endpoint` | string |  | UnifiedPush endpoint URL (https) |

### PushSubscription

| Field | Type | Required | Notes |
|---|---|---|---|
| `device_id` | string (uuid) | yes |  |
| `topic` | string | yes | Random server topic (128-bit) on this instance's ntfy |
| `ntfy_url` | string |  | Full topic URL to subscribe to, if ntfy is configured |
| `endpoint` | string |  |  |

### JobRef

| Field | Type | Required | Notes |
|---|---|---|---|
| `job_id` | string (uuid) | yes |  |

### BackupFile

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes |  |
| `size_bytes` | integer (int64) | yes |  |
| `created_at` | string (date-time) | yes |  |

### OpStatus

| Field | Type | Required | Notes |
|---|---|---|---|
| `at` | string (date-time) |  |  |
| `ok` | boolean |  |  |
| `message` | string |  |  |

### BackupStatus

| Field | Type | Required | Notes |
|---|---|---|---|
| `backups` | array of `BackupFile` | yes |  |
| `last_backup` | `OpStatus` |  |  |
| `last_scrub` | `OpStatus` |  |  |
| `last_unclean_shutdown` | `OpStatus` |  |  |

### Review

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `book_id` | string (uuid) | yes |  |
| `author` | `UserRef` |  |  |
| `stars` | integer | yes |  |
| `text` | string |  |  |
| `mine` | boolean | yes |  |
| `hidden` | boolean | yes | Hidden by a moderator (only the author and admins see it) |
| `hidden_reason` | string |  | Admins only |
| `created_at` | string (date-time) | yes |  |
| `updated_at` | string (date-time) | yes |  |

### RatingSummary

| Field | Type | Required | Notes |
|---|---|---|---|
| `count` | integer | yes |  |
| `average` | number | yes | 0 when there are no ratings |
| `histogram` | array of integer | yes | Number of 1- to 5-star ratings (index 0 is one star) |

### ReviewPage

| Field | Type | Required | Notes |
|---|---|---|---|
| `items` | array of `Review` | yes |  |
| `summary` | `RatingSummary` | yes |  |
| `mine` | `Review` |  |  |
| `next_cursor` | string |  |  |

### ReviewInput

| Field | Type | Required | Notes |
|---|---|---|---|
| `stars` | integer | yes |  |
| `text` | string |  |  |

### RecommendRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `user_ids` | array of string | yes |  |
| `note` | string |  |  |

### RecommendResult

| Field | Type | Required | Notes |
|---|---|---|---|
| `sent` | integer | yes |  |
| `skipped` | integer | yes | People who can't see the book (they get nothing) |

### Recommendation

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `book` | `BookSummary` | yes |  |
| `from` | `UserRef` | yes |  |
| `note` | string |  |  |
| `created_at` | string (date-time) | yes |  |

### Listener

| Field | Type | Required | Notes |
|---|---|---|---|
| `user` | `UserRef` | yes |  |
| `state` | string: listening \| finished | yes |  |
| `updated_at` | string (date-time) | yes |  |

### ReportReason

Enum: `copyright`, `inappropriate`, `spam`, `harassment`, `wrong_metadata`, `other`

### ReportCreate

| Field | Type | Required | Notes |
|---|---|---|---|
| `object_type` | string: book \| review \| user | yes |  |
| `object_id` | string (uuid) | yes |  |
| `reason` | `ReportReason` | yes |  |
| `details` | string |  |  |

### ReportRef

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |

### GroupRef

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `public` | boolean | yes | The built-in Public group (sharing to it is reviewed first) |

### ShareCreate

| Field | Type | Required | Notes |
|---|---|---|---|
| `target_type` | string: group \| user | yes |  |
| `target_id` | string (uuid) | yes |  |

### ShareResult

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes | The share, or the review request |
| `status` | string: shared \| pending_review | yes |  |

### Share

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `target_type` | string: group \| user | yes |  |
| `target_id` | string (uuid) | yes |  |
| `target_name` | string | yes |  |
| `created_at` | string (date-time) | yes |  |

### ShareRequest

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `target_id` | string (uuid) | yes |  |
| `target_name` | string | yes |  |
| `status` | string: pending \| approved \| rejected | yes |  |
| `reason` | string |  | Why it was rejected |
| `created_at` | string (date-time) | yes |  |
| `decided_at` | string (date-time) |  |  |

### BookShares

| Field | Type | Required | Notes |
|---|---|---|---|
| `shares` | array of `Share` | yes |  |
| `requests` | array of `ShareRequest` | yes |  |

### CollectionShare

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `target_type` | string: group \| user | yes |  |
| `target_id` | string (uuid) | yes |  |
| `target_name` | string | yes |  |
| `created_at` | string (date-time) | yes |  |

### SharedCollection

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `name` | string | yes |  |
| `description` | string |  |  |
| `owner` | `UserRef` | yes |  |
| `book_ids` | array of string | yes | Books in order (only books you can see) |
| `updated_at` | string (date-time) | yes |  |

### BookRef

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `title` | string | yes |  |
| `has_cover` | boolean | yes |  |

### ReviewQueueItem

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `book` | `BookRef` | yes |  |
| `uploader` | `UserRef` |  |  |
| `target` | `GroupRef` | yes |  |
| `status` | string: pending \| approved \| rejected | yes |  |
| `reason` | string |  |  |
| `decided_by` | `UserRef` |  |  |
| `created_at` | string (date-time) | yes |  |
| `decided_at` | string (date-time) |  |  |

### ReviewQueuePage

| Field | Type | Required | Notes |
|---|---|---|---|
| `items` | array of `ReviewQueueItem` | yes |  |
| `next_cursor` | string |  |  |

### ReportedReview

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `stars` | integer | yes |  |
| `text` | string |  |  |
| `author` | `UserRef` |  |  |
| `hidden` | boolean | yes |  |

### AdminReport

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string (uuid) | yes |  |
| `object_type` | string: book \| review \| user | yes |  |
| `object_id` | string (uuid) | yes |  |
| `object_label` | string | yes | Book title, reviewed book title, or username (empty if it no longer exists) |
| `book` | `BookRef` |  |  |
| `review` | `ReportedReview` |  |  |
| `user` | `UserRef` |  |  |
| `reporter` | `UserRef` |  |  |
| `reason` | `ReportReason` | yes |  |
| `details` | string |  |  |
| `status` | string: open \| resolved \| dismissed | yes |  |
| `resolution` | string: dismiss \| hide_review \| takedown \| resolved |  |  |
| `resolution_note` | string |  |  |
| `resolved_by` | `UserRef` |  |  |
| `created_at` | string (date-time) | yes |  |
| `resolved_at` | string (date-time) |  |  |

### ReportPage

| Field | Type | Required | Notes |
|---|---|---|---|
| `items` | array of `AdminReport` | yes |  |
| `next_cursor` | string |  |  |

### ReportResolve

| Field | Type | Required | Notes |
|---|---|---|---|
| `action` | string: dismiss \| hide_review \| takedown \| resolved | yes |  |
| `note` | string | yes |  |

