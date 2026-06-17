package hdbits

import (
	"context"
	"testing"
	"time"
)

// Test_gk_subflux_u31_torrent_cache_ttl_is_one_hour kills the ARITHMETIC_BASE
// mutant at hdbits.go:81 (`cache.New[[]int](1 * time.Hour)` -> `1 / time.Hour`).
//
// time.Hour is 3.6e12 ns, so `1 / time.Hour` truncates (integer division) to 0,
// constructing torrentCache with a zero TTL. cache.Set stores
// expires = time.Now().Add(ttl) and cache.Get reports an entry expired once
// time.Now().After(expires). With the real 1h TTL, a value set then read a few
// milliseconds later is still fresh (found == true). With a 0 TTL the entry's
// expiry is the exact Set instant, so any later Get reports it expired
// (found == false). Asserting found == true after the sleep therefore fails
// under the mutant and passes under the original.
func Test_gk_subflux_u31_torrent_cache_ttl_is_one_hour(t *testing.T) {
	p, err := Factory(context.Background(), map[string]any{
		settingUsername: "gk_subflux_u31_user",
		settingPasskey:  "gk_subflux_u31_passkey",
	})
	if err != nil {
		t.Fatalf("Factory() error = %v, want nil", err)
	}
	prov, ok := p.(*Provider)
	if !ok {
		t.Fatalf("Factory() returned %T, want *Provider", p)
	}

	const gk_subflux_u31_key = "gk_subflux_u31_cache_key"
	gk_subflux_u31_want := []int{11, 22, 33}
	prov.torrentCache.Set(gk_subflux_u31_key, gk_subflux_u31_want)

	// Advance the wall clock past a hypothetical zero-TTL expiry (which would
	// equal the Set instant) while staying far below the real 1h TTL.
	time.Sleep(5 * time.Millisecond)

	got, found := prov.torrentCache.Get(gk_subflux_u31_key)
	if !found {
		t.Fatalf("torrentCache.Get(%q) found = false, want true; TTL must be 1h, a `1 / time.Hour` mutation yields 0", gk_subflux_u31_key)
	}
	if len(got) != len(gk_subflux_u31_want) {
		t.Fatalf("torrentCache.Get(%q) = %v, want %v", gk_subflux_u31_key, got, gk_subflux_u31_want)
	}
	for i := range gk_subflux_u31_want {
		if got[i] != gk_subflux_u31_want[i] {
			t.Fatalf("torrentCache.Get(%q)[%d] = %d, want %d", gk_subflux_u31_key, i, got[i], gk_subflux_u31_want[i])
		}
	}
}
