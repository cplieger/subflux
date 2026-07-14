package boltstore

import (
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
)

// Bucket names. bbolt is a single file of top-level buckets, each an ordered
// []byte -> []byte map. The core store is the single bucket-schema owner: it
// bootstraps every primary AND index bucket below (including the auth buckets)
// in one Update, even though the auth-domain key builders live in
// internal/authstore. Names are string constants; call sites convert with
// []byte(...) for the bbolt API.
const (
	// Primary buckets.
	bucketSearchAttempts = "search_attempts" // attemptKey -> attemptRec
	bucketSubtitleState  = "subtitle_state"  // stateKey(id) -> stateRec
	bucketSubtitleFiles  = "subtitle_files"  // subtitleFileKey -> fileRec
	bucketScanState      = "scan_state"      // scanStateKey -> scanRec
	bucketSyncOffsets    = "sync_offsets"    // syncOffsetKey(path) -> be64(offset_ms)
	bucketPollState      = "poll_state"      // pollStateKey -> RFC3339 timestamp
	bucketAuthUsers      = "auth_users"      // be64(id) -> userRec
	bucketAuthPasskeys   = "auth_passkeys"   //nolint:gosec // G101: bbolt bucket name, not a credential
	bucketAuthAPIKeys    = "auth_api_keys"   //nolint:gosec // G101: bbolt bucket name, not a credential
	bucketMeta           = "meta"            // schema versions + O(1) counters
)

// Index (secondary) bucket names. Each is a sibling bucket maintained inside
// the same write transaction as its primary; keys encode an alternate access
// path and dereference back to a primary key.
const (
	// Core-domain indexes.
	bucketIxStateQuad     = "ix_state_quad"     // stateQuadKey -> projection
	bucketIxStateImported = "ix_state_imported" // stateImportedKey -> (empty)
	bucketIxStateVideo    = "ix_state_video"    // stateVideoKey -> (empty)
	bucketIxScanAt        = "ix_scan_at"        // scanAtKey -> (empty)

	// Auth-domain indexes (builders in internal/authstore; names owned here).
	bucketIxUserName    = "ix_user_name"    // lower(username) -> be64(user_id)
	bucketIxUserOIDC    = "ix_user_oidc"    // issuer 0x00 sub -> be64(user_id)
	bucketIxPasskeyUser = "ix_passkey_user" // be64(user_id) 0x00 credential_id
	bucketIxAPIKeyUser  = "ix_apikey_user"  //nolint:gosec // G101: bbolt bucket name, not a credential
)

// coreBuckets and authBuckets list every bucket the core store bootstraps. Kept
// adjacent to the name constants so store.go's bootstrap (task 2.3) cannot drift
// from the declared schema.
var (
	coreBuckets = [][]byte{
		[]byte(bucketSearchAttempts), []byte(bucketSubtitleState),
		[]byte(bucketSubtitleFiles), []byte(bucketScanState),
		[]byte(bucketSyncOffsets), []byte(bucketPollState), []byte(bucketMeta),
		[]byte(bucketIxStateQuad),
		[]byte(bucketIxStateImported), []byte(bucketIxStateVideo),
		[]byte(bucketIxScanAt),
	}
	authBuckets = [][]byte{
		[]byte(bucketAuthUsers), []byte(bucketAuthPasskeys), []byte(bucketAuthAPIKeys),
		[]byte(bucketIxUserName), []byte(bucketIxUserOIDC),
		[]byte(bucketIxPasskeyUser), []byte(bucketIxAPIKeyUser),
	}
)

// --- search_attempts (primary) ---

// attemptKey builds the search_attempts primary key:
//
//	mt 0x00 mid 0x00 lang 0x00 provider
//
// All four components are NUL-free text, so the key prefix-scans cleanly by
// triplePrefix (all providers for a triple) and round-trips through
// [kv.Split].
func attemptKey(mt api.MediaType, mid, lang string, p api.ProviderID) []byte {
	return kv.Join(string(mt), mid, lang, string(p))
}

// --- subtitle_state (primary) ---

