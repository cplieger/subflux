package plex

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/plexapi/v3"
)

// highWaterOf answers section's stored high-water.
func (a *adapter) highWaterOf(section string) int64 {
	a.idx.mu.Lock()
	defer a.idx.mu.Unlock()
	return a.idx.highWater[plexapi.RatingKey(section)]
}

// TestUpgradedFileMergedByUpdatedAt: Plex keeps an upgraded file's addedAt
// and moves its updatedAt (the recorded pair), so a miss's merge must read
// by updatedAt to find the item at its new path.
func TestUpgradedFileMergedByUpdatedAt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		before, after := recorded(t, "upgraded-file-before"), recorded(t, "upgraded-file-after")
		oldPath, newPath := files(before)[0], files(after)[0]
		f := newFakePlex()
		f.addSection("2", "show")
		f.put("2", before)
		a := inProcess(t, f)
		if keys, err := locate(t, a, oldPath, time.Minute); err != nil || !slices.Equal(keys, []string{"724218"}) {
			t.Fatalf("locate(before) = %v %v", keys, err)
		}
		if _, err := locate(t, a, "/media/absent.mkv", time.Minute); err != nil {
			t.Fatal(err)
		}
		if hw := a.highWaterOf("2"); hw != 1792199733 {
			t.Fatalf("high-water %d after merging the before answer, want its updatedAt 1792199733", hw)
		}
		f.put("2", after)
		time.Sleep(deltaReuse)
		if keys, err := locate(t, a, newPath, time.Minute); err != nil || !slices.Equal(keys, []string{"724218"}) {
			t.Errorf("locate(after) = %v %v, want the upgraded item at its new path", keys, err)
		}
		if keys, err := locate(t, a, oldPath, time.Minute); err != nil || len(keys) != 0 {
			t.Errorf("locate(old path) = %v %v, want it gone from the index", keys, err)
		}
		if n := f.countExact("GET /library/sections"); n != 1 {
			t.Errorf("%d builds, want the merge alone to find it", n)
		}
	})
}

func TestMissAfterBuildFindsUpdated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		a := inProcess(t, f)
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		added := "/media/tv/Show/S01E02.mkv"
		f.put("2", row("102", "episode", time.Now().Unix(), time.Now().Unix(), added))
		if keys, err := locate(t, a, added, time.Minute); err != nil || !slices.Equal(keys, []string{"102"}) {
			t.Errorf("locate(added) = %v %v, want [102] found by the merge", keys, err)
		}
		if n := f.countExact("GET /library/sections"); n != 1 {
			t.Errorf("%d builds, want no rebuild", n)
		}
	})
}

func TestMergeReusedWithinTenSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		a := inProcess(t, f)
		reads := func() int { return f.count("GET /library/sections/2/all?type=4&sort=updatedAt") }
		for range 3 {
			if _, err := locate(t, a, "/media/absent.mkv", time.Minute); err != nil {
				t.Fatal(err)
			}
		}
		if reads() != 1 {
			t.Errorf("%d updated-since reads for three misses at once, want one", reads())
		}
		time.Sleep(deltaReuse)
		if _, err := locate(t, a, "/media/absent.mkv", time.Minute); err != nil || reads() != 2 {
			t.Errorf("a miss 10 s on = %v after %d reads, want a fresh merge", err, reads())
		}
	})
}

