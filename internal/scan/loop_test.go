package scan_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

func TestCursorReadErrorRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sched := mediasource.Schedule{Change: 10 * time.Second, Whole: 24 * time.Hour}
		r := newRig(t, movieCaps, sched)
		r.put(1, 2)
		held := r.storeCursor(t)
		r.put(3, 3)
		r.db.fault(errors.New("bolt: read failed"), nil)
		r.start(t)
		if ch := r.ad.took("Changes"); len(ch) != 0 {
			t.Errorf("a tick whose cursor read failed checked changes %+v", ch)
		}
		if p := r.ad.took("RootFolders"); len(p) != 0 {
			t.Errorf("a tick whose cursor read failed went on to probe")
		}
		r.db.fault(nil, nil)
		advance(sched.Change)
		ch := r.ad.took("Changes")
		if len(ch) != 1 || !sameBytes(ch[0].cursor, held) {
			t.Errorf("the next tick checked %+v, want one from the stored %x", ch, held)
		}
		if roots := r.ad.took("Root"); len(roots) != 1 || roots[0].root != movieRef(3) {
			t.Errorf("the batch read %+v, want movie 3", roots)
		}
	})
}

func TestQuietLibraryDoesNotLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 3)
		boot := time.Now()
		r.start(t)
		advance(30 * time.Second)
		for range 2 {
			advance(daily.Whole)
		}
		advance(time.Hour)
		if got := gaps(boot, r.ad.took("Items")); !slices.Equal(got, []time.Duration{30 * time.Second, daily.Whole, daily.Whole}) {
			t.Errorf("walks came at gaps %v, want the boot pass and one per day", got)
		}
		if roots := r.ad.took("Root"); len(roots) != 0 {
			t.Errorf("a quiet library read %d roots", len(roots))
		}
		if got := r.stored(t); !sameBytes(got, r.head(t)) {
			t.Errorf("stored cursor %x, want the head", got)
		}
	})
}

func TestMassRenameThenQuietNoWholePassLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 100
		r := newRig(t, movieCaps, daily)
		r.put(1, n)
		r.start(t)
		advance(30 * time.Second)
		r.ad.took("Items")
		read := r.head(t)
		for i := 1; i <= n; i++ {
			lr := movieRoot(i)
			lr.Root.Title = fmt.Sprintf("Renamed %d", i)
			r.lib.Put(lr)
		}
		advance(daily.Change)
		if roots := r.ad.took("Root"); len(roots) != n {
			t.Errorf("the batch read %d roots, want the %d renamed", len(roots), n)
		}
		got := r.listed(t)
		if len(got) != n || got[0] != "Renamed 1" {
			t.Errorf("after the batch %d roots are listed, the first %q; want %d renamed", len(got), got[0], n)
		}
		advance(6 * time.Hour)
		if w := r.ad.took("Items"); len(w) != 0 {
			t.Errorf("%d whole passes followed the rename", len(w))
		}
		if roots := r.ad.took("Root"); len(roots) != 0 {
			t.Errorf("quiet ticks after the rename read %d roots", len(roots))
		}
		if got := r.stored(t); !sameBytes(got, read) {
			t.Errorf("stored cursor %x, want %x, the one the batch read from", got, read)
		}
	})
}

func TestNoChangesSourceDailyOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, mediasource.Capabilities{Kinds: []media.Kind{media.Movie}}, daily)
		r.put(1, 2)
		boot := time.Now()
		r.start(t)
		advance(30 * time.Second)
		r.put(3, 3)
		advance(daily.Whole)
		advance(daily.Whole)
		if got := gaps(boot, r.ad.took("Items")); !slices.Equal(got, []time.Duration{30 * time.Second, daily.Whole, daily.Whole}) {
			t.Errorf("walks came at gaps %v, want the boot pass and one per day", got)
		}
		if ch := r.ad.took("Changes"); len(ch) != 0 {
			t.Errorf("a source without Changes was asked %d times", len(ch))
		}
		if roots := r.ad.took("Root"); len(roots) != 0 {
			t.Errorf("a source without Changes had %d roots read in batches", len(roots))
		}
		if got := r.listed(t); len(got) != 3 {
			t.Errorf("listed %v, want movie 3 from the daily pass", got)
		}
	})
}

func TestNoCursorNoChangeCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sched := mediasource.Schedule{Change: 10 * time.Second, Whole: 24 * time.Hour}
		r := newRig(t, movieCaps, sched)
		r.put(1, 2)
		r.start(t)
		advance(29 * time.Second)
		if ch := r.ad.took("Changes"); len(ch) != 0 {
			t.Errorf("a fresh source checked changes %d times before its first whole pass", len(ch))
		}
		if p := r.ad.took("RootFolders"); len(p) != 3 {
			t.Errorf("%d probes before the first whole pass, want one per tick (3)", len(p))
		}
		advance(time.Second)
		if ch := r.ad.took("Changes"); len(ch) != 1 || len(ch[0].cursor) != 0 {
			t.Errorf("the first whole pass checked %+v, want one head read", ch)
		}
	})
}
