package seriesorder

import (
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

func titleOf(ev mediasource.SeriesEvidence, lang string) (mediasource.TitleSet, bool) {
	i := slices.IndexFunc(ev.Titles, func(t mediasource.TitleSet) bool { return t.Language == lang })
	if i < 0 {
		return mediasource.TitleSet{}, false
	}
	return ev.Titles[i], true
}

func sentIn(sent []request, lang string) int {
	n := 0
	for _, s := range sent {
		if s.lang == lang {
			n++
		}
	}
	return n
}

func allOK(joined []mediasource.Joined) bool {
	return !slices.ContainsFunc(joined, func(j mediasource.Joined) bool { return !j.OK })
}

// retitle renames, in lang's aired answer, the episode the DVD order lists
// at index i.
func retitle(r *rig, lang string, i int, title string) {
	id := r.sky.get(firefly, lang, "dvd").Episodes[i].ID
	off := r.sky.get(firefly, lang, airedOrder)
	for k := range off.Episodes {
		if off.Episodes[k].ID == id {
			off.Episodes[k].Title = title
		}
	}
	r.sky.set(firefly, lang, airedOrder, off)
}

func frenchDVD(t *testing.T, r *rig) *mediasource.JoinQuery {
	t.Helper()
	r.sky.record(t, firefly)
	return query("sonarr", firefly, dvdOrder, mustTag(t, "fr"), listed(r.sky.get(firefly, "fra", "dvd"), false))
}

func TestFullFetchTitleAnswerVerified(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	fra := r.sky.get(firefly, "fra", airedOrder)
	fra.Episodes = append(fra.Episodes, ep(9002, 1, 15, "2020-01-01", "Nouvel épisode"))
	r.sky.set(firefly, "fra", airedOrder, fra)
	q := query("sonarr", firefly, airedSeries, mustTag(t, "fr"), listed(r.sky.get(firefly, english, airedOrder), true))
	_, ev := r.mustJoin(t, q)
	if n := sentIn(r.sky.take(), "fra"); n != 1 {
		t.Errorf("%d fra requests, want 1", n)
	}
	if _, ok := titleOf(ev.Bound, "fra"); ok {
		t.Error("the reading holds a fra title set read against another aired list")
	}
	got, ok, err := r.svc.Evidence(t.Context(), q.Source, q.Root)
	if err != nil || !ok {
		t.Fatalf("Evidence = %v, %v", ok, err)
	}
	if _, ok := titleOf(got, "fra"); ok || slices.ContainsFunc(got.Catalogue.Episodes, func(e mediasource.CatalogueEpisode) bool { return e.ID == 9002 }) {
		t.Error("Evidence returns a title or an episode of another aired list")
	}
	r.mustJoin(t, q)
	if n := sentIn(r.sky.take(), "fra"); n != 0 {
		t.Errorf("a barred language was asked again %d times for the same reading", n)
	}
}

func TestCompletionVerifiedAgainstReading(t *testing.T) {
	for _, tc := range []struct {
		name  string
		moved bool
	}{{"same aired list", false}, {"another aired list", true}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig()
			r.sky.record(t, firefly)
			q := query("sonarr", firefly, airedSeries, language.Tag{}, listed(r.sky.get(firefly, english, airedOrder), true))
			r.mustJoin(t, q)
			r.sky.take()
			if tc.moved {
				fra := r.sky.get(firefly, "fra", airedOrder)
				fra.Episodes = fra.Episodes[1:]
				r.sky.set(firefly, "fra", airedOrder, fra)
			}
			r.store.setPolicy(policyOf(t, "fr"))
			_, ev := r.mustJoin(t, q)
			if sent := r.sky.take(); len(sent) != 1 || sent[0] != (request{tvdb: firefly, lang: "fra", order: airedOrder}) {
				t.Errorf("sent %v, want one fra aired request", sent)
			}
			ts, ok := titleOf(ev.Bound, "fra")
			if ok == tc.moved || (ok && (!ts.Found || len(ts.Titles) == 0)) {
				t.Errorf("fra title set %+v present %v, want it added only over the same aired list", ts, ok)
			}
			r.mustJoin(t, q)
			if sent := r.sky.take(); len(sent) != 0 {
				t.Errorf("the next Join sent %v", sent)
			}
		})
	}
}

