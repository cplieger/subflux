package mediasource_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"github.com/cplieger/subflux/internal/statedb"
	"go.yaml.in/yaml/v4"
)

// fakeJoin records each query and answers through answer, else each
// listed episode at its own place.
type fakeJoin struct {
	answer  func(mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error)
	queries []mediasource.JoinQuery
	mu      sync.Mutex
}

func (j *fakeJoin) Join(ctx context.Context, q mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
	j.mu.Lock()
	j.queries = append(j.queries, q)
	answer := j.answer
	j.mu.Unlock()
	if answer == nil {
		return sourcetest.OwnPlaces{}.Join(ctx, q)
	}
	return answer(q)
}

func (j *fakeJoin) seen() []mediasource.JoinQuery {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.queries)
}

var errListedOtherwise = fmt.Errorf("%w: 2 episodes are listed otherwise than SkyHook lists them", mediasource.ErrAiredOrderUnavailable)

var dvd = mediasource.Order{Type: "dvd", Name: "DVD Order"}

// fakeSet is a Set of one Fake source per id, every one over f.
func fakeSet(t *testing.T, f *sourcetest.Fake, j mediasource.OrderJoin, ids ...media.SourceID) *mediasource.Set {
	t.Helper()
	if len(ids) == 0 {
		ids = []media.SourceID{"src"}
	}
	cfgs := make([]mediasource.Config, len(ids))
	for i, id := range ids {
		cfgs[i] = mediasource.Config{ID: id, Type: "fake", Index: i}
	}
	set, err := mediasource.NewSet([]mediasource.AdapterKind{sourcetest.FakeKind("fake", f)}, cfgs, mediasource.Deps{Logger: slog.New(slog.DiscardHandler)}, j)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// show is an episode root in order, with its TVDB id on root and items.
func show(id string, tvdb uint64, order mediasource.Order, items ...mediasource.Item) sourcetest.LibRoot {
	r := mediasource.Root{Native: series(id), Title: "Show " + id, External: media.ExternalIDs{TVDB: tvdb}, Order: order}
	for i := range items {
		items[i].External = r.External
	}
	return sourcetest.LibRoot{Root: r, Items: items}
}

func listedEpisode(id string, season, ep int, date, title string) mediasource.Item {
	it := episode(id, season, ep)
	it.AirDate, it.Title = date, title
	return it
}

func numbering(season, ep int) media.Numbering {
	return media.Numbering{Scheme: media.TVDB, Season: season, Episode: ep}
}

// itemsOf walks src and returns what it yielded per root, and the unread.
func itemsOf(t *testing.T, set *mediasource.Set, src media.SourceID) (map[mediasource.NativeID][]mediasource.Item, map[mediasource.NativeID]mediasource.Root, *walkLog, error) {
	t.Helper()
	items := map[mediasource.NativeID][]mediasource.Item{}
	roots := map[mediasource.NativeID]mediasource.Root{}
	var w walkLog
	err := set.Items(t.Context(), src, func(r mediasource.Root, it []mediasource.Item) error {
		items[r.Native.ID], roots[r.Native.ID] = it, r
		return w.yield(r, it)
	}, w.report)
	return items, roots, &w, err
}

func TestSetKeysAired(t *testing.T) {
	var l sourcetest.Library
	serenity := listedEpisode("1", 1, 1, "2002-12-20", "Serenity")
	serenity.Numbering = numbering(1, 1)
	l.Put(show("ff", 78874, dvd, serenity))
	aired := listedEpisode("2", 2, 3, "2012-04-15", "Blackwater")
	aired.Absolute = 19
	l.Put(show("got", 121361, mediasource.Order{Type: "official", Aired: true}, aired))
	j := &fakeJoin{answer: func(q mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
		if q.Order.Aired {
			return sourcetest.OwnPlaces{}.Join(t.Context(), q)
		}
		return []mediasource.Joined{{Season: 1, Episode: 11, Absolute: 1, OK: true}}, nil, nil
	}}
	set := fakeSet(t, sourcetest.NewFake(&l, allCaps), j)
	items, _, _, err := itemsOf(t, set, "src")
	if err != nil {
		t.Fatal(err)
	}
	if got := items["ff"][0]; got.Numbering != numbering(1, 11) || got.Absolute != 1 || got.SeriesNumbering != numbering(1, 1) {
		t.Errorf("DVD S01E01 yielded Numbering %+v Absolute %d SeriesNumbering %+v, want aired S01E11, 1 and DVD S01E01", got.Numbering, got.Absolute, got.SeriesNumbering)
	}
	if got := items["got"][0]; got.Numbering != numbering(2, 3) || got.Absolute != 19 {
		t.Errorf("an aired-order episode yielded %+v absolute %d, want its own S02E03 and 19", got.Numbering, got.Absolute)
	}
	_, out, found, err := set.Root(t.Context(), "src", series("ff"))
	if err != nil || !found || out[0].Numbering != numbering(1, 11) {
		t.Errorf("Root = %v %v %+v, want the joined key", found, err, out)
	}
}

func TestSetJoinUnavailableUnread(t *testing.T) {
	var l sourcetest.Library
	for i := range 5 {
		l.Put(show(fmt.Sprint("r", i), uint64(100+i), dvd, episode("1", 1, 1)))
	}
	j := &fakeJoin{answer: func(q mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
		if q.TVDB < 104 {
			return nil, nil, errListedOtherwise
		}
		return sourcetest.OwnPlaces{}.Join(t.Context(), q)
	}}
	set := fakeSet(t, sourcetest.NewFake(&l, allCaps), j)
	var w walkLog
	var reports []*mediasource.SourceError
	err := set.Items(t.Context(), "src", w.yield, func(ref mediasource.NativeRef, se *mediasource.SourceError) error {
		reports = append(reports, se)
		return w.report(ref, se)
	})
	if err != nil || joined(w.unread) != "r0,r1,r2,r3" || joined(w.yielded) != "r4" {
		t.Fatalf("Items = %v, unread %s, yielded %s; want four unread and r4 yielded", err, joined(w.unread), joined(w.yielded))
	}
	for _, se := range reports {
		if se.Cause != mediasource.Unreachable || !errors.Is(se, mediasource.ErrAiredOrderUnavailable) || !strings.Contains(se.Error(), "the aired order of Show r") {
			t.Errorf("unread report %v, want Unreachable naming the series and wrapping ErrAiredOrderUnavailable", se)
		}
		if ae, ok := errors.AsType[*mediasource.AiredOrderError](se); !ok || !strings.HasPrefix(ae.Title, "Show r") || ae.Err.Error() != errListedOtherwise.Error() {
			t.Errorf("unread report %v, want an AiredOrderError with the series' title and the join's reason", se)
		}
	}
	_, _, found, err := set.Root(t.Context(), "src", series("r0"))
	if found || !errors.Is(err, mediasource.ErrAiredOrderUnavailable) || causeOf(t, err) != mediasource.Unreachable {
		t.Errorf("Root = %v, %v; want no root and the unavailable error", found, err)
	}

	t.Run("the unread root past the keep list's bound fails the call", func(t *testing.T) {
		var many sourcetest.Library
		for i := range 34 {
			many.Put(show(fmt.Sprint("m", i), uint64(200+i), dvd, episode("1", 1, 1)))
		}
		set := fakeSet(t, sourcetest.NewFake(&many, allCaps), &fakeJoin{answer: func(mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
			return nil, nil, errListedOtherwise
		}})
		var w walkLog
		err := set.Items(t.Context(), "src", w.yield, w.report)
		if !errors.Is(err, mediasource.ErrAiredOrderUnavailable) || len(w.unread) != 32 {
			t.Errorf("Items = %v after %d unread, want the 33rd's error after 32", err, len(w.unread))
		}
	})
}

func TestSetCarriesJoinEvidence(t *testing.T) {
	var l sourcetest.Library
	l.Put(show("ff", 78874, dvd, episode("1", 1, 1)))
	l.Put(show("untagged", 0, dvd, episode("1", 1, 1)))
	stale := evidence(t, 1, time.Now())
	movie := sourcetest.LibRoot{Root: mediasource.Root{Native: mediasource.NativeRef{ID: "m", Kind: media.Movie}, Evidence: &stale}, Items: []mediasource.Item{{Native: "m"}}}
	l.Put(movie)
	want := evidence(t, 78874, time.Now())
	j := &fakeJoin{answer: func(q mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
		joined, _, err := sourcetest.OwnPlaces{}.Join(t.Context(), q)
		return joined, &want, err
	}}
	set := fakeSet(t, sourcetest.NewFake(&l, allCaps), j)
	_, roots, _, err := itemsOf(t, set, "src")
	if err != nil {
		t.Fatal(err)
	}
	if got := roots["ff"].Evidence; got == nil || got.Bound.Catalogue.TVDB != 78874 {
		t.Errorf("the joined root carries %v, want the join's evidence", got)
	}
	if roots["untagged"].Evidence != nil || roots["m"].Evidence != nil {
		t.Error("a root that joins nothing carries evidence")
	}
	if q := j.seen(); len(q) != 1 || q[0].TVDB != 78874 {
		t.Errorf("joined %d roots, want Firefly alone", len(q))
	}
}

func TestSetJoinNotOKUnidentified(t *testing.T) {
	var l sourcetest.Library
	it := episode("1", 1, 3)
	it.Absolute, it.Numbering = 3, numbering(1, 3)
	l.Put(show("office", 73244, dvd, it))
	set := fakeSet(t, sourcetest.NewFake(&l, allCaps), &fakeJoin{answer: func(mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
		return []mediasource.Joined{{}}, nil, nil
	}})
	items, _, _, err := itemsOf(t, set, "src")
	if err != nil {
		t.Fatal(err)
	}
	if got := items["office"][0]; got.Numbering.Scheme != media.None || got.Absolute != 0 {
		t.Errorf("an unplaced episode yielded %+v absolute %d, want no key", got.Numbering, got.Absolute)
	}
}

func TestSetJoinQueryNamesSource(t *testing.T) {
	var l sourcetest.Library
	fr, _ := language.Parse("fr")
	r := show("ff", 78874, dvd, listedEpisode("1", 1, 1, "2002-12-20", "Serenity"), listedEpisode("2", 1, 2, "2002-09-20", "The Train Job"))
	r.Root.Language = fr
	r.Items[0].Absolute = 7
	l.Put(r)
	j := &fakeJoin{}
	set := fakeSet(t, sourcetest.NewFake(&l, allCaps), j, "a", "b")
	for _, src := range []media.SourceID{"a", "b"} {
		if _, _, _, err := itemsOf(t, set, src); err != nil {
			t.Fatal(err)
		}
	}
	q := j.seen()
	if len(q) != 2 || q[0].Source != "a" || q[1].Source != "b" {
		t.Fatalf("queries %+v, want one per source", q)
	}
	want := []mediasource.Listed{{AirDate: "2002-12-20", Title: "Serenity", Season: 1, Episode: 1, Absolute: 7}, {AirDate: "2002-09-20", Title: "The Train Job", Season: 1, Episode: 2}}
	if got := q[0]; got.Root != series("ff") || got.TVDB != 78874 || got.Order != dvd || got.Language != fr || !slices.Equal(got.Episodes, want) {
		t.Errorf("query %+v, want the root, its series, order, language and episodes in the source's order", got)
	}
}

func TestSetJoinStoreErrorFailsCall(t *testing.T) {
	errDisk := errors.New("statedb: root evidence: disk gone")
	for name, answer := range map[string]func(mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error){
		"a store failure": func(mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
			return nil, nil, errDisk
		},
		"a short join answer": func(mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
			return nil, nil, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			var l sourcetest.Library
			l.Put(show("ff", 78874, dvd, episode("1", 1, 1)))
			set := fakeSet(t, sourcetest.NewFake(&l, allCaps), &fakeJoin{answer: answer})
			var se *mediasource.SourceError
			_, _, w, err := itemsOf(t, set, "src")
			if err == nil || errors.As(err, &se) || len(w.unread)+len(w.yielded) != 0 {
				t.Errorf("Items = %v (unread %v), want the call failed with no *SourceError", err, w.unread)
			}
			if _, _, _, err := set.Root(t.Context(), "src", series("ff")); err == nil || errors.As(err, &se) {
				t.Errorf("Root = %v, want the call failed with no *SourceError", err)
			}
			if name == "a store failure" && !errors.Is(err, errDisk) {
				t.Errorf("Items = %v, want the store's error", err)
			}
		})
	}
}

