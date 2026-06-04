package dlcache

import (
	"testing"
)

// FuzzDownloadCache_PutGet exercises the cache with arbitrary keys and values,
// verifying the Get-after-Put invariant and that the cache never panics
// regardless of key/value combinations.
func FuzzDownloadCache_PutGet(f *testing.F) {
	f.Add("key1", []byte("data1"), "key2", []byte("data2"))
	f.Add("", []byte{}, "a", []byte("b"))
	f.Add("same", []byte("v1"), "same", []byte("v2"))

	f.Fuzz(func(t *testing.T, k1 string, v1 []byte, k2 string, v2 []byte) {
		dc := New(4, 1024)

		// Put first entry.
		ok1 := dc.Put(k1, v1, nil)

		// If stored, must be retrievable.
		if ok1 {
			got, found := dc.Get(k1)
			if !found {
				t.Fatal("Get returned false after successful Put for k1")
			}
			if len(got) != len(v1) {
				t.Fatalf("Get(k1) len = %d, want %d", len(got), len(v1))
			}
		}

		// Put second entry.
		ok2 := dc.Put(k2, v2, nil)

		if ok2 {
			got, found := dc.Get(k2)
			if !found {
				t.Fatal("Get returned false after successful Put for k2")
			}
			// If same key, the original value is retained (no overwrite).
			if k1 == k2 && ok1 {
				if len(got) != len(v1) {
					t.Fatalf("Get(k2) len = %d, want %d (same key as k1)", len(got), len(v1))
				}
			} else if len(got) != len(v2) {
				t.Fatalf("Get(k2) len = %d, want %d", len(got), len(v2))
			}
		}

		// Clear never panics.
		dc.Clear()

		// After clear, nothing is retrievable.
		if _, found := dc.Get(k1); found {
			t.Fatal("Get returned true after Clear for k1")
		}
	})
}
