// Package dlcache provides a generic LRU download-data cache with heap-based
// eviction. Reusable by any provider that caches season-pack or download data.
package dlcache

import (
	"container/heap"
	"sync"
	"time"
)

// DownloadCache is a thread-safe LRU cache for download data (e.g. season
// pack zip/rar contents). Evicts least-recently-used entries when the
// entry count exceeds maxEntries.
type DownloadCache struct {
	cache     map[string]*entry
	h         entryHeap
	mu        sync.Mutex
	saturated sync.Once

	maxEntries  int
	maxItemSize int64
}

// New creates a DownloadCache with the given limits.
func New(maxEntries int, maxItemSize int64) *DownloadCache {
	return &DownloadCache{
		cache:       make(map[string]*entry),
		maxEntries:  maxEntries,
		maxItemSize: maxItemSize,
	}
}

// Get retrieves cached data by key. Returns nil, false on miss.
func (dc *DownloadCache) Get(key string) ([]byte, bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	e, ok := dc.cache[key]
	if !ok {
		return nil, false
	}
	e.used = time.Now()
	heap.Fix(&dc.h, e.index)
	return e.data, true
}

// Put stores data under key. Returns false (without storing) if the data
// exceeds maxItemSize or the cache is full and cannot evict. The onSaturated
// callback is called at most once per Clear cycle when the cache refuses
// to store an entry.
func (dc *DownloadCache) Put(key string, data []byte, onSaturated func()) bool {
	if int64(len(data)) > dc.maxItemSize {
		if onSaturated != nil {
			dc.saturated.Do(onSaturated)
		}
		return false
	}
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if _, exists := dc.cache[key]; exists {
		return true // already cached
	}
	if len(dc.cache) >= dc.maxEntries {
		dc.evictOldest()
	}
	if len(dc.cache) >= dc.maxEntries {
		if onSaturated != nil {
			dc.saturated.Do(onSaturated)
		}
		return false
	}
	e := &entry{data: data, used: time.Now(), key: key}
	dc.cache[key] = e
	heap.Push(&dc.h, e)
	return true
}

// Clear removes all cached data and resets the saturation guard.
func (dc *DownloadCache) Clear() {
	dc.mu.Lock()
	dc.cache = make(map[string]*entry)
	dc.h = nil
	dc.saturated = sync.Once{}
	dc.mu.Unlock()
}

// evictOldest removes the least-recently-used entry. Caller must hold dc.mu.
func (dc *DownloadCache) evictOldest() {
	if len(dc.h) == 0 {
		return
	}
	oldest, _ := heap.Pop(&dc.h).(*entry)
	delete(dc.cache, oldest.key)
}

// --- heap implementation ---

type entry struct {
	used  time.Time
	key   string
	data  []byte
	index int
}

type entryHeap []*entry

func (h entryHeap) Len() int           { return len(h) }
func (h entryHeap) Less(i, j int) bool { return h[i].used.Before(h[j].used) }
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