// stateKey builds the subtitle_state primary key from the NextSequence
// surrogate id: be64(id). Big-endian makes ids sort in insertion order, so the
// "id DESC" tiebreak is recoverable. id is a positive surrogate from
// bbolt.NextSequence; the uint64 reinterpretation preserves ordering.
func stateKey(id int64) []byte {
	return kv.Be64(uint64(id)) //nolint:gosec // G115: positive surrogate id, ordering preserved
}

// parseStateKey decodes a subtitle_state primary key back into its surrogate id
// so an index walk can dereference the primary record. ok is false for a
// non-8-byte key.
func parseStateKey(key []byte) (id int64, ok bool) {
	v, ok := kv.DecodeBe64(key)
	if !ok {
		return 0, false
	}
	return int64(v), true //nolint:gosec // G115: inverse of stateKey
}

// --- prefixes shared across subtitle_state / search_attempts / subtitle_files ---

// triplePrefix builds the (media_type, media_id, language) scan prefix:
//
//	mt 0x00 mid 0x00 lang 0x00
//
// The trailing separator is what guarantees component-boundary safety: a prefix
// scan for lang "fr" cannot match a key whose language merely starts with "fr".
// For search_attempts it is the full row prefix (backoff has no variant
// dimension); for ix_state_quad it spans ALL variants of the language, which is
// exactly what the language-scoped reads (DownloadedRefs, any-variant lock
// checks) want.
func triplePrefix(mt api.MediaType, mid, lang string) []byte {
	return append(kv.Join(string(mt), mid, lang), kv.Sep)
}

// quadPrefix builds the (media_type, media_id, language, variant) scan prefix:
//
//	mt 0x00 mid 0x00 lang 0x00 variant 0x00
//
// It extends triplePrefix by the variant component, so a quad scan for
// fr/standard never matches fr/forced rows, and triplePrefix remains a strict
// prefix of every quad key (language-wide scans keep working).
func quadPrefix(mt api.MediaType, mid, lang string, variant api.Variant) []byte {
	return append(kv.Join(string(mt), mid, lang, string(variant)), kv.Sep)
}

// statePrefix returns the ix_state_quad scan prefix for a state read: the
// exact-variant quadPrefix, or the all-variants triplePrefix when variant is
// empty. It is the single place the "empty variant means every variant of the
// language" convention of the lock-domain reads is implemented.
func statePrefix(mt api.MediaType, mid, lang string, variant api.Variant) []byte {
	if variant == "" {
		return triplePrefix(mt, mid, lang)
	}
	return quadPrefix(mt, mid, lang, variant)
}

// mediaPrefix builds the (media_type, media_id) scan prefix:
//
//	mt 0x00 mid 0x00
//
// Used for all-rows-for-a-media-item scans. The trailing separator keeps mid
// "tt1" from matching a key for mid "tt12".
func mediaPrefix(mt api.MediaType, mid string) []byte {
	return append(kv.Join(string(mt), mid), kv.Sep)
}

// typePrefix builds the media-type scan prefix:
//
//	mt 0x00
//
// Used to scan every subtitle_files row of a media type (GetSubtitleFiles with
// no media-id filter). A media-id PREFIX filter (e.g. "tvdb-111-") appends the
// prefix bytes after this separator, which is why the boundary after the type
// must already be sealed by the trailing 0x00.
func typePrefix(mt api.MediaType) []byte {
	return append([]byte(mt), kv.Sep)
}

// --- subtitle_files (primary) ---

// subtitleFileKey builds the subtitle_files primary key:
//
//	mt 0x00 mid 0x00 lang 0x00 variant 0x00 source 0x00 path
//
// lang and variant live in the key so per-media coverage counts come from a
// key-only prefix walk. Filesystem paths are NUL-free, so the key round-trips
// through [kv.Split].
func subtitleFileKey(mt api.MediaType, mid, lang string, variant api.Variant, source api.SubtitleSource, path string) []byte {
	return kv.Join(string(mt), mid, lang, string(variant), string(source), path)
}

// --- scan_state (primary) ---

// scanStateKey builds the scan_state primary key: mt 0x00 mid. One row per
// (media_type, media_id).
func scanStateKey(mt api.MediaType, mid string) []byte {
	return kv.Join(string(mt), mid)
}

