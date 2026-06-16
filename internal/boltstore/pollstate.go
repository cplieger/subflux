package boltstore

import (
	"context"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/cplieger/subflux/internal/api"
)

// This file holds the poll_state domain (PollStore): the poll_state bucket
// (one row per canonical PollKey, e.g. "sonarr" / "radarr") holding the last
// successful poll cursor. It mirrors the old SQLite store/store_poll.go
// behaviour exactly (Requirements 6.2, 6.3).
//
// poll_state keys are the bare PollKey (pollStateKey); the value is the cursor
// formatted with time.RFC3339Nano, the same layout the old SQLite store wrote
// and parsed, so the full sub-second precision of the stored timestamp is
// preserved across a set/get round-trip. The bucket has no secondary index and
// no projection, so a value is a plain put/get of the formatted string rather
// than the JSON codec. Both methods reject a non-canonical key the same way the
// old store did (typo-induced silent rebaselining is the failure this prevents).

// pollTimeLayout is the serialisation format for a poll cursor. RFC3339Nano
// matches the old SQLite store so a stored timestamp round-trips with its
// original precision and timezone offset.
const pollTimeLayout = time.RFC3339Nano

// GetPollTimestamp returns the last poll cursor stored for an arr source, or the
// zero time with no error when the key has no stored value (Requirement 6.3),
// matching the old SQLite not-found-means-zero behaviour. A non-canonical key is
// rejected (mirrors the old store's key.Valid guard), and a stored value that
// cannot be parsed as RFC3339Nano is surfaced as an error rather than silently
// treated as the zero time.
func (d *DB) GetPollTimestamp(_ context.Context, key api.PollKey) (time.Time, error) {
	if !key.Valid() {
		return time.Time{}, fmt.Errorf("get poll timestamp: invalid key %q", key)
	}
	var result time.Time
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketPollState))
		if b == nil {
			return fmt.Errorf("boltstore: poll_state bucket not found")
		}
		v := b.Get(pollStateKey(key))
		if v == nil {
			return nil // no stored cursor -> zero time, no error
		}
		t, perr := time.Parse(pollTimeLayout, string(v))
		if perr != nil {
			return fmt.Errorf("parse poll timestamp %q: %w", key, perr)
		}
		result = t
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return result, nil
}

// SetPollTimestamp stores the last poll cursor for an arr source, formatted with
// RFC3339Nano so GetPollTimestamp recovers it with full precision (Requirement
// 6.2). A non-canonical key is rejected before any write (mirrors the old
// store's key.Valid guard), preventing a typo from silently creating a new
// cursor row and forcing a full history re-fetch. A later set overwrites the
// prior cursor.
func (d *DB) SetPollTimestamp(_ context.Context, key api.PollKey, t time.Time) error {
	if !key.Valid() {
		return fmt.Errorf("set poll timestamp: invalid key %q", key)
	}
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketPollState))
		if b == nil {
			return fmt.Errorf("boltstore: poll_state bucket not found")
		}
		return b.Put(pollStateKey(key), []byte(t.Format(pollTimeLayout)))
	})
}
