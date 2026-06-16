package boltkv

import (
	"encoding/binary"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// IndexSpec declares one secondary index that [PutIndexed] and [DeleteIndexed]
// keep consistent with a primary record. T is the decoded primary value type.
//
// The helper derives the index entry key from the primary key and value via
// Key. When Value is non-nil, its result is stored as the index entry's value:
// a small projection of query-relevant fields that lets a hot-path read answer
// from the index walk alone, without dereferencing the primary record. When
// Value is nil, the index is existence-only (an empty value is stored).
type IndexSpec[T any] struct {
	// Key derives the index entry key from the primary key and decoded value.
	Key func(primaryKey []byte, value *T) []byte
	// Value, when non-nil, derives the projected index entry value.
	Value func(primaryKey []byte, value *T) []byte
	// Bucket is the name of the secondary-index bucket.
	Bucket string
}

// CounterSpec declares a maintained integer counter held in a meta bucket.
// [PutIndexed] adds Delta on a genuine insert and [DeleteIndexed] subtracts it
// on a delete, in the same transaction as the primary write, so the counter
// answers O(1) count queries (e.g. Stats, TotalSubtitleFiles) that bbolt cannot
// serve from a live count. A zero Delta is treated as 1. Counters never go
// below zero.
type CounterSpec struct {
	// Bucket holds the counter (typically the meta bucket).
	Bucket string
	// Key is the counter's key within Bucket.
	Key []byte
	// Delta is added on insert and subtracted on delete; zero means 1.
	Delta int64
}

// PutIndexed writes value at key in primaryBucket and maintains the declared
// indexes and counters, all in the supplied read-write transaction. It is the
// single chokepoint for the secondary-index maintenance invariant: it reads the
// prior value (if any) to delete its stale index entries, then writes the new
// primary value, then adds the fresh index entries (including any projected
// index value). Counters are incremented only on a genuine insert (not on an
// update of an existing key). Because bbolt transactions are all-or-nothing, an
// index can never diverge from its primary.
//
// The value is JSON-encoded via [Encode]; T must be JSON-serialisable. All
// referenced buckets must already exist (the core store owns bucket bootstrap).
func PutIndexed[T any](tx *bolt.Tx, primaryBucket string, key []byte, value *T, indexes []IndexSpec[T], counters []CounterSpec) error {
	pb := tx.Bucket([]byte(primaryBucket))
	if pb == nil {
		return fmt.Errorf("boltkv: primary bucket %q not found", primaryBucket)
	}

	// Read the prior value (if any) to delete its stale index entries before
	// the new value overwrites it.
	existed := false
	if old := pb.Get(key); old != nil {
		existed = true
		var oldVal T
		if err := Decode(old, &oldVal); err != nil {
			return fmt.Errorf("boltkv: decode prior %s value for reindex: %w", primaryBucket, err)
		}
		for i := range indexes {
			ix := &indexes[i]
			ib := tx.Bucket([]byte(ix.Bucket))
			if ib == nil {
				return fmt.Errorf("boltkv: index bucket %q not found", ix.Bucket)
			}
			if err := ib.Delete(ix.Key(key, &oldVal)); err != nil {
				return fmt.Errorf("boltkv: delete stale index %q: %w", ix.Bucket, err)
			}
		}
	}

	// Write the new primary value.
	enc, err := Encode(value)
	if err != nil {
		return err
	}
	if err := pb.Put(key, enc); err != nil {
		return fmt.Errorf("boltkv: put primary %q: %w", primaryBucket, err)
	}

	// Add the fresh index entries.
	for i := range indexes {
		ix := &indexes[i]
		ib := tx.Bucket([]byte(ix.Bucket))
		if ib == nil {
			return fmt.Errorf("boltkv: index bucket %q not found", ix.Bucket)
		}
		var iv []byte
		if ix.Value != nil {
			iv = ix.Value(key, value)
		}
		if err := ib.Put(ix.Key(key, value), iv); err != nil {
			return fmt.Errorf("boltkv: put index %q: %w", ix.Bucket, err)
		}
	}

	// Counters track row existence, so they move only on insert/delete.
	if !existed {
		for i := range counters {
			if err := adjustCounter(tx, &counters[i], +1); err != nil {
				return err
			}
		}
	}
	return nil
}

// DeleteIndexed removes key from primaryBucket and the declared index entries,
// and decrements the declared counters, all in the supplied transaction. It
// reads the prior value to derive the exact index entries to delete. It returns
// existed=false (and makes no changes) when the key is absent, so a delete of a
// missing key is a no-op and the operation is idempotent.
func DeleteIndexed[T any](tx *bolt.Tx, primaryBucket string, key []byte, indexes []IndexSpec[T], counters []CounterSpec) (existed bool, err error) {
	pb := tx.Bucket([]byte(primaryBucket))
	if pb == nil {
		return false, fmt.Errorf("boltkv: primary bucket %q not found", primaryBucket)
	}
	old := pb.Get(key)
	if old == nil {
		return false, nil
	}
	var oldVal T
	if err := Decode(old, &oldVal); err != nil {
		return false, fmt.Errorf("boltkv: decode prior %s value for delete: %w", primaryBucket, err)
	}
	for i := range indexes {
		ix := &indexes[i]
		ib := tx.Bucket([]byte(ix.Bucket))
		if ib == nil {
			return false, fmt.Errorf("boltkv: index bucket %q not found", ix.Bucket)
		}
		if err := ib.Delete(ix.Key(key, &oldVal)); err != nil {
			return false, fmt.Errorf("boltkv: delete index %q: %w", ix.Bucket, err)
		}
	}
	if err := pb.Delete(key); err != nil {
		return false, fmt.Errorf("boltkv: delete primary %q: %w", primaryBucket, err)
	}
	for i := range counters {
		if err := adjustCounter(tx, &counters[i], -1); err != nil {
			return false, err
		}
	}
	return true, nil
}

// GetUint64 reads an 8-byte big-endian scalar (a counter or a schema version)
// from bucket b. It returns (value, true) when the key holds an 8-byte value
// and (0, false) otherwise.
func GetUint64(b *bolt.Bucket, key []byte) (uint64, bool) {
	raw := b.Get(key)
	if len(raw) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(raw), true
}

// PutUint64 writes v as an 8-byte big-endian scalar at key in bucket b. It is
// used for maintained counters and for the caller-owned schema-version meta
// keys (core_schema_version, auth_schema_version).
func PutUint64(b *bolt.Bucket, key []byte, v uint64) error {
	if err := b.Put(key, Be64(v)); err != nil {
		return fmt.Errorf("boltkv: put scalar: %w", err)
	}
	return nil
}

// ReadCounter returns the maintained counter at key in bucket b as a signed
// int64 (counters are non-negative), or 0 when absent.
func ReadCounter(b *bolt.Bucket, key []byte) int64 {
	v, _ := GetUint64(b, key)
	return int64(v) //nolint:gosec // G115: counters are non-negative and bounded by row count
}

// adjustCounter adds sign*Delta to the counter declared by c, clamping at zero.
func adjustCounter(tx *bolt.Tx, c *CounterSpec, sign int64) error {
	b := tx.Bucket([]byte(c.Bucket))
	if b == nil {
		return fmt.Errorf("boltkv: counter bucket %q not found", c.Bucket)
	}
	delta := c.Delta
	if delta == 0 {
		delta = 1
	}
	next := ReadCounter(b, c.Key) + delta*sign
	next = max(next, 0)
	return PutUint64(b, c.Key, uint64(next))
}