// --- sync_offsets (primary) ---

// syncOffsetKey builds the sync_offsets primary key: the bare subtitle path.
// This is distinct from subtitleFileKey; sync_offsets is keyed by path alone.
func syncOffsetKey(path string) []byte {
	return []byte(path)
}

// --- poll_state (primary) ---

// pollStateKey builds the poll_state primary key from the canonical PollKey
// ("sonarr" / "radarr").
func pollStateKey(k api.PollKey) []byte {
	return []byte(k)
}

// --- ix_state_quad (subtitle_state secondary) ---

// stateQuadKey builds the quad index key: mt 0x00 mid 0x00 lang 0x00 variant
// 0x00 be64(id). It shares quadPrefix / triplePrefix / mediaPrefix, so all rows
// for a quad, a language, or a media item are a prefix scan; the trailing
// be64(id) makes each entry unique and dereferences to the primary.
func stateQuadKey(mt api.MediaType, mid, lang string, variant api.Variant, id int64) []byte {
	return append(quadPrefix(mt, mid, lang, variant), stateKey(id)...)
}

// stateQuadKeyID extracts the surrogate id from the trailing 8 bytes of a
// stateQuadKey so a prefix walk can dereference the primary. ok is false for
// a key shorter than the 8-byte id.
func stateQuadKeyID(key []byte) (id int64, ok bool) {
	if len(key) < 8 {
		return 0, false
	}
	v, ok := kv.DecodeBe64(key[len(key)-8:])
	if !ok {
		return 0, false
	}
	return int64(v), true //nolint:gosec // G115: inverse of stateKey suffix
}

// --- ix_state_imported (subtitle_state secondary) ---

// stateImportedKey builds the import-time index key: be64(media_imported) 0x00
// be64(id). Forward walk is oldest-first; reverse walk drives the
// "media_imported DESC, id DESC" state ordering and keyset pagination.
func stateImportedKey(mediaImported time.Time, id int64) []byte {
	return kv.TimeIndexKey(mediaImported, stateKey(id))
}

// --- ix_state_video (subtitle_state secondary) ---

// stateVideoKey builds the video-path reverse-lookup index key:
//
//	video_path 0x00 be64(id)
//
// A single video file backs several rows (auto plus manual across languages),
// so DeleteStateByPaths prefix-scans videoPrefix and collects the ids. The
// fixed 8-byte id suffix keeps the boundary after video_path unambiguous.
func stateVideoKey(videoPath string, id int64) []byte {
	buf := make([]byte, 0, len(videoPath)+1+8)
	buf = append(buf, videoPath...)
	buf = append(buf, kv.Sep)
	buf = append(buf, stateKey(id)...)
	return buf
}

// videoPrefix builds the video-path scan prefix: video_path 0x00. The trailing
// separator stops a prefix scan for one path from matching a path that merely
// shares its prefix.
func videoPrefix(videoPath string) []byte {
	buf := make([]byte, 0, len(videoPath)+1)
	buf = append(buf, videoPath...)
	buf = append(buf, kv.Sep)
	return buf
}

// splitStateVideoKey parses a stateVideoKey back into its video path and
// surrogate id. The id is the trailing 8 bytes preceded by a separator; the
// video path is everything before that separator. ok is false for a malformed
// key.
func splitStateVideoKey(key []byte) (videoPath string, id int64, ok bool) {
	if len(key) < 9 || key[len(key)-9] != kv.Sep {
		return "", 0, false
	}
	v, ok := kv.DecodeBe64(key[len(key)-8:])
	if !ok {
		return "", 0, false
	}
	return string(key[:len(key)-9]), int64(v), true //nolint:gosec // G115: inverse of stateKey suffix
}

// --- ix_scan_at (scan_state secondary) ---

// scanAtKey builds the scan-recency index key: be64(scanned_at) 0x00 primary,
// where primary is the [scanStateKey]. RecentlyScanned seeks to be64(cutoff)
// and walks forward; LastScanTime reads the last entry.
func scanAtKey(scannedAt time.Time, primary []byte) []byte {
	return kv.TimeIndexKey(scannedAt, primary)
}
