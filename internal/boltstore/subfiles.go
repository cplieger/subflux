package boltstore

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cplieger/subflux/internal/api"
	boltkv "github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file holds the subtitle_files half of CoverageStore plus the
// SyncOffsetStore (sync_offsets bucket). The subtitle_files methods are
// implemented here (task 5.1); SetSyncOffset / GetSyncOffset remain stubs
// replaced in task 5.3.
//
// subtitle_files keys are mt 0x00 mid 0x00 lang 0x00 variant 0x00 source 0x00
// path (subtitleFileKey), so the language, variant, source, and path that drive
// per-media coverage all live in the KEY. Coverage counts therefore come from a
// key-only prefix walk with no value decode (Requirement 18.2); only the codec
// and the cumulative offset_ms live in the JSON value. Every write routes
// through the putSubtitleFile / deleteSubtitleFile index-maintenance
// chokepoints so the O(1) TotalSubtitleFiles counter in the meta bucket stays
// consistent in the same transaction as the row write (Requirement 18.1).

// subFileKey is the per-media-item identity tuple for a subtitle_files row: the
// part of the composite key after (media_type, media_id). It mirrors the old
// SQLite store's diff key so RecordSubtitleFiles reproduces the same insert/
// update/delete classification.
type subFileKey struct{ lang, variant, source, path string }

// RecordSubtitleFiles diff-syncs the subtitle_files rows for one media item
// against the set discovered on disk, in a single write transaction. It mirrors
// the old SQLite store's diff-based RecordSubtitleFiles (Requirement 5.1):
//
//   - rows present on disk but absent from the store are inserted (offset_ms 0),
//   - rows whose codec changed are updated in place, PRESERVING the existing
//     offset_ms (the old UPDATE set only codec/updated_at, never offset_ms),
//   - rows no longer on disk are deleted,
//   - it reports changed=true iff at least one insert, update, or delete
//     happened (a set identical to what is already stored returns false).
//
// Duplicate entries in files collapse (last codec wins) because they share a
// subFileKey, matching the old store's map build. Inserts and deletes move the
// maintained TotalSubtitleFiles counter through the chokepoints; a codec-only
// update does not (the row already existed).
func (d *DB) RecordSubtitleFiles(_ context.Context, mediaType api.MediaType, mediaID string, files []api.SubtitleFile) (bool, error) {
	want := make(map[subFileKey]string, len(files)) // -> codec
	for i := range files {
		f := &files[i]
		want[subFileKey{f.Language, string(f.Variant), string(f.Source), f.Path}] = f.Codec
	}

	changed := false
	err := d.db.Update(func(tx *bolt.Tx) error {
		have, err := loadFileRows(tx, mediaType, mediaID)
		if err != nil {
			return err
		}
		deleted, err := d.deleteStaleFileRows(tx, mediaType, mediaID, have, want)
		if err != nil {
			return err
		}
		applied, err := d.applyWantedFileRows(tx, mediaType, mediaID, have, want)
		if err != nil {
			return err
		}
		changed = deleted || applied
		return nil
	})
	if err != nil {
		return false, err
	}
	if changed {
		slog.Debug("RecordSubtitleFiles changed",
			"media_type", mediaType, "media_id", mediaID, "files", len(want))
	}
	return changed, nil
}

// deleteStaleFileRows deletes the subtitle_files rows present in `have` but
// absent from `want` (no longer on disk) through the deleteSubtitleFile
// chokepoint, returning whether any row was actually removed.
func (d *DB) deleteStaleFileRows(tx *bolt.Tx, mediaType api.MediaType, mediaID string, have map[subFileKey]fileRec, want map[subFileKey]string) (bool, error) {
	changed := false
	for k := range have {
		if _, ok := want[k]; ok {
			continue
		}
		key := subtitleFileKey(mediaType, mediaID, k.lang, api.Variant(k.variant), api.SubtitleSource(k.source), k.path)
		existed, derr := deleteSubtitleFile(tx, key)
		if derr != nil {
			return false, derr
		}
		if existed {
			changed = true
		}
	}
	return changed, nil
}

