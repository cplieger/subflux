package polling

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

var holdT0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

const excludeTagID = 7

// historyArr serves a fixed, date-ordered Sonarr history honouring since.
// When gate is set, the next HistorySince signals entered after reading its
// since and waits for release before answering.
type historyArr struct {
	entered, release chan struct{}
	seriesErr        error
	entries          []arrapi.HistoryRecord
	sinces           []time.Time
	fetchedAt        []time.Time
	original         *arrapi.Language
	excluded         int // a series id the config excludes by excludeTagID; 0 for none
	seriesReads      int
	mu               sync.Mutex
	gate             bool
}

func (a *historyArr) HistorySince(_ context.Context, since time.Time, _ ...arrapi.EventType) ([]arrapi.HistoryRecord, error) {
	a.mu.Lock()
	a.sinces = append(a.sinces, since)
	a.fetchedAt = append(a.fetchedAt, time.Now())
	gate := a.gate
	a.gate = false
	a.mu.Unlock()
	if gate {
		a.entered <- struct{}{}
		<-a.release
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []arrapi.HistoryRecord
	for _, e := range a.entries {
		if !e.Date.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (a *historyArr) lastSince() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sinces[len(a.sinces)-1]
}

// reads returns the HistorySince call times and the SeriesByID call count.
func (a *historyArr) reads() (fetches []time.Time, series int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.fetchedAt), a.seriesReads
}

func (a *historyArr) setSeriesErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seriesErr = err
}

func (a *historyArr) SeriesByID(_ context.Context, id int) (arrapi.Series, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seriesReads++
	s := arrapi.Series{ID: id, Title: "Show", OriginalLanguage: a.original}
	if a.excluded != 0 && id == a.excluded {
		s.Tags = []int{excludeTagID}
	}
	return s, a.seriesErr
}

func (*historyArr) EpisodeByID(context.Context, int) (arrapi.Episode, error) {
	return arrapi.Episode{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, HasFile: true}, nil
}

func (a *historyArr) ResolveExcludeTagIDs(context.Context, []string, bool) map[int]struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.excluded == 0 {
		return nil
	}
	return map[int]struct{}{excludeTagID: {}}
}

func (a *historyArr) exclude(seriesID int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.excluded = seriesID
}

func (*historyArr) RescanSeries(context.Context, int) error { return nil }

// holdEngine records the paths it searched and how many targets each search
// asked for; fail decides a path's result.
type holdEngine struct {
	fail    func(path string) (subflux.SearchResult, error)
	calls   map[string]int
	targets []int
	mu      sync.Mutex
}

func (e *holdEngine) SearchTargets(_ context.Context, _ *subflux.SearchRequest, path string, targets []subflux.SubtitleTarget) (subflux.SearchResult, error) {
	e.mu.Lock()
	e.calls[path]++
	e.targets = append(e.targets, len(targets))
	fail := e.fail
	e.mu.Unlock()
	if fail != nil {
		return fail(path)
	}
	return subflux.SearchResult{Langs: []subflux.LangOutcome{{Lang: "en", Kind: subflux.LangSearched, Searched: 1, Answered: 1}}}, nil
}

func (e *holdEngine) setFail(fn func(path string) (subflux.SearchResult, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fail = fn
}

func (e *holdEngine) count(path string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[path]
}

func (e *holdEngine) total() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, c := range e.calls {
		n += c
	}
	return n
}

// validatingCfg is mockCfg with the real media-root containment check.
type validatingCfg struct {
	*mockCfg
	cfg *config.Config
}

func (c validatingCfg) ValidatePath(ctx context.Context, path string) error {
	return c.cfg.ValidatePath(ctx, path)
}

// ruleCfg is validatingCfg resolving language targets through the real
// config's rules.
type ruleCfg struct{ validatingCfg }

func (c ruleCfg) ResolveTargetsWithFallback(orig string, audio []string) []subflux.SubtitleTarget {
	return c.cfg.ResolveTargetsWithFallback(orig, audio)
}

type writeCall struct {
	path string
	size int
}

// holdRig is a poller over a t.TempDir() media root with a real media writer
// whose writes fail for the folders in bad, and race (an inconclusive write
// test) for the folders in racing.
type holdRig struct {
	p      *Poller
	arr    *historyArr
	engine *holdEngine
	mw     *mediawrite.Writer
	alerts *mockAlerts
	bad    map[string]bool
	racing map[string]bool
	root   string
	writes []writeCall
	mu     sync.Mutex
}

