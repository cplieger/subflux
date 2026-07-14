package boltstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file holds the subtitle_state domain: the DownloadStore,
// ManualLockStore, HistoryStore, and the state-query half of QueryStore.

// SaveDownload records (or upgrades) a subtitle download in one write
// transaction, preserving the old SQLite store's observable behaviour while
// keying state by the (media_type, media_id, language, variant) quad:
//
//   - It first clears ALL adaptive-backoff rows for the language triple
//     (success clears backoff; backoff has no variant dimension), each through
//     the deleteAttempt chokepoint so the attempts counter stays consistent
//     (Requirement 3.3).
//   - For an AUTO download (Meta.Manual false), it updates every existing auto
//     row for the QUAD in place, preserving each row's original media_imported
//     and surrogate id (Requirement 3.1); if no auto row exists for the quad it
//     inserts a fresh one with media_imported = now (Requirement 3.2). An
//     fr/forced download therefore never overwrites the fr/standard auto row.
//   - For a MANUAL download (Meta.Manual true), it always appends a new manual
//     row (the manual row is the quad's automation lock), storing rec.Path
//     verbatim so the row's ordinal equals the ordinal the handler already
//     encoded in the filename (Requirement 4.5); the ordinal is authoritative
//     in rec.Path and is never re-derived from the bucket here.
//
// An empty rec.Variant is normalized to VariantStandard, so legacy callers
// that predate the variant dimension keep writing standard rows.
//
// All mutations route through the putState / deleteAttempt index-maintenance
// chokepoints, so the secondary indexes and the maintained meta counters are
// updated in the same all-or-nothing transaction as the primary writes.
func (d *DB) SaveDownload(_ context.Context, rec *api.DownloadRecord) error {
	m := rec.Meta
	if m == nil {
		m = &api.DownloadMeta{}
	}
	variant := rec.Variant
	if variant == "" {
		variant = api.VariantStandard
	}
	slog.Debug("SaveDownload",
		"media_type", rec.MediaType, "media_id", rec.MediaID,
		"lang", rec.Language, "variant", variant, "provider", rec.ProviderName,
		"release", rec.ReleaseName, "score", rec.Score,
		"manual", m.Manual)

	return d.db.Update(func(tx *bolt.Tx) error {
		// Clear adaptive search state for every provider on the language
		// triple: we got what we needed, so the language is no longer backed
		// off (search_attempts has no variant dimension).
		if err := clearTripleBackoff(tx, rec.MediaType, rec.MediaID, rec.Language); err != nil {
			return err
		}
		if m.Manual {
			// Manual rows are always appended (they act as the lock); the
			// ordinal lives in rec.Path and is stored verbatim.
			return insertStateRow(tx, rec, m, variant, true, time.Now())
		}
		return saveAutoRow(tx, rec, m, variant)
	})
}

// saveAutoRow upserts the auto (manual=false) rows for a quad. It updates
// every existing auto row in place, preserving that row's id and
// media_imported and only overwriting the mutable download fields (matching the
// old `UPDATE subtitle_state SET ... WHERE manual = 0` which left media_imported
// untouched). When the quad has no auto row it inserts a fresh one with
// media_imported = now.
func saveAutoRow(tx *bolt.Tx, rec *api.DownloadRecord, m *api.DownloadMeta, variant api.Variant) error {
	rows, err := collectStateRows(tx, rec.MediaType, rec.MediaID, rec.Language, variant)
	if err != nil {
		return err
	}
	updated := false
	for i := range rows {
		sr := rows[i]
		if sr.Manual {
			continue // never overwrite a manual lock row
		}
		// Preserve sr.ID, sr.MediaImported, sr.Manual (false), and the quad;
		// overwrite the mutable download fields, mirroring the old
		// auto-upgrade UPDATE.
		sr.Provider = rec.ProviderName
		sr.ReleaseName = rec.ReleaseName
		sr.Score = rec.Score
		sr.Path = rec.Path
		sr.Title = m.Title
		sr.ImdbID = m.ImdbID
		sr.Season = m.Season
		sr.Episode = m.Episode
		sr.ReleaseTag = m.ReleaseTag
		sr.VideoPath = m.VideoPath
		if err := putState(tx, &sr); err != nil {
			return err
		}
		updated = true
	}
	if updated {
		return nil
	}
	// No auto row existed for the quad: insert one with media_imported = now.
	return insertStateRow(tx, rec, m, variant, false, time.Now())
}

