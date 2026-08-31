package arrsvc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v2"
)

// fakeSonarr is a controllable sonarrReads implementation.
type fakeSonarr struct {
	err          error
	episodes     map[int][]arrapi.Episode
	episodeCalls map[int]int
	tagIDs       map[int]struct{}
	series       []arrapi.Series
	unmatched    []string
	mu           sync.Mutex
	seriesCalls  int
	tagCalls     int
}

func (f *fakeSonarr) Series(_ context.Context) ([]arrapi.Series, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seriesCalls++
	return f.series, f.err
}

func (f *fakeSonarr) Episodes(_ context.Context, seriesID int) ([]arrapi.Episode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.episodeCalls == nil {
		f.episodeCalls = make(map[int]int)
	}
	f.episodeCalls[seriesID]++
	return f.episodes[seriesID], f.err
}

func (f *fakeSonarr) ResolveTagIDs(_ context.Context, _ ...string) (map[int]struct{}, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagCalls++
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.tagIDs, f.unmatched, nil
}

func (f *fakeSonarr) counts() (series, tags int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seriesCalls, f.tagCalls
}

func (f *fakeSonarr) episodeCallCount(seriesID int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.episodeCalls[seriesID]
}

// fakeRadarr is a controllable radarrReads implementation.
type fakeRadarr struct {
	err        error
	tagIDs     map[int]struct{}
	movies     []arrapi.Movie
	unmatched  []string
	mu         sync.Mutex
	movieCalls int
	tagCalls   int
}

func (f *fakeRadarr) Movies(_ context.Context) ([]arrapi.Movie, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.movieCalls++
	return f.movies, f.err
}

func (f *fakeRadarr) ResolveTagIDs(_ context.Context, _ ...string) (map[int]struct{}, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagCalls++
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.tagIDs, f.unmatched, nil
}

func (f *fakeRadarr) counts() (movies, tags int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.movieCalls, f.tagCalls
}

func testGate(ctx context.Context) *ReadGate {
	return NewReadGate(func() context.Context { return ctx }, nil)
}

// testCachedSonarr wires a wrapper around fake shipped and wave clients (the
// embedded *Sonarr passthrough surface stays nil and unused).
func testCachedSonarr(shipped, wave sonarrReads, gate *ReadGate) *CachedSonarr {
	return &CachedSonarr{shipped: shipped, wave: wave, table: newReadTable(gate)}
}

func testCachedRadarr(shipped, wave radarrReads, gate *ReadGate) *CachedRadarr {
	return &CachedRadarr{shipped: shipped, wave: wave, table: newReadTable(gate)}
}

func TestCachedSonarr_plainUsesShippedAndMarkedUsesWaveClient(t *testing.T) {
	shipped := &fakeSonarr{series: []arrapi.Series{{ID: 1, TvdbID: 11}}}
	wave := &fakeSonarr{series: []arrapi.Series{{ID: 1, TvdbID: 11}, {ID: 2, TvdbID: 22}}}
	c := testCachedSonarr(shipped, wave, testGate(t.Context()))

	rows, err := c.Series(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("plain Series = %d rows, %v; want 1 row from the shipped client", len(rows), err)
	}
	if s, _ := shipped.counts(); s != 1 {
		t.Errorf("shipped series calls = %d, want 1", s)
	}
	if s, _ := wave.counts(); s != 0 {
		t.Errorf("wave series calls = %d, want 0 on a plain read", s)
	}

	// The marked read is never served by the fresh cache or the plain
	// flight's result: it runs its own wave on the wave client.
	rows, err = c.Series(WithRecovery(t.Context()))
	if err != nil || len(rows) != 2 {
		t.Fatalf("marked Series = %d rows, %v; want 2 rows from the wave client", len(rows), err)
	}
	if s, _ := wave.counts(); s != 1 {
		t.Errorf("wave series calls = %d, want 1 after a marked read", s)
	}
}

func TestCachedSonarr_seriesEntryCarriesTvdbIndex(t *testing.T) {
	shipped := &fakeSonarr{series: []arrapi.Series{{ID: 1, TvdbID: 11}, {ID: 2, TvdbID: 22}}}
	c := testCachedSonarr(shipped, &fakeSonarr{}, testGate(t.Context()))

	if _, err := c.Series(t.Context()); err != nil {
		t.Fatal(err)
	}
	e, ok := c.table.cache.Get(keySeriesList)
	if !ok {
		t.Fatal("series entry missing after a plain read")
	}
	snap := e.payload.(seriesSnapshot)
	if snap.byTvdb[22] != 1 {
		t.Errorf("byTvdb[22] = %d, want row 1 (the index lives inside the entry value)", snap.byTvdb[22])
	}
}

