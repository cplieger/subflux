package statedb

import (
	"reflect"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

const fireflyTVDB = 78874

var fireflyFetched = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

// fireflyReading is a SkyHook reading of Firefly's first three aired
// episodes under a DVD order that swaps the first two, fetched at fetched.
func fireflyReading(fetched time.Time) mediasource.SeriesEvidence {
	return mediasource.SeriesEvidence{
		Catalogue: mediasource.Catalogue{
			Fetched: fetched, TVDB: fireflyTVDB,
			Episodes: []mediasource.CatalogueEpisode{
				{ID: 297989, Season: 1, Episode: 1, Absolute: 1, AirDate: "2002-09-20"},
				{ID: 297990, Season: 1, Episode: 2, Absolute: 2, AirDate: "2002-09-27"},
				{ID: 297991, Season: 1, Episode: 3, Absolute: 3},
			},
			Orders: []mediasource.CatalogueOrder{{
				Type: "dvd", Name: "DVD",
				Places: []mediasource.Place{{Season: 1, Episode: 2, Listed: true}, {Season: 1, Episode: 1, Listed: true}, {Season: 1, Episode: 3, Listed: true}},
				Extra:  []mediasource.ExtraPlace{{Season: 1, Episode: 15, AirDate: "2003-06-23"}},
			}},
		},
		Titles: []mediasource.TitleSet{
			{Language: "eng", Found: true, Titles: []mediasource.EpisodeTitle{{ID: 297989, Title: "The Train Job"}, {ID: 297991, Title: "Our Mrs. Reynolds"}}},
			{Language: "fra", Found: true, Titles: []mediasource.EpisodeTitle{{ID: 297990, Title: "Le Train fantôme"}}},
		},
	}
}

func fireflyEvidence() *mediasource.RootEvidence {
	held := fireflyReading(fireflyFetched.Add(7 * 24 * time.Hour))
	return &mediasource.RootEvidence{Bound: fireflyReading(fireflyFetched), Held: &held}
}

// firefly is Firefly's root under its DVD order, carrying ev.
func firefly(t *testing.T, id string, ev *mediasource.RootEvidence) mediasource.Root {
	t.Helper()
	r := series(id, fireflyTVDB)
	r.Title, r.Evidence = "Firefly", ev
	r.Order = mediasource.Order{Type: "dvd", Name: "DVD"}
	r.Language = mustTag(t, "en")
	return r
}

func fireflyEpisodes() []mediasource.Item {
	e1 := episode("f1", 1, 1, "/tv/Firefly/S01E02.mkv")
	e1.SeriesNumbering = media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 2}
	e1.AirDate = "2002-09-20"
	e2 := episode("f2", 1, 2, "/tv/Firefly/S01E01.mkv")
	e2.SeriesNumbering = media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}
	e2.AirDate = "2002-09-27"
	return []mediasource.Item{e1, e2}
}

func evidenceRows(t *testing.T, s *Store) map[rootRef]evidenceRow {
	t.Helper()
	rows := map[rootRef]evidenceRow{}
	if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) error {
		return s.t.rootEvidence.Scan(r, bolttable.Range{}, func(k rootRef, v *evidenceRow) (bool, error) {
			rows[k] = *v
			return true, nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return rows
}

// checkEvidenceHasRoot fails for a root_evidence row with no root record
// at its source and root ref, or one naming another TVDB series.
func checkEvidenceHasRoot(t *testing.T, s *Store, step string) {
	t.Helper()
	for rr, ev := range evidenceRows(t, s) {
		root, found := getRow(t, s, s.t.roots, rr)
		switch {
		case !found:
			t.Errorf("%s: %s's root %s has an evidence row and no root record", step, rr.Source, rr.Root.ID)
		case root.External.TVDB != ev.Bound.Catalogue.TVDB:
			t.Errorf("%s: %s's root %s names TVDB %d, its evidence %d", step, rr.Source, rr.Root.ID, root.External.TVDB, ev.Bound.Catalogue.TVDB)
		}
	}
}

func storedEvidence(t *testing.T, s *Store, src media.SourceID, id string) (mediasource.RootEvidence, bool) {
	t.Helper()
	ev, found, err := s.RootEvidence(t.Context(), src, mediasource.NativeRef{Kind: media.Episode, ID: mediasource.NativeID(id)})
	if err != nil {
		t.Fatalf("RootEvidence(%s, %s): %v", src, id, err)
	}
	return ev, found
}

func TestEvidenceOnlyWithRootRecord(t *testing.T) {
	s, _ := newLibrary(t, "sonarr", "radarr")
	ff := firefly(t, "ff", fireflyEvidence())
	eps := fireflyEpisodes()

	observe(t, s, "sonarr", wholePass(t, s, "sonarr"), ff, eps...)
	checkEvidenceHasRoot(t, s, "observed")
	if _, found := storedEvidence(t, s, "sonarr", "ff"); !found {
		t.Fatal("the observed evidence was not stored")
	}

	passOver(t, s, "sonarr")
	checkEvidenceHasRoot(t, s, "a whole pass missed the root")

	observe(t, s, "sonarr", wholePass(t, s, "sonarr"), ff, eps...)
	checkEvidenceHasRoot(t, s, "observed again")

	activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: policyOf(t, "en")})
	checkEvidenceHasRoot(t, s, "its source removed")

	observe(t, s, "radarr", wholePass(t, s, "radarr"), firefly(t, "ff-b", fireflyEvidence()))
	checkEvidenceHasRoot(t, s, "a second source's root of the series")
	if n := len(evidenceRows(t, s)); n != 1 {
		t.Errorf("%d evidence rows, want the second source's one", n)
	}
}

