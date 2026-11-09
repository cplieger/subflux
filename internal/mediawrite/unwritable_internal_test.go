package mediawrite

import (
	"strconv"
	"testing"
	"time"
)

func TestUnwritableKeepsNewest4096(t *testing.T) {
	t.Parallel()
	u := newUnwritableDirs()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	id := dirID{dev: 1, ino: 2}
	key := func(i int) dirKey { return dirKey{root: "/media", dir: strconv.Itoa(i)} }
	for i := range 4097 {
		u.record(key(i), id, now)
	}
	if u.holds(key(0), id, now) {
		t.Error("the oldest of 4,097 folders is still remembered")
	}
	for i := 1; i < 4097; i++ {
		if !u.holds(key(i), id, now) {
			t.Fatalf("folder %d of the newest 4,096 is forgotten", i)
		}
	}
}