func TestCompletionErrorArms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sky.record(t, firefly)
		q := query("sonarr", firefly, airedSeries, language.Tag{}, listed(r.sky.get(firefly, english, airedOrder), true))
		r.mustJoin(t, q)
		r.sky.take()
		r.store.setPolicy(policyOf(t, "de", "es"))
		r.sky.failLang("spa", errSkyHookDown)
		_, ev := r.mustJoin(t, q)
		if sent := r.sky.take(); sentIn(sent, "deu") != 1 || sentIn(sent, "spa") != 1 {
			t.Errorf("sent %v, want one deu and one spa request", sent)
		}
		if ts, ok := titleOf(ev.Bound, "deu"); !ok || ts.Found || len(ts.Titles) != 0 {
			t.Errorf("deu title set %+v, %v; want it added with Found false", ts, ok)
		}
		if _, ok := titleOf(ev.Bound, "spa"); ok {
			t.Error("a failed spa request added a title set")
		}
		time.Sleep(10 * time.Minute)
		r.mustJoin(t, q)
		if sent := r.sky.take(); len(sent) != 0 {
			t.Errorf("ten minutes after a failure, sent %v", sent)
		}
		time.Sleep(titleRetryAfter)
		r.mustJoin(t, q)
		if sent := r.sky.take(); len(sent) != 1 || sent[0].lang != "spa" {
			t.Errorf("an hour after a failure, sent %v; want spa alone", sent)
		}
		time.Sleep(freshFor)
		r.mustJoin(t, q)
		// deu is never asked again for the old reading, so its one request is
		// the new reading's.
		if sent := r.sky.take(); fetches(sent) != 1 || sentIn(sent, "deu") != 1 {
			t.Errorf("an age fetch sent %v, want one fetch and deu asked afresh", sent)
		}
		if n := r.warns("did not answer a series' titles"); n != 1 {
			t.Errorf("%d title failure WARNs over one streak, want 1", n)
		}
	})
}

func TestHeldReadingPromotesWithoutRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		q := frenchDVD(t, r)
		_, r1 := r.mustJoin(t, q)
		time.Sleep(8 * 24 * time.Hour)
		r.sky.take()
		retitle(r, "fra", 3, "Sérénité, le pilote")
		joined, ev := r.mustJoin(t, q)
		if n := fetches(r.sky.take()); n != 1 {
			t.Errorf("%d fetches for age, want 1", n)
		}
		if !ev.Bound.Catalogue.Fetched.Equal(r1.Bound.Catalogue.Fetched) || ev.Held == nil || !allOK(joined) {
			t.Fatalf("evidence %+v, want Bound kept, the retitled reading Held, every episode OK", ev)
		}
		r2 := *ev.Held
		r.mustJoin(t, q)
		if sent := r.sky.take(); len(sent) != 0 {
			t.Errorf("a Join within 7 days of the held reading sent %v", sent)
		}
		q.Episodes[3].Title = "Sérénité, le pilote"
		joined, ev = r.mustJoin(t, q)
		if sent := r.sky.take(); len(sent) != 0 {
			t.Errorf("promoting the held reading sent %v", sent)
		}
		if !ev.Bound.Catalogue.Fetched.Equal(r2.Catalogue.Fetched) || ev.Held != nil || !allOK(joined) {
			t.Errorf("evidence %+v, want the held reading Bound, none held, every episode OK", ev)
		}
	})
}

func TestHeldBecomesBoundOnAgeFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		q := frenchDVD(t, r)
		r.mustJoin(t, q)
		time.Sleep(8 * 24 * time.Hour)
		retitle(r, "fra", 3, "Sérénité, le pilote")
		_, ev := r.mustJoin(t, q)
		r2 := *ev.Held
		time.Sleep(8 * 24 * time.Hour)
		q.Episodes[3].Title = "Sérénité, le pilote"
		retitle(r, "fra", 4, "Le train fantôme")
		r.sky.take()
		joined, ev := r.mustJoin(t, q)
		if n := fetches(r.sky.take()); n != 1 {
			t.Errorf("%d fetches for age, want 1", n)
		}
		if !ev.Bound.Catalogue.Fetched.Equal(r2.Catalogue.Fetched) || ev.Held == nil || !ev.Held.Catalogue.Fetched.After(r2.Catalogue.Fetched) || !allOK(joined) {
			t.Errorf("evidence %+v, want Held promoted to Bound and the fetched reading Held", ev)
		}
	})
}

func TestUnchangedListFetchedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sky.record(t, firefly)
		q := dvdQuery(t, r.sky, "sonarr", firefly)
		listedR1 := q.Episodes
		r.mustJoin(t, q)
		r.sky.take()
		time.Sleep(time.Minute)
		swapped := swapPlaces(listedR1, 3, 4)
		q.Episodes = swapped
		step := func(name string, wantFetches int, wantErr bool) {
			t.Helper()
			_, _, err := r.join(t, q)
			if got := errors.Is(err, mediasource.ErrAiredOrderUnavailable); got != wantErr {
				t.Errorf("%s: error %v, want unavailable %v", name, err, wantErr)
			}
			if n := fetches(r.sky.take()); n != wantFetches {
				t.Errorf("%s: %d fetches, want %d", name, n, wantFetches)
			}
		}
		_, _, err := r.join(t, q)
		if err == nil || !strings.Contains(err.Error(), "2 episodes are listed otherwise") {
			t.Errorf("error %v, want 2 episodes named", err)
		}
		if n := fetches(r.sky.take()); n != 1 {
			t.Errorf("%d fetches for a disagreeing list, want 1", n)
		}
		step("the same list", 0, true)
		step("the same list again", 0, true)
		time.Sleep(freshFor)
		step("the same list 7 days on", 1, true)
		step("the same list after the age fetch", 0, true)
		q.Episodes = swapPlaces(swapped, 5, 6)
		step("a changed list", 1, true)
		q.Episodes = listedR1
		step("a list agreeing with Bound", 0, false)
		q.Episodes = swapped
		step("a return to the swapped list", 1, true)

		fresh := newService(r.sky, r.store, nil)
		if _, _, err := fresh.Join(t.Context(), *q); !errors.Is(err, mediasource.ErrAiredOrderUnavailable) {
			t.Errorf("a new Service: error %v", err)
		}
		if n := fetches(r.sky.take()); n != 1 {
			t.Errorf("a new Service: %d fetches, want 1", n)
		}
	})
}

func TestAiredListAgreeingWithNoneFetchedOnce(t *testing.T) {
	r := newRig()
	r.sky.record(t, got)
	q := query("sonarr", got, airedSeries, language.Tag{}, listed(r.sky.get(got, english, airedOrder), true))
	q.Episodes = append(q.Episodes, mediasource.Listed{AirDate: "2030-01-01", Title: "Unknown", Season: 9, Episode: 1})
	_, ev := r.mustJoin(t, q)
	if ev == nil || fetches(r.sky.take()) != 1 {
		t.Fatalf("evidence %v: want the fetched reading kept for search after one fetch", ev)
	}
	joined, _ := r.mustJoin(t, q)
	if n := fetches(r.sky.take()); n != 0 || !allOK(joined) {
		t.Errorf("%d fetches, joined %+v; want none and every episode at its own place", n, joined)
	}
}

func TestTwoSourcesJoinFromOwnEvidence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sky.record(t, firefly)
		a := dvdQuery(t, r.sky, "sonarr-a", firefly)
		_, evA := r.mustJoin(t, a)
		time.Sleep(time.Hour)
		r.sky.set(firefly, english, "dvd", swapShow(r.sky.get(firefly, english, "dvd"), 3, 4))
		b := query("sonarr-b", firefly, dvdOrder, language.Tag{}, listed(r.sky.get(firefly, english, "dvd"), false))
		_, evB := r.mustJoin(t, b)
		r.sky.take()
		for _, q := range []*mediasource.JoinQuery{a, b} {
			joined, _ := r.mustJoin(t, q)
			if !allOK(joined) {
				t.Errorf("%s joined %+v, want every episode OK", q.Source, joined)
			}
		}
		if sent := r.sky.take(); len(sent) != 0 {
			t.Errorf("both sources joining from their own evidence sent %v", sent)
		}
		time.Sleep(time.Hour)
		a.Episodes = b.Episodes
		_, evA2 := r.mustJoin(t, a)
		if n := fetches(r.sky.take()); n != 1 || !evA2.Bound.Catalogue.Fetched.After(evA.Bound.Catalogue.Fetched) {
			t.Errorf("%d fetches, A bound at %v; want one fetch binding a new reading", n, evA2.Bound.Catalogue.Fetched)
		}
		if got, _ := r.store.stored(t, b); !got.Bound.Catalogue.Fetched.Equal(evB.Bound.Catalogue.Fetched) {
			t.Error("A's fetch changed B's evidence")
		}
		r.mustJoin(t, b)
		if sent := r.sky.take(); len(sent) != 0 {
			t.Errorf("B sent %v after A's fetch", sent)
		}
	})
}

// TestFetchFailsOnMissingOrder: an order SkyHook lists but answers 404 for
// fails the fetch, as a 404 for the series does.
func TestFetchFailsOnMissingOrder(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	off := r.sky.get(firefly, english, airedOrder)
	off.Orders = append(off.Orders, skyhook.Order{Type: "alternate", Name: "Story Order"})
	r.sky.set(firefly, english, airedOrder, off)
	err := r.unread(t, dvdQuery(t, r.sky, "sonarr", firefly))
	if !strings.Contains(err.Error(), "order alternate") {
		t.Errorf("error %q, want the missing order named", err)
	}
}