func newHoldRig(t *testing.T) *holdRig {
	t.Helper()
	r := &holdRig{
		arr:    &historyArr{entered: make(chan struct{}), release: make(chan struct{})},
		engine: &holdEngine{calls: map[string]int{}},
		alerts: &mockAlerts{},
		bad:    map[string]bool{},
		racing: map[string]bool{},
		root:   t.TempDir(),
	}
	mw, err := mediawrite.New(mediawrite.Config{
		Metrics: testsupport.NopMediaMetrics{}, Alerts: testsupport.NopAlerts{},
		Write: func(_ context.Context, p string, data []byte) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.writes = append(r.writes, writeCall{path: p, size: len(data)})
			if r.bad[filepath.Dir(p)] {
				return &fs.PathError{Op: "open", Path: p, Err: syscall.EROFS}
			}
			if r.racing[filepath.Dir(p)] {
				return fmt.Errorf("write %s: %w", p, atomicfile.ErrRaced)
			}
			return nil
		},
		Remove: func(string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{MediaRootDirs: []string{r.root}}
	mw.Bind([]string{r.root}, cfg.ValidatePath)
	r.mw = mw

	deps := fullDeps(&mockStore{})
	deps.Alerts = r.alerts
	deps.Media = mw
	deps.PollCache.set(t.Context(), subflux.PollKeySonarr, holdT0)
	ls := &LiveState{
		Cfg: validatingCfg{mockCfg: &mockCfg{
			interval: time.Hour, langs: []string{"en"}, targets: []subflux.SubtitleTarget{{Code: "en"}},
		}, cfg: cfg},
		Engine: r.engine,
		Sonarr: r.arr,
	}
	r.p = NewPoller(deps, func() *LiveState { return ls })
	return r
}

// video creates a video file in folder (relative to the root) and returns an
// import entry for it n seconds after holdT0.
func (r *holdRig) video(t *testing.T, folder string, n int) arrapi.HistoryRecord {
	t.Helper()
	dir := filepath.Join(r.root, folder)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "e"+string(rune('0'+n))+".mkv")
	if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	return arrapi.HistoryRecord{
		ID: n, Date: holdT0.Add(time.Duration(n) * time.Second),
		SeriesID: 1, EpisodeID: n,
		Data: map[string]string{"importedPath": path},
	}
}

func (r *holdRig) setHistory(entries ...arrapi.HistoryRecord) {
	r.arr.mu.Lock()
	defer r.arr.mu.Unlock()
	r.arr.entries = entries
}

func (r *holdRig) setBad(folder string, bad bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bad[filepath.Join(r.root, folder)] = bad
}

// recover makes folder writable and runs the media writer's recheck of its
// bad folders, which is what clears the mark.
func (r *holdRig) recover(t *testing.T, folder string) {
	t.Helper()
	r.setBad(folder, false)
	if err := r.mw.Preflight(t.Context(), mediawrite.PreflightRequest{RecheckBad: true}); err != nil {
		t.Fatalf("Setup: recheck Preflight = %v", err)
	}
	if f, blocked := r.mw.Blocked(filepath.Join(r.root, folder, "x.mkv")); blocked {
		t.Fatalf("Setup: %q still marked after the recheck", f)
	}
}

// setInterval sets the configured poll interval Run reads.
func (r *holdRig) setInterval(d time.Duration) {
	r.p.stateFunc().Cfg.(validatingCfg).interval = d
}

func (r *holdRig) cursor(t *testing.T) time.Time {
	t.Helper()
	return r.p.deps.PollCache.get(t.Context(), subflux.PollKeySonarr)
}

func (r *holdRig) detectHigh() (time.Time, uint64, bool) {
	r.p.detectMu.Lock()
	defer r.p.detectMu.Unlock()
	return r.p.detectHigh[subflux.PollKeySonarr], r.p.detectGen[subflux.PollKeySonarr], r.p.behind[subflux.PollKeySonarr]
}

func (r *holdRig) probeWrites() []writeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []writeCall
	for _, w := range r.writes {
		if strings.HasPrefix(filepath.Base(w.path), ".subflux-write-probe-") {
			out = append(out, w)
		}
	}
	return out
}

func pathOf(e arrapi.HistoryRecord) string { return e.ImportedPath() }

func past(e arrapi.HistoryRecord) time.Time { return e.Date.Add(time.Millisecond) }

func TestPoller_a_failed_write_test_holds_the_batch_until_recovery(t *testing.T) {
	r := newHoldRig(t)
	e1 := r.video(t, "Bad", 1)
	r.setHistory(e1)
	r.setBad("Bad", true)

	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if r.engine.total() != 0 {
		t.Fatalf("engine searches = %d, want 0 for a held batch", r.engine.total())
	}
	if got := r.cursor(t); !got.Equal(e1.Date) {
		t.Errorf("durable cursor = %v, want at the held E1 (%v)", got, e1.Date)
	}
	if n := len(r.p.importRetries); n != 0 {
		t.Errorf("retry counters = %d, want none consumed by a hold", n)
	}
	if high, _, behind := r.detectHigh(); !high.Equal(e1.Date) || !behind {
		t.Errorf("detectHigh = %v, behind = %v; want rewound to %v and behind", high, behind, e1.Date)
	}

	r.recover(t, "Bad")
	r.p.pollOnce(t.Context())
	if got := r.arr.lastSince(); !got.Equal(e1.Date) {
		t.Errorf("re-fetch since = %v, want the held cursor %v", got, e1.Date)
	}
	drainOne(t, r.p)
	if n := r.engine.count(pathOf(e1)); n != 1 {
		t.Errorf("E1 searches = %d, want 1", n)
	}
	if got := r.cursor(t); !got.Equal(past(e1)) {
		t.Errorf("durable cursor = %v, want just past E1 (%v)", got, past(e1))
	}
}

