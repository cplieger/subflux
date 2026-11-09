package seriesorder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/skyhook"
	"go.yaml.in/yaml/v4"
)

// recordings are skyhook's recorded SkyHook answers.
const recordings = "../skyhook/testdata"

const (
	firefly    = 78874
	futurama   = 73871
	got        = 121361
	monogatari = 102261
)

var errSkyHookDown = errors.New("skyhook: HTTP 503")

type request struct {
	lang, order string
	tvdb        int
}

func (r request) String() string { return fmt.Sprintf("%d/%s/%s", r.tvdb, r.lang, r.order) }

// fakeSky answers each request from its table, 404 for any other; down
// fails every request of a series, downLang every request in a language,
// and hung holds a series' requests, in one language or in every one for
// "", until their ctx ends.
type fakeSky struct {
	answers  map[request]skyhook.Show
	down     map[int]error
	downLang map[string]error
	hung     map[request]bool
	sent     []request
	mu       sync.Mutex
}

func newFakeSky() *fakeSky {
	return &fakeSky{answers: map[request]skyhook.Show{}, down: map[int]error{}, downLang: map[string]error{}, hung: map[request]bool{}}
}

func (f *fakeSky) Show(ctx context.Context, tvdb int, lang, order string) (skyhook.Show, bool, error) {
	f.mu.Lock()
	r := request{tvdb: tvdb, lang: lang, order: order}
	f.sent = append(f.sent, r)
	hung := f.hung[request{tvdb: tvdb}] || f.hung[request{tvdb: tvdb, lang: lang}]
	f.mu.Unlock()
	if hung {
		<-ctx.Done()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return skyhook.Show{}, false, err
	}
	if err := f.down[tvdb]; err != nil {
		return skyhook.Show{}, false, err
	}
	if err := f.downLang[lang]; err != nil {
		return skyhook.Show{}, false, err
	}
	s, ok := f.answers[r]
	return cloneShow(s), ok, nil
}

func (f *fakeSky) set(tvdb int, lang, order string, s skyhook.Show) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[request{tvdb: tvdb, lang: lang, order: order}] = cloneShow(s)
}

func (f *fakeSky) get(tvdb int, lang, order string) skyhook.Show {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneShow(f.answers[request{tvdb: tvdb, lang: lang, order: order}])
}

func (f *fakeSky) fail(tvdb int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.down, tvdb)
		return
	}
	f.down[tvdb] = err
}

func (f *fakeSky) hang(tvdb int, lang string, hung bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hung[request{tvdb: tvdb, lang: lang}] = hung
}

func (f *fakeSky) failLang(lang string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.downLang, lang)
		return
	}
	f.downLang[lang] = err
}

// take returns and forgets the requests sent since the last take.
func (f *fakeSky) take() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	sent := f.sent
	f.sent = nil
	return sent
}

func cloneShow(s skyhook.Show) skyhook.Show {
	return skyhook.Show{Orders: slices.Clone(s.Orders), Episodes: slices.Clone(s.Episodes)}
}

// wireShow reads a recording as skyhook's decoder does.
type wireShow struct {
	SeasonTypes []skyhook.Order `json:"seasonTypes"`
	Episodes    []struct {
		Season   int    `json:"seasonNumber"`
		Number   int    `json:"episodeNumber"`
		Absolute int    `json:"absoluteEpisodeNumber"`
		AirDate  string `json:"airDate"`
		Title    string `json:"title"`
		ID       uint64 `json:"tvdbId"`
	} `json:"episodes"`
}

func readRecording(t *testing.T, tvdb int, lang, order string) skyhook.Show {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(recordings, fmt.Sprintf("%d-%s-%s.json", tvdb, lang, order)))
	if err != nil {
		t.Fatal(err)
	}
	var w wireShow
	if err := json.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	s := skyhook.Show{Orders: w.SeasonTypes}
	for _, e := range w.Episodes {
		s.Episodes = append(s.Episodes, skyhook.Episode{AirDate: e.AirDate, Title: e.Title, ID: e.ID, Season: e.Season, Number: e.Number, Absolute: e.Absolute})
	}
	return s
}