func TestCachedRadarr_movieEntryCarriesTmdbIndex(t *testing.T) {
	shipped := &fakeRadarr{movies: []arrapi.Movie{{ID: 5, TmdbID: 55}, {ID: 6, TmdbID: 66}}}
	c := testCachedRadarr(shipped, &fakeRadarr{}, testGate(t.Context()))

	if _, err := c.Movies(t.Context()); err != nil {
		t.Fatal(err)
	}
	e, ok := c.table.cache.Get(keyMovieList)
	if !ok {
		t.Fatal("movie entry missing after a plain read")
	}
	snap := e.payload.(movieSnapshot)
	if snap.byTmdb[66] != 1 {
		t.Errorf("byTmdb[66] = %d, want row 1", snap.byTmdb[66])
	}
}

func TestExcludeTags_servedFromCache(t *testing.T) {
	shipped := &fakeSonarr{tagIDs: map[int]struct{}{7: {}}}
	c := testCachedSonarr(shipped, &fakeSonarr{}, testGate(t.Context()))

	for range 3 {
		ids := c.ResolveExcludeTagIDs(t.Context(), []string{"anime"}, false)
		if _, ok := ids[7]; !ok {
			t.Fatalf("ids = %v, want tag 7", ids)
		}
	}
	if _, tags := shipped.counts(); tags != 1 {
		t.Errorf("resolve calls = %d, want 1 (exclude-tag served from cache)", tags)
	}
}

