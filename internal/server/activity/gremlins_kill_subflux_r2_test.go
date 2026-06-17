package activity

// Round-2 mutant-killing test for internal/server/activity.
//
// Kills activity.go:212:13 CONDITIONALS_NEGATION (`if a.index == nil` ->
// `if a.index != nil`) in AppendEntry. On a freshly constructed Log the index
// map is nil; the guard lazily allocates it before the subsequent write. The
// negated guard skips the allocation on the nil case, so the following
// `a.index[e.ID] = ...` writes to a nil map and panics.

import "testing"

func TestGkSubfluxR2_AppendEntryAllocatesNilIndex(t *testing.T) {
	l := &Log{maxItems: 4} // zero-value index map (nil)
	l.Lock()
	l.AppendEntry(Entry{ID: "x", Action: "scan"})
	l.Unlock()

	entries := l.Entries()
	if len(entries) != 1 || entries[0].ID != "x" {
		t.Fatalf("Entries() = %v, want a single entry with ID \"x\"", entries)
	}
}
