package seriesorder

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/skyhook"
)

// TestMovedCounts pins DECISIONS 40's measured counts: Firefly's DVD order
// moves 14 regular episodes, Futurama's 138, Game of Thrones' none of 73.
func TestMovedCounts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tvdb      int
		moved     int
		reordered bool
	}{
		{"firefly", firefly, 14, true},
		{"futurama", futurama, 138, true},
		{"game of thrones", got, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig()
			r.sky.record(t, tc.tvdb)
			official := r.sky.get(tc.tvdb, english, airedOrder)
			official.Orders = append(official.Orders, skyhook.Order{Type: absoluteOrder, Name: "Absolute Order"})
			r.sky.set(tc.tvdb, english, airedOrder, official)
			q := dvdQuery(t, r.sky, "sonarr", tc.tvdb)
			joined, ev := r.mustJoin(t, q)
			moved := 0
			for i, j := range joined {
				l := q.Episodes[i]
				if j.OK && l.Season > 0 && j.Season > 0 && (j.Season != l.Season || j.Episode != l.Episode) {
					moved++
				}
			}
			if moved != tc.moved {
				t.Errorf("the DVD order moves %d of %d listed episodes, want %d", moved, len(joined), tc.moved)
			}
			if got := ev.Bound.Catalogue.Reordered(); got != tc.reordered {
				t.Errorf("Reordered() = %v, want %v", got, tc.reordered)
			}
			for _, s := range r.sky.take() {
				if s.lang != english || s.order == absoluteOrder {
					t.Errorf("sent %v: every order is asked in eng, absolute never", s)
				}
			}
		})
	}
}

func TestJoinFireflyDVD(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	q := dvdQuery(t, r.sky, "sonarr", firefly)
	joined, _ := r.mustJoin(t, q)
	i := slices.IndexFunc(q.Episodes, func(l mediasource.Listed) bool { return l.Season == 1 && l.Episode == 1 })
	if i < 0 || q.Episodes[i].Title != "Serenity" {
		t.Fatalf("the recorded DVD order lists no S01E01 Serenity: %v", q.Episodes)
	}
	if want := (mediasource.Joined{Season: 1, Episode: 11, Absolute: 1, OK: true}); joined[i] != want {
		t.Errorf("DVD S01E01 joined to %+v, want %+v", joined[i], want)
	}
}

// office is a DVD order holding an episode the aired list lacks (S01E03),
// and one place two aired episodes share (S02E01), as Friends' does.
func office(r *rig) *mediasource.JoinQuery {
	const tvdb = 73244
	aired := []skyhook.Episode{
		ep(1, 1, 1, "2005-03-24", "Pilot"), ep(2, 1, 2, "2005-03-29", "Diversity Day"),
		ep(3, 1, 3, "2005-04-05", "Health Care"), ep(4, 2, 1, "2005-09-20", "The Dundies"),
		ep(5, 2, 2, "2005-09-27", "Sexual Harassment"),
	}
	dvd := []skyhook.Episode{
		ep(1, 1, 1, "2005-03-24", "Pilot"), ep(2, 1, 2, "2005-03-29", "Diversity Day"),
		ep(900, 1, 3, "2005-04-01", "Deleted Scenes"), ep(3, 1, 4, "2005-04-05", "Health Care"),
		ep(4, 2, 1, "2005-09-20", "The Dundies"), ep(5, 2, 1, "2005-09-27", "Sexual Harassment"),
	}
	orders := []skyhook.Order{{Type: airedOrder}, {Type: "dvd", Name: "DVD Order"}}
	r.sky.set(tvdb, english, airedOrder, skyhook.Show{Orders: orders, Episodes: aired})
	r.sky.set(tvdb, english, "dvd", skyhook.Show{Orders: orders, Episodes: dvd})
	return query("sonarr", tvdb, dvdOrder, language.Tag{}, listed(skyhook.Show{Episodes: dvd[:5]}, false))
}

func TestJoinUnplacedEpisodeNotOK(t *testing.T) {
	r := newRig()
	q := office(r)
	joined, ev := r.mustJoin(t, q)
	want := []mediasource.Joined{
		{Season: 1, Episode: 1, OK: true},
		{Season: 1, Episode: 2, OK: true},
		{},
		{Season: 1, Episode: 3, OK: true},
		{},
	}
	if !slices.Equal(joined, want) {
		t.Errorf("joined %+v, want %+v", joined, want)
	}
	if ev == nil || len(ev.Bound.Catalogue.Orders) != 1 || len(ev.Bound.Catalogue.Orders[0].Extra) != 1 {
		t.Fatalf("evidence %+v, want one order whose Extra holds the deleted scenes", ev)
	}
	if n := r.warns("stay unidentified"); n != 1 || !strings.Contains(r.logs.String(), "episodes=2") {
		t.Errorf("logged %d unplaced WARNs (%s), want one naming 2 episodes", n, r.logs)
	}
}