// applyWantedFileRows inserts the rows in `want` missing from `have` (offset_ms
// 0) and rewrites rows whose codec changed (preserving the cumulative
// offset_ms), through the putFileRow chokepoint. It returns whether any insert
// or codec update happened.
func (d *DB) applyWantedFileRows(tx *bolt.Tx, mediaType api.MediaType, mediaID string, have map[subFileKey]fileRec, want map[subFileKey]string) (bool, error) {
	changed := false
	now := time.Now().UTC()
	for k, codec := range want {
		old, exists := have[k]
		switch {
		case !exists:
			rec := fileRec{Codec: codec, OffsetMs: 0, UpdatedAt: now}
			if perr := d.putFileRow(tx, mediaType, mediaID, k, &rec); perr != nil {
				return false, perr
			}
			changed = true
		case old.Codec != codec:
			// Codec changed: rewrite, preserving the cumulative offset_ms.
			rec := fileRec{Codec: codec, OffsetMs: old.OffsetMs, UpdatedAt: now}
			if perr := d.putFileRow(tx, mediaType, mediaID, k, &rec); perr != nil {
				return false, perr
			}
			changed = true
		}
	}
	return changed, nil
}

// putFileRow writes a fileRec at the composite key for (mediaType, mediaID, k)
// through the putSubtitleFile chokepoint (which maintains the TotalSubtitleFiles
// counter). It is a thin helper so RecordSubtitleFiles' insert and update arms
// build the key identically.
func (d *DB) putFileRow(tx *bolt.Tx, mediaType api.MediaType, mediaID string, k subFileKey, rec *fileRec) error {
	key := subtitleFileKey(mediaType, mediaID, k.lang, api.Variant(k.variant), api.SubtitleSource(k.source), k.path)
	return putSubtitleFile(tx, key, rec)
}

// loadFileRows reads the current subtitle_files rows for one media item into a
// subFileKey -> fileRec map by prefix-scanning mediaPrefix(mt, mid). The key
// components (lang/variant/source/path) come from the KEY, the codec/offset
// from the decoded value. subtitle_files is a derived bucket, so an undecodable
// value is tolerated: the row is still recorded (with a zero value) so a stale
// row is still deleted and an in-want row is rewritten cleanly, self-healing
// the corruption rather than aborting the scan (Requirement 13.4).
func loadFileRows(tx *bolt.Tx, mediaType api.MediaType, mediaID string) (map[subFileKey]fileRec, error) {
	b := tx.Bucket([]byte(bucketSubtitleFiles))
	if b == nil {
		return nil, errors.New("boltstore: subtitle_files bucket not found")
	}
	have := make(map[subFileKey]fileRec)
	prefix := mediaPrefix(mediaType, mediaID)
	c := b.Cursor()
	for key, v := c.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, v = c.Next() {
		fk, ok := parseSubFileKey(key)
		if !ok {
			continue // malformed key; a correctly built key always parses
		}
		var fr fileRec
		if derr := boltkv.Decode(v, &fr); derr != nil {
			slog.Warn("boltstore: undecodable subtitle_files value, treating as replaceable",
				"key", hex.EncodeToString(key), "error", derr)
			// Leave fr zero: a codec mismatch forces a rewrite if still wanted,
			// or the stale-row delete removes it.
		}
		have[fk] = fr
	}
	return have, nil
}

// parseSubFileKey splits a subtitle_files composite key (mt 0x00 mid 0x00 lang
// 0x00 variant 0x00 source 0x00 path) into its per-media identity tuple. ok is
// false for a key with fewer than the six components. The media_id is recovered
// by callers from their query, so it is not returned here.
func parseSubFileKey(key []byte) (subFileKey, bool) {
	parts := boltkv.Split(key)
	if len(parts) < 6 {
		return subFileKey{}, false
	}
	return subFileKey{lang: parts[2], variant: parts[3], source: parts[4], path: parts[5]}, true
}