func TestExcludeTags_sonarrAndRadarrNeverShareAnEntry(t *testing.T) {
	sonarrShipped := &fakeSonarr{tagIDs: map[int]struct{}{1: {}}}
	sonarrWave := &fakeSonarr{tagIDs: map[int]struct{}{2: {}}}
	radarrShipped := &fakeRadarr{tagIDs: map[int]struct{}{9: {}}}
	gate := testGate(t.Context())
	cs := testCachedSonarr(sonarrShipped, sonarrWave, gate)
	cr := testCachedRadarr(radarrShipped, &fakeRadarr{}, gate)

	names := []string{"anime"} // the same configured name set on both sides
	if ids := cr.ResolveExcludeTagIDs(t.Context(), names, false); len(ids) != 1 {
		t.Fatalf("radarr ids = %v", ids)
	}

	// A series-detail recovery resolves the SONARR tag key. The radarr entry
	// must stay untouched: same key text, different instance.
	ids, err := cs.ResolveExcludeTagIDsErr(WithRecovery(t.Context()), names, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids[2]; !ok {
		t.Fatalf("sonarr marked ids = %v, want the wave client's tag 2", ids)
	}
	if _, radarrTags := radarrShipped.counts(); radarrTags != 1 {
		t.Errorf("radarr resolve calls = %d, want 1 (untouched by the sonarr recovery)", radarrTags)
	}
	if ids := cr.ResolveExcludeTagIDs(t.Context(), names, false); len(ids) != 1 {
		t.Errorf("radarr ids after sonarr recovery = %v, want the cached tag 9", ids)
	}
	if _, radarrTags := radarrShipped.counts(); radarrTags != 1 {
		t.Errorf("radarr resolve calls = %d, want 1 (served from its own entry)", radarrTags)
	}
}

func TestExcludeTags_failOpenNilIsNeverCached(t *testing.T) {
	shipped := &fakeSonarr{err: errors.New("arr down")}
	c := testCachedSonarr(shipped, &fakeSonarr{}, testGate(t.Context()))

	if ids := c.ResolveExcludeTagIDs(t.Context(), []string{"anime"}, false); ids != nil {
		t.Fatalf("ids = %v, want the fail-open nil", ids)
	}
	if _, ok := c.table.cache.Get(tagsKey([]string{"anime"})); ok {
		t.Fatal("a fail-open nil was cached")
	}

	shipped.mu.Lock()
	shipped.err = nil
	shipped.tagIDs = map[int]struct{}{3: {}}
	shipped.mu.Unlock()
	ids := c.ResolveExcludeTagIDs(t.Context(), []string{"anime"}, false)
	if _, ok := ids[3]; !ok {
		t.Fatalf("ids after recovery = %v, want a fresh resolution", ids)
	}
	if _, tags := shipped.counts(); tags != 2 {
		t.Errorf("resolve calls = %d, want 2 (the failure was not cached)", tags)
	}
}

func TestExcludeTags_markedFailurePropagatesTyped(t *testing.T) {
	wave := &fakeSonarr{err: errors.New("arr down")}
	c := testCachedSonarr(&fakeSonarr{}, wave, testGate(t.Context()))

	ids, err := c.ResolveExcludeTagIDsErr(WithRecovery(t.Context()), []string{"anime"}, false)
	if !errors.Is(err, ErrRecoveryFailed) {
		t.Fatalf("error = %v, want ErrRecoveryFailed (never a silent empty exclusion)", err)
	}
	if ids != nil {
		t.Errorf("ids = %v, want nil beside the typed failure", ids)
	}
	if _, ok := c.table.cache.Get(tagsKey([]string{"anime"})); ok {
		t.Error("a failed wave's resolution was cached")
	}
}

func TestExcludeTags_emptyNameSetSkipsUpstream(t *testing.T) {
	shipped := &fakeSonarr{}
	c := testCachedSonarr(shipped, &fakeSonarr{}, testGate(t.Context()))

	if ids := c.ResolveExcludeTagIDs(t.Context(), nil, false); ids != nil {
		t.Fatalf("ids = %v, want nil for no configured tags", ids)
	}
	if _, tags := shipped.counts(); tags != 0 {
		t.Errorf("resolve calls = %d, want 0", tags)
	}
}

// captureSlog swaps the default logger for a buffer. Tests using it must not
// run in parallel (the default is process-global).
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestExcludeTags_hintLogsMissingNamesVerbatimOnCacheHits(t *testing.T) {
	t.Run("sonarr", func(t *testing.T) {
		shipped := &fakeSonarr{tagIDs: map[int]struct{}{1: {}}, unmatched: []string{"absent-tag"}}
		c := testCachedSonarr(shipped, &fakeSonarr{}, testGate(t.Context()))
		names := []string{"anime", "absent-tag"}

		if c.ResolveExcludeTagIDs(t.Context(), names, false) == nil {
			t.Fatal("priming resolution failed")
		}
		buf := captureSlog(t)
		if c.ResolveExcludeTagIDs(t.Context(), names, true) == nil {
			t.Fatal("cached resolution failed")
		}
		if _, tags := shipped.counts(); tags != 1 {
			t.Fatalf("resolve calls = %d, want 1 (the hint must fire on a cache hit)", tags)
		}
		logged := buf.String()
		if !strings.Contains(logged, "exclude_tag not found in arr") || !strings.Contains(logged, "tag=absent-tag") {
			t.Errorf("hint log = %q, want the missing NAME verbatim", logged)
		}
	})
	t.Run("radarr", func(t *testing.T) {
		shipped := &fakeRadarr{tagIDs: map[int]struct{}{1: {}}, unmatched: []string{"absent-tag"}}
		c := testCachedRadarr(shipped, &fakeRadarr{}, testGate(t.Context()))
		names := []string{"anime", "absent-tag"}

		if c.ResolveExcludeTagIDs(t.Context(), names, false) == nil {
			t.Fatal("priming resolution failed")
		}
		buf := captureSlog(t)
		if c.ResolveExcludeTagIDs(t.Context(), names, true) == nil {
			t.Fatal("cached resolution failed")
		}
		if _, tags := shipped.counts(); tags != 1 {
			t.Fatalf("resolve calls = %d, want 1", tags)
		}
		logged := buf.String()
		if !strings.Contains(logged, "exclude_tag not found in arr") || !strings.Contains(logged, "tag=absent-tag") {
			t.Errorf("hint log = %q, want the missing NAME verbatim", logged)
		}
	})
}

func TestEpisodesGate_staleListAwaitsWaveAndSucceeds(t *testing.T) {
	shipped := &fakeSonarr{series: []arrapi.Series{{ID: 1, TvdbID: 11}}}
	wave := &fakeSonarr{
		series:   []arrapi.Series{{ID: 1, TvdbID: 11}, {ID: 2, TvdbID: 22}}, // the just-added series
		episodes: map[int][]arrapi.Episode{2: {{ID: 201, SeasonNumber: 1, EpisodeNumber: 1}}},
	}
	c := testCachedSonarr(shipped, wave, testGate(t.Context()))

	if _, err := c.Series(t.Context()); err != nil { // the cached list is the stale one
		t.Fatal(err)
	}
	eps, err := c.Episodes(WithRecovery(t.Context()), 2)
	if err != nil {
		t.Fatalf("gated episodes read failed: %v", err)
	}
	if len(eps) != 1 || eps[0].ID != 201 {
		t.Fatalf("episodes = %v, want the wave-fetched episode 201", eps)
	}
	if s, _ := wave.counts(); s != 1 {
		t.Errorf("wave series calls = %d, want 1 (the gate's list wave)", s)
	}
	if got := wave.episodeCallCount(2); got != 1 {
		t.Errorf("wave episode calls = %d, want 1", got)
	}
}

func TestEpisodesGate_fabricatedIDsAnswer404WithZeroEpisodeCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shipped := &fakeSonarr{series: []arrapi.Series{{ID: 1, TvdbID: 11}}}
		wave := &fakeSonarr{series: []arrapi.Series{{ID: 1, TvdbID: 11}}}
		gate := testGate(t.Context())
		c := testCachedSonarr(shipped, wave, gate)

		if _, err := c.Series(t.Context()); err != nil {
			t.Fatal(err)
		}

		// Hold the permits so the N gated reads all join ONE queued list wave.
		blockTbl := newReadTable(gate)
		releases := holdPermits(t, t.Context(), blockTbl, maxConcurrentWaves)

		const n = 5
		results := make([]<-chan error, n)
		for i := range n {
			ch := make(chan error, 1)
			results[i] = ch
			fabricated := 100 + i
			go func() {
				_, err := c.Episodes(WithRecovery(t.Context()), fabricated)
				ch <- err
			}()
		}
		synctest.Wait()
		close(releases[0])

		for i, ch := range results {
			if err := <-ch; !errors.Is(err, ErrUnknownSeries) {
				t.Errorf("fabricated id %d: error = %v, want ErrUnknownSeries", 100+i, err)
			}
		}
		if s, _ := wave.counts(); s != 1 {
			t.Errorf("list wave calls = %d, want 1 shared pass for all %d ids", s, n)
		}
		for i := range n {
			if got := wave.episodeCallCount(100 + i); got != 0 {
				t.Errorf("episodes calls for fabricated id %d = %d, want 0 (key inflation never reaches an arr)", 100+i, got)
			}
			if got := shipped.episodeCallCount(100 + i); got != 0 {
				t.Errorf("shipped episodes calls for fabricated id %d = %d, want 0", 100+i, got)
			}
		}
		close(releases[1])
	})
}