// TestHighWaterMovesAfterMerge: the high-water starts at the build's start
// less a minute and moves to the largest updatedAt merged only once the
// merge commits, so a walk failing on its second page re-reads its items.
func TestHighWaterMovesAfterMerge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		a := inProcess(t, f)
		built := time.Now()
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		seed := built.Add(-time.Minute).Unix()
		if hw := a.highWaterOf("2"); hw != seed {
			t.Fatalf("high-water %d, want the build's start less a minute, %d", hw, seed)
		}
		base := time.Now().Unix() + 10
		for i := range pageSize + 1 {
			f.put("2", row(strconv.Itoa(1000+i), "episode", base, base+int64(i), fmt.Sprintf("/media/tv/Big/E%03d.mkv", i)))
		}
		var updated atomic.Int32
		f.hook = func(r *http.Request) int {
			if r.URL.Query().Get("sort") == "updatedAt:asc" && r.URL.Path == "/library/sections/2/all" && updated.Add(1) == 2 {
				return http.StatusInternalServerError
			}
			return 0
		}
		time.Sleep(deltaReuse)
		if _, err := locate(t, a, "/media/absent.mkv", time.Minute); err == nil {
			t.Fatal("a miss whose merge fails on its second page succeeded")
		}
		if hw := a.highWaterOf("2"); hw != seed {
			t.Errorf("high-water %d after a failed merge, want it kept at %d", hw, seed)
		}
		if keys, _, _ := a.lookup(keyOf("/media/tv/Big/E000.mkv")); len(keys) != 0 {
			t.Errorf("the failed merge's first page reached the index: %v", keys)
		}
		f.hook = nil
		if keys, err := locate(t, a, "/media/tv/Big/E500.mkv", time.Minute); err != nil || !slices.Equal(keys, []string{"1500"}) {
			t.Errorf("locate after recovery = %v %v, want [1500]", keys, err)
		}
		if hw := a.highWaterOf("2"); hw != base+pageSize {
			t.Errorf("high-water %d, want the largest merged updatedAt %d", hw, base+pageSize)
		}
		reread := fmt.Sprintf("GET /library/sections/2/all?type=4&sort=updatedAt:asc&updatedAt>=%d&", seed)
		if n := countContaining(f, reread); n != 2 {
			t.Errorf("%d reads of section 2 from %d, want the failed merge's and its retry's", n, seed)
		}
	})
}

func countContaining(f *fakePlex, s string) int {
	n := 0
	for _, r := range f.log() {
		if strings.Contains(r, s) {
			n++
		}
	}
	return n
}

// TestMergeSpanningRebuildKeepsSeeds: a merge whose snapshot predates a
// rebuild that finished during its walk read older paths from the old
// high-waters, so it is dropped: the rebuild's paths and seeds stay, and
// the next miss merges afresh instead of reusing the dropped merge.
func TestMergeSpanningRebuildKeepsSeeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		a := inProcess(t, f)
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(16 * time.Minute)
		buildGate, pageGate := make(chan struct{}), make(chan struct{})
		var armed atomic.Bool
		var merged atomic.Int32
		hold := func(r *http.Request, gate chan struct{}) {
			select {
			case <-gate:
			case <-r.Context().Done():
			}
		}
		f.hook = func(r *http.Request) int {
			switch {
			case r.URL.Path == "/library/sections":
				hold(r, buildGate)
			case r.URL.Path == "/library/sections/2/all" && r.URL.Query().Get("sort") == "updatedAt:asc" && armed.Load() && merged.Add(1) == 2:
				hold(r, pageGate)
			}
			return 0
		}
		rebuilt := time.Now()
		if _, err := locate(t, a, "/media/absent.mkv", time.Second); !errors.Is(err, errIndexBuilding) {
			t.Fatalf("stale miss = %v, want it waiting on the rebuild", err)
		}
		time.Sleep(deltaReuse)
		base := time.Now().Unix() + 10
		const oldPath, newPath = "/media/tv/Big/E000.mkv", "/media/tv/Big/E000 [2160p].mkv"
		for i := range pageSize + 1 {
			f.put("2", row(strconv.Itoa(1000+i), "episode", base, base+int64(i), fmt.Sprintf("/media/tv/Big/E%03d.mkv", i)))
		}
		armed.Store(true)
		done := make(chan error, 1)
		go func() {
			_, err := locate(t, a, "/media/absent.mkv", time.Hour)
			done <- err
		}()
		synctest.Wait()
		f.put("2", row("1000", "episode", base, base, newPath))
		close(buildGate)
		synctest.Wait()
		close(pageGate)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if keys, _, _ := a.lookup(keyOf(newPath)); !slices.Equal(keys, []string{"1000"}) {
			t.Errorf("the rebuild's path answers %v, want [1000]", keys)
		}
		if keys, _, _ := a.lookup(keyOf(oldPath)); len(keys) != 0 {
			t.Errorf("the dropped merge's older path came back: %v", keys)
		}
		if hw, seed := a.highWaterOf("2"), rebuilt.Add(-time.Minute).Unix(); hw != seed {
			t.Errorf("high-water %d, want the rebuild's seed %d", hw, seed)
		}
		reads := merged.Load()
		if _, err := locate(t, a, "/media/absent.mkv", time.Minute); err != nil || merged.Load() == reads {
			t.Errorf("a miss right after the dropped merge = %v with no new read, want a fresh merge", err)
		}
	})
}
