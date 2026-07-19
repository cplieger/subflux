package boltstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file holds the scan_state half of CoverageStore: the scan_state bucket
// (one row per (media_type, media_id)) plus its ix_scan_at recency index. It
// mirrors the old SQLite store/coveragedb/scan_state.go behaviour exactly
// (Requirements 5.3, 5.4, 5.6).
//
// scan_state keys are mt 0x00 mid (scanStateKey); the title/audio_lang/season/
// episode/scanned_at live in the JSON value (scanRec). The ix_scan_at index is
// be64(scanned_at) 0x00 primary (scanAtKey): a forward cursor walks oldest
// first, RecentlyScanned seeks to be64(cutoff) for the inclusive >= cutoff
// boundary, and LastScanTime reads the last (most recent) entry. Every write
// routes through the putScanState chokepoint so the index is maintained in the
// same transaction as the row (an upsert that changes scanned_at deletes the
// stale ix_scan_at entry and adds the fresh one).

// scanTimeLayout is the display/serialisation format for scanned_at, matching
// the old SQLite store's CURRENT_TIMESTAMP string ("YYYY-MM-DD HH:MM:SS", UTC).
// LastScanTime and GetScanStates both render scanned_at with this layout so the
// API shape is byte-identical to the SQLite engine.
const scanTimeLayout = "2006-01-02 15:04:05"

// RecordScanState upserts the scan-state row for a media item, keyed by
// (media_type, media_id), and refreshes its ix_scan_at recency entry, in one
// write transaction. It mirrors the old SQLite upsert (Requirement 5.3): a
// re-record overwrites title/season/episode/audio_lang and stamps scanned_at to
// now (UTC). Because scanned_at changes on every record, the putScanState
// chokepoint deletes the stale ix_scan_at entry and adds the fresh one in the
// same tx, so the recency index always reflects the latest scan.
func (d *DB) RecordScanState(_ context.Context, rec *api.ScanRecord) error {
	sr := scanRec{
		Title:     rec.Title,
		AudioLang: rec.AudioLang,
		Season:    rec.Season,
		Episode:   rec.Episode,
		Searched:  rec.Searched,
		ScannedAt: time.Now().UTC(),
	}
	return d.db.Update(func(tx *bolt.Tx) error {
		return putScanState(tx, rec.MediaType, rec.MediaID, &sr)
	})
}