func TestJoinOrderSecondPlaceToExtra(t *testing.T) {
	aired := airedEpisodes([]skyhook.Episode{ep(1, 1, 1, "2001-01-01", "A"), ep(2, 1, 2, "2001-01-08", "B")})
	o := joinOrder(skyhook.Order{Type: "dvd"}, aired, []skyhook.Episode{
		ep(2, 1, 1, "2001-01-08", "B"), ep(1, 1, 2, "2001-01-01", "A"), ep(1, 2, 1, "2001-01-01", "A"), ep(7, 1, 3, "", "Extra"),
	})
	wantPlaces := []mediasource.Place{{Season: 1, Episode: 2, Listed: true}, {Season: 1, Episode: 1, Listed: true}}
	wantExtra := []mediasource.ExtraPlace{{Season: 1, Episode: 3}, {AirDate: "2001-01-01", Season: 2, Episode: 1}}
	if !slices.Equal(o.Places, wantPlaces) || !slices.Equal(o.Extra, wantExtra) {
		t.Errorf("places %+v extra %+v, want %+v and %+v", o.Places, o.Extra, wantPlaces, wantExtra)
	}
}

// noDates is a series SkyHook dates nowhere, which Sonarr dates with its
// first-aired date on every episode.
func noDates(r *rig) *mediasource.JoinQuery {
	const tvdb = 5000
	aired := []skyhook.Episode{ep(51, 1, 1, "", "One"), ep(52, 1, 2, "", "Two")}
	dvd := []skyhook.Episode{ep(52, 1, 1, "", "Two"), ep(51, 1, 2, "", "One")}
	orders := []skyhook.Order{{Type: "dvd"}}
	r.sky.set(tvdb, english, airedOrder, skyhook.Show{Orders: orders, Episodes: aired})
	r.sky.set(tvdb, english, "dvd", skyhook.Show{Orders: orders, Episodes: dvd})
	eps := listed(skyhook.Show{Episodes: dvd}, false)
	for i := range eps {
		eps[i].AirDate = "1999-03-28"
	}
	return query("sonarr", tvdb, dvdOrder, language.Tag{}, eps)
}

func TestJoinCurrentMakesNoRequest(t *testing.T) {
	for _, tc := range []struct {
		build func(*testing.T, *rig) *mediasource.JoinQuery
		name  string
	}{
		{name: "firefly dvd", build: func(t *testing.T, r *rig) *mediasource.JoinQuery {
			r.sky.record(t, firefly)
			return dvdQuery(t, r.sky, "sonarr", firefly)
		}},
		{name: "french titles with TBA", build: func(t *testing.T, r *rig) *mediasource.JoinQuery {
			r.sky.record(t, firefly)
			return query("sonarr", firefly, dvdOrder, mustTag(t, "fr"), listed(r.sky.get(firefly, "fra", "dvd"), false))
		}},
		{name: "no air date anywhere", build: func(_ *testing.T, r *rig) *mediasource.JoinQuery { return noDates(r) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig()
			q := tc.build(t, r)
			first, _ := r.mustJoin(t, q)
			if fetches(r.sky.take()) != 1 {
				t.Fatal("the first Join did not fetch once")
			}
			second, _ := r.mustJoin(t, q)
			if sent := r.sky.take(); len(sent) != 0 {
				t.Errorf("a Join over current evidence sent %v", sent)
			}
			if !slices.Equal(first, second) || slices.ContainsFunc(second, func(j mediasource.Joined) bool { return !j.OK }) {
				t.Errorf("joined %+v then %+v, want the same answer, every episode OK", first, second)
			}
		})
	}
}

