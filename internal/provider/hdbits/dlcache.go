package hdbits

import (
	"container/heap"
	"sync"
	"time"
)

// downloadCache holds getdox bodies, so a season pack fetched for one
// episode serves the season's other episodes without another download. It
// keeps at most maxEntries bodies of at most maxItemSize bytes, each for
// downloadTTL after it was stored, evicting the least recently used when full.
type downloadCache struct {
	cache       map[string]*entry
	h           entryHeap
	maxEntries  int
	maxItemSize int64
	tick        uint64
	mu          sync.Mutex
	saturated   sync.Once
}

func newDownloadCache(maxEntries int, maxItemSize int64) *downloadCache {
	return &downloadCache{cache: map[string]*entry{}, maxEntries: maxEntries, maxItemSize: maxItemSize}
}

// get answers key's body while it is fresh, refreshing its recency.
func (dc *downloadCache) get(key string) ([]byte, bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	e, ok := dc.cache[key]
	if !ok {
		return nil, false
	}
	if e.expired() {
		dc.remove(e)
		return nil, false
	}
	dc.tick++
	e.used = dc.tick
	heap.Fix(&dc.h, e.index)
	return e.data, true
}

// put stores data under key, dropping the expired bodies and then the least
// recently used one when the cache is full. It stores nothing for a body
// over maxItemSize or a cache of no entries, calling onSaturated the first
// time it refuses one.
func (dc *downloadCache) put(key string, data []byte, onSaturated func()) bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if int64(len(data)) > dc.maxItemSize || dc.maxEntries <= 0 {
		if onSaturated != nil {
			dc.saturated.Do(onSaturated)
		}
		return false
	}
	if e, ok := dc.cache[key]; ok && !e.expired() {
		return true
	} else if ok {
		dc.remove(e)
	}
	if len(dc.cache) >= dc.maxEntries {
		for _, e := range dc.cache {
			if e.expired() {
				dc.remove(e)
			}
		}
	}
	if len(dc.cache) >= dc.maxEntries {
		dc.remove(dc.h[0])
	}
	dc.tick++
	e := &entry{data: data, stored: time.Now(), used: dc.tick, key: key}
	dc.cache[key] = e
	heap.Push(&dc.h, e)
	return true
}

// remove drops e from the map and the heap; the caller holds dc.mu.
func (dc *downloadCache) remove(e *entry) {
	heap.Remove(&dc.h, e.index)
	delete(dc.cache, e.key)
}

type entry struct {
	stored time.Time
	key    string
	data   []byte
	used   uint64
	index  int
}

func (e *entry) expired() bool { return time.Since(e.stored) >= downloadTTL }

// entryHeap orders entries least recently used first.
type entryHeap []*entry

func (h entryHeap) Len() int           { return len(h) }
func (h entryHeap) Less(i, j int) bool { return h[i].used < h[j].used }
func (h entryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }

func (h *entryHeap) Push(x any) {
	e, _ := x.(*entry)
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *entryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	*h = old[:n-1]
	return e
}
