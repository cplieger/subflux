# Design: bbolt Store-Engine Rewrite

> Adversarial review incorporated. See `adversarial-design-review.md`. Resolutions: surrogate-id `subtitle_state` model (was single auto-row, lost multi-row + history); reconcile is chunked and idempotent, not one atomic pass; durable auth lives in bbolt buckets in the core file (one engine, one backup, crash-durable on commit) with only ephemeral sessions/OIDC in memory, which superseded an interim JSON-file-via-atomicfile design once the on-disk-file constraint was lifted and removes the second persistence mechanism, the extra dependency, and the whole-document rewrite; `SaveDownload` clears backoff in-tx; `DeleteStateByPaths` prefix-scans the video index and cleans orphans; reader isolation claim softened.

## Overview

Replace subflux's SQLite (`modernc.org/sqlite`) persistence with a from-scratch, pure-Go-native store layer:

- **bbolt** (`go.etcd.io/bbolt`) for the core domain (search/subtitle/scan/poll state).
- **bbolt buckets** in the same file as the core store for durable auth (users, passkeys, API keys).
- **in-memory** maps for ephemeral auth (sessions, OIDC login states).

This is a clean break: no migration script, no backwards compatibility, no reuse of the old file/folder layout. The ideal architecture is designed first; the existing app is then fitted to it through the two interface seams it already exposes (`api.Store`, `authstore.AuthStore`), which stay stable so no consumer outside the store layer changes.

### Motivation

A principle-driven change (a fully Go-native, zero-C-lineage stack) with measured side benefits:

- Removes `modernc.org/sqlite` and `modernc.org/libc`; `go mod tidy` then drops `mathutil`, `memory`, `dustin/go-humanize`, `remyoudompheng/bigfft`, `ncruces/go-strftime`. The modernc source-generation tools (`cc/v4`, `ccgo/v4`, `gc/v3`, `goabi0`) and ~1.5 GB of module cache / 95 MB of transpiled-libc source stop being fetched.
- Drops roughly 4.3 MB off the binary (measured on this repo: sqlite+libc adds about 4.6 MB, bbolt about 0.28 MB; current binary 15.9 MB toward roughly 11.6 MB).
- Permanently kills the Go semantic-import-versioning Renovate trap (the `libc/v2` phantom): no modernc deps, no trap.
- Lowers baseline memory (no SQLite VM plus libc allocator state), which suits an app whose reason to exist is fixing a memory problem.

### Goals

- One embedded, crash-safe, Go-native store for the core domain.
- Auth durable state on disk via subflux's crash-safe atomic-write primitive; ephemeral state in memory.
- Preserve the `api.Store` and `authstore.AuthStore` contracts so `internal/server/**`, `internal/search/**`, `internal/provider/**`, `internal/subsync/**`, and the CLI are untouched.
- Same observable behaviour: coverage, reconciliation, adaptive backoff, manual locks, upgrade window, auth flows.

### Non-Goals

- No data migration from the existing `subflux.db`. Existing deployments start fresh (Part 3).
- No change to subtitle search, scoring, sync, or provider subsystems.
- No multi-node or distributed concerns (single process, single container).
- No SQL query surface or `sqlite3` tooling; replaced by typed Go accessors.

### Constraints

- bbolt is single-writer: one read-write transaction at a time, serialized. subflux's writes are bursty and spaced by `scan_delay` (default 5s), single process, so there is no steady-state contention. The one sustained write burst is the pre-scan reconcile (handled by chunking, Section 2.5).
- The only new dependency is `go.etcd.io/bbolt` (which pulls only `golang.org/x/sys`, already present). Durable auth lives in bbolt buckets in the same file as the core data, so it needs no separate persistence library: a bbolt `Update` commit fsyncs, making every successful write crash-durable. subflux's `internal/fsutil` is unchanged and keeps its existing role for user-path-derived subtitle-file writes.
- Distroless static binary, `CGO_ENABLED=0` (already the case).
- The `AuthStore` contract is owned by the external `cplieger/auth` library (`UserStore + SessionPersister + PasskeyStore + KeyStore + OIDCStateStore`); the new auth implementation must satisfy those library interfaces, including the durable, monotonic WebAuthn `sign_count` (clone-detection state; cf. CVE-2023-45669).

---

## Architecture

The data splits by access profile, and each tier gets the engine that fits it.

```
┌─────────────────────────────────────────────────────────────────┐
│ subflux process (single container, nonroot)                      │
│                                                                   │
│  ┌────────────────────────┐   api.Store      ┌────────────────┐  │
│  │ server / search / cli  │ ───────────────▶ │  core store    │  │
│  │ provider / subsync     │                  │  (bbolt)       │  │
│  └────────────────────────┘                  └───────┬────────┘  │
│            │                                          ▼           │
│            │ authstore.AuthStore     /config/subflux.bolt         │
│            ▼                          (core + auth buckets)        │
│  ┌────────────────────────┐  durable ──▶ auth_users / passkeys /  │
│  │ auth store             │             api_keys buckets          │
│  │  shares the bbolt DB   │                                       │
│  │  sessions/oidc (memory)│  ephemeral ─▶ in-memory only          │
│  └────────────────────────┘             (RWMutex + sweeper)       │
└─────────────────────────────────────────────────────────────────┘
```

| Tier | Engine | Backing | Holds | Rationale |
|---|---|---|---|---|
| Durable | bbolt | `/config/subflux.bolt` | core (search_attempts, subtitle_state, subtitle_files, scan_state, poll_state, sync_offsets) plus auth (users, passkeys, api_keys) | One engine, one file, one backup; composite-keyed KV with ordered range scans; crash-safe ACID commit; mmap reads |
| Ephemeral | in-memory | none | sessions, oidc_states | Hot read path / short-lived; re-login on restart is acceptable for a self-hosted tool |