func TestJoinRefetchesWhenSonarrDisagrees(t *testing.T) {
	for _, tc := range []struct {
		// change moves SkyHook and the listed episodes together, as Sonarr's
		// refresh of a TVDB edit does; it returns the new list.
		change func(*rig, []mediasource.Listed) []mediasource.Listed
		name   string
	}{
		{name: "a new episode", change: func(r *rig, eps []mediasource.Listed) []mediasource.Listed {
			off, dvd := r.sky.get(firefly, english, airedOrder), r.sky.get(firefly, english, "dvd")
			off.Episodes = append(off.Episodes, ep(9001, 1, 15, "2020-01-01", "Reunion"))
			dvd.Episodes = append(dvd.Episodes, ep(9001, 1, 15, "2020-01-01", "Reunion"))
			r.sky.set(firefly, english, airedOrder, off)
			r.sky.set(firefly, english, "dvd", dvd)
			return append(slices.Clone(eps), mediasource.Listed{AirDate: "2020-01-01", Title: "Reunion", Season: 1, Episode: 15})
		}},
		{name: "a moved air date", change: func(r *rig, eps []mediasource.Listed) []mediasource.Listed {
			off, dvd := r.sky.get(firefly, english, airedOrder), r.sky.get(firefly, english, "dvd")
			id := dvd.Episodes[3].ID
			for i := range off.Episodes {
				if off.Episodes[i].ID == id {
					off.Episodes[i].AirDate = "2002-12-21"
				}
			}
			dvd.Episodes[3].AirDate = "2002-12-21"
			r.sky.set(firefly, english, airedOrder, off)
			r.sky.set(firefly, english, "dvd", dvd)
			out := slices.Clone(eps)
			out[3].AirDate = "2002-12-21"
			return out
		}},
		{name: "a moved place", change: func(r *rig, eps []mediasource.Listed) []mediasource.Listed {
			r.sky.set(firefly, english, "dvd", swapShow(r.sky.get(firefly, english, "dvd"), 3, 4))
			return swapPlaces(eps, 3, 4)
		}},
		{name: "a retitle", change: func(r *rig, eps []mediasource.Listed) []mediasource.Listed {
			off := r.sky.get(firefly, english, airedOrder)
			id := r.sky.get(firefly, english, "dvd").Episodes[3].ID
			for i := range off.Episodes {
				if off.Episodes[i].ID == id {
					off.Episodes[i].Title = "Serenity (Pilot)"
				}
			}
			r.sky.set(firefly, english, airedOrder, off)
			out := slices.Clone(eps)
			out[3].Title = "Serenity (Pilot)"
			return out
		}},
		{name: "seven days", change: func(_ *rig, eps []mediasource.Listed) []mediasource.Listed {
			time.Sleep(freshFor)
			return eps
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRig()
				r.sky.record(t, firefly)
				q := dvdQuery(t, r.sky, "sonarr", firefly)
				_, first := r.mustJoin(t, q)
				r.sky.take()
				time.Sleep(time.Minute)
				q.Episodes = tc.change(r, q.Episodes)
				joined, ev := r.mustJoin(t, q)
				if n := fetches(r.sky.take()); n != 1 {
					t.Errorf("%d fetches, want 1", n)
				}
				if !ev.Bound.Catalogue.Fetched.After(first.Bound.Catalogue.Fetched) || ev.Held != nil {
					t.Errorf("evidence %v / held %v, want the new reading Bound and none held", ev.Bound.Catalogue.Fetched, ev.Held)
				}
				if slices.ContainsFunc(joined, func(j mediasource.Joined) bool { return !j.OK }) {
					t.Errorf("joined %+v, want every episode OK", joined)
				}
			})
		})
	}
}

func TestJoinSwapAfterFailedRefreshUnavailable(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	q := dvdQuery(t, r.sky, "sonarr", firefly)
	_, before := r.mustJoin(t, q)
	i := slices.IndexFunc(q.Episodes, func(l mediasource.Listed) bool { return l.Season == 1 && l.Episode == 1 })
	q.Episodes = swapPlaces(q.Episodes, i, i+1)
	r.sky.fail(firefly, errSkyHookDown)
	err := r.unread(t, q)
	if !strings.Contains(err.Error(), "did not answer") || !strings.Contains(err.Error(), errSkyHookDown.Error()) {
		t.Errorf("error %q, want SkyHook's failure named", err)
	}
	if after, _ := r.store.stored(t, q); !after.Bound.Catalogue.Fetched.Equal(before.Bound.Catalogue.Fetched) {
		t.Error("an unread root changed its stored evidence")
	}
}

