package boltstore

import (
	"fmt"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file owns the bbolt record value structs for the core domain, the
// thin typed (de)serialisation wrappers over the kv codec, the
// schema-version constants/keys/helpers, and the per-bucket decode policy.
//
// Bucket VALUES are JSON (via kv.Encode/Decode): stdlib-only, debuggable,
// and additively forward-compatible without a migration script. Bucket KEYS
// stay binary and live in keys.go. The mt/mid/lang components of most records
// live in the KEY (and in the secondary indexes), so the value structs below
// deliberately omit them; a read reconstructs an api.* value from the key
// components plus the decoded record.

// --- Core-domain record value structs (JSON-encoded bbolt bucket values) ---

// attemptRec is the search_attempts value: per-(media_type, media_id,
// language, provider) adaptive-backoff state. The four key components plus
// these fields reconstruct an api.BackoffEntry.
type attemptRec struct {
	LastTried time.Time `json:"last_tried"`
	NextRetry time.Time `json:"next_retry"`
	Failures  int       `json:"failures"`
}

// stateRec is the subtitle_state value, keyed by the be64(id) surrogate. The
// value is SELF-CONTAINED: it carries the (media_type, media_id, language,
// variant) quad and the surrogate id even though both also appear in the
// ix_state_quad index key and the primary key respectively. That duplication
// is deliberate: reads that start from a surrogate id (GetState's reverse
// ix_state_imported walk, DeleteStateByPaths' ix_state_video walk, reconcile's
// primary scan) recover the quad from one decode instead of rebuilding an
// id -> quad map from a full index walk, and a row dumped by the bbolt CLI is
// meaningful on its own. The putState chokepoint derives the ix_state_quad key
// FROM these value fields, so key and value can never disagree. Provider is
// api.ProviderID to match api.DownloadRecord.ProviderName /
// api.StateEntry.Provider exactly.
type stateRec struct {
	MediaImported time.Time      `json:"media_imported"`
	MediaType     api.MediaType  `json:"media_type"`
	MediaID       string         `json:"media_id"`
	Language      string         `json:"language"`
	Variant       api.Variant    `json:"variant"`
	Provider      api.ProviderID `json:"provider"`
	ReleaseName   string         `json:"release_name"`
	Path          string         `json:"path"`
	Title         string         `json:"title"`
	ImdbID        string         `json:"imdb_id"`
	ReleaseTag    string         `json:"release_tag"`
	VideoPath     string         `json:"video_path"`
	ID            int64          `json:"id"` // NextSequence surrogate
	Score         int            `json:"score"`
	Season        int            `json:"season"`
	Episode       int            `json:"episode"`
	Manual        bool           `json:"manual"`
}

// fileRec is the subtitle_files value. The media_type, media_id, language,
// variant, source, and path all live in the key (so per-media coverage is a
// key-only prefix walk), leaving only these fields in the value. With the key
// components they reconstruct an api.SubtitleEntry. A subtitle's cumulative
// sync offset is NOT stored here: it lives solely in the sync_offsets bucket
// (keyed by bare path), which GetSubtitleFiles joins at read time.
type fileRec struct {
	UpdatedAt time.Time `json:"updated_at"`
	Codec     string    `json:"codec"`
}

// scanRec is the scan_state value, one row per (media_type, media_id) carried
// in the key. With the key components these fields reconstruct an
// api.ScanStateRow / mirror an api.ScanRecord.
type scanRec struct {
	ScannedAt time.Time `json:"scanned_at"`
	Title     string    `json:"title"`
	AudioLang string    `json:"audio_lang"`
	Season    int       `json:"season"`
	Episode   int       `json:"episode"`
	// Searched records whether provider work ran to completion for this
	// stamp (false = inventory-only visit). Additive field: rows written
	// before it existed decode as false, which reads as "no attested
	// provider work", the conservative truth.
	Searched bool `json:"searched,omitempty"`
}

// sync_offsets stores a bare be64(offset_ms) value (keyed by path) and
// poll_state stores an RFC3339 timestamp string; neither is a JSON record, so
// they have no struct here (see keys.go and the pollstate/subfiles domains).

// --- Typed codec wrappers ---

// encodeRecord serialises a core-domain record value as JSON via the shared
// kv codec. It is a thin typed wrapper so call sites read as
// encodeRecord(&rec) rather than threading the codec mechanism; PutIndexed
// already encodes for index-maintained writes, so this is for the rare direct
// put.
func encodeRecord[T any](v *T) ([]byte, error) {
	return kv.Encode(v)
}

// decodeRecord decodes a core-domain record value into v, applying the
// supplied decode-failure policy. It is a thin typed wrapper over
// kv.DecodeOrHandle: on a cursor walk the caller continues to the next
// record when skip is true (TolerantSkip), or aborts the read when err is
// non-nil (FailClosed). Callers pick the mode with bucketDecodeMode, overriding
// to kv.FailClosed for lock- or uniqueness-bearing reads (see below).
func decodeRecord[T any](mode kv.DecodeMode, bucket string, key, data []byte, v *T) (skip bool, err error) {
	return kv.DecodeOrHandle(mode, bucket, key, data, v)
}

// --- Schema versioning ---
//
// The core and auth domains version independently via two SEPARATE meta keys,
// so an additive change to one does not bump the other. This build writes and
// understands coreSchemaVersion / authSchemaVersion. Open reads the stamps
// strictly (readRequiredVersion in migrate.go: missing, malformed, older,
// current, and newer are all distinguished), runs any pending migration-ladder
// steps, and only then bootstraps buckets and re-stamps the current versions
// (store.go). Additive value changes never bump a version; a breaking change
// bumps it and ships a ladder step (migrations.go).

const (
	// coreSchemaVersion is the core-domain (search_attempts, subtitle_state,
	// subtitle_files, scan_state, sync_offsets, poll_state) schema version
	// this build writes and understands.
	//
	// v1 is the first (and, pre-release, only) core schema: subtitle_state
	// keyed by a be64 surrogate id with a self-contained value (the record
	// carries its own media_type/media_id/language/variant quad, and every
	// state index key derives from the value), the ix_state_quad /
	// ix_state_imported / ix_state_video indexes, and no secondary index on
	// search_attempts. The counter was reset to 1 before the first release
	// (the internal pre-release iterations that bumped it never shipped);
	// a dev file stamped with a higher version is refused by the
	// newer-than-binary guard (restore a matching build or a snapshot).
	coreSchemaVersion uint64 = 1

	// authSchemaVersion is the auth-domain (auth_users, auth_passkeys,
	// auth_api_keys) value-schema version. It evolves independently of
	// coreSchemaVersion.
	authSchemaVersion uint64 = 1

	// coreSchemaBaseVersion is the FIRST core schema version ever shipped:
	// the fixed floor of the core migration ladder. It NEVER changes once
	// set; only coreSchemaVersion moves. validateLadder (migrate.go) accepts
	// an empty core ladder only while coreSchemaVersion still equals this
	// base — a version bump without a registered base-to-current path
	// refuses the open.
	coreSchemaBaseVersion uint64 = 1

	// authSchemaBaseVersion is coreSchemaBaseVersion's auth-domain sibling.
	authSchemaBaseVersion uint64 = 1
)

// meta-bucket scalar keys for the two schema versions. Kept as separate keys so
// the core and auth schemas evolve independently: each migration-ladder step
// advances exactly its OWN domain's stamp (in the same transaction as the
// step's transform) and never touches the other domain's key.
var (
	metaKeyCoreSchemaVersion = []byte("core_schema_version")
	metaKeyAuthSchemaVersion = []byte("auth_schema_version")
)

// writeSchemaVersion writes version as an 8-byte big-endian scalar at key in
// the meta bucket. It is called by the bucket bootstrap (store.go) inside the
// same Update that creates the buckets, and by the migration runner
// (migrate.go) to advance a domain's stamp in the same transaction as the
// step's transform. It errors if the meta bucket does not exist (bootstrap
// creates it; a populated pre-ladder file always has it, since its stamp was
// just read from it).
func writeSchemaVersion(tx *bolt.Tx, key []byte, version uint64) error {
	mb := tx.Bucket([]byte(bucketMeta))
	if mb == nil {
		return fmt.Errorf("boltstore: write schema version: meta bucket %q not found", bucketMeta)
	}
	return kv.PutUint64(mb, key, version)
}

// --- Decode policy ---
//
// Requirement 13.4: a derived core bucket skips an undecodable record with a
// logged warning (the next full scan rebuilds it), while an auth bucket, or any
// lock- or uniqueness-bearing read, fails closed (returns an error and treats
// any affected lock as held) so a security-relevant record is never silently
// dropped.

// TolerantSkip (skip-with-warning) buckets: the four derived core buckets whose
// contents the next full scan rebuilds.
//
//   - search_attempts (adaptive backoff; a missing row just means eligible)
//   - subtitle_state  (auto rows re-detected by the scanner)
//   - subtitle_files  (coverage re-detected by the scanner)
//   - scan_state      (re-populated on the next scan)
//
// FAIL-CLOSED buckets (everything else): auth_users, auth_passkeys,
// auth_api_keys, and meta. An undecodable auth record must abort the read, not
// vanish.
//
// OVERRIDE: a lock-bearing read (IsManuallyLocked and the manual-row reads it
// guards) or a uniqueness-bearing read MUST pass kv.FailClosed explicitly
// even though subtitle_state defaults to TolerantSkip, and MUST treat the
// affected triple as locked on a decode error. bucketDecodeMode gives the
// per-bucket DEFAULT for ordinary scans; the convention for those special
// reads is to override.
func bucketDecodeMode(bucket string) kv.DecodeMode {
	switch bucket {
	case bucketSearchAttempts, bucketSubtitleState, bucketSubtitleFiles, bucketScanState:
		return kv.TolerantSkip
	default:
		// auth_users, auth_passkeys, auth_api_keys, meta, and any unknown
		// bucket fail closed.
		return kv.FailClosed
	}
}
