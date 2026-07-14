package boltstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file holds the adaptive-backoff domain (the search_attempts bucket):
// RecordNoResult, BackedOffProviders, GetBackoffItems, and GetBackoffByPrefix.
// The bucket has no secondary index: it is bounded by the number of
// currently-backed-off (triple, provider) pairs, so the ordered listings sort
// in memory instead of maintaining a due-order index on every write.

// computeNextRetry computes a backoff window's next_retry from the prior
// failure count and the backoff parameters, mirroring the SQLite formula the
// old store used in its upsert:
//
//	delay_seconds = MIN(maxDelay, initialDelay * multiplier^oldFailures)
//	next_retry    = now + CAST(delay_seconds AS INTEGER) seconds
//
// The SQL truncated the delay to whole seconds (CAST(... AS INTEGER)); this
// reproduces that truncation so the computed next_retry matches the old engine.
// oldFailures is the failure count BEFORE this attempt (0 for a brand-new row),
// matching the SQL's reference to the pre-increment search_attempts.failures.
func computeNextRetry(now time.Time, oldFailures int, bp api.BackoffParams) time.Time {
	delaySec := bp.InitialDelay.Seconds() * math.Pow(bp.Multiplier, float64(oldFailures))
	if maxSec := bp.MaxDelay.Seconds(); delaySec > maxSec {
		delaySec = maxSec
	}
	return now.Add(time.Duration(int64(delaySec)) * time.Second)
}

// RecordNoResult records a failed provider search attempt for a triple and
// recomputes its backoff window, all in one write transaction. It reads the
// prior attempt (if any) to obtain the failure count, increments it, computes
// the new next_retry from the BackoffParams, and writes the row through the
// putAttempt chokepoint, which maintains the attempts counter in the same
// transaction.
//
// A brand-new row starts at failures=1 with next_retry = now + InitialDelay,
// matching the old SQLite INSERT branch (which used the full InitialDelay
// duration without the integer-second truncation of the upsert path).
func (d *DB) RecordNoResult(_ context.Context, mediaType api.MediaType, mediaID, language string, providerName api.ProviderID,
	bp api.BackoffParams,
) error {
	now := time.Now()
	err := d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketSearchAttempts))
		if b == nil {
			return errors.New("boltstore: search_attempts bucket not found")
		}
		key := attemptKey(mediaType, mediaID, language, providerName)

		var nextRetry time.Time
		failures := 1
		if raw := b.Get(key); raw != nil {
			// A write that increments an existing row must read its prior
			// failure count. A corrupt prior value fails the write closed (the
			// putAttempt chokepoint would re-decode and fail anyway); we surface
			// a clean error rather than silently resetting the count.
			var old attemptRec
			if derr := kv.Decode(raw, &old); derr != nil {
				return fmt.Errorf("boltstore: decode prior search_attempts row: %w", derr)
			}
			failures = old.Failures + 1
			nextRetry = computeNextRetry(now, old.Failures, bp)
		} else {
			// New row: failures=1, next_retry = now + InitialDelay.
			nextRetry = now.Add(bp.InitialDelay)
		}

		rec := attemptRec{LastTried: now, NextRetry: nextRetry, Failures: failures}
		return putAttempt(tx, mediaType, mediaID, language, providerName, &rec)
	})
	if err != nil {
		return err
	}
	slog.Debug("recorded no-result backoff",
		"media_id", mediaID, "lang", language, "provider", providerName)
	return nil
}

// BackedOffProviders returns the providers that should be skipped for a triple
// due to adaptive backoff. A provider is backed off when its failure count
// reaches maxAttempts OR its next_retry is in the future; when maxAttempts is
// zero or negative the threshold check is disabled and only the next_retry
// check applies. Providers with no recorded row for the triple are absent from
// the scan, so they are never backed off (no row means immediately eligible).
//
// Rows with an empty provider component are skipped, matching the old store's
// `provider != ”` filter.
func (d *DB) BackedOffProviders(_ context.Context, mediaType api.MediaType, mediaID, language string, maxAttempts int) ([]api.ProviderID, error) {
	if maxAttempts < 0 {
		maxAttempts = 0
	}
	now := time.Now()
	prefix := triplePrefix(mediaType, mediaID, language)

	var backed []api.ProviderID
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketSearchAttempts))
		if b == nil {
			return errors.New("boltstore: search_attempts bucket not found")
		}
		var serr error
		backed, serr = scanBackedOffProviders(b, prefix, maxAttempts, now)
		return serr
	})
	if err != nil {
		return nil, err
	}
	return backed, nil
}

// scanBackedOffProviders prefix-scans the search_attempts triple under prefix
// and returns the providers that are currently backed off. The provider is the
// only key component after the triple prefix; an empty-provider row is skipped
// (the old store's `provider != ”` filter), and an undecodable derived row is
// skipped with a warning (logged by decodeRecord) since a missing row just
// means the provider is eligible.
func scanBackedOffProviders(b *bolt.Bucket, prefix []byte, maxAttempts int, now time.Time) ([]api.ProviderID, error) {
	var backed []api.ProviderID
	c := b.Cursor()
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		provider := api.ProviderID(k[len(prefix):])
		if provider == "" {
			continue
		}
		var rec attemptRec
		skip, derr := decodeRecord(bucketDecodeMode(bucketSearchAttempts), bucketSearchAttempts, k, v, &rec)
		if derr != nil {
			return nil, derr
		}
		if skip {
			continue
		}
		if providerBackedOff(&rec, maxAttempts, now) {
			backed = append(backed, provider)
		}
	}
	return backed, nil
}