// UpsertSubtitleFile inserts or updates a single subtitle_files row, mirroring
// the old SQLite `INSERT ... ON CONFLICT DO UPDATE SET codec, updated_at`
// (Requirement 15.7): on a conflicting key only the codec and updated_at change
// and the existing offset_ms is preserved; a fresh row starts at offset_ms 0.
// The write routes through putSubtitleFile so the TotalSubtitleFiles counter is
// maintained.
func (d *DB) UpsertSubtitleFile(_ context.Context, mediaType api.MediaType, mediaID string, f *api.SubtitleFile) error {
	key := subtitleFileKey(mediaType, mediaID, f.Language, f.Variant, f.Source, f.Path)
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketSubtitleFiles))
		if b == nil {
			return errors.New("boltstore: subtitle_files bucket not found")
		}
		var offset int64
		if old := b.Get(key); old != nil {
			var ofr fileRec
			if boltkv.Decode(old, &ofr) == nil {
				offset = ofr.OffsetMs // preserve cumulative offset on update
			}
		}
		rec := fileRec{Codec: f.Codec, OffsetMs: offset, UpdatedAt: time.Now().UTC()}
		return putSubtitleFile(tx, key, &rec)
	})
}

// DeleteSubtitleFile removes the single subtitle_files row identified by its
// full composite key, mirroring the old SQLite DELETE on the full primary key
// (Requirement 15.7). It is idempotent: deleting an absent row is a no-op, and
// the TotalSubtitleFiles counter only moves when a row actually existed.
func (d *DB) DeleteSubtitleFile(_ context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant, source api.SubtitleSource, path string) error {
	key := subtitleFileKey(mediaType, mediaID, language, variant, source, path)
	return d.db.Update(func(tx *bolt.Tx) error {
		_, err := deleteSubtitleFile(tx, key)
		return err
	})
}