// record serves every recording of tvdb, its season types cut to the orders
// recorded in English, so a fetch asks no order the fixtures lack.
func (f *fakeSky) record(t *testing.T, tvdb int) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(recordings, fmt.Sprintf("%d-*-*.json", tvdb)))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recordings of %d: %v", tvdb, err)
	}
	recorded := map[string]bool{}
	var shows []request
	for _, file := range files {
		parts := strings.Split(strings.TrimSuffix(filepath.Base(file), ".json"), "-")
		if len(parts) != 3 {
			continue
		}
		r := request{tvdb: tvdb, lang: parts[1], order: parts[2]}
		shows = append(shows, r)
		if r.lang == english {
			recorded[r.order] = true
		}
	}
	for _, r := range shows {
		s := readRecording(t, tvdb, r.lang, r.order)
		s.Orders = slices.DeleteFunc(s.Orders, func(o skyhook.Order) bool { return !recorded[o.Type] })
		f.set(tvdb, r.lang, r.order, s)
	}
}

// fakeStore is statedb's root_evidence table: observe stores what a yielded
// root carries, as ObserveItems does, refusing what Validate refuses; a
// read under a done ctx answers ctx's error, as bolttable's View does.
type fakeStore struct {
	err    error
	ev     map[rootID][]byte
	policy language.Policy
	mu     sync.Mutex
}

func newFakeStore() *fakeStore { return &fakeStore{ev: map[rootID][]byte{}} }

func (s *fakeStore) RootEvidence(ctx context.Context, src media.SourceID, root mediasource.NativeRef) (mediasource.RootEvidence, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return mediasource.RootEvidence{}, false, fmt.Errorf("statedb: root evidence: %w", err)
	}
	if s.err != nil {
		return mediasource.RootEvidence{}, false, s.err
	}
	b, ok := s.ev[rootID{root: root, src: src}]
	if !ok {
		return mediasource.RootEvidence{}, false, nil
	}
	var ev mediasource.RootEvidence
	if err := json.Unmarshal(b, &ev); err != nil {
		panic(err)
	}
	return ev, true, nil
}

func (s *fakeStore) Policy() language.Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy
}

func (s *fakeStore) setPolicy(p language.Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = p
}

func (s *fakeStore) observe(t *testing.T, q *mediasource.JoinQuery, ev *mediasource.RootEvidence) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	id := idOf(q)
	if ev == nil {
		delete(s.ev, id)
		return
	}
	if err := ev.Validate(); err != nil {
		t.Fatalf("observed evidence: %v", err)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	s.ev[id] = b
}

func (s *fakeStore) stored(t *testing.T, q *mediasource.JoinQuery) (mediasource.RootEvidence, bool) {
	t.Helper()
	ev, ok, err := s.RootEvidence(context.Background(), q.Source, q.Root)
	if err != nil {
		t.Fatal(err)
	}
	return ev, ok
}

type rig struct {
	sky   *fakeSky
	store *fakeStore
	svc   *Service
	logs  *bytes.Buffer
}

