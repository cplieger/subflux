# Requirements Document

Feature: bbolt Store-Engine Rewrite

## Introduction

Replace subflux's SQLite (`modernc.org/sqlite`) persistence with a pure-Go-native store layer: bbolt (`go.etcd.io/bbolt`) for the core domain, bbolt buckets in the same file for durable auth, and in-memory maps for ephemeral auth. The rewrite is behaviour-preserving at the two interface seams (`api.Store`, `authstore.AuthStore`) and is a clean break with no data migration and no backwards compatibility. Requirements below are derived from `design.md` and the incorporated adversarial review.

These requirements describe observable behaviour and correctness obligations, not implementation. The design document specifies how each is met.

## Requirements

### Requirement 1: Pure-Go core persistence engine

**User Story:** As the maintainer, I want the core data persisted by a pure-Go embedded engine, so that the binary carries no transpiled-C dependency and the modernc/libc supply-chain trap is eliminated.

#### Acceptance Criteria

1. WHEN the project is built THEN the binary SHALL NOT link `modernc.org/sqlite` or `modernc.org/libc`, and `go.mod` SHALL NOT require them.
2. WHEN `go mod tidy` runs after the change THEN `modernc.org/{mathutil,memory}`, `github.com/dustin/go-humanize`, `github.com/remyoudompheng/bigfft`, and `github.com/ncruces/go-strftime` SHALL be absent unless required by another package.
3. THE core store SHALL persist all non-auth state in a single bbolt file at `/config/subflux.bolt`.
4. THE only new third-party runtime dependency SHALL be `go.etcd.io/bbolt` (transitively `golang.org/x/sys`, already present), with no others; durable auth uses bbolt buckets, not a separate library.
5. THE build SHALL remain `CGO_ENABLED=0` and produce a smaller binary than the SQLite build.

### Requirement 2: Adaptive backoff parity

**User Story:** As a user relying on adaptive search backoff, I want per-provider backoff to behave exactly as today, so that providers are not hammered and retries happen on schedule.

#### Acceptance Criteria

1. WHEN a search yields no result THEN the system SHALL record a per-`(media_type, media_id, language, provider)` attempt with incremented failures and a computed `next_retry`.
2. WHEN backed-off providers are queried for a triple THEN the system SHALL return providers whose failures reach the max-attempts threshold OR whose `next_retry` is in the future; WHEN max-attempts is zero or negative THEN only the `next_retry` check SHALL apply.
3. WHEN due backoff items are listed THEN the system SHALL return them in ascending `next_retry` order.
4. WHEN a provider has no recorded attempt for a triple THEN it SHALL be immediately eligible (no row means eligible).

### Requirement 3: Download record and upgrade parity

**User Story:** As a user, I want auto and manual subtitle downloads recorded with the same upgrade and history semantics as today, so that upgrades, scores, and the upgrade window behave identically.

#### Acceptance Criteria

1. WHEN an auto download is saved for a triple that already has an auto row THEN the system SHALL update it in place and SHALL preserve the original `media_imported`.
2. WHEN an auto download is saved for a triple with no auto row THEN the system SHALL insert a new row with `media_imported` set to now.
3. WHEN any download is saved THEN the system SHALL clear all `search_attempts` for that triple within the same transaction (success clears backoff).
4. WHEN the current score for a triple is requested THEN the system SHALL return the highest auto-row score for the triple, the `media_imported` of that row, and a found flag that is false when no auto row exists.
5. WHEN downloaded references for a triple are requested THEN the system SHALL return the distinct `(release_name, provider)` pairs across that triple's rows (auto and manual), excluding rows whose `release_name` is empty.

### Requirement 4: Manual lock semantics parity

**User Story:** As a user who manually picks subtitles, I want manual locks, counts, ordinals, and unlocking to behave exactly as today, so that automation does not overwrite my choices and manual files are numbered correctly.

#### Acceptance Criteria

