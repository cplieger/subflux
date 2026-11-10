package syncsched

import (
	"testing"

	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subsync"
)

func TestSpanCacheEvictsLeastRecent(t *testing.T) {
	c := newSpanCache()
	key := func(i int) spanKey { return spanKey{inst: statedb.InstanceID(i + 1), stream: 2} }
	for i := range referenceCacheSize {
		c.put(key(i), []subsync.Span{{StartMs: int64(i)}})
	}
	if _, ok := c.get(key(0)); !ok {
		t.Fatal("a full cache lost its first entry")
	}
	c.put(key(referenceCacheSize), nil)
	if _, ok := c.get(key(1)); ok {
		t.Error("the least recently used entry survived a put past the size")
	}
	for _, i := range []int{0, 2, referenceCacheSize} {
		if _, ok := c.get(key(i)); !ok {
			t.Errorf("entry %d evicted, want only the least recently used one gone", i)
		}
	}
	c.put(key(0), []subsync.Span{{StartMs: 99}})
	if got, _ := c.get(key(0)); len(got) != 1 || got[0].StartMs != 99 {
		t.Errorf("a put for a held key: %v, want the new spans", got)
	}
	if n := len(c.entries); n != referenceCacheSize {
		t.Errorf("%d entries, want %d", n, referenceCacheSize)
	}
}