func newRig() *rig {
	r := &rig{sky: newFakeSky(), store: newFakeStore(), logs: &bytes.Buffer{}}
	r.svc = newService(r.sky, r.store, slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	return r
}

// join is one Set step: Join, then ObserveItems storing the evidence of a
// root the Set yields; an unread root stores nothing.
func (r *rig) join(t *testing.T, q *mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
	t.Helper()
	joined, ev, err := r.svc.Join(t.Context(), *q)
	switch {
	case err == nil:
		if len(joined) != len(q.Episodes) {
			t.Fatalf("Join answered %d episodes for %d listed", len(joined), len(q.Episodes))
		}
		r.store.observe(t, q, ev)
	case joined != nil || ev != nil:
		t.Fatalf("Join answered %v with evidence %v beside %v", joined, ev, err)
	}
	return joined, ev, err
}

// mustJoin is a join that must answer.
func (r *rig) mustJoin(t *testing.T, q *mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence) {
	t.Helper()
	joined, ev, err := r.join(t, q)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	return joined, ev
}

// unread is a join that must answer ErrAiredOrderUnavailable.
func (r *rig) unread(t *testing.T, q *mediasource.JoinQuery) error {
	t.Helper()
	_, _, err := r.join(t, q)
	if !errors.Is(err, mediasource.ErrAiredOrderUnavailable) {
		t.Fatalf("Join error %v, want one wrapping ErrAiredOrderUnavailable", err)
	}
	return err
}

func (r *rig) warns(msg string) int { return strings.Count(r.logs.String(), msg) }

// warnsOf is the logged lines naming series tvdb.
func (r *rig) warnsOf(tvdb int) []string {
	var out []string
	for line := range strings.Lines(r.logs.String()) {
		if strings.Contains(line, fmt.Sprintf(" series=%d ", tvdb)) {
			out = append(out, line)
		}
	}
	return out
}

// listed is an order answer as Sonarr lists it: its places and air dates,
// Sonarr's placeholder for a null title, and absolute numbers only under the
// aired order.
func listed(s skyhook.Show, aired bool) []mediasource.Listed {
	out := make([]mediasource.Listed, len(s.Episodes))
	for i, e := range s.Episodes {
		title := e.Title
		if title == "" {
			title = placeholderTitle
		}
		out[i] = mediasource.Listed{AirDate: e.AirDate, Title: title, Season: e.Season, Episode: e.Number}
		if aired {
			out[i].Absolute = e.Absolute
		}
	}
	return out
}

func query(src string, tvdb int, order mediasource.Order, lang language.Tag, eps []mediasource.Listed) *mediasource.JoinQuery {
	return &mediasource.JoinQuery{
		Episodes: eps,
		Source:   media.SourceID(src),
		Root:     mediasource.NativeRef{ID: mediasource.NativeID(fmt.Sprint(tvdb)), Kind: media.Episode},
		Language: lang,
		Order:    order,
		TVDB:     uint64(tvdb),
	}
}

var (
	dvdOrder    = mediasource.Order{Type: "dvd", Name: "DVD Order"}
	airedSeries = mediasource.Order{Type: airedOrder, Name: "Aired Order", Aired: true}
)

func ep(id uint64, season, number int, date, title string) skyhook.Episode {
	return skyhook.Episode{AirDate: date, Title: title, ID: id, Season: season, Number: number}
}

// swapPlaces swaps two listed episodes' places, as a TVDB edit Sonarr
// refreshed would; their titles and air dates go with them.
func swapPlaces(eps []mediasource.Listed, i, j int) []mediasource.Listed {
	out := slices.Clone(eps)
	out[i].Season, out[j].Season = out[j].Season, out[i].Season
	out[i].Episode, out[j].Episode = out[j].Episode, out[i].Episode
	return out
}

// swapShow is swapPlaces on an order answer.
func swapShow(s skyhook.Show, i, j int) skyhook.Show {
	s = cloneShow(s)
	a, b := &s.Episodes[i], &s.Episodes[j]
	a.Season, b.Season = b.Season, a.Season
	a.Number, b.Number = b.Number, a.Number
	return s
}

// dvdQuery is tvdb listed in DVD order by source src, in English.
func dvdQuery(t *testing.T, sky *fakeSky, src string, tvdb int) *mediasource.JoinQuery {
	t.Helper()
	return query(src, tvdb, dvdOrder, language.Tag{}, listed(sky.get(tvdb, english, "dvd"), false))
}

func mustTag(t *testing.T, raw string) language.Tag {
	t.Helper()
	tag, ok := language.Parse(raw)
	if !ok {
		t.Fatalf("language.Parse(%q) failed", raw)
	}
	return tag
}

// policyOf is the languages section wanting each tag as a default target.
func policyOf(t *testing.T, tags ...string) language.Policy {
	t.Helper()
	var b strings.Builder
	b.WriteString("default:\n")
	for _, tag := range tags {
		b.WriteString("  - tag: " + tag + "\n")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatal(err)
	}
	p, errs := language.ParsePolicy(doc.Content[0])
	if len(errs) > 0 {
		t.Fatalf("ParsePolicy(%v): %v", tags, errs)
	}
	return p
}

func fetches(sent []request) int {
	n := 0
	for _, r := range sent {
		if r.lang == english && r.order == airedOrder {
			n++
		}
	}
	return n
}

func TestJoinStoreErrorIsTheStores(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	q := dvdQuery(t, r.sky, "sonarr", firefly)
	r.store.err = errors.New("statedb: root evidence: disk gone")
	_, _, err := r.join(t, q)
	if !errors.Is(err, r.store.err) || errors.Is(err, mediasource.ErrAiredOrderUnavailable) {
		t.Fatalf("Join error %v, want the store's", err)
	}
	if sent := r.sky.take(); len(sent) != 0 {
		t.Errorf("a store failure sent %v", sent)
	}
	if _, ok, err := r.svc.Evidence(t.Context(), q.Source, q.Root); ok || !errors.Is(err, r.store.err) {
		t.Errorf("Evidence = %v, %v; want the store's error", ok, err)
	}
}

func TestJoinCancelledAnswersContextError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sky.record(t, firefly)
		q := dvdQuery(t, r.sky, "sonarr", firefly)
		r.mustJoin(t, q)
		time.Sleep(freshFor)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, err := r.svc.Join(ctx, *q)
		if !errors.Is(err, context.Canceled) || errors.Is(err, mediasource.ErrAiredOrderUnavailable) {
			t.Fatalf("Join error %v, want context.Canceled", err)
		}
		if n := r.warns("did not answer"); n != 0 {
			t.Errorf("a cancelled Join logged %d WARNs", n)
		}
		r.sky.take()
		if _, ev := r.mustJoin(t, q); ev == nil || fetches(r.sky.take()) != 1 {
			t.Error("the Join after a cancelled one did not fetch for age")
		}
	})
}