func TestJoinAgeOnlyFailureKeepsStored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sky.record(t, firefly)
		q := dvdQuery(t, r.sky, "sonarr", firefly)
		want, stored := r.mustJoin(t, q)
		time.Sleep(8 * 24 * time.Hour)
		r.sky.fail(firefly, errSkyHookDown)
		for range 2 {
			joined, ev := r.mustJoin(t, q)
			if !slices.Equal(joined, want) || ev.Held != nil || !ev.Bound.Catalogue.Fetched.Equal(stored.Bound.Catalogue.Fetched) {
				t.Errorf("joined %+v from %v, want the stored reading's join", joined, ev.Bound.Catalogue.Fetched)
			}
		}
		warns := r.warnsOf(firefly)
		if len(warns) != 1 {
			t.Fatalf("failure WARNs over one streak %q, want 1", warns)
		}
		wantErr := `err="SkyHook (skyhook.sonarr.tv) did not answer: ` + errSkyHookDown.Error() + `; it is asked again at the next pass"`
		if w := warns[0]; !strings.Contains(w, wantErr) {
			t.Errorf("WARN %q, want %s", w, wantErr)
		}
	})
}

func TestJoinFailureWithoutStoredUnavailable(t *testing.T) {
	t.Run("nothing kept", func(t *testing.T) {
		r := newRig()
		r.sky.record(t, firefly)
		q := dvdQuery(t, r.sky, "sonarr", firefly)
		r.sky.fail(firefly, errSkyHookDown)
		r.unread(t, q)
		if _, ok := r.store.stored(t, q); ok {
			t.Error("an unread root stored evidence")
		}
	})
	t.Run("french reading lacking fra", func(t *testing.T) {
		r := newRig()
		r.sky.record(t, firefly)
		r.sky.failLang("fra", errSkyHookDown)
		q := query("sonarr", firefly, dvdOrder, mustTag(t, "fr"), listed(r.sky.get(firefly, "fra", "dvd"), false))
		r.unread(t, q)
		r.unread(t, q)
	})
}

func TestJoinAiredOrderFailureAnswersOwnPlaces(t *testing.T) {
	r := newRig()
	r.sky.record(t, got)
	r.sky.fail(got, errSkyHookDown)
	q := query("sonarr", got, airedSeries, language.Tag{}, listed(r.sky.get(got, english, airedOrder), true))
	joined, ev := r.mustJoin(t, q)
	if ev != nil {
		t.Errorf("evidence %v, want none while SkyHook never answered", ev)
	}
	for i, j := range joined {
		l := q.Episodes[i]
		if want := (mediasource.Joined{Season: l.Season, Episode: l.Episode, Absolute: l.Absolute, OK: true}); j != want {
			t.Fatalf("episode %d joined %+v, want its own place %+v", i, j, want)
		}
	}
}

// hungReason is design 4.4's reason for a Join whose SkyHook requests
// outlived the join's 30 s budget, and for each Join barred after it.
const hungReason = "SkyHook (skyhook.sonarr.tv) did not answer within 30 seconds; it is asked again 10 minutes later"

func TestJoinHungSkyHookBounded(t *testing.T) {
	t.Run("another order is unread at the budget", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig()
			r.sky.record(t, firefly)
			r.sky.hang(firefly, "", true)
			q := dvdQuery(t, r.sky, "sonarr", firefly)
			start := time.Now()
			err := r.unread(t, q)
			if took := time.Since(start); took != 30*time.Second {
				t.Errorf("Join took %v, want the 30 s budget", took)
			}
			if !strings.Contains(err.Error(), hungReason) {
				t.Errorf("error %q, want %q", err, hungReason)
			}
		})
	})
	t.Run("the aired order answers own places at the budget", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig()
			r.sky.record(t, got)
			r.sky.hang(got, "", true)
			q := query("sonarr", got, airedSeries, language.Tag{}, listed(r.sky.get(got, english, airedOrder), true))
			start := time.Now()
			joined, ev := r.mustJoin(t, q)
			if took := time.Since(start); took != 30*time.Second {
				t.Errorf("Join took %v, want the 30 s budget", took)
			}
			if ev != nil || !allOK(joined) || joined[0].Season != q.Episodes[0].Season || joined[0].Episode != q.Episodes[0].Episode {
				t.Errorf("joined %+v with evidence %v, want own places and none", joined[0], ev)
			}
		})
	})
	t.Run("the caller's earlier deadline is a failed fetch", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig()
			r.sky.record(t, firefly)
			r.sky.hang(firefly, "", true)
			q := dvdQuery(t, r.sky, "sonarr", firefly)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			start := time.Now()
			_, _, err := r.svc.Join(ctx, *q)
			if took := time.Since(start); took != 10*time.Second || !errors.Is(err, mediasource.ErrAiredOrderUnavailable) {
				t.Fatalf("Join answered %v after %v, want ErrAiredOrderUnavailable at the caller's 10 s", err, took)
			}
			if strings.Contains(err.Error(), hungReason) {
				t.Errorf("error %q blames the join's budget for the caller's deadline", err)
			}
			r.sky.hang(firefly, "", false)
			r.sky.take()
			r.mustJoin(t, q)
			if n := fetches(r.sky.take()); n != 1 {
				t.Errorf("the next Join made %d fetches, want 1: the caller's deadline bars nothing", n)
			}
		})
	})
	t.Run("a fetched reading outlives its titles' budget", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig()
			r.sky.record(t, firefly)
			r.store.setPolicy(policyOf(t, "fr"))
			r.sky.hang(firefly, "fra", true)
			q := dvdQuery(t, r.sky, "sonarr", firefly)
			_, ev := r.mustJoin(t, q)
			if _, ok := titleOf(ev.Bound, "fra"); ok || fetches(r.sky.take()) != 1 {
				t.Fatalf("evidence %+v, want the fetched reading without fra", ev.Bound.Titles)
			}
			r.sky.hang(firefly, "fra", false)
			time.Sleep(10 * time.Minute)
			_, ev = r.mustJoin(t, q)
			if sent := r.sky.take(); len(sent) != 1 || sent[0].lang != "fra" {
				t.Errorf("after the bar sent %v, want the reading's fra alone", sent)
			}
			if _, ok := titleOf(ev.Bound, "fra"); !ok {
				t.Error("the kept reading did not gain fra once SkyHook answered")
			}
		})
	})
}

