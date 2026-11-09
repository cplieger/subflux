package mediawrite

import "sync"

// hashCacheSize bounds List's hash cache.
const hashCacheSize = 4096

// hashKey is a file as one directory read sees it. A same-size edit within
// one mtime tick hits a stale entry; Retime, Delete and Current never read
// the cache, so it costs one ErrExpectation.
type hashKey struct {
	root, path    string
	size, mtimeNs int64
	inode         uint64
}

// hashEntry is one cached hash in the cache's recency ring.
type hashEntry struct {
	prev, next *hashEntry
	key        hashKey
	sum        [32]byte
}

// hashCache is an LRU of content hashes keyed by hashKey; ring.next is the
// most recently used entry and ring.prev the eviction candidate.
type hashCache struct {
	byKey map[hashKey]*hashEntry
	ring  hashEntry
	mu    sync.Mutex
}

func newHashCache() *hashCache {
	c := &hashCache{byKey: map[hashKey]*hashEntry{}}
	c.ring.prev, c.ring.next = &c.ring, &c.ring
	return c
}

func (c *hashCache) get(k hashKey) ([32]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[k]
	if !ok {
		return [32]byte{}, false
	}
	unlink(e)
	c.pushFront(e)
	return e.sum, true
}

func (c *hashCache) put(k hashKey, sum [32]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.byKey[k]; ok {
		e.sum = sum
		unlink(e)
		c.pushFront(e)
		return
	}
	e := &hashEntry{key: k, sum: sum}
	c.byKey[k] = e
	c.pushFront(e)
	if len(c.byKey) > hashCacheSize {
		oldest := c.ring.prev
		unlink(oldest)
		delete(c.byKey, oldest.key)
	}
}

func unlink(e *hashEntry) {
	e.prev.next, e.next.prev = e.next, e.prev
}

func (c *hashCache) pushFront(e *hashEntry) {
	e.prev, e.next = &c.ring, c.ring.next
	c.ring.next.prev = e
	c.ring.next = e
}