func TestPoller_a_save_learning_a_fault_holds_mid_batch(t *testing.T) {
	sink := capture.Default(t)
	r := newHoldRig(t)
	e1, e2, e3 := r.video(t, "A", 1), r.video(t, "A", 2), r.video(t, "A", 3)
	r.setHistory(e1, e2, e3)
	r.engine.setFail(func(path string) (subflux.SearchResult, error) {
		if path != pathOf(e2) {
			return subflux.SearchResult{}, nil
		}
		uerr := &mediawrite.UnwritableError{Folder: filepath.Dir(path), Op: "write", Err: syscall.EROFS}
		return subflux.SearchResult{WriteFailure: uerr}, uerr
	})

	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if a, b, c := r.engine.count(pathOf(e1)), r.engine.count(pathOf(e2)), r.engine.count(pathOf(e3)); a != 1 || b != 1 || c != 0 {
		t.Fatalf("searches E1 %d, E2 %d, E3 %d; want 1, 1, 0 (stopped at E2)", a, b, c)
	}
	if got := r.cursor(t); !got.Equal(e2.Date) {
		t.Errorf("durable cursor = %v, want at the held E2 (%v)", got, e2.Date)
	}
	if sink.CountLevel(slog.LevelError, "poll: subtitle search failed") != 0 || len(r.alerts.warns) != 0 {
		t.Errorf("a held entry logged a search ERROR or raised %v", r.alerts.warns)
	}

	r.engine.setFail(nil)
	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if a, b, c := r.engine.count(pathOf(e1)), r.engine.count(pathOf(e2)), r.engine.count(pathOf(e3)); a != 1 || b != 2 || c != 1 {
		t.Errorf("searches after recovery E1 %d, E2 %d, E3 %d; want 1, 2, 1", a, b, c)
	}
	if got := r.cursor(t); !got.Equal(past(e3)) {
		t.Errorf("durable cursor = %v, want past E3 (%v)", got, past(e3))
	}
}

// A refused folder anywhere in a batch holds the whole batch before any
// search, so no entry ahead of it reaches a provider while the folder stays
// unwritable, and each entry searches once after it recovers.
func TestPoller_a_later_unwritable_folder_holds_the_whole_batch_until_it_recovers(t *testing.T) {
	r := newHoldRig(t)
	e1, e2, e3 := r.video(t, "A", 1), r.video(t, "Bad", 2), r.video(t, "C", 3)
	r.setHistory(e1, e2, e3)
	r.setBad("Bad", true)

	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	for range 2 {
		r.p.pollOnce(t.Context())
	}
	if got := r.cursor(t); !got.Equal(e1.Date) {
		t.Errorf("durable cursor = %v, want at the batch's first entry E1 (%v)", got, e1.Date)
	}
	if fetches, _ := r.arr.reads(); len(fetches) != 1 || len(r.p.work) != 0 {
		t.Errorf("history fetches %d, queued batches %d over three cycles; want 1 and 0 while E2's folder stays marked",
			len(fetches), len(r.p.work))
	}
	if n := r.engine.total(); n != 0 {
		t.Errorf("engine searches over three held cycles = %d, want 0", n)
	}
	if n := len(r.p.importRetries); n != 0 {
		t.Errorf("retry counters = %d, want none consumed by a hold", n)
	}
	if _, _, behind := r.detectHigh(); !behind {
		t.Error("behind is unset while E2's folder is still unwritable")
	}

	r.recover(t, "Bad")
	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	for _, e := range []arrapi.HistoryRecord{e1, e2, e3} {
		if n := r.engine.count(pathOf(e)); n != 1 {
			t.Errorf("searches of %s after recovery = %d, want 1", pathOf(e), n)
		}
	}
	if got := r.cursor(t); !got.Equal(past(e3)) {
		t.Errorf("durable cursor after recovery = %v, want past E3 (%v)", got, past(e3))
	}
}

// A folder another save learns is unwritable after the batch's write test
// passed holds the entry that writes there, before it searches.
func TestPoller_a_fault_learned_mid_batch_holds_the_entry_before_it_searches(t *testing.T) {
	r := newHoldRig(t)
	e1, e2, e3 := r.video(t, "A", 1), r.video(t, "Bad", 2), r.video(t, "C", 3)
	r.setHistory(e1, e2, e3)
	r.engine.setFail(func(path string) (subflux.SearchResult, error) {
		if path == pathOf(e1) {
			r.setBad("Bad", true)
			if err := r.mw.WriteFile(t.Context(), filepath.Join(filepath.Dir(pathOf(e2)), "other.en.srt"), []byte("1")); err == nil {
				t.Error("Setup: the save into the unwritable folder succeeded")
			}
		}
		return subflux.SearchResult{}, nil
	})

	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if a, b, c := r.engine.count(pathOf(e1)), r.engine.count(pathOf(e2)), r.engine.count(pathOf(e3)); a != 1 || b != 0 || c != 0 {
		t.Errorf("searches E1 %d, E2 %d, E3 %d; want 1, 0, 0", a, b, c)
	}
	if got := r.cursor(t); !got.Equal(e2.Date) {
		t.Errorf("durable cursor = %v, want at the held E2 (%v)", got, e2.Date)
	}
	r.p.pollOnce(t.Context())
	if fetches, _ := r.arr.reads(); len(fetches) != 1 || len(r.p.work) != 0 {
		t.Errorf("history fetches %d, queued batches %d after the hold; want 1 and 0 while E2's folder stays marked",
			len(fetches), len(r.p.work))
	}
}