1. THE store SHALL represent multiple manual rows and multiple auto rows per `(media_type, media_id, language)`.
2. WHEN a triple has at least one manual row THEN `IsManuallyLocked` SHALL report locked.
3. WHEN a manual lock is cleared THEN the system SHALL convert manual rows to auto without deleting them, and the rows SHALL remain visible to state queries and downloaded-reference lookups.
4. WHEN the next manual number is requested THEN the system SHALL return one greater than the highest existing manual ordinal for the triple.
5. WHEN a manual download is saved THEN the stored ordinal SHALL equal the ordinal encoded in the record's file path, even under interleaved manual downloads for the same triple.

### Requirement 5: Coverage and scan-state parity

**User Story:** As a user viewing the coverage table, I want subtitle-file inventory and scan state to track exactly as today, so that coverage badges and scan resume are correct.

#### Acceptance Criteria

1. WHEN subtitle files for a media item are recorded THEN the system SHALL diff against existing entries, insert new, update changed, delete stale, and report whether anything changed.
2. WHEN subtitle files or scan states are queried by media prefix THEN the system SHALL return all matching entries.
3. WHEN scan state is recorded THEN the system SHALL upsert one row per `(media_type, media_id)` and update the scan-recency index.
4. WHEN recently scanned media are requested with a cutoff THEN the system SHALL return exactly the media whose `scanned_at` is at or after the cutoff.
5. WHEN the total subtitle-file count is requested THEN the system SHALL return the exact number of tracked subtitle-file rows.
6. WHEN the last scan time is requested THEN the system SHALL return the most recent `scanned_at` formatted as `"YYYY-MM-DD HH:MM:SS"` in UTC, or an empty string when no scan has been recorded.

### Requirement 6: Sync offsets and poll cursors parity

**User Story:** As a user, I want subtitle timing offsets and arr poll cursors persisted, so that sync adjustments and incremental polling survive restarts.

#### Acceptance Criteria

1. WHEN a sync offset is set for a subtitle path THEN a later get for that path SHALL return the same offset.
2. WHEN a poll timestamp is set for a canonical poll key THEN a later get SHALL return the same timestamp.
3. IF a poll key has no stored value THEN the get SHALL return the zero time without error.

### Requirement 7: Maintenance and reconciliation parity

**User Story:** As an operator, I want reconciliation and path-based cleanup to keep the store consistent with the filesystem, without stalling the live poller.

#### Acceptance Criteria

1. WHEN reconciliation finds a state row whose video file is gone THEN the system SHALL delete that row, its orphaned subtitle file, the triple's backoff, and scan-state rows for media left with no state.
2. WHEN a row's subtitle file is gone, its video exists, AND at least one other subtitle row for the same triple still exists on disk THEN the system SHALL delete only that row and SHALL preserve the remaining rows and any manual lock, without clearing backoff or changing `media_imported`.
3. WHEN all subtitle files for a triple are gone but the video exists THEN the system SHALL reset the triple's auto rows (clear path/score/provider/release_name, set `media_imported` to now), delete the triple's manual rows, and clear the triple's backoff.
4. WHEN reconciliation runs over a large library THEN it SHALL execute in bounded transaction batches rather than one whole-library transaction and SHALL NOT hold the single write lock across the full pass. This is a deliberate concurrency improvement over the prior single-transaction reconcile, not behaviour preserved from the SQLite implementation.
5. IF reconciliation is interrupted mid-pass THEN re-running it SHALL converge to the same result (operations are idempotent).
6. WHEN state is deleted by video paths THEN the system SHALL remove every state row sharing each video path, their index entries, orphaned subtitle files, the affected triples' backoff, and scan-state rows for media left with no state, within the operation.
7. WHEN config drift removes languages or providers THEN the system SHALL delete the corresponding backoff entries.
8. WHEN config drift disables adaptive search THEN the system SHALL clear all backoff entries.

### Requirement 8: Index and key-ordering correctness

**User Story:** As the maintainer, I want hand-maintained indexes to stay consistent with their primaries and ordered scans to be exact, so that the loss of SQL indexing introduces no correctness regressions.

#### Acceptance Criteria