func TestSetJoinCancelled(t *testing.T) {
	var l sourcetest.Library
	l.Put(show("ff", 78874, dvd, episode("1", 1, 1)))
	ctx, cancel := context.WithCancel(t.Context())
	set := fakeSet(t, sourcetest.NewFake(&l, allCaps), &fakeJoin{answer: func(mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
		cancel()
		return nil, nil, context.Canceled
	}})
	var w walkLog
	if err := set.Items(ctx, "src", w.yield, w.report); !errors.Is(err, context.Canceled) || len(w.unread) != 0 {
		t.Errorf("Items = %v unread %v, want the cancellation and no unread root", err, w.unread)
	}
}

func TestNewSetRefusesNilOrders(t *testing.T) {
	f := sourcetest.NewFake(&sourcetest.Library{}, allCaps)
	kinds := []mediasource.AdapterKind{sourcetest.FakeKind("fake", f)}
	cfgs := []mediasource.Config{{ID: "src", Type: "fake"}}
	for name, j := range map[string]mediasource.OrderJoin{"nil": nil, "typed nil": (*fakeJoin)(nil)} {
		if set, err := mediasource.NewSet(kinds, cfgs, mediasource.Deps{}, j); err == nil || set != nil {
			t.Errorf("NewSet with a %s OrderJoin = %v, %v; want a refusal", name, set, err)
		}
	}
}