// A completed batch ends the hold, so a later fault in the same folder that
// holds nothing of this source does not stop its detection.
func TestPoller_a_released_source_ignores_a_later_mark_on_its_old_folder(t *testing.T) {
	r := newHoldRig(t)
	e1, e2 := r.video(t, "Bad", 1), r.video(t, "Other", 2)
	r.setHistory(e1)
	r.setBad("Bad", true)
	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	r.recover(t, "Bad")
	r.p.pollOnce(t.Context())
	drainOne(t, r.p)

	r.setBad("Bad", true)
	if err := r.mw.WriteFile(t.Context(), filepath.Join(filepath.Dir(pathOf(e1)), "other.en.srt"), []byte("1")); !errors.Is(err, mediawrite.ErrUnwritable) {
		t.Fatalf("Setup: a scan's save into the unwritable folder = %v, want ErrUnwritable", err)
	}
	r.setHistory(e1, e2)
	if n := r.p.pollOnce(t.Context()); n != 1 || len(r.p.work) != 1 {
		t.Errorf("PollOnce after the release = %d new, %d queued batches; want E2 fetched (1 and 1)", n, len(r.p.work))
	}
}

// A save that confirms its folder unwritable holds the source on that
// folder, whether the save failed or a target was skipped for the known
// fault: nothing is fetched until the writer clears the folder.
func TestPoller_a_hold_a_save_learned_waits_for_the_folder(t *testing.T) {
	cases := []struct {
		name     string
		reported func(uerr error) (subflux.SearchResult, error)
	}{
		{name: "save_error", reported: func(uerr error) (subflux.SearchResult, error) {
			return subflux.SearchResult{WriteFailure: uerr}, uerr
		}},
		{name: "target_skipped", reported: func(error) (subflux.SearchResult, error) {
			return subflux.SearchResult{Langs: []subflux.LangOutcome{{Lang: "en", Kind: subflux.LangWriteBlocked, WriteBlocked: 1}}}, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newHoldRig(t)
			e1 := r.video(t, "A", 1)
			r.setHistory(e1)
			r.engine.setFail(func(path string) (subflux.SearchResult, error) {
				r.setBad("A", true)
				werr := r.mw.WriteFile(t.Context(), strings.TrimSuffix(path, ".mkv")+".en.srt", []byte("1"))
				if !errors.Is(werr, mediawrite.ErrUnwritable) {
					t.Errorf("Setup: save into the unwritable folder = %v, want ErrUnwritable", werr)
				}
				return tc.reported(werr)
			})

			r.p.pollOnce(t.Context())
			drainOne(t, r.p)
			r.p.pollOnce(t.Context())
			if fetches, _ := r.arr.reads(); len(fetches) != 1 || len(r.p.work) != 0 {
				t.Errorf("history fetches %d, queued batches %d after the hold; want 1 and 0 while the folder stays marked",
					len(fetches), len(r.p.work))
			}

			r.recover(t, "A")
			r.engine.setFail(nil)
			r.p.pollOnce(t.Context())
			drainOne(t, r.p)
			if n := r.engine.count(pathOf(e1)); n != 2 {
				t.Errorf("E1 searches = %d, want 2 (held once, run once after recovery)", n)
			}
			if got := r.cursor(t); !got.Equal(past(e1)) {
				t.Errorf("durable cursor = %v, want past E1 (%v)", got, past(e1))
			}
		})
	}
}

// A lone transiently-failed entry, once it succeeds, moves the cursor past
// itself: the batch that re-fetched it started exactly at its date.
func TestPoller_a_recovered_lone_entry_is_not_searched_again(t *testing.T) {
	r := newHoldRig(t)
	e1 := r.video(t, "A", 1)
	r.setHistory(e1)
	r.arr.setSeriesErr(errors.New("sonarr restarting"))
	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	r.arr.setSeriesErr(nil)

	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if got := r.cursor(t); !got.Equal(past(e1)) {
		t.Errorf("durable cursor = %v, want past E1 (%v)", got, past(e1))
	}
	if n := r.p.pollOnce(t.Context()); n != 0 {
		t.Errorf("a third poll fetched %d entries, want none", n)
	}
	if n := r.engine.count(pathOf(e1)); n != 1 {
		t.Errorf("E1 searches = %d, want 1", n)
	}
}

// History out of date order: an entry listed after one a save held but dated
// before it stays above the cursor, so it is not lost.
func TestPoller_a_hold_keeps_an_earlier_dated_unprocessed_entry(t *testing.T) {
	r := newHoldRig(t)
	e1, e2, e3 := r.video(t, "A", 1), r.video(t, "B", 2), r.video(t, "C", 3)
	r.engine.setFail(func(path string) (subflux.SearchResult, error) {
		if path != pathOf(e3) {
			return subflux.SearchResult{}, nil
		}
		uerr := &mediawrite.UnwritableError{Folder: filepath.Dir(path), Op: "write", Err: syscall.EROFS}
		return subflux.SearchResult{WriteFailure: uerr}, uerr
	})
	r.p.enqueue(&sourceBatch{
		source: pollSourceSonarr, key: subflux.PollKeySonarr, since: holdT0,
		entries: []arrapi.HistoryRecord{e1, e3, e2},
	})

	drainOne(t, r.p)
	if got := r.cursor(t); !got.Equal(e2.Date) {
		t.Errorf("durable cursor = %v, want at the unprocessed E2 (%v)", got, e2.Date)
	}
	if n := r.engine.count(pathOf(e2)); n != 0 {
		t.Errorf("E2 searches = %d, want 0 behind the held E3", n)
	}
}