1. WHEN any sequence of writes and deletes is applied THEN every query (state, due-backoff, scan-recency, video-path lookup, prefix scan) SHALL return exactly the records a full scan of the underlying data would return, with no stale, duplicated, or missing entries.
2. WHEN a time-ordered index is scanned THEN results SHALL be in chronological order and a seek to a cutoff SHALL be exact.
3. WHEN a prefix query is issued for one component (for example a media id) THEN results SHALL NOT include records whose value merely shares a prefix of the next component.
4. WHEN state rows share a `media_imported` value THEN state-query ordering SHALL break ties by a stable surrogate id (newest first), matching the prior `media_imported DESC, id DESC` order.

### Requirement 9: Durable auth persistence

**User Story:** As an operator, I want users, passkeys, and API keys persisted safely on disk, so that local accounts and credentials survive restarts with their security properties intact.

#### Acceptance Criteria

1. THE durable auth records (users, passkeys, api_keys) SHALL be persisted in bbolt buckets in the same file as the core store, and a write SHALL be crash-durable on transaction commit.
2. WHEN a durable auth mutation occurs THEN it SHALL be applied in a single bbolt write transaction (uniqueness checked, record written, indexes maintained); IF the transaction fails THEN it SHALL roll back atomically leaving no partial state and an error SHALL be returned.
3. THE system SHALL reject duplicate username (case-insensitive), duplicate `(oidc_issuer, oidc_sub)`, duplicate `credential_id`, and duplicate API-key hash.
4. WHEN a user is deleted THEN the system SHALL remove that user's passkeys, API keys, and sessions atomically.
5. WHEN a passkey authentication completes THEN the system SHALL persist `sign_count` as the maximum of the stored and incoming values (never regressing) durably, so cloned-authenticator detection survives restarts, and SHALL round-trip every passkey field including the clone-warning flag.
6. WHEN the store file is absent at startup THEN the system SHALL start with empty auth buckets (first boot); IF the file is present but cannot be opened THEN startup SHALL fail with a descriptive error.

### Requirement 10: Ephemeral auth in memory

**User Story:** As a user, I accept re-logging-in after a restart, in exchange for sessions and login state that never touch disk.

#### Acceptance Criteria

1. THE sessions and OIDC login states SHALL be held only in memory and SHALL NOT be written to disk.
2. WHEN a session's activity is updated THEN the update SHALL touch only memory (no disk write).
3. WHILE the process runs THEN a background sweeper SHALL evict expired sessions and OIDC states within one bounded sweep interval of their expiry; a session SHALL be considered expired when it exceeds either the idle timeout or the absolute timeout.
4. WHEN the process restarts THEN sessions and OIDC states SHALL be empty and users SHALL re-authenticate; an in-flight OIDC login MAY be retried.

### Requirement 11: Interface seam stability

**User Story:** As the maintainer, I want the rewrite contained behind the existing interfaces, so that the blast radius is the store layer only.

#### Acceptance Criteria

1. THE new core store SHALL satisfy `api.Store` unchanged, and the new auth store SHALL satisfy `authstore.AuthStore` (the `cplieger/auth/store.AuthStore` composite) unchanged.
2. WHEN the rewrite lands THEN no code under `internal/server`, `internal/search`, `internal/provider`, or `internal/subsync` SHALL change; the only composition roots that change are `main.go` and `cli.go` (the latter because the auth CLI commands consume the auth store).
3. WHEN `main.go` is updated THEN it SHALL open the core store and the auth store separately and pass each to the correct consumer.

### Requirement 12: Clean-break cutover

**User Story:** As an operator, I want a no-migration cutover with predictable, documented impact, so that deploying the new version is safe and reversible.

#### Acceptance Criteria

1. WHEN the new version starts against a volume containing an old `subflux.db` THEN it SHALL ignore that file and create a fresh `subflux.bolt`.
2. THE system SHALL NOT read, convert, or require the old SQLite database.
3. WHEN the new version first runs THEN derived state (scan_state, subtitle_files, coverage, auto subtitle_state) SHALL be rebuilt by the first scan, and local auth users SHALL be recreatable via the auth CLI commands routed through the running server; the CLI SHALL NOT open the bbolt file directly while the server holds the exclusive lock.
4. THE breaking, no-migration nature and the rebuild-on-first-scan impact SHALL be documented in the release notes and steering.
5. WHEN rolling back to the prior image THEN the retained `subflux.db` SHALL allow the old version to resume, losing only state written to bbolt since cutover.
6. WHEN the new version first runs THEN manual locks, manual download history, and sync offsets from the old database SHALL be absent (not migrated); manual subtitles SHALL revert to automation until re-locked and offsets SHALL reset to zero, and this loss SHALL be documented in the release notes alongside the rebuild-on-first-scan impact.