// TestMediaServerDepsHoldNoJoin: Deps, which every adapter's and media
// server's New receives, carries no OrderJoin.
func TestMediaServerDepsHoldNoJoin(t *testing.T) {
	join := reflect.TypeFor[mediasource.OrderJoin]()
	deps := reflect.TypeFor[mediasource.Deps]()
	for f := range deps.Fields() {
		if f.Type.Implements(join) || f.Type == join {
			t.Errorf("Deps.%s can carry an OrderJoin", f.Name)
		}
	}
}

// evidence is one valid reading of tvdb in DVD order: S01E01 aired S01E11.
func evidence(t *testing.T, tvdb uint64, fetched time.Time) mediasource.RootEvidence {
	t.Helper()
	ev := mediasource.RootEvidence{Bound: mediasource.SeriesEvidence{
		Catalogue: mediasource.Catalogue{
			Fetched:  fetched.UTC().Round(0),
			TVDB:     tvdb,
			Episodes: []mediasource.CatalogueEpisode{{AirDate: "2002-12-20", ID: 297989, Season: 1, Episode: 11, Absolute: 1}},
			Orders:   []mediasource.CatalogueOrder{{Type: "dvd", Name: "DVD Order", Places: []mediasource.Place{{Season: 1, Episode: 1, Listed: true}}}},
		},
		Titles: []mediasource.TitleSet{{Language: "eng", Found: true, Titles: []mediasource.EpisodeTitle{{Title: "Serenity", ID: 297989}}}},
	}}
	if err := ev.Validate(); err != nil {
		t.Fatal(err)
	}
	return ev
}