func TestPoller_a_queued_batch_does_not_overtake_a_hold(t *testing.T) {
	r := newHoldRig(t)
	e1, e2 := r.video(t, "Bad", 1), r.video(t, "Good", 2)
	r.setBad("Bad", true)
	r.setHistory(e1)
	r.p.pollOnce(t.Context())
	r.setHistory(e1, e2)
	r.p.pollOnce(t.Context())
	if n := len(r.p.work); n != 2 {
		t.Fatalf("Setup: queued batches = %d, want 2", n)
	}

	drainOne(t, r.p)
	drainOne(t, r.p)
	if r.engine.total() != 0 {
		t.Errorf("engine searches = %d, want 0 (A held, B discarded)", r.engine.total())
	}
	if got := r.cursor(t); !got.Equal(e1.Date) {
		t.Errorf("durable cursor = %v, want at the held E1 (%v)", got, e1.Date)
	}

	r.recover(t, "Bad")
	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if a, b := r.engine.count(pathOf(e1)), r.engine.count(pathOf(e2)); a != 1 || b != 1 {
		t.Errorf("searches E1 %d, E2 %d; want 1 and 1", a, b)
	}
	if got := r.cursor(t); !got.Equal(past(e2)) {
		t.Errorf("durable cursor = %v, want past E2 (%v)", got, past(e2))
	}
}

func TestPoller_a_queued_batch_does_not_overtake_a_transient_failure(t *testing.T) {
	r := newHoldRig(t)
	e1, e2 := r.video(t, "A", 1), r.video(t, "B", 2)
	r.setHistory(e1)
	r.p.pollOnce(t.Context())
	r.setHistory(e1, e2)
	r.p.pollOnce(t.Context())

	r.arr.setSeriesErr(errors.New("sonarr restarting"))
	drainOne(t, r.p)
	r.arr.setSeriesErr(nil)
	drainOne(t, r.p)
	if r.engine.total() != 0 {
		t.Errorf("engine searches = %d, want 0 (E1 failed its metadata fetch, B discarded)", r.engine.total())
	}
	if got := r.cursor(t); !got.Equal(e1.Date) {
		t.Errorf("durable cursor = %v, want held at E1 (%v)", got, e1.Date)
	}

	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if a, b := r.engine.count(pathOf(e1)), r.engine.count(pathOf(e2)); a != 1 || b != 1 {
		t.Errorf("searches E1 %d, E2 %d; want 1 and 1", a, b)
	}
	if got := r.cursor(t); !got.Equal(past(e2)) {
		t.Errorf("durable cursor = %v, want past E2 (%v)", got, past(e2))
	}
}

// An entry the arr tag excludes is skipped before its folder is tested, so a
// read-only excluded folder neither marks anything nor holds later imports.
func TestPoller_an_excluded_entry_in_an_unwritable_folder_does_not_hold(t *testing.T) {
	r := newHoldRig(t)
	e1 := r.video(t, "Excluded", 1)
	r.setBad("Excluded", true)
	r.arr.exclude(e1.SeriesID)
	r.setHistory(e1)

	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if n := len(r.probeWrites()); n != 0 {
		t.Errorf("probe writes = %d, want 0 for an excluded entry", n)
	}
	if f, blocked := r.mw.Blocked(pathOf(e1)); blocked {
		t.Errorf("Blocked = %q, true; want the excluded folder unmarked", f)
	}
	if got := r.cursor(t); !got.Equal(past(e1)) {
		t.Errorf("durable cursor = %v, want past the excluded entry (%v)", got, past(e1))
	}
	if _, _, behind := r.detectHigh(); behind {
		t.Error("an excluded entry set behind")
	}
}

// A rule with `subtitles: []` asks for nothing, so the entry is inventoried
// without a write test and an unwritable folder holds no later import.
func TestPoller_an_entry_whose_rule_asks_for_no_subtitle_does_not_hold(t *testing.T) {
	r := newHoldRig(t)
	e1 := r.video(t, "Bad", 1)
	r.setBad("Bad", true)
	r.arr.mu.Lock()
	r.arr.original = &arrapi.Language{Name: "English"}
	r.arr.mu.Unlock()
	cfg := &config.Config{
		MediaRootDirs: []string{r.root},
		Languages:     config.LanguageRules{Rules: []config.AudioRule{{Audio: "en", Subtitles: nil}}},
	}
	ls := r.p.stateFunc()
	ls.Cfg = ruleCfg{validatingCfg{mockCfg: &mockCfg{interval: time.Hour, langs: []string{"en"}}, cfg: cfg}}
	r.setHistory(e1)

	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if n := len(r.probeWrites()); n != 0 {
		t.Errorf("probe writes = %d, want 0 for an entry with no targets", n)
	}
	if f, blocked := r.mw.Blocked(pathOf(e1)); blocked {
		t.Errorf("Blocked = %q, true; want the folder unmarked", f)
	}
	r.engine.mu.Lock()
	targets := slices.Clone(r.engine.targets)
	r.engine.mu.Unlock()
	if !slices.Equal(targets, []int{0}) {
		t.Errorf("searches asked for %v targets, want one inventory search with none", targets)
	}
	if got := r.cursor(t); !got.Equal(past(e1)) {
		t.Errorf("durable cursor = %v, want past the entry (%v)", got, past(e1))
	}
	if _, _, behind := r.detectHigh(); behind {
		t.Error("an entry with no targets set behind")
	}
}