### Requirement 13: Crash-safety, durability, and concurrency

**User Story:** As an operator, I want the store to be crash-safe and to fail fast on contention, so that an unclean shutdown or a double-open does not corrupt or hang.

#### Acceptance Criteria

1. WHEN a write transaction fails mid-operation THEN the engine SHALL roll it back atomically, leaving no partial primary/index state.
2. WHEN the bbolt file is already locked by another process THEN opening SHALL fail fast within a bounded timeout rather than hang.
3. WHILE writes are serialized by the single writer THEN concurrent reads SHALL proceed, and read transactions SHALL be kept short and SHALL NOT perform filesystem I/O while open.
4. WHEN a stored record in a derived core bucket cannot be decoded THEN the read path SHALL skip it with a logged warning rather than abort the scan; WHEN a record in an auth bucket, or in a lock-bearing or uniqueness-bearing read, cannot be decoded THEN the read SHALL fail closed (return an error, and treat any affected lock as held) rather than silently skip.

### Requirement 14: Verification gates

**User Story:** As the maintainer, I want the rewrite proven against the same contracts and end-to-end suite, so that behaviour parity is demonstrated before release.

#### Acceptance Criteria

1. THE new core store SHALL pass an engine-agnostic `api.Store` contract suite, and the new auth store SHALL pass an `authstore.AuthStore` contract suite.
2. THE contract suites SHALL include the resolved-finding and edge cases: the three-way reconcile branches (video gone; subtitle gone with another subtitle still present; all subtitles gone), non-destructive `ClearManualLock`, backoff cleared on save, interleaved manual downloads keeping filename and ordinal aligned, `DeleteStateByPaths` cleaning all rows plus orphaned coverage and backoff for a shared video path, `GetState` filter/search/limit/offset, `CleanupDrift` adaptive-disabled clearing all backoff, single-use `ConsumeOIDCState`, and credential ownership checks on delete.
3. THE store and auth packages SHALL pass the race detector and property/fuzz tests for key encoding, codec round-trips, and the index-equals-rescan invariant.
4. THE existing functional suite (`tests/functional/run.sh`) SHALL pass against the new engine as the behavioural acceptance gate.

### Requirement 15: Query and listing behaviour parity

**User Story:** As a user of the CLI and coverage UI, I want status, locks, history, and state queries to return exactly what they do today, so that listings and counts are correct.

#### Acceptance Criteria