func TestEpisodesGate_markedHitOnCachedListSkipsTheListWave(t *testing.T) {
	shipped := &fakeSonarr{series: []arrapi.Series{{ID: 1, TvdbID: 11}}}
	wave := &fakeSonarr{episodes: map[int][]arrapi.Episode{1: {{ID: 101}}}}
	c := testCachedSonarr(shipped, wave, testGate(t.Context()))

	if _, err := c.Series(t.Context()); err != nil {
		t.Fatal(err)
	}
	eps, err := c.Episodes(WithRecovery(t.Context()), 1)
	if err != nil || len(eps) != 1 {
		t.Fatalf("episodes = %v, %v; want the waved episodes", eps, err)
	}
	if s, _ := wave.counts(); s != 0 {
		t.Errorf("list wave calls = %d, want 0 (the cached list already holds the id)", s)
	}
}

func TestEpisodesGate_plainMissFallsThroughToUpstream(t *testing.T) {
	shipped := &fakeSonarr{
		series:   []arrapi.Series{{ID: 1, TvdbID: 11}},
		episodes: map[int][]arrapi.Episode{},
	}
	c := testCachedSonarr(shipped, &fakeSonarr{}, testGate(t.Context()))

	if _, err := c.Series(t.Context()); err != nil {
		t.Fatal(err)
	}
	// 99 is not in the cached list; a PLAIN read keeps today's behavior and
	// calls upstream anyway.
	eps, err := c.Episodes(t.Context(), 99)
	if err != nil {
		t.Fatalf("plain episodes read failed: %v", err)
	}
	if len(eps) != 0 {
		t.Fatalf("episodes = %v, want the upstream's empty answer", eps)
	}
	if got := shipped.episodeCallCount(99); got != 1 {
		t.Errorf("upstream episodes calls = %d, want 1 (miss falls through)", got)
	}
}