func TestEvidenceWrittenWithKeys(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	want := fireflyEvidence()
	ff := firefly(t, "ff", want)
	eps := fireflyEpisodes()
	p := wholePass(t, s, "sonarr")

	since := headRev(t, s)
	observe(t, s, "sonarr", p, ff, eps...)
	d, err := s.Since(t.Context(), s.db.Epoch(), since)
	if err != nil {
		t.Fatal(err)
	}
	k1 := epKey(t, fireflyTVDB, 1, 1)
	if _, recorded := getRow(t, s, s.t.items, k1); !recorded || len(d.Roots) == 0 {
		t.Fatalf("the observation's keys reached no commit: recorded %v, roots %v", recorded, d.Roots)
	}
	got, found := storedEvidence(t, s, "sonarr", "ff")
	if !found || !reflect.DeepEqual(got, *want) {
		t.Errorf("RootEvidence = %+v %v, want %+v", got, found, *want)
	}

	head := headRev(t, s)
	changed := s.Changed()
	observe(t, s, "sonarr", p, ff, eps...)
	if rev := headRev(t, s); rev != head {
		t.Errorf("a byte-equal observation moved Rev from %d to %d", head, rev)
	}
	select {
	case <-changed:
		t.Error("a byte-equal observation closed the Changed channel")
	default:
	}

	rr := mediasource.NativeRef{Kind: media.Episode, ID: "ff"}
	root, found, err := s.Root(t.Context(), k1.Root())
	if err != nil || !found || root.Root.Evidence != nil {
		t.Errorf("Root = %v %v, Evidence %v; want the root with no evidence", found, err, root.Root.Evidence)
	}

	refused := map[string]mediasource.Root{
		"evidence its Validate refuses": func() mediasource.Root {
			ev := fireflyEvidence()
			ev.Held.Catalogue.Fetched = fireflyFetched
			return firefly(t, "ff", ev)
		}(),
		"evidence of another series": func() mediasource.Root {
			r := firefly(t, "ff", fireflyEvidence())
			r.External.TVDB = 100
			r.Evidence.Bound.Catalogue.TVDB, r.Evidence.Held.Catalogue.TVDB = 200, 200
			return r
		}(),
	}
	for name, r := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			before := headRev(t, s)
			if err := s.ObserveItems(t.Context(), "sonarr", p, r, nil); err == nil {
				t.Fatal("ObserveItems succeeded")
			}
			if got, _ := storedEvidence(t, s, "sonarr", "ff"); !reflect.DeepEqual(got, *want) || headRev(t, s) != before {
				t.Error("a refused observation wrote")
			}
			if row, _ := getRow(t, s, s.t.roots, rootRef{Source: "sonarr", Root: rr}); row.External.TVDB != fireflyTVDB {
				t.Errorf("a refused observation rewrote the root record to TVDB %d", row.External.TVDB)
			}
		})
	}

	observe(t, s, "sonarr", p, firefly(t, "ff", nil), eps...)
	if _, found := storedEvidence(t, s, "sonarr", "ff"); found {
		t.Error("an observation carrying no evidence kept the stored row")
	}
}

