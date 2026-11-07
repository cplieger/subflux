package cache

import (
	"context"
	"testing"
)

// FuzzGetOrFetch_firstValueKept drives a cache through a key sequence:
// with no time passing, every call answers the key's first value.
func FuzzGetOrFetch_firstValueKept(f *testing.F) {
	f.Add([]byte{0, 1, 2, 0, 1, 2})
	f.Add([]byte{255, 0, 255})
	f.Fuzz(func(t *testing.T, keys []byte) {
		c := New(func(v int) bool { return v != 0 })
		first := map[byte]int{}
		for i, k := range keys {
			got, err := c.GetOrFetch(t.Context(), string(rune(k)), func(context.Context) (int, error) { return i, nil })
			if err != nil {
				t.Fatal(err)
			}
			want, seen := first[k]
			if !seen {
				first[k], want = i, i
			}
			if got != want {
				t.Fatalf("key %d: %d, want its first value %d", k, got, want)
			}
		}
	})
}