func TestWantedEpisodes_scanBypassRegistersWriteThrough(t *testing.T) {
	shipped := &fakeSonarr{
		series: []arrapi.Series{{ID: 1, TvdbID: 11}},
		episodes: map[int][]arrapi.Episode{
			1: {{ID: 101, HasFile: true, EpisodeFile: &arrapi.EpisodeFile{SceneName: "x"}}},
		},
	}
	c := testCachedSonarr(shipped, &fakeSonarr{}, testGate(t.Context()))

	var seen int
	err := c.WantedEpisodes(t.Context(), nil, func(_ arrapi.Series, _ arrapi.Episode) error {
		seen++
		return nil
	})
	if err != nil || seen != 1 {
		t.Fatalf("WantedEpisodes seen=%d err=%v, want the one wanted episode", seen, err)
	}

	// The scan's fetches registered: a plain read now serves from cache.
	if _, err := c.Series(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s, _ := shipped.counts(); s != 1 {
		t.Errorf("series calls = %d, want 1 (the scan's fetch seeded the cache)", s)
	}
	if _, err := c.Episodes(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if got := shipped.episodeCallCount(1); got != 1 {
		t.Errorf("episode calls = %d, want 1", got)
	}
	// The write-through registered as a wave: the floor clock is set.
	ks := c.table.key(keySeriesList)
	ks.mu.Lock()
	floorSet := !ks.lastWaveStart.IsZero()
	ks.mu.Unlock()
	if !floorSet {
		t.Error("the scan bypass did not reset the floor clock")
	}
}

// The scan's episode write-throughs are what make the episodes family grow
// with the LIBRARY, so the resident set stays bounded across a full walk while
// the series list — a different family, a different store — survives it.
func TestWantedEpisodes_scanEpisodeRetentionIsBounded(t *testing.T) {
	const seriesCount = maxResidentEpisodeEntries * 4
	shipped := &fakeSonarr{episodes: make(map[int][]arrapi.Episode, seriesCount)}
	for i := 1; i <= seriesCount; i++ {
		shipped.series = append(shipped.series, arrapi.Series{ID: i, TvdbID: 1000 + i})
		shipped.episodes[i] = []arrapi.Episode{
			{ID: 100000 + i, HasFile: true, EpisodeFile: &arrapi.EpisodeFile{SceneName: "x"}},
		}
	}
	c := testCachedSonarr(shipped, &fakeSonarr{}, testGate(t.Context()))

	var seen int
	if err := c.WantedEpisodes(t.Context(), nil, func(_ arrapi.Series, _ arrapi.Episode) error {
		seen++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != seriesCount {
		t.Fatalf("WantedEpisodes walked %d episodes, want %d", seen, seriesCount)
	}

	if got := c.table.episodes.resident(); got > maxResidentEpisodeEntries {
		t.Errorf("resident episode entries after a %d-series scan = %d, want <= %d",
			seriesCount, got, maxResidentEpisodeEntries)
	}
	if _, ok := c.table.lookup(keySeriesList); !ok {
		t.Error("the series-list entry was swept with the episodes; the bound must be scoped to that family")
	}
}

// The bound changes retention, never the write-through's wave semantics: at an
// episodes key it still resets the floor clock and still wins as the newest
// write, and a sweep leaves the floor clock standing.
func TestWriteThrough_episodesKeyKeepsItsWaveSemantics(t *testing.T) {
	c := testCachedSonarr(&fakeSonarr{}, &fakeSonarr{}, testGate(t.Context()))
	key := episodesKey(1)

	begin := time.Now()
	c.table.writeThrough(key, readEntry{payload: []arrapi.Episode{{ID: 101}}, readBegin: begin})
	if e, ok := c.table.lookup(key); !ok || payloadAs[[]arrapi.Episode](e.payload)[0].ID != 101 {
		t.Fatalf("entry = %v (ok=%v), want the write-through's episodes", e.payload, ok)
	}
	ks := c.table.key(key)
	ks.mu.Lock()
	floor := ks.lastWaveStart
	ks.mu.Unlock()
	if !floor.Equal(begin) {
		t.Errorf("floor clock = %v, want the write-through's read-begin %v", floor, begin)
	}

	// An older write-through loses to the entry already held.
	c.table.writeThrough(key, readEntry{payload: []arrapi.Episode{{ID: 999}}, readBegin: begin.Add(-time.Second)})
	if e, _ := c.table.lookup(key); payloadAs[[]arrapi.Episode](e.payload)[0].ID != 101 {
		t.Errorf("entry = %v, want the newer write to survive a stale write-through", e.payload)
	}

	// Sweeping the generation drops the payload, never the key's floor clock.
	for i := range maxResidentEpisodeEntries * 2 {
		c.table.put(episodesKey(1000+i), readEntry{payload: []arrapi.Episode{}, readBegin: time.Now()})
	}
	if _, ok := c.table.lookup(key); ok {
		t.Fatalf("key %q survived %d newer episode entries; the bound did not bind", key, maxResidentEpisodeEntries*2)
	}
	ks.mu.Lock()
	floor, commit := ks.lastWaveStart, ks.lastCommit
	ks.mu.Unlock()
	if !floor.Equal(begin) {
		t.Errorf("floor clock after a sweep = %v, want the retained %v", floor, begin)
	}
	if !commit.Equal(begin) {
		t.Errorf("write-ordering clock after a sweep = %v, want the retained %v", commit, begin)
	}
}

func TestWantedMovies_scanBypassRegistersWriteThrough(t *testing.T) {
	shipped := &fakeRadarr{
		movies: []arrapi.Movie{{ID: 5, TmdbID: 55, HasFile: true, MovieFile: &arrapi.MovieFile{SceneName: "m"}}},
	}
	c := testCachedRadarr(shipped, &fakeRadarr{}, testGate(t.Context()))

	var seen int
	err := c.WantedMovies(t.Context(), nil, func(_ arrapi.Movie) error {
		seen++
		return nil
	})
	if err != nil || seen != 1 {
		t.Fatalf("WantedMovies seen=%d err=%v", seen, err)
	}
	if _, err := c.Movies(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m, _ := shipped.counts(); m != 1 {
		t.Errorf("movie calls = %d, want 1 (the scan's fetch seeded the cache)", m)
	}
}

func TestCachedSonarr_reloadPublishesFreshInstance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := testGate(t.Context())
		oldWave := &fakeSonarr{series: []arrapi.Series{{ID: 1, TvdbID: 11}}}
		oldC := testCachedSonarr(&fakeSonarr{}, oldWave, gate)

		// A wave is mid-flight on the old instance when the reload publishes
		// the rebuilt clients.
		block := make(chan struct{})
		blockingFetch := func(ctx context.Context) (any, error) {
			select {
			case <-block:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			rows, err := oldWave.Series(ctx)
			if err != nil {
				return nil, err
			}
			return newSeriesSnapshot(rows), nil
		}
		rctx := WithRecovery(t.Context())
		rec, _ := recoveryFrom(rctx)
		oldRead := make(chan error, 1)
		go func() {
			_, err := oldC.table.waveRead(rctx, rec, keySeriesList, blockingFetch)
			oldRead <- err
		}()
		synctest.Wait()

		newShipped := &fakeSonarr{series: []arrapi.Series{{ID: 9, TvdbID: 99}}}
		newC := testCachedSonarr(newShipped, &fakeSonarr{}, gate)

		close(block) // the old wave lands — into the orphaned instance only
		if err := <-oldRead; err != nil {
			t.Fatalf("old wave read failed: %v", err)
		}

		rows, err := newC.Series(t.Context())
		if err != nil || len(rows) != 1 || rows[0].ID != 9 {
			t.Fatalf("post-reload Series = %v, %v; want the new instance's data", rows, err)
		}
		if e, ok := newC.table.cache.Get(keySeriesList); !ok || e.payload.(seriesSnapshot).rows[0].ID != 9 {
			t.Errorf("post-reload cache = %v (ok=%v); the old wave's write must not land in the new instance", e.payload, ok)
		}
	})
}

func TestCachedSonarr_SeriesByTvdbID(t *testing.T) {
	shipped := &fakeSonarr{series: []arrapi.Series{
		{ID: 1, TvdbID: 11},
		{ID: 2, TvdbID: 22, Title: "Two"},
	}}
	c := testCachedSonarr(shipped, &fakeSonarr{}, testGate(t.Context()))

	ser, found, err := c.SeriesByTvdbID(t.Context(), 22)
	if err != nil || !found {
		t.Fatalf("SeriesByTvdbID(22) = found %v, err %v; want the indexed row", found, err)
	}
	if ser.ID != 2 || ser.Title != "Two" {
		t.Errorf("SeriesByTvdbID(22) = %+v, want ID 2 / Title \"Two\"", ser)
	}

	// Absence is a normal answer: found false, nil error — and lookups share
	// the ONE cached list entry, so no extra upstream call is made.
	_, found, err = c.SeriesByTvdbID(t.Context(), 404404)
	if err != nil || found {
		t.Errorf("SeriesByTvdbID(unknown) = found %v, err %v; want false, nil", found, err)
	}
	if s, _ := shipped.counts(); s != 1 {
		t.Errorf("shipped series calls = %d, want 1 (index lookups share the list entry)", s)
	}
}

func TestCachedSonarr_SeriesByTvdbID_propagates_fetch_error(t *testing.T) {
	errUpstream := errors.New("upstream down")
	shipped := &fakeSonarr{err: errUpstream}
	c := testCachedSonarr(shipped, &fakeSonarr{}, testGate(t.Context()))

	_, found, err := c.SeriesByTvdbID(t.Context(), 11)
	if !errors.Is(err, errUpstream) || found {
		t.Errorf("SeriesByTvdbID(fetch error) = found %v, err %v; want false, errUpstream", found, err)
	}
}

func TestCachedRadarr_MovieByTmdbID(t *testing.T) {
	shipped := &fakeRadarr{movies: []arrapi.Movie{
		{ID: 5, TmdbID: 55},
		{ID: 6, TmdbID: 66, Title: "Six"},
	}}
	c := testCachedRadarr(shipped, &fakeRadarr{}, testGate(t.Context()))

	m, found, err := c.MovieByTmdbID(t.Context(), 66)
	if err != nil || !found {
		t.Fatalf("MovieByTmdbID(66) = found %v, err %v; want the indexed row", found, err)
	}
	if m.ID != 6 || m.Title != "Six" {
		t.Errorf("MovieByTmdbID(66) = %+v, want ID 6 / Title \"Six\"", m)
	}

	_, found, err = c.MovieByTmdbID(t.Context(), 404404)
	if err != nil || found {
		t.Errorf("MovieByTmdbID(unknown) = found %v, err %v; want false, nil", found, err)
	}
	if mv, _ := shipped.counts(); mv != 1 {
		t.Errorf("shipped movie calls = %d, want 1 (index lookups share the list entry)", mv)
	}
}

// The wrapper builds TWO transports — the shipped 3-attempt client and its own
// single-attempt wave client — so its Close has to release both. It OVERRIDES
// the Close promoted from the embedded *Sonarr, and that is the whole hazard:
// deleting the override leaves the promoted method, which still satisfies the
// `interface{ Close() }` assertion activation reaches it through
// (server/reload.go closeArrClient), so the wave transport would leak with no
// compile error anywhere. Nothing else can catch that, which is why these two
// tests exist.
func TestNewCachedSonarr_Close_releases_the_wave_transport(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	c, err := NewCachedSonarr(srv.URL, APIKey("k"), testGate(t.Context()))
	if err != nil {
		t.Fatalf("NewCachedSonarr: %v", err)
	}
	if c.waveClose == nil {
		t.Fatal("NewCachedSonarr left waveClose nil; the wave transport has no owner")
	}
	waveClosed := 0
	c.waveClose = func() { waveClosed++ }

	c.Close()

	if waveClosed != 1 {
		t.Errorf("wave closes after one Close() = %d, want 1", waveClosed)
	}
}

func TestNewCachedRadarr_Close_releases_the_wave_transport(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	c, err := NewCachedRadarr(srv.URL, APIKey("k"), testGate(t.Context()))
	if err != nil {
		t.Fatalf("NewCachedRadarr: %v", err)
	}
	if c.waveClose == nil {
		t.Fatal("NewCachedRadarr left waveClose nil; the wave transport has no owner")
	}
	waveClosed := 0
	c.waveClose = func() { waveClosed++ }

	c.Close()

	if waveClosed != 1 {
		t.Errorf("wave closes after one Close() = %d, want 1", waveClosed)
	}
}