// insertStateRow allocates a surrogate id and inserts a new subtitle_state row
// for the quad via the putState chokepoint (which maintains ix_state_quad,
// ix_state_imported, ix_state_video and the downloads counter). manual marks the
// row as auto (false) or a manual lock (true); imported sets media_imported.
func insertStateRow(tx *bolt.Tx, rec *api.DownloadRecord, m *api.DownloadMeta, variant api.Variant, manual bool, imported time.Time) error {
	sb := tx.Bucket([]byte(bucketSubtitleState))
	if sb == nil {
		return errors.New("boltstore: subtitle_state bucket not found")
	}
	id, _, err := kv.NextID(sb)
	if err != nil {
		return err
	}
	sr := stateRec{
		ID:            int64(id), //nolint:gosec // G115: surrogate id from NextSequence, positive and bounded
		MediaType:     rec.MediaType,
		MediaID:       rec.MediaID,
		Language:      rec.Language,
		Variant:       variant,
		Provider:      rec.ProviderName,
		ReleaseName:   rec.ReleaseName,
		Path:          rec.Path,
		Title:         m.Title,
		ImdbID:        m.ImdbID,
		ReleaseTag:    m.ReleaseTag,
		Score:         rec.Score,
		Season:        m.Season,
		Episode:       m.Episode,
		Manual:        manual,
		VideoPath:     m.VideoPath,
		MediaImported: imported,
	}
	return putState(tx, &sr)
}

// collectStateRows returns every subtitle_state record under the (mt, mid,
// lang, variant) quad — or, when variant is empty, under every variant of the
// language — by walking ix_state_quad and dereferencing each primary. The
// records are self-contained (they carry their quad), so callers rewrite them
// through putState directly. It is a lock-bearing read (the auto-vs-manual
// partition decides whether a manual lock is overwritten), so it FAILS CLOSED
// on a primary decode error rather than tolerantly skipping. It is shared by
// the subtitle_state domain methods (tasks 4.1-4.4).
func collectStateRows(tx *bolt.Tx, mt api.MediaType, mid, lang string, variant api.Variant) ([]stateRec, error) {
	idx := tx.Bucket([]byte(bucketIxStateQuad))
	if idx == nil {
		return nil, errors.New("boltstore: ix_state_quad bucket not found")
	}
	sb := tx.Bucket([]byte(bucketSubtitleState))
	if sb == nil {
		return nil, errors.New("boltstore: subtitle_state bucket not found")
	}
	prefix := statePrefix(mt, mid, lang, variant)
	var out []stateRec
	c := idx.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		id, ok := stateQuadKeyID(k)
		if !ok {
			continue // malformed index key; skip defensively
		}
		raw := sb.Get(stateKey(id))
		if raw == nil {
			continue // index/primary drift: no primary for this access path
		}
		var sr stateRec
		if err := kv.Decode(raw, &sr); err != nil {
			return nil, fmt.Errorf("boltstore: decode subtitle_state id=%d: %w", id, err)
		}
		out = append(out, sr)
	}
	return out, nil
}

// clearTripleBackoff deletes every search_attempts row under the triple (any
// provider, including an empty-provider row) via
// the deleteAttempt chokepoint, matching the old
// `DELETE FROM search_attempts WHERE media_type = ? AND media_id = ? AND language = ?`.
// Keys are collected before deletion (bbolt skips the next key if you delete
// during cursor iteration).
func clearTripleBackoff(tx *bolt.Tx, mt api.MediaType, mid, lang string) error {
	b := tx.Bucket([]byte(bucketSearchAttempts))
	if b == nil {
		return errors.New("boltstore: search_attempts bucket not found")
	}
	prefix := triplePrefix(mt, mid, lang)
	var providers []api.ProviderID
	c := b.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		providers = append(providers, api.ProviderID(k[len(prefix):]))
	}
	for _, p := range providers {
		if _, err := deleteAttempt(tx, mt, mid, lang, p); err != nil {
			return err
		}
	}
	return nil
}