func TestEvidenceDeletedWithRoot(t *testing.T) {
	ff, eps := func(t *testing.T) mediasource.Root { return firefly(t, "ff", fireflyEvidence()) }, fireflyEpisodes()

	t.Run("a whole pass retiring the root", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr", "radarr")
		observe(t, s, "sonarr", wholePass(t, s, "sonarr"), ff(t), eps...)
		observe(t, s, "radarr", wholePass(t, s, "radarr"), firefly(t, "ff-b", fireflyEvidence()))
		passOver(t, s, "sonarr")
		if _, found := storedEvidence(t, s, "sonarr", "ff"); found {
			t.Error("the retired root's evidence survived the pass")
		}
		if _, found := storedEvidence(t, s, "radarr", "ff-b"); !found {
			t.Error("the other source's evidence went with it")
		}
	})

	t.Run("Activate removing the source", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr", "radarr")
		observe(t, s, "sonarr", wholePass(t, s, "sonarr"), ff(t), eps...)
		observe(t, s, "radarr", wholePass(t, s, "radarr"), firefly(t, "ff-b", fireflyEvidence()))
		activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: policyOf(t, "en")})
		if _, found := storedEvidence(t, s, "sonarr", "ff"); found {
			t.Error("the removed source's evidence survived Activate")
		}
		if _, found := storedEvidence(t, s, "radarr", "ff-b"); !found {
			t.Error("the other source's evidence went with it")
		}
	})

	t.Run("a later observation under another series", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr", "radarr")
		p := wholePass(t, s, "sonarr")
		observe(t, s, "sonarr", p, ff(t), eps...)
		observe(t, s, "radarr", wholePass(t, s, "radarr"), firefly(t, "ff-b", fireflyEvidence()))
		renamed := firefly(t, "ff", nil)
		renamed.External.TVDB = 200
		observe(t, s, "sonarr", p, renamed)
		if _, found := storedEvidence(t, s, "sonarr", "ff"); found {
			t.Error("the old series' evidence survived the root's new TVDB id")
		}
		if _, found := storedEvidence(t, s, "radarr", "ff-b"); !found {
			t.Error("the other source's evidence went with it")
		}
	})
}

func TestRootOrderRoundTrip(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	ff, eps := firefly(t, "ff", fireflyEvidence()), fireflyEpisodes()
	observe(t, s, "sonarr", wholePass(t, s, "sonarr"), ff, eps...)
	k := epKey(t, fireflyTVDB, 1, 1)

	root, found, err := s.Root(t.Context(), k.Root())
	if err != nil || !found {
		t.Fatalf("Root = %v %v", found, err)
	}
	if root.Root.Order != ff.Order || root.Root.Language != ff.Language {
		t.Errorf("Root order %+v language %s; want %+v %s", root.Root.Order, root.Root.Language, ff.Order, ff.Language)
	}
	row, found, err := s.ItemRow(t.Context(), k)
	if err != nil || !found {
		t.Fatalf("ItemRow = %v %v", found, err)
	}
	if row.Item.SeriesNumbering != eps[0].SeriesNumbering || row.Item.AirDate != eps[0].AirDate {
		t.Errorf("ItemRow series numbering %+v air date %q; want %+v %q", row.Item.SeriesNumbering, row.Item.AirDate, eps[0].SeriesNumbering, eps[0].AirDate)
	}
	if row.Item.Numbering == row.Item.SeriesNumbering {
		t.Errorf("the aired numbering %+v reads back as the series' own", row.Item.Numbering)
	}
	aired := firefly(t, "ff", nil)
	aired.Order, aired.Language = mediasource.Order{Type: "official", Name: "Aired", Aired: true}, language.Tag{}
	observe(t, s, "sonarr", wholePass(t, s, "sonarr"), aired, eps...)
	if root, _, _ := s.Root(t.Context(), k.Root()); root.Root.Order != aired.Order || root.Root.Language != (language.Tag{}) {
		t.Errorf("a reobserved root reads back order %+v language %q; want %+v and none", root.Root.Order, root.Root.Language, aired.Order)
	}
}