func TestHungSkyHookBarred(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sky.record(t, firefly)
		r.sky.record(t, futurama)
		r.sky.record(t, got)
		r.sky.hang(firefly, "", true)
		r.unread(t, dvdQuery(t, r.sky, "sonarr", firefly))
		r.sky.take()
		other := dvdQuery(t, r.sky, "sonarr", futurama)
		aired := query("sonarr", got, airedSeries, language.Tag{}, listed(r.sky.get(got, english, airedOrder), true))
		start := time.Now()
		err := r.unread(t, other)
		if joined, ev := r.mustJoin(t, aired); ev != nil || !allOK(joined) {
			t.Errorf("an aired root under the bar joined %v with evidence %v, want own places", len(joined), ev)
		}
		if took, sent := time.Since(start), r.sky.take(); took != 0 || len(sent) != 0 {
			t.Errorf("under the bar two Joins took %v and sent %v, want nothing asked", took, sent)
		}
		if !strings.Contains(err.Error(), hungReason) {
			t.Errorf("error %q, want %q", err, hungReason)
		}
		warns := r.warnsOf(futurama)
		if len(warns) != 1 {
			t.Fatalf("WARNs of the barred root %q, want 1", warns)
		}
		if w := warns[0]; strings.Contains(w, "next pass") || strings.Contains(w, "did not answer for a series") || !strings.Contains(w, `err="`+hungReason) {
			t.Errorf("WARN %q, want no claim SkyHook was asked and the 10-minute retry in err", w)
		}
		time.Sleep(10 * time.Minute)
		if _, ev := r.mustJoin(t, other); ev == nil || fetches(r.sky.take()) != 1 {
			t.Error("10 minutes after the hang a Join did not fetch")
		}
	})
}

func TestUnreadRootAnswersNoEvidence(t *testing.T) {
	t.Run("a disagreement after a fetch", func(t *testing.T) {
		r := newRig()
		r.sky.record(t, firefly)
		q := dvdQuery(t, r.sky, "sonarr", firefly)
		q.Episodes = append(q.Episodes, mediasource.Listed{AirDate: "2030-01-01", Title: "Unknown", Season: 9, Episode: 9})
		err := r.unread(t, q)
		if !strings.Contains(err.Error(), "1 episode is listed otherwise") {
			t.Errorf("error %q, want the one disagreeing episode counted", err)
		}
		if n := r.warns("listed otherwise than SkyHook lists it"); n != 1 {
			t.Errorf("%d disagreement WARNs, want 1", n)
		}
		r.unread(t, q)
		if n := r.warns("listed otherwise than SkyHook lists it"); n != 1 {
			t.Errorf("%d disagreement WARNs over one streak, want 1", n)
		}
	})
	t.Run("an outage with nothing agreeing", func(t *testing.T) {
		r := newRig()
		r.sky.record(t, firefly)
		q := dvdQuery(t, r.sky, "sonarr", firefly)
		r.mustJoin(t, q)
		q.Episodes = swapPlaces(q.Episodes, 3, 4)
		r.sky.fail(firefly, errSkyHookDown)
		r.unread(t, q)
	})
}
