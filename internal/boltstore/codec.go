package boltstore

import (
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/cplieger/subflux/internal/api"
	boltkv "github.com/cplieger/subflux/internal/store/kv"
)

// This file owns the bbolt record value structs for the core domain, the
// thin typed (de)serialisation wrappers over the boltkv codec, the
// schema-version constants/keys/helpers, and the per-bucket decode policy.
//
// Bucket VALUES are JSON (via boltkv.Encode/Decode): stdlib-only, debuggable,
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
// media_type, media_id, and language are NOT stored here: they are carried by
// the ix_state_triple index key (mt 0x00 mid 0x00 lang 0x00 be64(id)), so a
// state read takes those from the index walk and the rest from this record.
// Together they reconstruct an api.StateEntry. Provider is api.ProviderID to
// match api.DownloadRecord.ProviderName / api.StateEntry.Provider exactly.
type stateRec struct {
	ID            int64          `json:"id"` // NextSequence surrogate
	Provider      api.ProviderID `json:"provider"`
	ReleaseName   string         `json:"release_name"`
	Path          string         `json:"path"`
	Title         string         `json:"title"`
	ImdbID        string         `json:"imdb_id"`
	ReleaseTag    string         `json:"release_tag"`
	Score         int            `json:"score"`
	Season        int            `json:"season"`
	Episode       int            `json:"episode"`
	Manual        bool           `json:"manual"`
	VideoPath     string         `json:"video_path"`
	MediaImported time.Time      `json:"media_imported"`
}

// fileRec is the subtitle_files value. The media_type, media_id, language,
// variant, source, and path all live in the key (so per-media coverage is a
// key-only prefix walk), leaving only these fields in the value. With the key
// components they reconstruct an api.SubtitleEntry.
type fileRec struct {
	Codec     string    `json:"codec"`
	OffsetMs  int64     `json:"offset_ms"`
	UpdatedAt time.Time `json:"updated_at"`
}

// scanRec is the scan_state value, one row per (media_type, media_id) carried
// in the key. With the key components these fields reconstruct an
// api.ScanStateRow / mirror an api.ScanRecord.
type scanRec struct {
	Title     string    `json:"title"`
	AudioLang string    `json:"audio_lang"`
	Season    int       `json:"season"`
	Episode   int       `json:"episode"`
	ScannedAt time.Time `json:"scanned_at"`
}

// sync_offsets stores a bare be64(offset_ms) value (keyed by path) and
// poll_state stores an RFC3339 timestamp string; neither is a JSON record, so
// they have no struct here (see keys.go and the pollstate/subfiles domains).

// --- Typed codec wrappers ---

// encodeRecord serialises a core-domain record value as JSON via the shared
// boltkv codec. It is a thin typed wrapper so call sites read as
// encodeRecord(&rec) rather than threading the codec mechanism; PutIndexed
// already encodes for index-maintained writes, so this is for the rare direct
// put.
func encodeRecord[T any](v *T) ([]byte, error) {
	return boltkv.Encode(v)
}

// decodeRecord decodes a core-domain record value into v, applying the
// supplied decode-failure policy. It is a thin typed wrapper over
// boltkv.DecodeOrHandle: on a cursor walk the caller continues to the next
// record when skip is true (TolerantSkip), or aborts the read when err is
// non-nil (FailClosed). Callers pick the mode with bucketDecodeMode, overriding
// to boltkv.FailClosed for lock- or uniqueness-bearing reads (see below).
func decodeRecord[T any](mode boltkv.DecodeMode, bucket string, key, data []byte, v *T) (skip bool, err error) {
	return boltkv.DecodeOrHandle(mode, bucket, key, data, v)
}

// --- Schema versioning ---
//
// The core and auth domains version independently via two SEPARATE meta keys,
// so an additive change to one does not bump the other. This build writes and
// understands coreSchemaVersion / authSchemaVersion; the actual write-on-
// bootstrap happens in store.go's bucket bootstrap (task 2.3) using
// writeSchemaVersion, after verifySchemaVersions has confirmed the on-disk
// file is not from a future, breaking version.