### Data domains and ownership

- **Core domain (bbolt).** No foreign keys, no normalization. Every record is keyed by a flavour of `(media_type, media_id[, language, provider/variant])`. Media is not an entity; `media_id` is an external arr/IMDB identifier and metadata is denormalized into each record. Access is point-get plus ordered range scans, which is bbolt's shape.
- **Auth domain (bbolt + memory).** A `users` aggregate with `passkeys` and `api_keys` (durable, in their own bbolt buckets) plus `sessions` and `oidc_states` (ephemeral, in memory). Foreign-key cascade and uniqueness are enforced inside a single bbolt `Update`: uniqueness checked against an index bucket before insert, cascade by deleting the user key and its child keys together.

### bbolt bucket model

bbolt is a single file of top-level buckets, each an ordered `[]byte -> []byte` map. One bucket per collection; secondary indexes are sibling buckets maintained inside the same write transaction. Key ordering is byte-lexicographic, so composite keys use a `0x00` separator (no text component contains NUL) and time-ordered index keys use 8-byte big-endian timestamps.

`subtitle_state` keeps a surrogate id allocated by bbolt `b.NextSequence()` (the README's documented autoincrement pattern), not a composite primary key. This is the resolution to adversarial finding C1: the SQL model allows multiple auto rows per `(mt,mid,lang)` (reconcile produces them, `TestReconcileState_multiple_auto_rows_all_reset` asserts it) and `ClearManualLock` is a non-destructive flag flip that preserves history. A composite single-row key cannot represent either; a surrogate id does, and it preserves the `media_imported DESC, id DESC` ordering tiebreak.

| Bucket | Key | Value | Secondary index buckets |
|---|---|---|---|
| `search_attempts` | `mt 0x00 mid 0x00 lang 0x00 provider` | `{last_tried, failures, next_retry}` | `ix_attempts_due`: `be64(next_retry) 0x00 primarykey` |
| `subtitle_state` | `be64(id)` from `NextSequence()` | full download record incl. `manual bool` | `ix_state_triple`: `mt 0x00 mid 0x00 lang 0x00 be64(id)`; `ix_state_imported`: `be64(media_imported) 0x00 be64(id)`; `ix_state_video`: `video_path 0x00 be64(id)` |
| `subtitle_files` | `mt 0x00 mid 0x00 lang 0x00 variant 0x00 source 0x00 path` | `{codec, offset_ms, updated_at}` | none (per-item prefix scan) |
| `scan_state` | `mt 0x00 mid` | `{title, season, episode, audio_lang, scanned_at}` | `ix_scan_at`: `be64(scanned_at) 0x00 key` |
| `sync_offsets` | `path` | `be64(offset_ms)` | none |
| `poll_state` | PollKey (`sonarr` / `radarr`) | RFC3339 timestamp | none |
| `auth_users` | `be64(id)` from `NextSequence()` | user record | `ix_user_name`: `lower(username)`; `ix_user_oidc`: `issuer 0x00 sub` (only when oidc_sub set) |
| `auth_passkeys` | `credential_id` (unique) | passkey record incl. `sign_count` | `ix_passkey_user`: `be64(user_id) 0x00 credential_id` |
| `auth_api_keys` | `key_hash` (unique) | api-key record | `ix_apikey_user`: `be64(user_id) 0x00 key_hash` |
| `meta` | `schema_version`, etc. | small scalars | none |

Index buckets store either an empty value (existence-only indexes) or a small projection of query-relevant fields (hot-path indexes; see Read-path optimization below). Key-design notes:

- **subtitle_state access paths via `ix_state_triple`.** All rows for a triple is a prefix scan on `mt 0x00 mid 0x00 lang 0x00`; all rows for a media item is a prefix scan on `mt 0x00 mid 0x00`. "Is manually locked" is "any row under the triple has `manual=true`". The auto row(s) are the `manual=false` rows under the triple. `ManualSubtitlePaths` / `ManualDownloadCount` filter the triple prefix by `manual=true`.
- **Manual ordinal seam (finding M3).** The `movie.fr.N.srt` ordinal `N` is authoritative in `rec.Path` (the handler builds the filename from `NextManualNumber` before the async download). `SaveDownload` parses `N` out of `rec.Path` rather than re-deriving it from the bucket, so the stored row and the on-disk filename never diverge under interleaved manual downloads. `NextManualNumber` continues to derive the next ordinal by parsing existing manual paths for the triple (the current behaviour).
- **Secondary indexes only where a non-key access path exists**: due-for-retry backoff, triple/media prefix, import-time ordering, video-path reverse lookup (for `DeleteStateByPaths`), scan recency. Everything else is a direct get or a prefix scan.

### Read-path optimization

The naive index pattern (empty index value, deref the primary, JSON-decode per row) turns a filtered query into N point-gets plus N decodes, which is the dominant cost on subflux's read-heavy paths. So:

- **Projected index values.** Hot-path index entries carry a small fixed projection of the fields the query needs, answered from the index walk without touching the primary. `ix_state_triple` carries `(manual, score, provider)` so `IsManuallyLocked`, `ManualDownloadCount`, `DownloadedRefs`, and `CurrentScore` are served from the index alone; the full primary row is decoded only for detail reads (CLI `state`). The index-equals-rescan property test asserts the projection matches the primary, so the projection cannot silently drift.
- **Key-only coverage.** `subtitle_files` encodes `lang` and `variant` in the key, so per-media coverage counts come from a key-only prefix walk with no value decode.
- **Maintained counters.** `Stats` (downloads, attempts) and `TotalSubtitleFiles` read O(1) integer counters kept in the `meta` bucket, incremented and decremented inside the same `Update` as the row write (bbolt has no O(1) live count; `Bucket.Sequence` is a high-water mark, not a count). The counters are part of the index-maintenance invariant, so a write that does not update its counter fails the property test.
- **Keyset pagination.** Deep `GetState` pages seek `ix_state_imported` to the last `(media_imported, id)` cursor of the previous page and walk forward, so a page is O(pageSize) regardless of depth. Numeric offset is retained for shallow callers but documented as O(offset).

### Auth persistence model

**Durable (bbolt buckets in `/config/subflux.bolt`).** Users, passkeys, and API keys live in their own buckets alongside the core data, using the same JSON codec and index-maintenance discipline. The auth store shares the core store's `*bbolt.DB` handle (opened once in `main.go`), so there is one file, one lock, and one backup. Durability is inherent: a bbolt `Update` commit fsyncs, so a write that returns nil is crash-durable. There is no separate document, no build-then-swap, and no `Result.Durable` checking.

- Lookups: `auth_users` is keyed by a `NextSequence` surrogate id; `ix_user_name` (lower username) and `ix_user_oidc` ((issuer, sub), only when set) give the case-insensitive and OIDC lookups. `auth_passkeys` is keyed by credential id, so the WebAuthn auth hot path is a direct get; `ix_passkey_user` lists a user's passkeys. `auth_api_keys` is keyed by key hash (the API-auth hot path); `ix_apikey_user` lists a user's keys.
- Uniqueness: checked against the relevant index bucket inside the write `Update` before the put; a conflict returns the existing `ErrConflict` sentinel the `cplieger/auth` handlers branch on.
- Cascade: `DeleteUser` deletes the user key AND the user's own `ix_user_name` and (when set) `ix_user_oidc` entries, then walks the `ix_passkey_user` and `ix_apikey_user` prefixes deleting the child records and their index entries, all in one `Update` (a leftover `ix_user_name` would block recreating the same admin username, breaking the documented clean-break recovery path); the user's in-memory sessions are dropped after commit. `UpdateUser` re-keys `ix_user_name` / `ix_user_oidc` (delete-old, add-new) when username, issuer, or sub change. All prefix-scan deletions collect keys first and delete after the cursor walk (bbolt's documented cursor-skip-after-delete hazard).
- `sign_count`: `UpdatePasskeyAfterLogin` is a single-key `Put` of the passkey record in an `Update`, crash-durable on commit (clone-detection state, CVE-2023-45669).
- Forward evolution: the same JSON value codec and `meta.schema_version` as the core store; additive-safe, with a semantically breaking field change out of scope (clean-break ethos).

**Ephemeral (in-memory only).** `sessions` and `oidc_states` are maps guarded by a `sync.RWMutex`, with a single background sweeper goroutine evicting expired entries. `last_activity` updates touch memory only (zero disk churn). On restart these are empty: users re-authenticate, in-flight OIDC logins retry.

### Package layout (from scratch)

The old `internal/store/{*.go, authdb, coveragedb, migrations, reconcile, txutil}` layout is discarded.

```
internal/store/                core bbolt store (implements api.Store)
  store.go        DB type wrapping *bbolt.DB; Open/Close; bucket bootstrap
  keys.go         composite-key build/parse; be32/be64 keys; NextSequence ids
  codec.go        JSON record (de)serialization; schema_version handling
  index.go        secondary-index put/delete helpers (run inside a tx)
  attempts.go     BackoffStore        (search_attempts + ix_attempts_due)
  substate.go     DownloadStore + ManualLockStore + HistoryStore + QueryStore
  subfiles.go     CoverageStore (files half) + SyncOffsetStore
  scanstate.go    CoverageStore (scan half)
  pollstate.go    PollStore
  maint.go        MaintStore (DeleteStateByPaths, CleanupDrift, ReconcileState)
  backup.go       BackupInto (bbolt tx.WriteTo hot backup; NOT compaction)
  storetest/      engine-agnostic api.Store contract suite

internal/authstore/            auth store (implements authstore.AuthStore)
  store.go        AuthStore type alias (unchanged: cplieger/auth/store.AuthStore)
  authdb.go       Store type: shares the core *bbolt.DB + in-memory ephemeral maps; Open/Close
  users.go        UserStore        (durable bbolt buckets; uniqueness + cascade)
  passkeys.go     PasskeyStore     (durable bbolt; sign_count per-key Put)
  apikeys.go      KeyStore         (durable bbolt)
  sessions.go     SessionPersister (ephemeral + sweeper)
  oidcstates.go   OIDCStateStore   (ephemeral + TTL)
```

`internal/api`, `internal/wiring`, and everything under `internal/server`, `internal/search`, `internal/provider`, `internal/subsync` are unchanged.

### Dependency seam and blast radius

- `api.Store` = `BackoffStore + DownloadStore + ManualLockStore + QueryStore + HistoryStore + CoverageStore + SyncOffsetStore + MaintStore + PollStore` (all in `internal/api/store_iface.go`). The new bbolt `*store.DB` satisfies it.
- `authstore.AuthStore` = alias of `cplieger/auth/store.AuthStore`. The new `*authstore.Store` satisfies it.

The only composition-root change is `main.go`: today one `*store.DB` satisfies both interfaces; in the new design they split into a bbolt core store and an auth store, opened separately and passed to the right consumer. No handler, search/provider/subsync code, or CLI command changes.

### Data lifecycle on cutover

subflux's core data is mostly derived and self-healing, which is what makes the clean break safe; but not all of it is derivable, and the design states the real impact (finding pressure-tested):

- Rebuilt by the next full scan: `scan_state`, `subtitle_files`, coverage, and auto `subtitle_state` (the scanner re-detects on-disk subtitles via `DetectExisting`).
- Empty-start is harmless: `search_attempts` backoff (every item simply becomes immediately eligible, the documented new-provider behaviour) and `poll_state` cursors (first poll re-baselines to now; a full scan covers the existing library).
- Genuinely lost and not derivable: manual locks, manual download history, sync offsets, and the auth users. Impact: a one-time full re-scan storm on first boot (already what a fresh install does), manual subtitles revert to automation unless re-locked, applied timing offsets reset to zero, and local-auth users are recreated via CLI. All acceptable per the no-backwards-compat directive and documented in the release notes.

---

## Data Models

bbolt record structs (JSON-encoded values; see Section "Record codecs"). Field names are illustrative; exact types come from `internal/api`:

```go
type attemptRec struct { LastTried, NextRetry time.Time; Failures int }
type stateRec   struct { ID int64                                  // NextSequence surrogate
                         Provider, ReleaseName, Path, Title, ImdbID, ReleaseTag string
                         Score, Season, Episode int; Manual bool
                         VideoPath string; MediaImported time.Time }
type fileRec    struct { Codec string; OffsetMs int64; UpdatedAt time.Time }
type scanRec    struct { Title, AudioLang string; Season, Episode int; ScannedAt time.Time }
// poll_state: string; sync_offsets: int64; meta: scalars
```

Auth durable records (JSON-encoded bbolt bucket values; binary fields base64):

```go
type userRec   struct { ID int64; Username, Email, DisplayName, PasswordHash, Role string
                        OIDCSub, OIDCIssuer string; Enabled bool; CreatedAt, UpdatedAt time.Time }
type pkRec     struct { ID, UserID int64; CredentialID, PublicKey, AAGUID []byte
                        AttestationType, Transport string; SignCount uint32; Name string
                        BackupEligible, BackupState, UserPresent, UserVerified, CloneWarning bool
                        RawAttestation []byte; CreatedAt time.Time }
type keyRec    struct { ID, UserID int64; KeyHash, KeyPrefix, KeySuffix, Label string; CreatedAt time.Time }
// auth_users keyed by be64(ID); auth_passkeys by CredentialID; auth_api_keys by KeyHash
```

Ephemeral auth (`sessions`, `oidc_states`) has no on-disk model; it lives only in the in-memory maps of the auth-store internals section.

---

## Components and Interfaces

### Key encoding

All key construction lives in `keys.go`.

- **Composite text keys**: components joined by a single `0x00` byte. Components are UTF-8 text without NUL, so the separator unambiguously delimits boundaries and preserves prefix-scan correctness.
- **Surrogate ids**: `binary.BigEndian.PutUint64` of the `b.NextSequence()` value, so `subtitle_state` keys sort in insertion order and the `id DESC` tiebreak is recoverable.
- **Time-ordered index keys**: `be64(unixnano)` then `0x00` then the primary key (for uniqueness and reverse deref). Big-endian makes byte order chronological, so a forward cursor walks oldest to newest and `Seek` jumps to a cutoff.

```go
func attemptKey(mt api.MediaType, mid, lang string, p api.ProviderID) []byte
func stateKey(id int64) []byte                       // be64(id)
func triplePrefix(mt api.MediaType, mid, lang string) []byte
func mediaPrefix(mt api.MediaType, mid string) []byte
func timeIndexKey(t time.Time, primary []byte) []byte // be64(unixnano) 0x00 primary
```

### Record codecs

Bucket values are `encoding/json`: stdlib-only, debuggable, and additively forward-compatible without a migration script. Keys remain binary; only values are JSON. A package-level `schemaVersion` is written to `meta` on bootstrap for detect-and-refuse on a future breaking change (not for migration, which does not exist by design).

### Secondary-index maintenance invariant

The rule that keeps indexes correct: every primary write happens in a `db.Update` transaction that, before putting the new value, reads the prior value (if any) to delete its stale index entries, then writes the new value and its fresh index entries (including any projected index value and the maintained `meta` counters), all in the same tx. bbolt transactions are all-or-nothing, so an index can never diverge from its primary. `index.go` centralizes this so each collection only declares which index keys it derives. A property test asserts every index equals a full primary re-scan after random operation sequences.

### Per-bucket specification

**`search_attempts` (`attempts.go`)**

- `RecordNoResult`: one `Update` tx; read old record to delete its `ix_attempts_due` entry, compute new `NextRetry` from `BackoffParams`, put, insert the new index entry.
- `BackedOffProviders(mt, mid, lang, maxAttempts)`: prefix-scan `triplePrefix`, return providers whose `Failures >= maxAttempts` or `NextRetry > now`.
- `GetBackoffItems` / `GetBackoffByPrefix`: forward cursor over `ix_attempts_due` (due order) joined back to records, or a prefix scan for the by-prefix variant.

**`subtitle_state` (`substate.go`)**

- `SaveDownload(rec)`: in one `Update` tx. Manual: parse ordinal `N` from `rec.Path` (M3), allocate an id via `NextSequence`, put, index. Auto: find the existing auto row(s) for the triple via `ix_state_triple` filtered to `manual=false`, update in place preserving `MediaImported` (or insert with `MediaImported = now` if none), index. In the same tx, delete all `search_attempts` under `triplePrefix` and their `ix_attempts_due` entries (M1: "clears adaptive backoff on success"). Maintain `ix_state_imported` and `ix_state_video`.
- `CurrentScore(mt, mid, lang)`: read the auto row(s) for the triple; return `(score, mediaImported, found)`.
- `DownloadedRefs(mt, mid, lang)`: prefix-scan `ix_state_triple`, deref each id, and return the distinct `(release_name, provider)` pairs, excluding rows whose `release_name` is empty (matches the current `SELECT DISTINCT ... WHERE release_name <> ''`).
- `IsManuallyLocked`: prefix-scan `ix_state_triple`; locked iff any deref'd row has `manual=true`.
- `ManualDownloadCount` / `ManualSubtitlePaths` / `NextManualNumber`: derived from the triple scan filtered to `manual=true` (ordinal parsed from path).
- `ClearManualLock`: flag flip (set `manual=false`) on the triple's manual rows, preserving `path`/`score`/`provider`/`media_imported` so they stay visible to `GetState`/`DownloadedRefs` (C1: non-destructive, matches SQL `UPDATE manual=0`). Re-index unaffected (the rows keep their ids).
- `GetState(StateQuery)`: scan (full or media-scoped via prefix), filter by type/lang/provider, limit; sort by `media_imported DESC, id DESC` (m2: id tiebreak preserved by the surrogate). Deep pages use keyset pagination over `ix_state_imported` (see Read-path optimization); numeric offset stays for shallow callers.
- `HistoryMediaIDs(mt, prefix)`: scan, collect distinct media ids.

**`subtitle_files` (`subfiles.go`)**

- `RecordSubtitleFiles(mt, mid, files)`: diff-based per-item sync in one tx; prefix-scan current keys, insert new, update changed, delete stale; return changed.
- `UpsertSubtitleFile` / `DeleteSubtitleFile`: direct put/delete by composite key.
- `GetSubtitleFiles(mt, prefix)`: prefix scan. `TotalSubtitleFiles`: O(1) read of a maintained counter in `meta` (not a full bucket walk).

**`scan_state` (`scanstate.go`)**

- `RecordScanState(rec)`: upsert by `mt 0x00 mid`; maintain `ix_scan_at`.
- `RecentlyScanned(cutoff)`: `Seek` `ix_scan_at` to `be64(cutoff)` and walk forward into `map[string]bool`. `GetScanStates`: prefix scan. `LastScanTime`: last `ix_scan_at` entry.

**`sync_offsets` (`subfiles.go`)** keyed by bare `path` (distinct from the files composite key). **`poll_state` (`pollstate.go`)** keyed by `PollKey`.

**`maint.go`**

- `DeleteStateByPaths(paths)` (M6): for each path, **prefix-scan** `ix_state_video` on `video_path 0x00` (a video file backs several rows: auto plus manual across languages), collect ids, delete the rows and all their index entries, delete the orphaned `subtitle_files`, clear `search_attempts` (+ `ix_attempts_due`) for affected triples, and remove `scan_state` rows for media left with no state (matches `cleanOrphanedCoverage`). One `Update` tx per path batch.
- `CleanupDrift`: delete `search_attempts` for removed languages/providers and their index entries.
- `ReconcileState`: three-way and group-aware, chunked, idempotent. Video gone: delete the row, its orphaned subtitle file, the triple's backoff, and orphaned `scan_state`. Subtitle gone but another subtitle for the triple is still present: delete only that row, preserving the rest and any manual lock, without clearing backoff or changing `media_imported`. All subtitles for the triple gone: reset the auto rows (clear path/score/provider/release_name, `media_imported` to now), delete the manual rows, and clear the triple's backoff. Every reset and delete routes through the index helpers: a reset changes `media_imported`, so it MUST delete and re-add the row's `ix_state_imported` entry, and a delete MUST remove the row's `ix_state_triple` / `ix_state_imported` / `ix_state_video` entries. Use collect-then-delete inside each batch (bbolt skips the next key if you delete during cursor iteration). Batching is per Section "Transactions and concurrency".

### Transactions and concurrency

- Reads use `db.View`. bbolt is MVCC, so readers do not block writers in steady state; but the writer must periodically grow and re-map the file and cannot remap while a read txn is open, and copy-on-write pages are not reclaimed while an old read txn is alive (M4). Rule: keep `View` transactions short, copy values out, and never hold a read txn open while doing filesystem I/O (relevant to coverage and reconcile read phases).
- Writes use `db.Update` (serialized single writer). Per-item, small multi-bucket operations (`SaveDownload` clearing backoff; `SearchTargets` upserting scan_state + subtitle_files + subtitle_state) run as one `Update` and are genuinely atomic.
- `ReconcileState` is chunked into bounded `Update` batches (C2), not one whole-library transaction; holding the single write lock across a 52k-item pass would starve the poller. Target 200 to 500 rows per batch so the worst-case write-lock hold stays in the low tens of milliseconds (each `Update` commit costs two fsyncs). Each batch is all-or-none; the pass as a whole is not. Safety rests on per-operation idempotency: deleting a stale row and resetting a missing-subtitle row are both idempotent, so a crash mid-reconcile is fully recovered by the next run. The read phase (stat each `video_path`) copies rows out under short `View` txns and does the filesystem stats outside any txn.

### The ffmpeg / store boundary

The subtitle sync engine (`internal/subsync/**`, including ffmpeg/ffprobe wrappers) and the search/provider stack never touch persistence directly; they receive data through `api.Store` accessors and hand results to handlers that persist them. The rewrite stops at `api.Store`: ffmpeg invocations, the 360p preview transcode, and VAD/alignment are unaffected. The only store-adjacent value the sync path persists is `offset_ms` via `SyncOffsetStore` / `subtitle_files`, semantics unchanged.

### Auth store internals

```go
type Store struct {
    db       *bbolt.DB               // shared with the core store (one file)
    mu       sync.RWMutex            // guards the ephemeral maps only
    sessions map[string]*api.Session // ephemeral, never persisted
    oidc     map[string]*oidcRec     // ephemeral, never persisted
    stop     chan struct{}           // sweeper shutdown
}
```

- **Durable mutations** run in a bbolt `Update` on the shared handle: check the relevant uniqueness index, put the record, and maintain its index buckets, all in one transaction that is crash-durable on commit. `DeleteUser` deletes the user key and cascades to its passkey and API-key child keys (and index entries) in the same `Update`, then drops the user's in-memory sessions.
- **Uniqueness** is enforced against `ix_user_name` / `ix_user_oidc` / the credential-id and key-hash keys before the put, returning the existing `ErrConflict` / `ErrNotFound` sentinels.
- **`UpdatePasskeyAfterLogin`** persists `max(stored, incoming)` for `sign_count` (never regress; a lower incoming count must not overwrite a higher stored one, or clone detection is defeated, CVE-2023-45669) plus the flags as a single-key `Put` in an `Update`, durable on commit (clone detection, m3). The persisted `pkRec` round-trips every `auth.PasskeyCredential` field including `CloneWarning`.
- **Sessions** and **OIDC states** are the only in-memory data, guarded by `mu`; `last_activity` is a memory write, `ConsumeOIDCState` deletes on read, and a background sweeper (started in `Open`, stopped in `Close`) evicts expired entries, replacing the SQL cleanup queries.

### Interface to implementation mapping

| Interface (sub-)contract | New owner | Backing |
|---|---|---|
| `BackoffStore` | `store/attempts.go` | bbolt `search_attempts` + `ix_attempts_due` |
| `DownloadStore`, `ManualLockStore`, `HistoryStore`, `QueryStore` | `store/substate.go` (+ `attempts.go` reads) | bbolt `subtitle_state` + indexes |
| `CoverageStore` | `store/subfiles.go` + `scanstate.go` | bbolt `subtitle_files`, `scan_state` |
| `SyncOffsetStore` | `store/subfiles.go` | bbolt `sync_offsets` |
| `MaintStore` | `store/maint.go` | cross-bucket `Update` txs (reconcile chunked) |
| `PollStore` | `store/pollstate.go` | bbolt `poll_state` |
| `UserStore`, `PasskeyStore`, `KeyStore` | `authstore/{users,passkeys,apikeys}.go` | bbolt `auth_*` buckets (shared core file) |
| `SessionPersister`, `OIDCStateStore` | `authstore/{sessions,oidcstates}.go` | in-memory + sweeper |
| `BackupInto` (server backup) | `store/backup.go` | bbolt `tx.WriteTo` of the single file (core plus auth), one consistent snapshot |

### Paths, health, bootstrap

- `DefaultDBPath`: `/config/subflux.db` becomes `/config/subflux.bolt` (different format; rename avoids confusing a bbolt file with the dead SQLite one). No separate auth path: auth lives in buckets in the same file.
- `store.Open(ctx, path)` opens bbolt with mode `0o600` (the live file holds password hashes, API-key hashes, and passkey material; the current SQLite store pins 0600 deliberately and this carries it forward) and `Options{Timeout: 5s}` (fail fast instead of hanging on the file lock). The core store owns the handle: it creates all core AND auth buckets in a bootstrap `Update` (single owner of the schema), and `authstore.New(db)` wraps the same handle, owning only the ephemeral maps and sweeper. `store.DB.Close` closes the handle; `authstore.Store.Close` stops the sweeper and never closes the shared handle. `main.go` shuts down auth-store first, then the core store.
- CLI auth commands (`reset-password`, `generate-api-key`, `enable-password-login`) currently open the bbolt file in-process, but bbolt takes an exclusive OS lock, so they collide with the always-running server (a regression from SQLite's multi-process open). They MUST be routed through the running server over HTTP (an admin-bootstrap endpoint, first-boot/localhost-guarded), matching the other remote CLI commands; see Migration step for the wiring. This is a hard correctness item, not optional.
- The Dockerfile needs no structural change; the binary gets smaller. The shipped empty `/config` dir remains correct for the bare-image smoke test (the single bbolt file is created on first run).

---

## Correctness Properties

### Property 1: Index/primary consistency

After any sequence of writes, every secondary-index bucket exactly mirrors its primary, because each primary write deletes stale index entries and adds fresh ones in the same `Update` tx. Verified by a property test comparing each index against a full primary re-scan.

**Validates: Requirements 8.1**

### Property 2: Chronological key ordering

Big-endian 8-byte timestamps make bbolt's byte order equal chronological order, so `ix_attempts_due`, `ix_state_imported`, and `ix_scan_at` cursors yield ordered results and `Seek(cutoff)` is exact.

**Validates: Requirements 8.2**

### Property 3: Component-boundary safety

The `0x00` separator unambiguously delimits composite-key components (no component contains NUL), so prefix scans never match across a component boundary.

**Validates: Requirements 8.3**

### Property 4: Atomic per-item operations

`SaveDownload` (record plus backoff clear) and `SearchTargets` (scan_state plus subtitle_files plus subtitle_state) each mutate several buckets in one `db.Update`; a crash leaves all or none applied. `ReconcileState` is explicitly excluded (it is chunked; see Property 5).

**Validates: Requirements 3.3, 7.6**

### Property 5: Idempotent, restart-safe reconcile and upserts

Reconcile operations (delete stale row, reset missing-subtitle row) and upserts (`RecordScanState`, auto `SaveDownload`, diff-sync) are individually idempotent, so a crash mid-reconcile or a replayed scan converges to the same result. This is what makes chunked reconcile safe without whole-pass atomicity.

**Validates: Requirements 7.4, 7.5**

### Property 6: Multi-row and lock semantics preserved

Multiple auto rows per `(mt,mid,lang)` are representable (surrogate id); `ClearManualLock` is a non-destructive flag flip that keeps rows visible to `GetState`/`DownloadedRefs`; manual filename ordinal equals the stored row ordinal (parsed from `rec.Path`).

**Validates: Requirements 4.1, 4.3, 4.5**

### Property 7: Auth uniqueness, cascade, and durable sign_count

Username (NOCASE), `(oidc_issuer, oidc_sub)`, `credential_id`, and `key_hash` are unique; `DeleteUser` cascades to passkeys/keys/sessions; `sign_count` is monotonic and durably persisted across restarts (clone detection, CVE-2023-45669); all durable auth writes commit in a bbolt `Update`, so a returned-nil write is crash-durable.

**Validates: Requirements 9.2, 9.3, 9.4, 9.5**

## Error Handling

- **bbolt open contention**: `Options{Timeout: 5s}` fails fast with a clear error rather than hanging the entrypoint.
- **Missing file is not an error**: an absent `subflux.bolt` means first boot, create all buckets empty.
- **Corrupt store file**: bbolt fails to open a corrupt file with a descriptive error (fail fast). A malformed individual record is handled by domain: in the core (derived) buckets it is skipped with a logged warning and rebuilt on the next scan; in the auth buckets and in any lock-bearing or uniqueness-bearing read (`IsManuallyLocked`, auth uniqueness), a decode error FAILS CLOSED (surface an error, treat the lock as held) rather than silently skipping, because a skipped auth/lock record is a silent security regression (orphaned credential, disabled sign_count, or an unlocked manual item).
- **Durable auth writes**: auth mutations commit in a bbolt `Update`, which fsyncs on commit; a returned-nil write is durable, and a failed `Update` rolls back atomically (no partial user or index state).
- **Write txn failure mid-`Update`**: bbolt rolls the tx back atomically; no partial primary/index state; the method returns a wrapped error and the caller retries on the next scan/poll.
- **Malformed stored record**: `codec.Decode` returns an error; read paths skip the bad key with a logged warning rather than aborting the whole scan; a re-scan rewrites it.
- **Long reconcile**: chunked into bounded `Update` batches so the single writer lock is never held across a full library pass.
- **Crash-safety caveat**: bbolt is crash-safe ACID on modern kernels; note the documented ext4 `fast_commit` corruption issue on kernels older than 5.10.94 / 5.15.17, irrelevant to the distroless container on a current host but a stated dependency.

All errors are wrapped with `%w` and operation context so callers branch on the existing sentinel errors.

## Testing Strategy

- **Contract suites are the safety net.** The engine-agnostic `storetest` suite asserts `api.Store` behaviour; running the existing assertions against the new bbolt implementation is the primary regression guard. A new auth-store contract suite covers the `cplieger/auth` library interfaces: uniqueness, cascade, `sign_count` durability across reopen, session expiry, OIDC consume.
- **Behaviour-parity tests for the resolved findings**: multiple auto rows reset by reconcile; `ClearManualLock` preserves history; backoff cleared on `SaveDownload`; two interleaved manual downloads keep filename and ordinal aligned; `DeleteStateByPaths` removes all rows for a shared `video_path` plus orphaned coverage and backoff.
- **Property / fuzz**: key encode/parse round-trips, be64 ordering, JSON codec round-trips, and the index-equals-rescan invariant after random op sequences (`pgregory.net/rapid`, already used in the repo).
- **Race detector** on the store and auth packages (concurrent `View` reads plus serialized `Update` writes; the auth `RWMutex` plus sweeper).
- **Functional suite** (`tests/functional/run.sh`, 27 sections) against the live binary is the behavioural gate: coverage, scans, manual download/lock, hot reload, auth flows.

---

## Migration and Cutover

### Stance: clean break, no migration

No reader for the old `subflux.db`. On deploy, the new binary creates `subflux.bolt` fresh (core and auth buckets); the orphaned `subflux.db` is ignored and may be deleted by the operator. Derived data rebuilds on the first scan; local auth users are recreated via CLI; manual locks/history are not carried over (accepted). Documented in the release notes and steering.

### Build-from-scratch, then swap (ordered)

1. Add `go.etcd.io/bbolt` (the only new dependency; it pulls only `golang.org/x/sys`, already present). No auth-specific dependency: durable auth lives in bbolt buckets in the same file. `internal/fsutil` is untouched and keeps its role for user-path-derived subtitle writes.
2. Write the new `internal/store` (bbolt) implementing `api.Store`, in parallel; add `var _ api.Store = (*store.DB)(nil)`.
3. Write the new `internal/authstore` implementation over the shared `*bbolt.DB` (durable buckets plus in-memory sessions/OIDC) satisfying `authstore.AuthStore`; add the compile-time assertion.
4. Port the contract suites to be engine-agnostic and run them against the new `*store.DB`; add the auth-store contract suite. Green here gates the `main.go` swap.
5. Swap the composition root (`main.go`): open one bbolt DB via `store.Open`, build the core store and `authstore.New(db)` over the shared handle, pass each to the right consumer, update the interface assertions, and change `DefaultDBPath` to `.bolt`.
6. Delete the old SQLite packages (`store/{authdb,coveragedb,migrations,reconcile,txutil}` and the SQLite `store/*.go`); remove `modernc.org/sqlite`; `go mod tidy`.
7. Run `go build ./...`, `go vet ./...`, unit plus contract suites, the race detector, and the encoding/path fuzz targets.
8. Functional suite against the live binary (the behavioural gate).
9. Update steering: rewrite `subflux-store.md`, touch `subflux.md` "Database" line, log an observation for any rule change.

### go.mod / Dockerfile / config diffs

- `go.mod`: remove `modernc.org/sqlite`; add `go.etcd.io/bbolt`. `go mod tidy` drops `libc`/`mathutil`/`memory`/`go-humanize`/`bigfft`/`go-strftime`; `golang.org/x/sys` stays.
- `Dockerfile`: no structural change (`CGO_ENABLED=0` already); binary shrinks; shipped empty `/config` dir stays.
- `config`: `DefaultDBPath` value change only; `config.example.yaml` unchanged.
- compose / homelab: no change; the `/config` bind mount now holds a single `subflux.bolt`.

### Rollback

Clean break, so rollback is redeploy the previous image tag. Keep the old `subflux.db` on the volume until the new version is proven; a revert then loses only what bbolt wrote since cutover (rebuildable by a scan). Do not delete `subflux.db` until a few days of green.

### Risks and mitigations

| Risk | Severity | Mitigation |
|---|---|---|
| Secondary index drift | High | Centralized index maintenance in `index.go`; property test asserts index equals full re-scan |
| Manual ordinal / filename divergence | Medium | `SaveDownload` parses ordinal from `rec.Path`; contract test for two interleaved manual downloads |
| Backoff not cleared on save | Medium | `SaveDownload` clears `search_attempts` in-tx; contract test asserts it |
| Auth durable write lost on crash | Low | bbolt `Update` fsyncs on commit; a returned-nil write is durable, a failed `Update` rolls back atomically |
| `sign_count` not durably bumped | Medium | `UpdatePasskeyAfterLogin` persists; contract test asserts bumped count survives reopen |
| Reconcile starves the poller | Medium | Chunked bounded `Update` batches; reconcile excluded from whole-pass atomicity |
| Long read txn stalls writer growth | Low | Keep `View` short; copy out; no FS I/O inside a read txn |
| File never shrinks after churn | Low | bbolt grows to a high-water mark and reuses freed pages; reclaiming disk requires a separate `bbolt.Compact` (copy-out) step, NOT `tx.WriteTo` (which is a hot backup, not a compactor); see Operability |
| Lost manual history surprises the user | Low | Documented; derived data self-heals on first scan |

## Rejected Alternatives

- **Single composite auto-row key for `subtitle_state` (no surrogate id).** Rejected: cannot represent multiple auto rows (reconcile produces them; a current test asserts it) and forces `ClearManualLock` to delete history. The `NextSequence` surrogate id plus `ix_state_triple` preserves both at a smaller, closer-to-SQL diff (adversarial finding C1).
- **Durable auth in a separate JSON file (via `atomicfile/v2`) instead of bbolt buckets.** An interim choice while the "auth on disk and in memory" directive stood. Rejected once that constraint was lifted: it added a second persistence mechanism, a second backup artifact, an extra dependency, and a whole-document rewrite with build-then-swap and `Result.Durable` checking. Folding durable auth into bbolt buckets gives one engine, one file, one backup, per-key crash-durable writes, and reuses the core store's codec and index discipline; only the genuinely ephemeral sessions and OIDC states stay in memory.
- **`ncruces/go-sqlite3` (WASM SQLite on wazero) instead of a store rewrite.** Achieves the zero-transpiled-C principle with a driver swap, keeping the SQL schema and the ten-table model. Rejected for this effort because the explicit goal includes the memory and binary-size reduction, where bbolt wins, and because the principle decision was already made; recorded here as the lowest-risk path to the principle alone.

## Decisions Confirmed

These were the open questions; resolved here per the adversarial review and the user's directives:

1. **Auth durable backing**: bbolt buckets in the core file (not a separate JSON file, and no `atomicfile` dependency). One engine, one backup, crash-durable on `Update` commit. This supersedes the interim atomicfile-JSON choice after the "on disk" constraint was lifted; `internal/fsutil` is untouched and keeps its subtitle-write role.
2. **Ephemeral auth**: sessions and OIDC states in memory only, with a sweeper; lost on restart by design.
3. **`subtitle_state` keying**: `NextSequence` surrogate id plus `ix_state_triple`, not a composite single-row key.
4. **DB filename**: `subflux.bolt`.
5. **Auth secure-delete**: not attempted via overwrite-in-place (ineffective under bbolt copy-on-write, and defeated by SSD wear-leveling / CoW-filesystem snapshots anyway, which is the same limit SQLite's `secure_delete` hit). The only DB secrets are Argon2id password hashes and emails; API keys are stored as SHA-256 hashes (a hash residue cannot be replayed), passkey public keys and OIDC ids are not secret, and provider API keys live in config files, not the DB. Mitigation: 0600 file mode, hashed-not-plaintext storage, and compaction reclaims freed pages and drops deleted-secret residue (backups are taken from a compacted snapshot). The residual window until the next compaction is an accepted, bounded risk for a single-user self-hosted tool. No separate auth file (the data is already logically isolated to the `auth_*` buckets; a second file is disproportionate for a storage-defeated mitigation).
6. **In-memory session/OIDC cap**: not added. Sessions are created post-authentication and subflux sits behind Authentik forward-auth, so there is no unauthenticated flood vector, and the sweeper bounds both maps. If subflux is ever exposed without a fronting auth proxy, add an `oidc_states` creation cap (the only pre-auth map); documented as a conditional rather than built, to avoid a reject-when-full failure mode for a threat the deployment blocks.

## Out of Scope

- Subtitle search/scoring/sync/provider changes.
- UI changes.
- Any auth feature change (the contract and flows are preserved exactly).