// library is a statedb under t.TempDir with src active, wanting en.
func library(t *testing.T, src media.SourceID) *statedb.Store {
	t.Helper()
	s, err := statedb.Open(filepath.Join(t.TempDir(), "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("default:\n  - tag: en\n"), &doc); err != nil {
		t.Fatal(err)
	}
	p, errs := language.ParsePolicy(doc.Content[0])
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := s.Activate(t.Context(), statedb.Activation{Sources: []media.SourceID{src}, Languages: p}); err != nil {
		t.Fatal(err)
	}
	return s
}

// wholePass is one complete whole pass of src through set into s, keeping
// the roots reported unread, as scan's pass does.
func wholePass(t *testing.T, s *statedb.Store, set *mediasource.Set, src media.SourceID) []mediasource.NativeRef {
	t.Helper()
	p, err := s.BeginPass(t.Context(), src, statedb.PassScope{})
	if err != nil {
		t.Fatal(err)
	}
	var keep []mediasource.NativeRef
	err = set.Items(t.Context(), src, func(r mediasource.Root, items []mediasource.Item) error {
		return s.ObserveItems(t.Context(), src, p, r, items)
	}, func(ref mediasource.NativeRef, _ *mediasource.SourceError) error {
		keep = append(keep, ref)
		return nil
	})
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if _, err := s.FinishPass(t.Context(), src, p, true, mediasource.Cursor{}, keep); err != nil {
		t.Fatalf("FinishPass: %v", err)
	}
	return keep
}

func episodeKey(t *testing.T, tvdb uint64, season, ep int) media.Key {
	t.Helper()
	k, ok := media.Choose(media.Episode, media.ExternalIDs{TVDB: tvdb}, numbering(season, ep))
	if !ok {
		t.Fatalf("no key for %d S%02dE%02d", tvdb, season, ep)
	}
	return k
}

func TestSetStaleDisagreementRetiresNothing(t *testing.T) {
	s := library(t, "src")
	var l sourcetest.Library
	pilot := listedEpisode("1", 1, 1, "2002-12-20", "Serenity")
	pilot.VideoPath = "/tv/Firefly/Firefly - S01E01.mkv"
	l.Put(show("ff", 78874, dvd, pilot))
	j := &fakeJoin{answer: func(mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
		return []mediasource.Joined{{Season: 1, Episode: 11, Absolute: 1, OK: true}}, nil, nil
	}}
	set := fakeSet(t, sourcetest.NewFake(&l, allCaps), j)
	wholePass(t, s, set, "src")
	k := episodeKey(t, 78874, 1, 11)
	row, found, err := s.ItemRow(t.Context(), k)
	if err != nil || !found {
		t.Fatalf("ItemRow = %v, %v after the keyed pass", found, err)
	}
	en, _ := language.Parse("en")
	if err := s.PutExternal(t.Context(), row.Inst, []statedb.LocalFile{{Name: "Firefly - S01E11.en.srt", Tag: en}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLock(t.Context(), row.Inst, en, language.Standard, true); err != nil {
		t.Fatal(err)
	}

	j.mu.Lock()
	j.answer = func(mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
		return nil, nil, errListedOtherwise
	}
	j.mu.Unlock()
	if keep := wholePass(t, s, set, "src"); len(keep) != 1 || keep[0] != series("ff") {
		t.Fatalf("the pass kept %v, want the unread root", keep)
	}
	row, found, err = s.ItemRow(t.Context(), k)
	if err != nil || !found || len(row.Coverage) == 0 || !slices.ContainsFunc(row.Coverage, func(c statedb.CoverageRow) bool { return c.Locked }) {
		t.Errorf("after the unread pass ItemRow = %v %v, coverage %+v; want the record, its coverage and its lock", found, err, row.Coverage)
	}
	_, _, found, err = set.Root(t.Context(), "src", series("ff"))
	if found || !errors.Is(err, mediasource.ErrAiredOrderUnavailable) {
		t.Errorf("a batch's Root = %v, %v; want the unavailable error and no root", found, err)
	}
}

func TestSetEvidenceWrittenWithKeys(t *testing.T) {
	s := library(t, "src")
	var l sourcetest.Library
	l.Put(show("ff", 78874, dvd, listedEpisode("1", 1, 1, "2002-12-20", "Serenity")))
	r1 := evidence(t, 78874, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	unavailable := map[uint64]bool{}
	var mu sync.Mutex
	j := &fakeJoin{answer: func(q mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
		mu.Lock()
		defer mu.Unlock()
		if unavailable[q.TVDB] {
			return nil, nil, errListedOtherwise
		}
		ev := r1
		return []mediasource.Joined{{Season: 1, Episode: 11, Absolute: 1, OK: true}}, &ev, nil
	}}
	set := fakeSet(t, sourcetest.NewFake(&l, allCaps), j)
	stored := func(id string) (mediasource.RootEvidence, bool) {
		t.Helper()
		ev, found, err := s.RootEvidence(t.Context(), "src", series(id))
		if err != nil {
			t.Fatal(err)
		}
		return ev, found
	}
	wholePass(t, s, set, "src")
	if got, found := stored("ff"); !found || !got.Bound.Catalogue.Fetched.Equal(r1.Bound.Catalogue.Fetched) {
		t.Fatalf("RootEvidence = %+v %v, want R1 stored with the keys", got, found)
	}

	mu.Lock()
	unavailable[78874] = true
	mu.Unlock()
	l.Put(show("new", 4242, dvd, episode("1", 1, 1)))
	mu.Lock()
	unavailable[4242] = true
	mu.Unlock()
	wholePass(t, s, set, "src")
	if got, found := stored("ff"); !found || !got.Bound.Catalogue.Fetched.Equal(r1.Bound.Catalogue.Fetched) {
		t.Errorf("an unread pass left %+v %v, want R1", got, found)
	}
	if _, found, err := s.ItemRow(t.Context(), episodeKey(t, 78874, 1, 11)); err != nil || !found {
		t.Errorf("an unread pass dropped the stored key: %v %v", found, err)
	}
	if _, found := stored("new"); found {
		t.Error("a root whose first join failed has evidence")
	}
	l.Remove(series("new"))
	wholePass(t, s, set, "src")
	if _, found := stored("new"); found {
		t.Error("a root gone from its source has evidence")
	}
}