const (
	// coreSchemaVersion is the core-domain (search_attempts, subtitle_state,
	// subtitle_files, scan_state, sync_offsets, poll_state) value-schema
	// version this build writes and understands.
	coreSchemaVersion uint64 = 1

	// authSchemaVersion is the auth-domain (auth_users, auth_passkeys,
	// auth_api_keys) value-schema version. It evolves independently of
	// coreSchemaVersion.
	authSchemaVersion uint64 = 1
)

// meta-bucket scalar keys for the two schema versions. Kept as separate keys so
// the core and auth schemas evolve independently. There is NO migration by
// design (clean break); these exist only for detect-and-refuse on a future
// breaking change.
var (
	metaKeyCoreSchemaVersion = []byte("core_schema_version")
	metaKeyAuthSchemaVersion = []byte("auth_schema_version")
)

// readSchemaVersion reads an 8-byte big-endian schema-version scalar from the
// meta bucket. present is false when the meta bucket is absent (an
// un-bootstrapped or fresh file) or the key is unset, in which case version is
// zero.
func readSchemaVersion(tx *bolt.Tx, key []byte) (version uint64, present bool) {
	mb := tx.Bucket([]byte(bucketMeta))
	if mb == nil {
		return 0, false
	}
	return boltkv.GetUint64(mb, key)
}

// writeSchemaVersion writes version as an 8-byte big-endian scalar at key in
// the meta bucket. It is called by the bucket bootstrap (task 2.3) inside the
// same Update that creates the buckets. It errors if the meta bucket does not
// exist (bootstrap must create it first).
func writeSchemaVersion(tx *bolt.Tx, key []byte, version uint64) error {
	mb := tx.Bucket([]byte(bucketMeta))
	if mb == nil {
		return fmt.Errorf("boltstore: write schema version: meta bucket %q not found", bucketMeta)
	}
	return boltkv.PutUint64(mb, key, version)
}

// checkSchemaVersion applies the detect-and-refuse policy: an absent version
// (fresh file) is accepted, an equal-or-lower stored version is accepted
// (lower is forward-compatible since value changes are additive by design),
// and a stored version NEWER than current is refused, because the file was
// written by a future build whose schema this build does not understand and
// there is no migration path. domain names the schema for the error message.
func checkSchemaVersion(domain string, stored uint64, present bool, current uint64) error {
	if !present {
		return nil
	}
	if stored > current {
		return fmt.Errorf("boltstore: %s schema version %d on disk is newer than this build's %d; "+
			"the store was written by a newer version and cannot be opened (no downgrade migration exists)",
			domain, stored, current)
	}
	return nil
}

// verifySchemaVersions reads both schema-version meta keys and applies
// checkSchemaVersion to each, returning the first refusal. It is read-only and
// is run during Open (task 2.3) before any write. A fresh file (no meta bucket
// yet, or unset keys) passes; the bootstrap then writes the current versions.
func verifySchemaVersions(tx *bolt.Tx) error {
	coreStored, corePresent := readSchemaVersion(tx, metaKeyCoreSchemaVersion)
	if err := checkSchemaVersion("core", coreStored, corePresent, coreSchemaVersion); err != nil {
		return err
	}
	authStored, authPresent := readSchemaVersion(tx, metaKeyAuthSchemaVersion)
	return checkSchemaVersion("auth", authStored, authPresent, authSchemaVersion)
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
// guards) or a uniqueness-bearing read MUST pass boltkv.FailClosed explicitly
// even though subtitle_state defaults to TolerantSkip, and MUST treat the
// affected triple as locked on a decode error. bucketDecodeMode gives the
// per-bucket DEFAULT for ordinary scans; the convention for those special
// reads is to override.
func bucketDecodeMode(bucket string) boltkv.DecodeMode {
	switch bucket {
	case bucketSearchAttempts, bucketSubtitleState, bucketSubtitleFiles, bucketScanState:
		return boltkv.TolerantSkip
	default:
		// auth_users, auth_passkeys, auth_api_keys, meta, and any unknown
		// bucket fail closed.
		return boltkv.FailClosed
	}
}