// parseManualOrdinal extracts the manual subtitle ordinal N from a path of the
// form movie.lang[.variant].N.srt, mirroring the old SQLite NextManualNumber
// extraction (strip the .srt suffix, then take the trailing run of digits). It
// returns ok=false when no trailing numeric component is present. Shared by the
// manual-lock methods (task 4.3) and used to assert SaveDownload stores the
// path's ordinal unchanged (Requirement 4.5).
func parseManualOrdinal(path string) (int, bool) {
	s := strings.TrimSuffix(path, ".srt")
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	digits := s[i:]
	if digits == "" {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return n, true
}

// DownloadedRefs returns every distinct (release_name, provider) pair already
// downloaded for the language, across BOTH auto and manual rows of EVERY
// variant, excluding rows whose release_name is empty. It mirrors the old
// SQLite `SELECT DISTINCT release_name, provider FROM subtitle_state WHERE ...
// AND release_name <> ”` (Requirement 3.5): the manual-search popup uses it to
// mark every previously-saved subtitle as already on disk, and the popup lists
// all variants of the language, so this read is deliberately language-scoped
// (empty-variant scan). An empty release_name can never match a search
// result's non-empty ReleaseName, so it is dropped (matching the legacy WHERE
// clause).
//
// release_name lives on the primary row, not in the ix_state_quad projection
// (which carries only manual/score/provider), so this walks the language via
// the shared collectStateRows helper, which dereferences each primary and
// fails closed on a decode error. Distinctness is preserved with first-seen
// ordering over the scan (ascending (variant, surrogate id)).
func (d *DB) DownloadedRefs(_ context.Context, mediaType api.MediaType, mediaID, language string) ([]api.DownloadedRef, error) {
	var out []api.DownloadedRef
	seen := make(map[api.DownloadedRef]struct{})
	err := d.db.View(func(tx *bolt.Tx) error {
		rows, err := collectStateRows(tx, mediaType, mediaID, language, "")
		if err != nil {
			return err
		}
		for i := range rows {
			r := &rows[i]
			if r.ReleaseName == "" {
				continue // legacy/empty release name never matches a result
			}
			ref := api.DownloadedRef{ReleaseName: r.ReleaseName, Provider: r.Provider}
			if _, ok := seen[ref]; ok {
				continue // DISTINCT
			}
			seen[ref] = struct{}{}
			out = append(out, ref)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CurrentScore returns the highest auto-row (manual=false) score for the quad,
// that winning row's media_imported, and a found flag that is false when the
// quad has no auto row. It mirrors the old SQLite
// `SELECT score, media_imported FROM subtitle_state WHERE ... AND manual = 0
// ORDER BY score DESC LIMIT 1` (Requirement 3.4), refined per variant: manual
// rows are ignored, an fr/forced row never answers for fr/standard, and no
// auto row means (0, zero-time, false, nil) with no error.
//
// The ix_state_quad projection carries the score but not media_imported, so
// the winning row is dereferenced via the shared collectStateRows helper
// (which fails closed on a decode error). On a score tie the first row in
// quad-scan order (ascending surrogate id) wins, a deterministic choice the
// contract leaves open.
func (d *DB) CurrentScore(_ context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) (score int, mediaImported time.Time, found bool, err error) {
	err = d.db.View(func(tx *bolt.Tx) error {
		rows, derr := collectStateRows(tx, mediaType, mediaID, language, variant)
		if derr != nil {
			return derr
		}
		for i := range rows {
			r := &rows[i]
			if r.Manual {
				continue // auto rows only
			}
			if !found || r.Score > score {
				score = r.Score
				mediaImported = r.MediaImported
				found = true
			}
		}
		return nil
	})
	if err != nil {
		return 0, time.Time{}, false, err
	}
	return score, mediaImported, found, nil
}

// walkStateProjection walks ix_state_quad for the (mt, mid, lang, variant)
// quad — or every variant of the language when variant is empty — and invokes
// fn with each entry's decoded (manual, score, provider) projection, WITHOUT
// dereferencing the primary subtitle_state row. It is the index-only read path
// that lets IsManuallyLocked and ManualDownloadCount answer the "manual"
// question from a single index walk (Requirement 18.3): the projection value
// carries the manual flag, so neither method touches a primary.
//
// These are lock-bearing reads, so a malformed projection value FAILS CLOSED
// (Requirement 13.4): it returns an error and the caller treats the quad as
// locked rather than silently dropping the entry. decodeStateProjection only
// returns ok=false for a value shorter than the fixed manual+score prefix,
// which a correctly maintained index can never produce.
func walkStateProjection(tx *bolt.Tx, mt api.MediaType, mid, lang string, variant api.Variant, fn func(manual bool, score int, provider api.ProviderID)) error {
	idx := tx.Bucket([]byte(bucketIxStateQuad))
	if idx == nil {
		return errors.New("boltstore: ix_state_quad bucket not found")
	}
	prefix := statePrefix(mt, mid, lang, variant)
	c := idx.Cursor()
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		manual, score, provider, ok := decodeStateProjection(v)
		if !ok {
			return fmt.Errorf("boltstore: malformed ix_state_quad projection for %s/%s/%s/%s", mt, mid, lang, variant)
		}
		fn(manual, score, provider)
	}
	return nil
}

// IsManuallyLocked reports whether the quad has at least one manual row, so it
// should be excluded from all automated actions. An empty variant asks whether
// ANY variant of the language is locked (the language-level summary the manual
// search popup shows). It mirrors the old SQLite
// `SELECT EXISTS(... WHERE ... AND manual = 1)` (Requirement 4.2), refined per
// variant.
//
// The answer comes purely from the ix_state_quad projection's manual flag via
// walkStateProjection, so no primary subtitle_state row is dereferenced
// (Requirement 18.3). As a lock-bearing read it fails closed: if the projection
// cannot be read the quad is reported locked AND the error is returned, so a
// decode fault can never silently unlock an item (Requirement 13.4).
func (d *DB) IsManuallyLocked(_ context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) (bool, error) {
	locked := false
	err := d.db.View(func(tx *bolt.Tx) error {
		return walkStateProjection(tx, mediaType, mediaID, language, variant, func(manual bool, _ int, _ api.ProviderID) {
			if manual {
				locked = true
			}
		})
	})
	if err != nil {
		return true, err // fail closed: treat the lock as held on a read fault
	}
	return locked, nil
}

// ClearManualLock removes the quad's manual lock so automated scans and
// upgrades resume; an empty variant clears the locks of EVERY variant of the
// language (the CLI/API "unlock this item+language" default). It is
// NON-destructive: it flips each manual row's flag to auto (manual=false) and
// rewrites the row, preserving its id, path, score, provider, release_name,
// and media_imported, so the rows stay visible to GetState and DownloadedRefs
// (Requirement 4.3). This mirrors the old SQLite `UPDATE subtitle_state SET
// manual = 0 WHERE ... AND manual = 1`, which was a flag flip, not a delete.
//
// Each flipped row routes through the putState chokepoint (the record carries
// its own quad), so the ix_state_quad projection is rewritten
// with manual=false in the same transaction (the row keeps its id, so its
// other index entries are unchanged and the downloads counter is not
// double-counted). A quad with no manual row is a no-op.
func (d *DB) ClearManualLock(_ context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) error {
	slog.Debug("ClearManualLock",
		"media_type", mediaType, "media_id", mediaID, "lang", language, "variant", variant)
	return d.db.Update(func(tx *bolt.Tx) error {
		rows, err := collectStateRows(tx, mediaType, mediaID, language, variant)
		if err != nil {
			return err
		}
		for i := range rows {
			sr := rows[i]
			if !sr.Manual {
				continue // only flip the manual lock rows
			}
			sr.Manual = false
			if err := putState(tx, &sr); err != nil {
				return err
			}
		}
		return nil
	})
}

// ManualDownloadCount returns how many manual rows exist for the quad (exact
// variant), mirroring the old SQLite `SELECT COUNT(*) ... WHERE ... AND
// manual = 1` (Requirement 15.6). Like IsManuallyLocked it is served purely
// from the ix_state_quad projection's manual flag via walkStateProjection,
// with no primary dereference (Requirement 18.3).
func (d *DB) ManualDownloadCount(_ context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) (int, error) {
	count := 0
	err := d.db.View(func(tx *bolt.Tx) error {
		return walkStateProjection(tx, mediaType, mediaID, language, variant, func(manual bool, _ int, _ api.ProviderID) {
			if manual {
				count++
			}
		})
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// ManualSubtitlePaths returns the subtitle file paths from every manual row for
// the quad — or every variant of the language when variant is empty —
// excluding rows with an empty path, mirroring the old SQLite
// `SELECT path ... WHERE ... AND manual = 1 AND path != ”` (Requirement 15.6).
// maybeRevertManualLock uses it (exact variant) to check which manual files
// still exist on disk.
//
// The path lives on the primary row, not in the ix_state_quad projection
// (which carries only manual/score/provider), so this walks the quad via the
// shared collectStateRows helper, which dereferences each primary and fails
// closed on a decode error.
func (d *DB) ManualSubtitlePaths(_ context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) ([]string, error) {
	var paths []string
	err := d.db.View(func(tx *bolt.Tx) error {
		rows, err := collectStateRows(tx, mediaType, mediaID, language, variant)
		if err != nil {
			return err
		}
		for i := range rows {
			r := &rows[i]
			if r.Manual && r.Path != "" {
				paths = append(paths, r.Path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// NextManualNumber returns the next manual subtitle ordinal for the quad: one
// greater than the highest ordinal currently encoded in the quad's manual
// paths, or 1 when the quad has no manual row (Requirement 4.4). It mirrors
// the old SQLite `COALESCE(MAX(<ordinal>), 0) + 1 ... WHERE ... AND manual = 1`
// refined per variant: movie.fr.1.srt (standard) and movie.fr.forced.1.srt
// (forced) advance independent sequences, matching the variant-aware manual
// file naming.
//
// The ordinal lives on the primary path, so this walks the quad via
// collectStateRows and parses each manual row's ordinal with the shared
// parseManualOrdinal helper (rows whose path has no trailing ordinal, including
// empty paths, contribute nothing, matching the legacy CAST of a non-numeric
// suffix to 0). The contract has no error channel, so a read fault falls back
// to ManualDownloadCount + 1, and to 1 if that also fails, matching the old
// store's degraded path.
func (d *DB) NextManualNumber(_ context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) int {
	maxOrdinal := 0
	err := d.db.View(func(tx *bolt.Tx) error {
		rows, err := collectStateRows(tx, mediaType, mediaID, language, variant)
		if err != nil {
			return err
		}
		for i := range rows {
			r := &rows[i]
			if !r.Manual {
				continue
			}
			if n, ok := parseManualOrdinal(r.Path); ok && n > maxOrdinal {
				maxOrdinal = n
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("NextManualNumber scan failed, falling back to count", "error", err)
		count, cerr := d.ManualDownloadCount(context.Background(), mediaType, mediaID, language, variant)
		if cerr != nil {
			return 1
		}
		return count + 1
	}
	return maxOrdinal + 1
}

// defaultQueryLimit is the safety cap applied when a caller passes Limit <= 0
// ("no explicit limit"), matching the old SQLite store's 1000-row hard cap that
// prevents unbounded allocation on an unfiltered GetState (Requirement 15.3).
const defaultQueryLimit = 1000

// preallocCap caps the result-slice capacity hint so a large requested Limit
// does not over-allocate up front; append grows the slice as needed. A literal
// constant (not a value derived from Limit) is used deliberately, matching the
// old store's CodeQL-friendly allocation.
const preallocCap = 256

// stateQuadInfo is a comparable (media_type, media_id, language, variant)
// tuple used as a grouping/map key by the index-only reads (GetManualLocks'
// accumulator) and reconcile's per-quad grouping. Row-bearing reads take the
// quad from the self-contained stateRec instead.
type stateQuadInfo struct {
	mt      api.MediaType
	mid     string
	lang    string
	variant api.Variant
}

// splitStateQuadKey parses an ix_state_quad key (mt 0x00 mid 0x00 lang 0x00
// variant 0x00 be64(id)) back into its quad components and surrogate id. It
// serves the index-only reads (GetManualLocks, HistoryMediaIDs), which answer
// from the index walk without dereferencing primaries. ok is false for a key
// too short to hold the id or missing the quad components.
func splitStateQuadKey(key []byte) (mt api.MediaType, mid, lang string, variant api.Variant, id int64, ok bool) {
	if len(key) < 8 {
		return "", "", "", "", 0, false
	}
	v, vok := kv.DecodeBe64(key[len(key)-8:])
	if !vok {
		return "", "", "", "", 0, false
	}
	// The bytes before the id are quadPrefix(mt,mid,lang,variant) = mt 0x00
	// mid 0x00 lang 0x00 variant 0x00, so Split yields [mt, mid, lang,
	// variant, ""] (trailing empty from the separator).
	parts := kv.Split(key[:len(key)-8])
	if len(parts) < 5 {
		return "", "", "", "", 0, false
	}
	return api.MediaType(parts[0]), parts[1], parts[2], api.Variant(parts[3]), int64(v), true //nolint:gosec // G115: inverse of stateKey suffix
}

// asciiLower lowercases only the ASCII letters A-Z, matching SQLite's default
// LIKE case folding (which folds only ASCII, not the full Unicode range).
func asciiLower(s string) string {
	var changed bool
	for i := range len(s) {
		if s[i] >= 'A' && s[i] <= 'Z' {
			changed = true
			break
		}
	}
	if !changed {
		return s
	}
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// asciiContainsFold reports whether s contains substr, comparing ASCII letters
// case-insensitively. It reproduces the old `title LIKE '%'||escaped(search)||
// '%' ESCAPE '\'` contains-match: the escape made the user's %/_/\ literal, so
// in Go a plain (case-folded) substring test is the exact equivalent — every
// character of substr, including %/_/\, is matched literally (Requirement 8.4).
func asciiContainsFold(s, substr string) bool {
	if substr == "" {
		return true
	}
	return strings.Contains(asciiLower(s), asciiLower(substr))
}

// asciiHasPrefixFold reports whether s starts with prefix, comparing ASCII
// letters case-insensitively. It reproduces the old media_id `LIKE escaped(
// prefix)||'%' ESCAPE '\'` prefix filter used by HistoryMediaIDs: the escape
// made the user's %/_/\ literal, matched literally here.
func asciiHasPrefixFold(s, prefix string) bool {
	if prefix == "" {
		return true
	}
	return strings.HasPrefix(asciiLower(s), asciiLower(prefix))
}

// stateEntryFrom assembles an api.StateEntry from a decoded (self-contained)
// primary record.
func stateEntryFrom(sr *stateRec) api.StateEntry {
	return api.StateEntry{
		ID:            sr.ID,
		MediaType:     sr.MediaType,
		MediaID:       sr.MediaID,
		Language:      sr.Language,
		Variant:       sr.Variant,
		Provider:      sr.Provider,
		ReleaseName:   sr.ReleaseName,
		Score:         sr.Score,
		Path:          sr.Path,
		Title:         sr.Title,
		ImdbID:        sr.ImdbID,
		Season:        sr.Season,
		Episode:       sr.Episode,
		Manual:        sr.Manual,
		MediaImported: sr.MediaImported,
	}
}

// matchStateRow resolves one ix_state_imported entry (indexKey) to its
// api.StateEntry and applies the query's filters against the decoded
// (self-contained) primary record. It returns matched=false to skip the row on
// any index/primary drift, a filtered-out row, or a tolerated decode skip;
// derr is non-nil only on a fail-closed decode error. Extracted from GetState
// so the reverse-walk loop stays a thin offset/limit pager over the matched
// rows.
func (d *DB) matchStateRow(sb *bolt.Bucket, q *api.StateQuery, indexKey []byte) (entry api.StateEntry, matched bool, derr error) {
	_, primary, ok := kv.SplitTimeIndexKey(indexKey)
	if !ok {
		return api.StateEntry{}, false, nil
	}
	raw := sb.Get(primary)
	if raw == nil {
		return api.StateEntry{}, false, nil // index/primary drift
	}
	var sr stateRec
	skip, err := decodeRecord(bucketDecodeMode(bucketSubtitleState), bucketSubtitleState, primary, raw, &sr)
	if err != nil {
		return api.StateEntry{}, false, err
	}
	if skip {
		return api.StateEntry{}, false, nil
	}
	if q.MediaType != "" && sr.MediaType != q.MediaType {
		return api.StateEntry{}, false, nil
	}
	if q.Language != "" && sr.Language != q.Language {
		return api.StateEntry{}, false, nil
	}
	if q.Provider != "" && sr.Provider != q.Provider {
		return api.StateEntry{}, false, nil
	}
	if q.Search != "" && !asciiContainsFold(sr.Title, q.Search) {
		return api.StateEntry{}, false, nil
	}
	return stateEntryFrom(&sr), true, nil
}

// GetState returns subtitle-state rows matching the query, most-recently-
// imported first. It mirrors the old SQLite GetState (Requirement 8.4, 15.1,
// 15.2, 15.3):
//
//   - Filters by media_type, language (both carried in the ix_state_quad key)
//     and provider (carried in the primary row); zero-value fields mean no
//     filter.
//   - Title search is a case-insensitive CONTAINS match in which the user's
//     %, _, and \ are treated literally (see asciiContainsFold), matching the
//     old `LIKE ... ESCAPE '\'`.
//   - Orders by media_imported DESC, then surrogate-id DESC. This is exactly a
//     REVERSE walk of ix_state_imported (be64(media_imported) 0x00 be64(id)),
//     so the ordering — including the id tiebreak on equal media_imported —
//     comes from the index, not an in-memory sort.
//   - Applies the numeric Offset (skip that many matches) for shallow callers
//     and caps the result at Limit, defaulting to defaultQueryLimit (1000) when
//     Limit <= 0. Walking the index and skipping matched rows is the keyset-
//     friendly path: a page costs O(offset + limit) index steps regardless of
//     table size, with no full primary scan or sort.
//
// The record is self-contained (it carries its quad), so the page walk
// dereferences and decodes ONLY the rows it visits — a page costs
// O(offset + limit) index steps plus that many primary decodes, independent
// of table size. A row whose primary cannot be decoded is skipped with a
// warning (subtitle_state is a derived bucket the next scan rebuilds; this is
// not a lock-bearing read).
func (d *DB) GetState(_ context.Context, q *api.StateQuery) ([]api.StateEntry, error) {
	slog.Debug("GetState",
		"media_type", q.MediaType, "lang", q.Language,
		"provider", q.Provider, "search", q.Search,
		"limit", q.Limit, "offset", q.Offset)

	limit := q.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	offset := max(q.Offset, 0)

	var out []api.StateEntry
	err := d.db.View(func(tx *bolt.Tx) error {
		sb := tx.Bucket([]byte(bucketSubtitleState))
		if sb == nil {
			return errors.New("boltstore: subtitle_state bucket not found")
		}
		imp := tx.Bucket([]byte(bucketIxStateImported))
		if imp == nil {
			return errors.New("boltstore: ix_state_imported bucket not found")
		}
		var err error
		out, err = d.collectStatePage(imp, sb, q, limit, offset)
		return err
	})
	if err != nil {
		return nil, err
	}
	slog.Debug("GetState result", "count", len(out))
	return out, nil
}

// collectStatePage performs a REVERSE walk of ix_state_imported (which sorts
// ascending by (media_imported, id), so Last->Prev yields media_imported DESC,
// id DESC), resolves each entry to an api.StateEntry through matchStateRow
// (which applies the query's filters), skips the first `offset` matched rows,
// and collects up to `limit` entries. It preallocates a fixed, modest capacity
// rather than one derived from the untrusted limit; append grows it as needed.
func (d *DB) collectStatePage(imp, sb *bolt.Bucket, q *api.StateQuery, limit, offset int) ([]api.StateEntry, error) {
	out := make([]api.StateEntry, 0, preallocCap)
	skipped := 0
	c := imp.Cursor()
	for k, _ := c.Last(); k != nil; k, _ = c.Prev() {
		if len(out) >= limit {
			break
		}
		entry, matched, derr := d.matchStateRow(sb, q, k)
		if derr != nil {
			return nil, derr
		}
		if !matched {
			continue
		}
		// Matched: apply the numeric offset, then collect up to limit.
		if skipped < offset {
			skipped++
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// GetManualLocks returns one entry per manually locked quad (a quad with at
// least one manual row), each carrying its manual-row count, ordered by
// media_type then media_id. It mirrors the old SQLite
// `SELECT media_type, media_id, language, COUNT(*) ... WHERE manual = 1
// GROUP BY media_type, media_id, language ORDER BY media_type, media_id`
// (Requirement 15.5), refined per variant: an fr/forced lock and an
// fr/standard lock list as separate entries.
//
// It is served entirely from the ix_state_quad projection: the manual flag
// lives in the projection value and the quad components in the key, so no
// primary subtitle_state row is dereferenced. Walking ix_state_quad visits
// entries in (mt, mid, lang, variant, id) byte order, so accumulating per quad
// yields groups already ordered by (media_type, media_id) — a deterministic
// refinement of the old ORDER BY. As a lock-bearing read it FAILS CLOSED: an
// undecodable projection aborts the read rather than silently dropping a lock.
func (d *DB) GetManualLocks(_ context.Context) ([]api.ManualLockEntry, error) {
	var acc manualLockAccumulator
	err := d.db.View(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(bucketIxStateQuad))
		if idx == nil {
			return errors.New("boltstore: ix_state_quad bucket not found")
		}
		c := idx.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			mt, mid, lang, variant, _, ok := splitStateQuadKey(k)
			if !ok {
				return fmt.Errorf("boltstore: malformed ix_state_quad key %x", k)
			}
			manual, _, _, pok := decodeStateProjection(v)
			if !pok {
				return fmt.Errorf("boltstore: malformed ix_state_quad projection for %s/%s/%s/%s", mt, mid, lang, variant)
			}
			acc.add(stateQuadInfo{mt: mt, mid: mid, lang: lang, variant: variant}, manual)
		}
		acc.flush()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return acc.out, nil
}

// manualLockAccumulator groups consecutive ix_state_quad entries (which are
// sorted by (mt, mid, lang, variant, id), so a quad's rows are contiguous)
// into one api.ManualLockEntry per quad that has at least one manual row,
// carrying the manual-row count.
type manualLockAccumulator struct {
	cur     stateQuadInfo
	out     []api.ManualLockEntry
	curCnt  int
	haveCur bool
}

// flush emits the quad currently being accumulated, if it had any manual row.
func (a *manualLockAccumulator) flush() {
	if a.haveCur && a.curCnt > 0 {
		a.out = append(a.out, api.ManualLockEntry{
			MediaType: a.cur.mt, MediaID: a.cur.mid, Language: a.cur.lang,
			Variant: a.cur.variant, Count: a.curCnt,
		})
	}
}

// add folds one ix_state_quad entry into the accumulator: when the quad
// changes it flushes the previous tally and starts a fresh one, then counts the
// entry when it is a manual row.
func (a *manualLockAccumulator) add(quad stateQuadInfo, manual bool) {
	if !a.haveCur || quad != a.cur {
		a.flush()
		a.haveCur, a.cur, a.curCnt = true, quad, 0
	}
	if manual {
		a.curCnt++
	}
}

// Stats returns the maintained O(1) download and attempt counters from the meta
// bucket, mirroring the old SQLite `COUNT(*) FROM subtitle_state` and
// `COUNT(*) FROM search_attempts` without scanning either bucket (Requirements
// 18.1, 18.4). The counters are moved inside the same Update as every row
// insert/delete via the index-maintenance chokepoints, so they track row
// existence exactly like COUNT(*).
func (d *DB) Stats(_ context.Context) (downloads, attempts int, err error) {
	err = d.db.View(func(tx *bolt.Tx) error {
		downloads = int(readDownloadCount(tx))
		attempts = int(readAttemptCount(tx))
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return downloads, attempts, nil
}

// HistoryMediaIDs returns the distinct media ids that have download history for
// the given media_type, optionally filtered to those whose id starts with
// mediaIDPrefix. It mirrors the old SQLite `SELECT DISTINCT media_id FROM
// subtitle_state WHERE media_type = ? [AND media_id LIKE escaped(prefix)||'%'
// ESCAPE '\']` (Requirement 8.4): the prefix match is case-insensitive (ASCII)
// with the user's %/_/\ treated literally (asciiHasPrefixFold).
//
// The media_type and media_id live in the ix_state_quad key, so the distinct
// set is built from a single ix_state_quad walk without dereferencing any
// primary. Walking in (mt, mid, lang, variant, id) byte order yields ids in
// ascending order; first-seen dedup keeps each id once.
func (d *DB) HistoryMediaIDs(_ context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]string, error) {
	var ids []string
	seen := make(map[string]struct{})
	err := d.db.View(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(bucketIxStateQuad))
		if idx == nil {
			return errors.New("boltstore: ix_state_quad bucket not found")
		}
		return idx.ForEach(func(k, _ []byte) error {
			mt, mid, _, _, _, ok := splitStateQuadKey(k)
			if !ok {
				return nil
			}
			if mt != mediaType {
				return nil
			}
			if !asciiHasPrefixFold(mid, mediaIDPrefix) {
				return nil
			}
			if _, dup := seen[mid]; dup {
				return nil
			}
			seen[mid] = struct{}{}
			ids = append(ids, mid)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}