// GetSubtitleFiles returns the subtitle_files rows for a media type and an
// optional media-id filter, ordered by media_id, language, variant, source
// (the natural byte order of the composite key, so the cursor yields it for
// free). It mirrors the old SQLite GetSubtitleFiles, including the prefix
// semantics and the subtitle_state join (Requirement 5.2):
//
//   - an empty filter returns every row of the media type,
//   - a filter NOT ending in "-" is an EXACT media-id match,
//   - a filter ending in "-" is a media-id PREFIX match (e.g. "tvdb-111-").
//
// The MediaID/Language/Variant/Source/Path fields come from the KEY (no value
// decode), and only Codec/OffsetMs come from the value. Score and VideoPath
// reproduce the old `LEFT JOIN subtitle_state ... AND s.manual = 0 AND
// f.source != 'embedded'` with `CASE WHEN f.source = 'embedded' THEN 0` and the
// COALESCE defaults: an embedded row always reports score 0 and an empty
// video_path; an external row takes them from the triple's auto subtitle_state
// row (see autoStateInfo). An undecodable value is skipped (derived bucket).
func (d *DB) GetSubtitleFiles(_ context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.SubtitleEntry, error) {
	scanPrefix := filesScanPrefix(mediaType, mediaIDPrefix)

	var out []api.SubtitleEntry
	autoCache := make(map[string]autoStateJoin)

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketSubtitleFiles))
		if b == nil {
			return errors.New("boltstore: subtitle_files bucket not found")
		}
		c := b.Cursor()
		for key, v := c.Seek(scanPrefix); key != nil && bytes.HasPrefix(key, scanPrefix); key, v = c.Next() {
			entry, skip, derr := d.subtitleEntryFromRow(tx, mediaType, key, v, autoCache)
			if derr != nil {
				return derr
			}
			if skip {
				continue
			}
			out = append(out, entry)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// autoStateJoin caches the subtitle_state auto-row join result (score and
// video_path) for one (media_id, language) so GetSubtitleFiles stays O(rows)
// even when a triple's rows are non-adjacent in cursor order.
type autoStateJoin struct {
	videoPath string
	score     int
}

// subtitleEntryFromRow decodes one subtitle_files cursor row (key+value) into an
// api.SubtitleEntry. MediaID/Language/Variant/Source/Path come from the KEY; only
// Codec/OffsetMs come from the decoded value. For a non-embedded source it
// applies the subtitle_state auto-row join (Score, VideoPath) via the per-(media
// _id, language) cache; embedded rows keep the COALESCE defaults (score 0, empty
// video_path). It returns skip=true for a malformed key or an undecodable
// (tolerated, derived-bucket) value.
func (d *DB) subtitleEntryFromRow(tx *bolt.Tx, mediaType api.MediaType, key, v []byte, autoCache map[string]autoStateJoin) (api.SubtitleEntry, bool, error) {
	parts := boltkv.Split(key)
	if len(parts) < 6 {
		return api.SubtitleEntry{}, true, nil // malformed key
	}
	mid, lang := parts[1], parts[2]
	variant, source, path := parts[3], parts[4], parts[5]

	var fr fileRec
	skip, derr := decodeRecord(bucketDecodeMode(bucketSubtitleFiles), bucketSubtitleFiles, key, v, &fr)
	if derr != nil {
		return api.SubtitleEntry{}, false, derr
	}
	if skip {
		return api.SubtitleEntry{}, true, nil
	}

	entry := api.SubtitleEntry{
		MediaID:  mid,
		Language: lang,
		Variant:  variant,
		Source:   source,
		Codec:    fr.Codec,
		Path:     path,
		OffsetMs: fr.OffsetMs,
	}
	// The JOIN excludes embedded rows, so they keep the COALESCE defaults
	// (score 0, empty video_path).
	if source != string(api.SourceEmbedded) {
		info := d.cachedAutoStateInfo(tx, mediaType, mid, lang, autoCache)
		entry.Score = info.score
		entry.VideoPath = info.videoPath
	}
	return entry, false, nil
}

// cachedAutoStateInfo returns the auto-row join (score, video_path) for the
// (media_id, language) triple, memoizing the autoStateInfo lookup in cache.
func (d *DB) cachedAutoStateInfo(tx *bolt.Tx, mediaType api.MediaType, mediaID, language string, cache map[string]autoStateJoin) autoStateJoin {
	cacheKey := mediaID + string(boltkv.Sep) + language
	info, ok := cache[cacheKey]
	if !ok {
		score, vp := autoStateInfo(tx, mediaType, mediaID, language)
		info = autoStateJoin{score: score, videoPath: vp}
		cache[cacheKey] = info
	}
	return info
}

// filesScanPrefix builds the bbolt cursor prefix for a GetSubtitleFiles query,
// reproducing the old SQLite prefix semantics:
//
//   - "" -> typePrefix(mt) (mt 0x00): all rows of the media type,
//   - a filter not ending in "-" -> mediaPrefix(mt, filter) (mt 0x00 filter
//     0x00): an exact media-id match (the trailing separator stops "tmdb-12"
//     from matching "tmdb-123"),
//   - a filter ending in "-" -> typePrefix(mt) + filter: a media-id byte-prefix
//     match (the media_id component is sealed by the 0x00 after the type).
func filesScanPrefix(mediaType api.MediaType, mediaIDPrefix string) []byte {
	switch {
	case mediaIDPrefix == "":
		return typePrefix(mediaType)
	case !strings.HasSuffix(mediaIDPrefix, "-"):
		return mediaPrefix(mediaType, mediaIDPrefix)
	default:
		return append(typePrefix(mediaType), mediaIDPrefix...)
	}
}

// autoStateInfo returns the representative auto (manual=false) subtitle_state
// score and video_path for a triple, reproducing the GetSubtitleFiles LEFT JOIN
// on (media_type, media_id, language) AND s.manual = 0. When the triple has no
// auto row the COALESCE defaults apply (score 0, empty video_path). When
// reconcile has produced multiple auto rows for the triple, the highest-scored
// row is the representative (matching CurrentScore), a deterministic refinement
// of the SQL join's unspecified row choice.
//
// The score lives in the ix_state_triple projection, so the winning row is
// found from an index-only walk; only that one primary is dereferenced for its
// video_path. This is a coverage (derived) read, so an undecodable winning
// primary is tolerated by leaving video_path empty rather than failing the
// whole listing.
func autoStateInfo(tx *bolt.Tx, mediaType api.MediaType, mediaID, language string) (score int, videoPath string) {
	idx := tx.Bucket([]byte(bucketIxStateTriple))
	if idx == nil {
		return 0, ""
	}
	bestID, score, found := bestAutoStateID(idx, mediaType, mediaID, language)
	if !found {
		return 0, ""
	}
	return score, stateVideoPath(tx, bestID)
}

// bestAutoStateID scans ix_state_triple for the triple and returns the surrogate
// id and score of the highest-scored auto (manual=false) row. found is false
// when the triple has no auto row. Only auto rows feed the GetSubtitleFiles
// join; the highest-scored row is the representative, matching CurrentScore.
func bestAutoStateID(idx *bolt.Bucket, mediaType api.MediaType, mediaID, language string) (id int64, score int, found bool) {
	prefix := triplePrefix(mediaType, mediaID, language)
	c := idx.Cursor()
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		manual, s, _, ok := decodeStateProjection(v)
		if !ok || manual {
			continue // only auto rows feed the join
		}
		if !found || s > score {
			score = s
			if kid, idok := stateTripleKeyID(k); idok {
				id = kid
			}
			found = true
		}
	}
	return id, score, found
}