// providerBackedOff reports whether an attempt row means the provider should be
// skipped: its failure count reached maxAttempts (only when the threshold is
// enabled, i.e. maxAttempts > 0) OR its next_retry is still in the future.
func providerBackedOff(rec *attemptRec, maxAttempts int, now time.Time) bool {
	if maxAttempts > 0 && rec.Failures >= maxAttempts {
		return true
	}
	return now.Before(rec.NextRetry)
}

// decodeAttemptEntry turns one search_attempts row (key + value) into a fully
// populated api.BackoffEntry. It parses (mt, mid, lang, provider) out of the
// composite primary key and decodes the value, reporting skip=true when the
// provider component is empty (matching the old store's `provider != ”`
// filter) or the row is an undecodable derived record (decodeRecord logs a
// warning). A genuine decode error other than tolerant-skip is returned.
func decodeAttemptEntry(key, raw []byte) (api.BackoffEntry, bool, error) {
	parts := kv.Split(key)
	if len(parts) != 4 {
		// Malformed key (not mt 0x00 mid 0x00 lang 0x00 provider); skip rather
		// than surface a half-parsed entry.
		return api.BackoffEntry{}, true, nil
	}
	provider := api.ProviderID(parts[3])
	if provider == "" {
		return api.BackoffEntry{}, true, nil // provider != '' filter
	}
	var rec attemptRec
	skip, derr := decodeRecord(bucketDecodeMode(bucketSearchAttempts), bucketSearchAttempts, key, raw, &rec)
	if derr != nil {
		return api.BackoffEntry{}, false, derr
	}
	if skip {
		return api.BackoffEntry{}, true, nil
	}
	return api.BackoffEntry{
		MediaType: api.MediaType(parts[0]),
		MediaID:   parts[1],
		Language:  parts[2],
		Provider:  provider,
		Failures:  rec.Failures,
		LastTried: rec.LastTried,
		NextRetry: rec.NextRetry,
	}, false, nil
}

// GetBackoffItems returns every backed-off provider row ordered by ascending
// next_retry, then by primary key for a deterministic tie order. It scans the
// primary bucket and sorts in memory: the bucket holds only currently
// backed-off (triple, provider) pairs, so the sort input is small and bounded,
// and this listing is a rare introspection call (CLI `subflux backoff`, the
// backoff API). Rows with an empty provider component are excluded, matching
// the old store's `WHERE provider != ” ORDER BY next_retry ASC`.
func (d *DB) GetBackoffItems(_ context.Context) ([]api.BackoffEntry, error) {
	var out []api.BackoffEntry
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketSearchAttempts))
		if b == nil {
			return errors.New("boltstore: search_attempts bucket not found")
		}
		return b.ForEach(func(k, v []byte) error {
			entry, skip, derr := decodeAttemptEntry(k, v)
			if derr != nil {
				return derr
			}
			if !skip {
				out = append(out, entry)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].NextRetry.Before(out[j].NextRetry)
	})
	return out, nil
}

// GetBackoffByPrefix returns the backed-off provider rows for one media type,
// optionally narrowed to media ids that start with mediaIDPrefix, ordered by
// media id then ascending next_retry. It prefix-scans the search_attempts
// primary bucket on `mediaType 0x00 mediaIDPrefix` (an empty prefix returns
// every row for the type) and then sorts by (media_id, next_retry), because no
// single index orders by media id then next_retry. Rows with an empty provider
// component are excluded, matching the old store's
// `WHERE media_type = ? AND provider != ” ... ORDER BY media_id, next_retry ASC`.
//
// The prefix is a media-id starts-with match (LIKE 'prefix%'): querying "tt1"
// intentionally returns both "tt1" and "tt12", unlike the exact triple scans
// which use a trailing separator for component-boundary isolation.
func (d *DB) GetBackoffByPrefix(_ context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.BackoffEntry, error) {
	// Build `mediaType 0x00 mediaIDPrefix`. Join with a single component yields
	// the bare media type with no trailing separator, then the separator and
	// the (possibly empty) media-id prefix follow.
	prefix := append(kv.Join(string(mediaType)), kv.Sep)
	prefix = append(prefix, mediaIDPrefix...)

	var out []api.BackoffEntry
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketSearchAttempts))
		if b == nil {
			return errors.New("boltstore: search_attempts bucket not found")
		}
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			entry, skip, derr := decodeAttemptEntry(k, v)
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

	// Order by media id, then ascending next_retry (mirrors the old
	// `ORDER BY media_id, next_retry ASC`). The scan already groups rows by
	// media id ascending, but next_retry within a media id is unordered.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MediaID != out[j].MediaID {
			return out[i].MediaID < out[j].MediaID
		}
		return out[i].NextRetry.Before(out[j].NextRetry)
	})
	return out, nil
}
