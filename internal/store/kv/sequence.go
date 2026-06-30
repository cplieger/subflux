package kv

import (
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// NextID allocates the next surrogate id for bucket b via bbolt's monotonic
// Bucket.NextSequence and returns it both as the raw uint64 and its [Be64] key
// encoding. The be64 encoding makes the id sort in insertion order, so a
// surrogate-keyed bucket walks oldest to newest and an "id DESC" tiebreak is
// recoverable. Must be called inside a read-write transaction.
func NextID(b *bolt.Bucket) (id uint64, key []byte, err error) {
	id, err = b.NextSequence()
	if err != nil {
		return 0, nil, fmt.Errorf("kv: next sequence: %w", err)
	}
	return id, Be64(id), nil
}