// stateVideoPath reads the video_path of the subtitle_state row with the given
// surrogate id, returning "" when the row is absent or undecodable (tolerated
// for this coverage read).
func stateVideoPath(tx *bolt.Tx, id int64) string {
	sb := tx.Bucket([]byte(bucketSubtitleState))
	if sb == nil {
		return ""
	}
	raw := sb.Get(stateKey(id))
	if raw == nil {
		return ""
	}
	var sr stateRec
	if boltkv.Decode(raw, &sr) != nil {
		return ""
	}
	return sr.VideoPath
}

// TotalSubtitleFiles returns the total subtitle_files row count from the O(1)
// maintained meta counter (readFileCount), never a full-bucket scan
// (Requirement 18.1). The counter is moved inside the same transaction as every
// insert/delete by the putSubtitleFile / deleteSubtitleFile chokepoints, so it
// equals COUNT(*) of the bucket.
func (d *DB) TotalSubtitleFiles(_ context.Context) (int, error) {
	var n int64
	err := d.db.View(func(tx *bolt.Tx) error {
		n = readFileCount(tx)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// SetSyncOffset stores the cumulative sync offset (in milliseconds) for a
// subtitle file, keyed by its bare path in the dedicated sync_offsets bucket
// (Requirement 6.1). The value is be64(offset_ms): a single fixed-width put, no
// JSON, since sync_offsets has no secondary index and no projection. The int64
// is reinterpreted as a uint64 for encoding, which is bit-preserving, so a
// negative offset (subtitle ahead of audio) round-trips exactly through
// GetSyncOffset. A later set for the same path overwrites the prior value.
func (d *DB) SetSyncOffset(_ context.Context, path string, offsetMs int64) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketSyncOffsets))
		if b == nil {
			return errors.New("boltstore: sync_offsets bucket not found")
		}
		return b.Put(syncOffsetKey(path), boltkv.Be64(uint64(offsetMs))) //nolint:gosec // G115: bit-preserving round-trip, no ordering on this bucket
	})
}

// GetSyncOffset returns the stored sync offset (in milliseconds) for a subtitle
// path, or 0 with no error when the path has no stored offset (Requirement
// 6.1), matching the old SQLite store's not-found-means-zero behaviour. The
// value is the be64(offset_ms) written by SetSyncOffset, decoded and
// reinterpreted back to int64 (bit-preserving, so negatives round-trip).
func (d *DB) GetSyncOffset(_ context.Context, path string) (int64, error) {
	var offset int64
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketSyncOffsets))
		if b == nil {
			return errors.New("boltstore: sync_offsets bucket not found")
		}
		v := b.Get(syncOffsetKey(path))
		if v == nil {
			return nil // no stored offset -> 0, no error
		}
		u, ok := boltkv.DecodeBe64(v)
		if !ok {
			return fmt.Errorf("boltstore: malformed sync_offset value for path %q", path)
		}
		offset = int64(u) //nolint:gosec // G115: inverse of SetSyncOffset encoding
		return nil
	})
	if err != nil {
		return 0, err
	}
	return offset, nil
}