// TestJoinDeadlineInReadUnavailable: a caller's deadline passing in the
// root's store read is a failed fetch under every order, never the store's
// error, which would fail the scan's batch.
func TestJoinDeadlineInReadUnavailable(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	r.sky.record(t, got)
	for _, c := range []struct {
		q    *mediasource.JoinQuery
		name string
	}{
		{name: "another order", q: dvdQuery(t, r.sky, "sonarr", firefly)},
		{name: "the aired order", q: query("sonarr", got, airedSeries, language.Tag{}, listed(r.sky.get(got, english, airedOrder), true))},
	} {
		q := c.q
		t.Run(c.name, func(t *testing.T) {
			r.mustJoin(t, q)
			r.sky.take()
			ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			defer cancel()
			joined, ev, err := r.svc.Join(ctx, *q)
			if !errors.Is(err, mediasource.ErrAiredOrderUnavailable) || joined != nil || ev != nil {
				t.Fatalf("Join = %d episodes, evidence %v, error %v; want ErrAiredOrderUnavailable alone", len(joined), ev, err)
			}
			if !strings.Contains(err.Error(), "SkyHook (skyhook.sonarr.tv) was not asked") {
				t.Errorf("error %q, want SkyHook named as not asked", err)
			}
			if sent := r.sky.take(); len(sent) != 0 {
				t.Errorf("a Join out of time sent %v", sent)
			}
		})
	}
}

// TestFetchFollowsNewestKept: a wall clock behind the newest kept reading
// still dates the new reading after it, so RootEvidence.Validate accepts it
// as Held.
func TestFetchFollowsNewestKept(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	q := dvdQuery(t, r.sky, "sonarr", firefly)
	after := time.Now().Add(time.Hour)
	got, err := r.svc.fetch(t.Context(), q, after)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Catalogue.Fetched.After(after) {
		t.Errorf("fetched at %v, not after the newest kept reading's %v", got.Catalogue.Fetched, after)
	}
}

func TestFetchRefusesInvalidAnswer(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	off := r.sky.get(firefly, english, airedOrder)
	off.Episodes = append(off.Episodes, off.Episodes[0])
	r.sky.set(firefly, english, airedOrder, off)
	err := r.unread(t, dvdQuery(t, r.sky, "sonarr", firefly))
	if !strings.Contains(err.Error(), "listed twice") {
		t.Errorf("error %q, want the repeated episode named", err)
	}
}

func TestEvidenceForAnotherSeriesCountsAsNone(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	// The list agrees with the stored reading of another series, which a
	// corrected series id left.
	q := query("sonarr", firefly, airedSeries, language.Tag{}, []mediasource.Listed{{AirDate: "2002-09-20", Title: "Pilot", Season: 1, Episode: 1}})
	r.store.observe(t, q, &mediasource.RootEvidence{Bound: reading(time.Now(), "Another series")})
	_, ev := r.mustJoin(t, q)
	if n := fetches(r.sky.take()); n != 1 || ev.Bound.Catalogue.TVDB != firefly || ev.Held != nil {
		t.Errorf("%d fetches, evidence of %d; want one fetch and Firefly's reading alone", n, ev.Bound.Catalogue.TVDB)
	}
}

// TestStateDroppedOnceIdle: the service holds memory only for roots with a
// list fact, a title memo or a WARN streak, so it does not grow with every
// root ever joined.
func TestStateDroppedOnceIdle(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	q := dvdQuery(t, r.sky, "sonarr", firefly)
	q.Episodes = swapPlaces(q.Episodes, 3, 4)
	r.unread(t, q)
	if len(r.svc.roots) != 1 {
		t.Fatalf("an unread root left %d states, want its list fact", len(r.svc.roots))
	}
	q.Episodes = swapPlaces(q.Episodes, 3, 4)
	r.mustJoin(t, q)
	if len(r.svc.roots) != 0 {
		t.Errorf("a root agreeing with its reading left %d states, want none", len(r.svc.roots))
	}
}

func TestNewDefaultsLogger(t *testing.T) {
	if s := New(skyhook.New(nil, nil), newFakeStore(), nil); s.log == nil || s.sky == nil {
		t.Fatal("New left the logger or the client nil")
	}
}