// GetScanStates returns the scan_state rows for a media type and an optional
// media-id prefix, ordered by media_id. It mirrors the old SQLite GetScanStates
// (Requirement 5.2), which builds its filter with txutil.AppendPrefixFilter: an
// empty prefix returns every row of the media type, and a non-empty prefix is a
// byte-PREFIX match (media_id LIKE 'prefix%'). The bbolt cursor yields keys in
// byte order, so within a fixed media type the rows already come out ordered by
// media_id, matching the SQL ORDER BY for free. scanned_at is rendered with
// scanTimeLayout to match the SQLite string shape. scan_state is a derived
// bucket, so an undecodable value is skipped with a warning (Requirement 13.4).
func (d *DB) GetScanStates(_ context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.ScanStateRow, error) {
	prefix := append(typePrefix(mediaType), mediaIDPrefix...)

	var out []api.ScanStateRow
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketScanState))
		if b == nil {
			return errors.New("boltstore: scan_state bucket not found")
		}
		c := b.Cursor()
		for key, v := c.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, v = c.Next() {
			parts := kv.Split(key)
			if len(parts) < 2 {
				continue // malformed key; a correctly built key always parses
			}
			var sr scanRec
			skip, derr := decodeRecord(bucketDecodeMode(bucketScanState), bucketScanState, key, v, &sr)
			if derr != nil {
				return derr
			}
			if skip {
				continue
			}
			out = append(out, api.ScanStateRow{
				MediaID:   parts[1],
				Title:     sr.Title,
				Season:    sr.Season,
				Episode:   sr.Episode,
				AudioLang: sr.AudioLang,
				Searched:  sr.Searched,
				ScannedAt: sr.ScannedAt.UTC().Format(scanTimeLayout),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RecentlyScanned returns the set of media ids scanned at or AFTER cutoff
// (inclusive), across every media type, mirroring the old SQLite
// `WHERE scanned_at >= ?` (Requirement 5.4). It seeks the ix_scan_at index to
// be64(cutoff.UnixNano()) and walks forward: a row whose scanned_at equals the
// cutoff produces an index key be64(cutoff) 0x00 primary, which sorts at or
// after the 8-byte seek key, so the cutoff itself is included while the instant
// just before it is excluded. The media id is recovered from the dereferenced
// primary key (mt 0x00 mid); like the SQL query it keys the result set by
// media id alone, independent of media type.
func (d *DB) RecentlyScanned(_ context.Context, cutoff time.Time) (map[string]bool, error) {
	seek := kv.Be64(uint64(cutoff.UnixNano()))

	out := make(map[string]bool)
	err := d.db.View(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(bucketIxScanAt))
		if idx == nil {
			return errors.New("boltstore: ix_scan_at bucket not found")
		}
		c := idx.Cursor()
		for k, _ := c.Seek(seek); k != nil; k, _ = c.Next() {
			_, primary, ok := kv.SplitTimeIndexKey(k)
			if !ok {
				continue // malformed index key
			}
			parts := kv.Split(primary)
			if len(parts) < 2 {
				continue
			}
			out[parts[1]] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// LastScanTime returns the most recent scanned_at, formatted with
// scanTimeLayout in UTC, or the empty string when nothing has been scanned. It
// mirrors the old SQLite `SELECT MAX(scanned_at)` returning NULL -> "" semantics
// (Requirement 5.6). The newest entry is the last key of the ix_scan_at index
// (keys sort chronologically), so this is an O(1) cursor.Last() plus a decode of
// the timestamp embedded in the index key.
func (d *DB) LastScanTime(_ context.Context) (string, error) {
	var result string
	err := d.db.View(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(bucketIxScanAt))
		if idx == nil {
			return errors.New("boltstore: ix_scan_at bucket not found")
		}
		k, _ := idx.Cursor().Last()
		if k == nil {
			return nil // no scans recorded -> empty string
		}
		unixNano, _, ok := kv.SplitTimeIndexKey(k)
		if !ok {
			return fmt.Errorf("boltstore: malformed ix_scan_at key %x", k)
		}
		result = time.Unix(0, unixNano).UTC().Format(scanTimeLayout)
		return nil
	})
	if err != nil {
		return "", err
	}
	return result, nil
}

// --- Scan-cycle mark (duration-aware resume) ---

// metaKeyScanCycleStart is the meta-bucket key holding the in-progress full
// scan's start time (RFC3339Nano). Present = a scan cycle is running (or was
// interrupted); absent = the last cycle completed normally. See the scanning
// package's ScanStore contract.
var metaKeyScanCycleStart = []byte("scan_cycle_start")

// ScanCycleStart returns the persisted cycle-start mark, or the zero time
// with no error when no mark is stored. An unparsable stored value is
// surfaced as an error rather than silently treated as absent.
func (d *DB) ScanCycleStart(_ context.Context) (time.Time, error) {
	var result time.Time
	err := d.db.View(func(tx *bolt.Tx) error {
		mb := tx.Bucket([]byte(bucketMeta))
		if mb == nil {
			return errors.New("boltstore: meta bucket not found")
		}
		v := mb.Get(metaKeyScanCycleStart)
		if v == nil {
			return nil
		}
		t, perr := time.Parse(time.RFC3339Nano, string(v))
		if perr != nil {
			return fmt.Errorf("parse scan cycle mark: %w", perr)
		}
		result = t
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return result, nil
}

// SetScanCycleStart persists the cycle-start mark (RFC3339Nano), overwriting
// any prior mark.
func (d *DB) SetScanCycleStart(_ context.Context, t time.Time) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		mb := tx.Bucket([]byte(bucketMeta))
		if mb == nil {
			return errors.New("boltstore: meta bucket not found")
		}
		return mb.Put(metaKeyScanCycleStart, []byte(t.Format(time.RFC3339Nano)))
	})
}

// ClearScanCycleStart removes the cycle-start mark. Idempotent.
func (d *DB) ClearScanCycleStart(_ context.Context) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		mb := tx.Bucket([]byte(bucketMeta))
		if mb == nil {
			return errors.New("boltstore: meta bucket not found")
		}
		return mb.Delete(metaKeyScanCycleStart)
	})
}