1. WHEN store statistics are requested THEN the system SHALL return the download-record count and the backoff-attempt count.
2. WHEN manual locks are listed THEN the system SHALL return one entry per locked triple with its manual count, ordered by `(media_type, media_id)`.
3. WHEN history media ids are requested for a type and optional prefix THEN the system SHALL return the distinct matching media ids.
4. WHEN backoff is listed by prefix THEN the system SHALL return entries for rows that have a provider, ordered by media id then ascending `next_retry`.
5. WHEN state is queried THEN the system SHALL filter by media type, language, and provider when given; SHALL apply a title contains-match with `%`, `_`, and `\` escaped when a search term is given; SHALL default to a 1000-row cap when no limit is given; and SHALL honour an offset for pagination.
6. WHEN the manual download count or manual subtitle paths for a triple are requested THEN the system SHALL return the count of manual rows and the non-empty manual subtitle paths respectively.
7. WHEN a single subtitle file is upserted or deleted by its full identity THEN the system SHALL add or remove exactly that entry.

### Requirement 16: Auth lookup, session, and credential management parity

**User Story:** As a user authenticating, I want lookups, session management, and credential operations to behave exactly as today, so that login, logout-other-devices, and credential management are correct and safe.

#### Acceptance Criteria

1. WHEN a user is looked up by username or email THEN the match SHALL be case-insensitive.
2. WHEN the user count is requested THEN the system SHALL return the number of users (used for first-boot detection).
3. WHEN an OIDC state is consumed THEN the system SHALL return its stored values exactly once; a second consume of the same state SHALL return not-found (single-use, anti-replay).
4. WHEN a passkey or API key is deleted THEN the system SHALL delete it only if it belongs to the supplied user id.
5. WHEN a user's sessions are bulk-deleted THEN the system SHALL delete all of that user's sessions except an optionally named session to keep.
6. WHEN a passkey is looked up by credential id, or an API key by hash, THEN the system SHALL return the matching credential for authentication.
7. THE system SHALL support renaming a passkey, counting a user's passkeys, listing a user's API keys, and batch session-activity updates, exercised by the auth contract suite (Requirement 14.1).

### Requirement 17: Store observability

**User Story:** As an operator, I want visibility into the store's health, so that file growth, write contention, and backup failures are detectable before they cause an incident.

#### Acceptance Criteria

1. THE system SHALL export metrics for the store file size and freelist (free-page) bytes, sourced from bbolt `DB.Stats()` / `Tx.Size()`.
2. THE system SHALL export reconcile progress (items processed) and the last backup's success timestamp and duration.
3. WHEN write transactions fail repeatedly (for example, a full disk) THEN the system SHALL raise a persistent alert and continue degrading (no crash-loop).
4. THE system SHALL log reconcile start and completion at INFO with summary statistics.

### Requirement 18: Read-path efficiency

**User Story:** As a user of a large library, I want coverage, status, and state queries to stay fast as the library grows, so that the UI and CLI stay responsive.

#### Acceptance Criteria

1. WHEN store statistics or the total subtitle-file count are requested THEN they SHALL be served from O(1) maintained counters, not a full-bucket scan.
2. WHEN per-media coverage is computed THEN it SHALL be derivable from subtitle-file keys without decoding every record value.
3. WHEN lock, count, current-score, or downloaded-reference queries run THEN they SHALL be answerable from a single index walk without dereferencing every primary record.
4. WHEN state is paginated deeply THEN a page SHALL be retrievable in time proportional to the page size rather than the offset (keyset pagination); numeric offset MAY remain for shallow pages.
5. WHERE an index value carries a projection of primary fields THEN the index-equals-rescan verification SHALL also assert the projection matches the primary.

## Glossary

- **bbolt**: `go.etcd.io/bbolt`, a pure-Go embedded B+tree key/value store; single writer, concurrent MVCC readers, single file.
- **Core domain**: the non-auth persisted state (search_attempts, subtitle_state, subtitle_files, scan_state, poll_state, sync_offsets).
- **Triple**: the `(media_type, media_id, language)` tuple that keys most subtitle state.
- **Auto row**: a `subtitle_state` record with `manual=false`, written by automation; at most one is updated in place per save, preserving `media_imported`.
- **Manual row**: a `subtitle_state` record with `manual=true`, created by a user manual download; acts as the automation lock and carries a per-triple ordinal that matches its `movie.lang.N.srt` filename.
- **Surrogate id**: a stable per-row identifier that lets multiple rows per triple and a deterministic newest-first ordering tiebreak be represented; the design specifies the mechanism.
- **Secondary index bucket**: a sibling bbolt bucket whose keys encode an alternate access path (due time, triple, import time, video path, scan time) and dereference to a primary key; maintained in the same transaction as its primary.
- **Durable auth**: users, passkeys, API keys; persisted in bbolt buckets in the same file as the core store.
- **Ephemeral auth**: sessions and OIDC login states; in-memory only, lost on restart by design.
- **Seam**: the `api.Store` and `authstore.AuthStore` interfaces that contain the rewrite's blast radius.
- **Clean break**: no migration script, no backwards compatibility; the old `subflux.db` is ignored and derived state is rebuilt on first scan.
- **sign_count**: the WebAuthn signature counter persisted with a passkey for cloned-authenticator detection; must be durable and monotonic.
