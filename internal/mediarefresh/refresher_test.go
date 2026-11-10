package mediarefresh_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
)

var otherShow = show{native: "91", tvdb: 200, episodes: []int{1}}

// twoSources is a rig over srcA and srcB, each listing a series of its own.
func twoSources(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, srcA, srcB)
	r.list(srcA, firstShow)
	r.list(srcB, otherShow)
	after(settle)
	return r
}

func TestReloadDuringDebounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := twoSources(t)
		r.deliver(r.ep(srcA, 100, 1))
		time.Sleep(2 * time.Second)
		r.replace(srcB)
		r.reload(srcA, srcB)
		after(settle)
		if got := len(r.src(srcA).sent(firstShow.ref())); got != 1 {
			t.Errorf("a write in the debounce window, then another source's edit: %d rescans, want 1", got)
		}
	})
}

func TestReloadDuringBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := twoSources(t)
		r.src(srcA).script(downUntil(time.Now().Add(time.Minute)))
		r.deliver(r.ep(srcA, 100, 1))
		after(settle)
		if got := len(r.src(srcA).sent(firstShow.ref())); got != 1 {
			t.Fatalf("%d rescans before the reload, want the one that failed", got)
		}
		r.replace(srcB)
		r.reload(srcA, srcB)
		after(time.Hour)
		if got := r.src(srcA).succeeded(firstShow.ref()); got != 1 {
			t.Errorf("a write in a backoff, then another source's edit: %d successful rescans, want 1", got)
		}
	})
}

func TestSameIDReplacement(t *testing.T) {
	t.Run("in the debounce window", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := twoSources(t)
			old := r.src(srcA)
			r.deliver(r.ep(srcA, 100, 1))
			time.Sleep(2 * time.Second)
			now := r.replace(srcA)
			r.reload(srcA, srcB)
			after(settle)
			if got, prev := len(now.sent(firstShow.ref())), len(old.sent(firstShow.ref())); got != 1 || prev != 0 {
				t.Errorf("rescans: %d through the replacement, %d through the replaced; want 1 and 0", got, prev)
			}
		})
	})
	t.Run("in a backoff", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := twoSources(t)
			old := r.src(srcA)
			old.script(downUntil(time.Now().Add(24 * time.Hour)))
			r.deliver(r.ep(srcA, 100, 1))
			after(settle)
			now := r.replace(srcA)
			r.reload(srcA, srcB)
			after(time.Hour)
			if got, prev := now.succeeded(firstShow.ref()), len(old.sent(firstShow.ref())); got != 1 || len(now.sent(firstShow.ref())) != 1 || prev != 1 {
				t.Errorf("rescans: %d through the replacement, %d through the replaced; want one success and the one failure", got, prev)
			}
		})
	})
}

func TestFullMovesToHead(t *testing.T) {
	t.Run("at start", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t)
			r.list(srcA, firstShow)
			rec := r.deliver(r.ep(srcA, 100, 1))
			r.reload(srcA)
			after(settle)
			if got := r.src(srcA).refs(); len(got) != 0 {
				t.Errorf("a new sink rescanned %v, written before it started", got)
			}
			r.retime(rec)
			after(settle)
			if got := len(r.src(srcA).sent(firstShow.ref())); got != 1 {
				t.Errorf("a write after the start: %d rescans, want 1", got)
			}
		})
	})
	t.Run("an activation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, srcA)
			r.list(srcA, firstShow)
			after(settle)
			rec := r.deliver(r.ep(srcA, 100, 1))
			r.activate("en", "fr")
			after(settle)
			if got := r.src(srcA).refs(); len(got) != 0 {
				t.Errorf("a batch read Full after an activation rescanned %v", got)
			}
			r.retime(rec)
			after(settle)
			if got := len(r.src(srcA).sent(firstShow.ref())); got != 1 {
				t.Errorf("a write after the activation: %d rescans, want 1", got)
			}
		})
	})
}

func TestReloadStopsAndStarts(t *testing.T) {
	t.Run("a removed source", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := twoSources(t)
			r.reload(srcB)
			r.deliver(r.ep(srcA, 100, 1))
			r.deliver(r.ep(srcB, 200, 1))
			after(settle)
			if got := r.src(srcA).refs(); len(got) != 0 {
				t.Errorf("a removed source rescanned %v", got)
			}
			if w := r.logs.warns(); len(w) != 0 {
				t.Errorf("a removed source's sink still ran: %q", w)
			}
			if got := len(r.src(srcB).sent(otherShow.ref())); got != 1 {
				t.Errorf("the remaining source: %d rescans, want 1", got)
			}
		})
	})
	t.Run("a source that can no longer rescan", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := twoSources(t)
			r.mu.Lock()
			r.serving[srcA] = &source{Fake: sourcetest.NewFake(&sourcetest.Library{}, mediasource.Capabilities{Kinds: []media.Kind{media.Episode}})}
			r.mu.Unlock()
			r.reload(srcA, srcB)
			r.deliver(r.ep(srcA, 100, 1))
			after(settle)
			if got := r.src(srcA).refs(); len(got) != 0 {
				t.Errorf("a source without Rescan was asked for %v", got)
			}
			if w := r.logs.warns(); len(w) != 0 {
				t.Errorf("the sink of a source without Rescan still ran: %q", w)
			}
		})
	})
	t.Run("a new source starts at the head", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := twoSources(t)
			sh := show{native: "55", tvdb: 300, episodes: []int{1}}
			r.list(srcC, sh)
			rec := r.deliver(r.ep(srcC, 300, 1))
			after(settle)
			r.reload(srcA, srcB, srcC)
			after(settle)
			if got := r.src(srcC).refs(); len(got) != 0 {
				t.Errorf("a new source's sink rescanned %v, written before it started", got)
			}
			r.retime(rec)
			after(settle)
			if got := len(r.src(srcC).sent(sh.ref())); got != 1 {
				t.Errorf("the new source after a later write: %d rescans, want 1", got)
			}
		})
	})
}