// A poller stopping mid-batch ends it with the cursor where it was: an entry
// decided under the cancelled context is neither stepped past nor held.
func TestPoller_a_batch_cut_by_shutdown_leaves_the_cursor_and_holds_nothing(t *testing.T) {
	r := newHoldRig(t)
	e1 := r.video(t, "Show", 1)
	r.setHistory(e1)
	r.p.pollOnce(t.Context())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	b := <-r.p.work
	r.p.executeBatch(ctx, &b)
	if r.engine.total() != 0 {
		t.Errorf("engine searches = %d, want 0 after the shutdown", r.engine.total())
	}
	if got := r.cursor(t); !got.Equal(holdT0) {
		t.Errorf("durable cursor = %v, want unchanged %v", got, holdT0)
	}
	if _, _, behind := r.detectHigh(); behind {
		t.Error("a shutdown set behind")
	}
}

func TestPoller_an_out_of_root_entry_probes_nothing(t *testing.T) {
	r := newHoldRig(t)
	outside := filepath.Join(t.TempDir(), "x.mkv")
	if err := os.WriteFile(outside, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := arrapi.HistoryRecord{ID: 1, Date: holdT0.Add(time.Second), Data: map[string]string{"importedPath": outside}}
	r.setHistory(e)

	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if n := len(r.probeWrites()); n != 0 {
		t.Errorf("probe writes = %d, want 0 for a folder outside the root", n)
	}
	if got := r.cursor(t); !got.Equal(past(e)) {
		t.Errorf("durable cursor = %v, want past the skipped entry (%v)", got, past(e))
	}
}

func TestPoller_a_vanished_folder_does_not_hold(t *testing.T) {
	r := newHoldRig(t)
	gone := filepath.Join(r.root, "Gone", "e.mkv")
	e := arrapi.HistoryRecord{ID: 1, Date: holdT0.Add(time.Second), Data: map[string]string{"importedPath": gone}}
	r.setHistory(e)
	store := r.p.deps.Store.(*mockStore)

	r.p.pollOnce(t.Context())
	drainOne(t, r.p)
	if len(store.deletedPaths) != 1 {
		t.Errorf("gone-file cleanups = %d, want 1 (the entry took the gone-file path)", len(store.deletedPaths))
	}
	if got := r.cursor(t); !got.Equal(past(e)) {
		t.Errorf("durable cursor = %v, want past the entry (%v)", got, past(e))
	}
}

func TestPoller_a_detection_in_flight_across_a_hold_does_not_stall_the_source(t *testing.T) {
	sink := capture.Default(t)
	r := newHoldRig(t)
	e1, e2 := r.video(t, "Bad", 1), r.video(t, "Good", 2)
	r.setBad("Bad", true)
	r.setHistory(e1)
	r.p.pollOnce(t.Context())

	r.setHistory(e1, e2)
	r.arr.mu.Lock()
	r.arr.gate = true
	r.arr.mu.Unlock()
	done := make(chan struct{})
	go func() { r.p.pollOnce(t.Context()); close(done) }()
	<-r.arr.entered
	drainOne(t, r.p)
	close(r.arr.release)
	<-done

	drainOne(t, r.p)
	if r.engine.total() != 0 {
		t.Errorf("engine searches = %d, want 0 (B discarded)", r.engine.total())
	}
	if n := sink.CountLevel(slog.LevelDebug, "poll: batch detected past a held batch, discarded"); n != 1 {
		t.Errorf("discard DEBUG records = %d, want 1", n)
	}

	r.recover(t, "Bad")
	r.p.pollOnce(t.Context())
	if got := r.arr.lastSince(); !got.Equal(e1.Date) {
		t.Fatalf("next detection since = %v, want the durable cursor at E1 (%v)", got, e1.Date)
	}
	drainOne(t, r.p)
	if a, b := r.engine.count(pathOf(e1)), r.engine.count(pathOf(e2)); a != 1 || b != 1 {
		t.Errorf("searches E1 %d, E2 %d; want 1 and 1", a, b)
	}
	if got := r.cursor(t); !got.Equal(past(e2)) {
		t.Errorf("durable cursor = %v, want past E2 (%v)", got, past(e2))
	}
	if _, _, behind := r.detectHigh(); behind {
		t.Error("behind is still set after a clean batch from the durable cursor")
	}
}

func TestPoller_a_per_target_write_refusal_advances_the_cursor(t *testing.T) {
	r := newHoldRig(t)
	e := r.video(t, "Show", 1)
	sub := strings.TrimSuffix(pathOf(e), ".mkv") + ".en.srt"
	data := []byte("1\n00:00:01,000 --> 00:00:02,000\nhello there\n")
	r.mu.Lock()
	r.bad = map[string]bool{}
	r.mu.Unlock()
	writes := 0
	refused, err := mediawrite.New(mediawrite.Config{
		Metrics: testsupport.NopMediaMetrics{}, Alerts: testsupport.NopAlerts{},
		Write: func(_ context.Context, p string, d []byte) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.writes = append(r.writes, writeCall{path: p, size: len(d)})
			if p == sub {
				writes++
				return &fs.PathError{Op: "rename", Path: p, Err: syscall.EACCES}
			}
			return nil
		},
		Remove: func(string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	refused.Bind([]string{r.root}, (&config.Config{MediaRootDirs: []string{r.root}}).ValidatePath)
	r.p.deps.Media = refused
	r.engine.setFail(func(string) (subflux.SearchResult, error) {
		werr := refused.WriteFile(t.Context(), sub, data)
		if uerr, ok := errors.AsType[*mediawrite.UnwritableError](werr); ok {
			return subflux.SearchResult{WriteFailure: uerr}, uerr
		}
		return subflux.SearchResult{Langs: []subflux.LangOutcome{{Lang: "en", Kind: subflux.LangSearched, Searched: 1, Answered: 1, Failed: 1}}}, nil
	})
	r.setHistory(e)

	for range 2 {
		r.p.pollOnce(t.Context())
		select {
		case b := <-r.p.work:
			r.p.executeBatch(t.Context(), &b)
		default:
		}
	}
	confirms := 0
	for _, w := range r.probeWrites() {
		if w.size == len(data) {
			confirms++
		}
	}
	if r.engine.count(pathOf(e)) != 1 || writes != 1 || confirms != 1 {
		t.Errorf("searches %d, subtitle writes %d, confirming probes %d; want 1 each",
			r.engine.count(pathOf(e)), writes, confirms)
	}
	if _, blocked := refused.Blocked(pathOf(e)); blocked {
		t.Error("a per-target refusal marked the folder")
	}
	if _, _, behind := r.detectHigh(); behind {
		t.Error("a per-target refusal set behind")
	}
	if got := r.cursor(t); !got.Equal(past(e)) {
		t.Errorf("durable cursor = %v, want past the entry (%v)", got, past(e))
	}
}

func TestPoller_a_discard_never_erases_a_valid_mark(t *testing.T) {
	r := newHoldRig(t)
	e1, e2, e3 := r.video(t, "Bad", 1), r.video(t, "Good", 2), r.video(t, "Good", 3)
	r.setBad("Bad", true)
	r.setHistory(e1, e2, e3)
	key := subflux.PollKeySonarr
	r.p.enqueue(&sourceBatch{source: pollSourceSonarr, key: key, since: holdT0, entries: []arrapi.HistoryRecord{e1}})
	r.p.enqueue(&sourceBatch{source: pollSourceSonarr, key: key, since: past(e1), entries: []arrapi.HistoryRecord{e2}})
	r.p.enqueue(&sourceBatch{source: pollSourceSonarr, key: key, since: past(e2), entries: []arrapi.HistoryRecord{e3}})

	drainOne(t, r.p)
	_, genAfterHold, _ := r.detectHigh()
	r.recover(t, "Bad")
	if r.p.detectSonarr(t.Context(), r.p.stateFunc()) != 3 {
		t.Fatal("Setup: the detection from the durable cursor did not fetch E1 to E3")
	}
	drainOne(t, r.p)
	drainOne(t, r.p)
	high, gen, _ := r.detectHigh()
	if !high.Equal(past(e3)) || gen != genAfterHold+1 {
		t.Errorf("after both discards detectHigh = %v (gen %d), want D's mark %v (gen %d)", high, gen, past(e3), genAfterHold+1)
	}
	if n := r.p.detectSonarr(t.Context(), r.p.stateFunc()); n != 0 || !r.arr.lastSince().Equal(past(e3)) {
		t.Errorf("extra detection fetched %d entries from %v, want none from D's mark %v", n, r.arr.lastSince(), past(e3))
	}
	if n := len(r.p.work); n != 1 {
		t.Fatalf("queued batches = %d, want only D", n)
	}

	drainOne(t, r.p)
	for _, e := range []arrapi.HistoryRecord{e1, e2, e3} {
		if n := r.engine.count(pathOf(e)); n != 1 {
			t.Errorf("searches for %s = %d, want 1", filepath.Base(pathOf(e)), n)
		}
	}
	if got := r.cursor(t); !got.Equal(past(e3)) {
		t.Errorf("durable cursor = %v, want past E3 (%v)", got, past(e3))
	}
	if _, _, behind := r.detectHigh(); behind {
		t.Error("behind is still set after D completed")
	}
}

// runFor runs the poller under synctest's clock for d, then returns with Run
// still running; stop ends it.
func (r *holdRig) runFor(t *testing.T, d time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		r.p.Run(ctx)
		close(done)
	}()
	time.Sleep(d)
	synctest.Wait()
	return func() {
		cancel()
		<-done
	}
}

// While a batch is held on a folder the media writer still marks, its source
// is fetched and resolved again at most once per the writer's recheck
// interval; the first cycle after the recheck clears the folder runs the
// batch.
func TestRun_a_source_held_on_a_marked_folder_reads_the_arr_once_per_recheck_interval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newHoldRig(t)
		r.setInterval(30 * time.Second)
		e1 := r.video(t, "Bad", 1)
		r.setHistory(e1)
		r.setBad("Bad", true)

		stop := r.runFor(t, 8*time.Minute+15*time.Second)
		fetches, series := r.arr.reads()
		if len(fetches) != 2 || series != 2 {
			t.Errorf("over 8m15s of a held folder: history fetches %d, series reads %d; want 2 and 2", len(fetches), series)
		}
		for i := 1; i < len(fetches); i++ {
			if gap := fetches[i].Sub(fetches[i-1]); gap < r.mw.RecheckInterval() {
				t.Errorf("history fetch %d came %v after the one before, want at least the recheck interval %v", i, gap, r.mw.RecheckInterval())
			}
		}
		if n := r.engine.total(); n != 0 {
			t.Errorf("engine searches while held = %d, want 0", n)
		}

		r.recover(t, "Bad")
		time.Sleep(30 * time.Second)
		synctest.Wait()
		stop()
		if n := r.engine.count(pathOf(e1)); n != 1 {
			t.Errorf("E1 searches after the recheck cleared its folder = %d, want 1", n)
		}
		if got := r.cursor(t); !got.Equal(past(e1)) {
			t.Errorf("durable cursor = %v, want past E1 (%v)", got, past(e1))
		}
	})
}

