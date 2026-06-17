package cache

// Round-2 mutant-killing test for internal/cache.
//
// Kills cache.go:108:10 CONDITIONALS_NEGATION (`if err == nil` -> `if err != nil`)
// in GetOrFetchCtx. The guard caches the fetch result only on success; the
// negated form would skip caching successes (and cache errors instead), so a
// second lookup re-invokes the fetch function.

import (
	"context"
	"testing"
	"time"
)

func TestGkSubfluxR2_GetOrFetchCtxCachesSuccess(t *testing.T) {
	c := New[int](time.Minute)
	calls := 0
	fn := func(context.Context) (int, error) {
		calls++
		return 42, nil
	}

	if v, err := c.GetOrFetchCtx(context.Background(), "k", fn); err != nil || v != 42 {
		t.Fatalf("first GetOrFetchCtx = (%d, %v), want (42, nil)", v, err)
	}
	if v, err := c.GetOrFetchCtx(context.Background(), "k", fn); err != nil || v != 42 {
		t.Fatalf("second GetOrFetchCtx = (%d, %v), want (42, nil)", v, err)
	}
	if calls != 1 {
		t.Errorf("fetch fn called %d times, want 1 (a successful result must be cached)", calls)
	}
}