// A held entry that stops asking for a subtitle in its still-marked folder
// releases the source at the next bounded re-read, so a later import in a
// healthy folder is searched without waiting for the folder to recover.
func TestRun_a_hold_ends_once_the_held_entry_no_longer_needs_its_folder(t *testing.T) {
	cases := []struct {
		change func(t *testing.T, r *holdRig, e1 arrapi.HistoryRecord)
		name   string
	}{
		{name: "series_excluded_by_tag", change: func(_ *testing.T, r *holdRig, e1 arrapi.HistoryRecord) {
			r.arr.exclude(e1.SeriesID)
		}},
		{name: "video_deleted", change: func(t *testing.T, _ *holdRig, e1 arrapi.HistoryRecord) {
			if err := os.Remove(pathOf(e1)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newHoldRig(t)
				r.setInterval(30 * time.Second)
				e1 := r.video(t, "Bad", 1)
				r.setHistory(e1)
				r.setBad("Bad", true)

				stop := r.runFor(t, time.Minute)
				if _, _, behind := r.detectHigh(); !behind {
					t.Fatal("Setup: E1's unwritable folder did not hold the source")
				}
				tc.change(t, r, e1)
				e2 := r.video(t, "Good", 2)
				e2.SeriesID = 2
				r.setHistory(e1, e2)
				time.Sleep(r.mw.RecheckInterval())
				synctest.Wait()
				stop()

				fetches, _ := r.arr.reads()
				if len(fetches) < 2 {
					t.Fatalf("history fetches = %d, want the held batch re-read once the recheck interval passed", len(fetches))
				}
				if gap := fetches[1].Sub(fetches[0]); gap < r.mw.RecheckInterval() {
					t.Errorf("held source re-read %v after the hold, want at least the recheck interval %v", gap, r.mw.RecheckInterval())
				}
				if a, b := r.engine.count(pathOf(e1)), r.engine.count(pathOf(e2)); a != 0 || b != 1 {
					t.Errorf("searches E1 %d, E2 %d; want 0 and 1", a, b)
				}
				if got := r.cursor(t); !got.Equal(past(e2)) {
					t.Errorf("durable cursor = %v, want past E2 (%v)", got, past(e2))
				}
				if _, _, behind := r.detectHigh(); behind {
					t.Error("behind is still set after the re-read batch completed")
				}
				if _, blocked := r.mw.Blocked(pathOf(e1)); !blocked {
					t.Error("Setup: E1's folder was unmarked, so the hold ended by recovery instead")
				}
			})
		})
	}
}

// A refusal the writer does not mark (an inconclusive write test) re-fetches
// the held entries each cycle, and a re-fetch is not new activity: Run leaves
// its burst cadence burstPollWindow after the import was first seen.
func TestRun_re_fetching_held_entries_does_not_extend_the_burst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = 30 * time.Second
		r := newHoldRig(t)
		r.setInterval(interval)
		e1 := r.video(t, "Racing", 1)
		r.setHistory(e1)
		r.mu.Lock()
		r.racing[filepath.Join(r.root, "Racing")] = true
		r.mu.Unlock()

		stop := r.runFor(t, 6*time.Minute+15*time.Second)
		stop()
		fetches, _ := r.arr.reads()
		if len(fetches) < 3 {
			t.Fatalf("history fetches = %d, want the held entry re-fetched each cycle", len(fetches))
		}
		if gap := fetches[len(fetches)-1].Sub(fetches[len(fetches)-2]); gap != interval {
			t.Errorf("Run's interval 6m into a hold = %v, want the configured %v", gap, interval)
		}
		if n := r.engine.total(); n != 0 {
			t.Errorf("engine searches = %d, want 0 while the write test stays inconclusive", n)
		}
	})
}
