package scheduler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/auth/v6"
	"github.com/cplieger/keyenc"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/boltstore"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/httpwire"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/server"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/scheduler"
	"github.com/cplieger/subflux/internal/server/showskip"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subtitlefile"
	"github.com/cplieger/subflux/internal/testsupport"
	"github.com/cplieger/subflux/internal/wiring"
)

// The pipeline harness drives the scheduler's own full-scan entry over the
// production engine, store, arr clients, provider gate and media writer. Only
// the remote ends are fakes: Sonarr, Radarr and each provider's API are
// httptest servers, and the media writer's Write is the injected filesystem.
// Every test swaps the process-wide logger, so none runs in parallel.

const (
	goodPassword  = "placeholder-good-password"
	wrongPassword = "placeholder-wrong-password"
	arrKey        = "placeholder-arr-key"

	primary   subflux.ProviderID = "primary"
	secondary subflux.ProviderID = "secondary"
)

// --- Sonarr and Radarr ---

type fakeArr struct {
	srv    *httptest.Server
	eps    map[int][]arrapi.Episode
	calls  map[string]int
	series []arrapi.Series
	mu     sync.Mutex
}

func newFakeArr(t *testing.T, series []arrapi.Series, eps map[int][]arrapi.Episode) *fakeArr {
	t.Helper()
	a := &fakeArr{series: series, eps: eps, calls: map[string]int{}}
	a.srv = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *fakeArr) serve(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.calls[r.URL.Path]++
	a.mu.Unlock()
	var body any
	switch r.URL.Path {
	case "/api/v3/series":
		body = a.series
	case "/api/v3/episode":
		id, _ := strconv.Atoi(r.URL.Query().Get("seriesId"))
		body = a.eps[id]
	case "/api/v3/tag", "/api/v3/movie", "/api/v3/history/since":
		body = []struct{}{}
	case "/api/v3/command":
		body = map[string]any{"id": 1, "status": "queued"}
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (a *fakeArr) callsTo(path string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[path]
}

// show is one series of the fixture library, all of it in season 1.
type show struct {
	title    string
	imdb     string
	tvdb     int
	episodes int
}

// episodeFixture is one stub video on disk and where its subtitle belongs.
type episodeFixture struct {
	key      string // "Title S01E02", the key the provider fake counts searches by
	video    string
	subtitle string
	mediaID  string
	release  string // the video's scene name
	episode  int
}

func buildLibrary(t *testing.T, root string, shows []show) ([]arrapi.Series, map[int][]arrapi.Episode, []episodeFixture) {
	t.Helper()
	var series []arrapi.Series
	eps := map[int][]arrapi.Episode{}
	var fixtures []episodeFixture
	for i, sh := range shows {
		id := i + 1
		dir := filepath.Join(root, sh.title, "Season 01")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("Setup: mkdir %s: %v", dir, err)
		}
		series = append(series, arrapi.Series{
			ID: id, Title: sh.title, ImdbID: sh.imdb, TvdbID: sh.tvdb, Year: 2015,
			Path:       filepath.Join(root, sh.title),
			Statistics: &arrapi.SeriesStatistics{SeasonCount: 1, EpisodeFileCount: sh.episodes, EpisodeCount: sh.episodes},
			Seasons: []arrapi.Season{{SeasonNumber: 1, Monitored: true, Statistics: &arrapi.SeasonStatistics{
				EpisodeFileCount: sh.episodes, EpisodeCount: sh.episodes, TotalEpisodeCount: sh.episodes,
			}}},
		})
		for n := 1; n <= sh.episodes; n++ {
			release := fmt.Sprintf("%s.S01E%02d.1080p.WEB-DL.DDP5.1.H.264-NTb", sh.title, n)
			video := filepath.Join(dir, release+".mkv")
			if err := os.WriteFile(video, []byte("stub video"), 0o600); err != nil {
				t.Fatalf("Setup: write %s: %v", video, err)
			}
			eps[id] = append(eps[id], arrapi.Episode{
				ID: id*100 + n, SeriesID: id, SeasonNumber: 1, EpisodeNumber: n,
				Title: fmt.Sprintf("Episode %d", n), HasFile: true, Monitored: true,
				EpisodeFile: &arrapi.EpisodeFile{ID: id*100 + n, SeriesID: id, SeasonNumber: 1, Path: video, SceneName: release},
			})
			fixtures = append(fixtures, episodeFixture{
				key:      fmt.Sprintf("%s S01E%02d", sh.title, n),
				video:    video,
				subtitle: subtitlefile.Path(video, subtitlefile.Tags{Lang: "en"}),
				mediaID:  mediaid.Episode(sh.tvdb, sh.imdb, mediaid.SeasonEpisode{Season: 1, Episode: n}),
				release:  release,
				episode:  n,
			})
		}
	}
	return series, eps, fixtures
}

// --- providers ---

type wireSub struct {
	ID      string `json:"id"`
	Release string `json:"release"`
}

// sourceSpec describes one provider's remote API. results lists what a
// search for one episode returns; a non-zero downloadStatus answers every
// download with that status instead of a subtitle.
type sourceSpec struct {
	results        func(title string, season, episode int) []wireSub
	name           subflux.ProviderID
	downloadStatus int
	countsShows    bool
}

// subtitleSource is a provider's API. It counts every call it receives and
// refuses any whose X-Password is not goodPassword with 401.
type subtitleSource struct {
	spec      sourceSpec
	srv       *httptest.Server
	searches  map[string]int // by "Title S01E02"
	downloads map[string]int // by subtitle ID
	counts    map[string]int // by IMDb ID
	mu        sync.Mutex
}

type sourceCalls struct{ searches, downloads, counts int }

func (c sourceCalls) total() int { return c.searches + c.downloads + c.counts }

func newSubtitleSource(t *testing.T, spec sourceSpec) *subtitleSource {
	t.Helper()
	s := &subtitleSource{spec: spec, searches: map[string]int{}, downloads: map[string]int{}, counts: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *subtitleSource) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	season, _ := strconv.Atoi(q.Get("season"))
	episode, _ := strconv.Atoi(q.Get("episode"))
	s.mu.Lock()
	switch r.URL.Path {
	case "/search":
		s.searches[fmt.Sprintf("%s S%02dE%02d", q.Get("title"), season, episode)]++
	case "/download":
		s.downloads[q.Get("id")]++
	case "/count":
		s.counts[q.Get("imdb")]++
	}
	s.mu.Unlock()
	if r.Header.Get("X-Password") != goodPassword {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/search":
		subs := []wireSub{}
		if s.spec.results != nil {
			subs = s.spec.results(q.Get("title"), season, episode)
		}
		_ = json.NewEncoder(w).Encode(subs)
	case "/download":
		if s.spec.downloadStatus != 0 {
			w.WriteHeader(s.spec.downloadStatus)
			return
		}
		_, _ = fmt.Fprintf(w, "1\r\n00:00:01,000 --> 00:00:02,000\r\n%s\r\n\r\n", q.Get("id"))
	case "/count":
		_, _ = io.WriteString(w, "40")
	}
}

func (s *subtitleSource) calls() sourceCalls {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c sourceCalls
	for _, n := range s.counts {
		c.counts += n
	}
	for _, n := range s.searches {
		c.searches += n
	}
	for _, n := range s.downloads {
		c.downloads += n
	}
	return c
}

func (s *subtitleSource) searchesFor(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.searches[key]
}

func (s *subtitleSource) countsFor(imdb string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[imdb]
}

func (s *subtitleSource) downloadsOf(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.downloads[id]
}

// sourceProvider is the provider package for a subtitleSource. Like
// SubSource it copies the requested season and episode onto every result, so
// only the release name says which episode a result is for. A 406 is its
// download quota, as OpenSubtitles maps it; every other status goes through
// httpwire.CheckHTTPStatus.
type sourceProvider struct {
	src      *subtitleSource
	password string
}

func (p *sourceProvider) Name() subflux.ProviderID { return p.src.spec.name }

func (p *sourceProvider) Search(ctx context.Context, req *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	q := url.Values{"title": {req.Title}, "season": {strconv.Itoa(req.Season)}, "episode": {strconv.Itoa(req.Episode)}}
	body, err := p.get(ctx, "/search?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var subs []wireSub
	if err := json.Unmarshal(body, &subs); err != nil {
		return nil, err
	}
	out := make([]subflux.Subtitle, 0, len(subs))
	for _, s := range subs {
		out = append(out, subflux.Subtitle{
			Provider: p.Name(), ID: s.ID, Language: "en", ReleaseName: s.Release,
			MatchedBy: subflux.MatchByIMDB, Title: req.Title, Season: req.Season, Episode: req.Episode,
		})
	}
	return out, nil
}

func (p *sourceProvider) Download(ctx context.Context, sub *subflux.Subtitle) ([]byte, error) {
	return p.get(ctx, "/download?"+url.Values{"id": {sub.ID}}.Encode())
}

func (p *sourceProvider) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.src.srv.URL+path, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Password", p.password)
	resp, err := p.src.srv.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotAcceptable {
		return nil, &subflux.RateLimitError{Msg: "download quota exhausted (HTTP 406)", RetryAfter: time.Hour}
	}
	if err := httpwire.CheckHTTPStatus(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(httpwire.LimitedBody(resp))
}

// countingProvider adds the OpenSubtitles-style show-level count.
type countingProvider struct{ *sourceProvider }

func (p countingProvider) CountShowSubtitles(ctx context.Context, q subflux.ShowSubtitleQuery) (int, error) {
	body, err := p.get(ctx, "/count?"+url.Values{"imdb": {q.ImdbID}}.Encode())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(body))
}

func (s *subtitleSource) factory(_ context.Context, settings map[string]any) (provider.Provider, error) {
	password, _ := settings["password"].(string)
	p := &sourceProvider{src: s, password: password}
	if s.spec.countsShows {
		return countingProvider{p}, nil
	}
	return p, nil
}

// --- the rig ---

type stepClock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type writeFn func(ctx context.Context, path string, data []byte) error

// realWrite is the media writer's default write, which a scenario wraps to
// inject a fault.
func realWrite(ctx context.Context, path string, data []byte) error {
	_, err := atomicfile.WriteFile(ctx, path, data, atomicfile.WithMaxBytes(httpwire.MaxDownloadBytes))
	return err
}

func isProbe(path string) bool {
	return strings.HasPrefix(filepath.Base(path), ".subflux-write-probe-")
}

// logCapture collects the text-handler output of every record at INFO and above.
type logCapture struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *logCapture) offset() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Len()
}

func (c *logCapture) since(off int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Split(strings.TrimSpace(c.buf.String()[off:]), "\n")
}

// engineState is one activation's config and engine.
type engineState struct {
	cfg *config.Config
	res wiring.Result
}

type noDelayCfg struct{ scanning.ScanCfg }

// Search drops scan_delay, whose 5 s floor is enforced at config load.
func (c noDelayCfg) Search() subflux.SearchConfig {
	s := c.ScanCfg.Search()
	s.ScanDelay = 0
	return s
}

type pipelineRig struct {
	t        *testing.T
	db       *boltstore.DB
	sonarr   *fakeArr
	radarr   *fakeArr
	sonarrC  *arrsvc.CachedSonarr
	radarrC  *arrsvc.CachedRadarr
	sources  map[subflux.ProviderID]*subtitleSource
	reg      *provider.Registry
	clock    *stepClock
	hasher   *auth.Hasher
	logs     *logCapture
	activity *activity.Log
	skip     *showskip.Cache
	stops    activity.StopRegistry
	root     string
	order    []subflux.ProviderID
	episodes []episodeFixture
	write    atomic.Pointer[writeFn]

	probesMu sync.Mutex
	probes   map[string]int // probe writes by folder

	// One process: rebuilt by restart.
	m      *obs.Metrics
	alerts *activity.AlertLog
	bus    *events.EventBus
	gate   *providergate.Gate
	media  *mediawrite.Writer
	live   atomic.Pointer[engineState]
}

func newPipelineRig(t *testing.T, shows []show, specs ...sourceSpec) *pipelineRig {
	t.Helper()
	r := &pipelineRig{
		t:        t,
		root:     t.TempDir(),
		sources:  map[subflux.ProviderID]*subtitleSource{},
		reg:      provider.NewRegistry(),
		clock:    &stepClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)},
		logs:     &logCapture{},
		activity: activity.New(50),
		skip:     showskip.New(24 * time.Hour),
		probes:   map[string]int{},
	}
	testsupport.SwapDefaultLogger(t, slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	r.setWrite(realWrite)

	series, eps, fixtures := buildLibrary(t, r.root, shows)
	r.episodes = fixtures
	r.sonarr = newFakeArr(t, series, eps)
	r.radarr = newFakeArr(t, nil, nil)
	gate := arrsvc.NewReadGate(func() context.Context { return t.Context() }, nil)
	var err error
	if r.sonarrC, err = arrsvc.NewCachedSonarr(r.sonarr.srv.URL, arrKey, gate); err != nil {
		t.Fatalf("Setup: NewCachedSonarr: %v", err)
	}
	t.Cleanup(r.sonarrC.Close)
	if r.radarrC, err = arrsvc.NewCachedRadarr(r.radarr.srv.URL, arrKey, gate); err != nil {
		t.Fatalf("Setup: NewCachedRadarr: %v", err)
	}
	t.Cleanup(r.radarrC.Close)

	for _, spec := range specs {
		src := newSubtitleSource(t, spec)
		r.sources[spec.name] = src
		r.order = append(r.order, spec.name)
		r.reg.Register(spec.name, src.factory)
		r.reg.RegisterSchema(spec.name, "Source "+string(spec.name),
			[]subflux.ProviderSchemaField{{Key: "password", Label: "Password", Type: "secret", Secret: true}})
	}

	if r.db, err = boltstore.Open(filepath.Join(t.TempDir(), "subflux.bolt")); err != nil {
		t.Fatalf("Setup: boltstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = r.db.Close(context.Background()) })
	if r.hasher, err = auth.NewHasher(auth.Argon2Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}); err != nil {
		t.Fatalf("Setup: NewHasher: %v", err)
	}
	r.startProcess()
	return r
}

// startProcess builds what a process holds in memory, over the same store,
// arrs and providers: a second call is a restart.
func (r *pipelineRig) startProcess() {
	t := r.t
	t.Helper()
	r.live.Store(nil)
	r.m = obs.New()
	r.alerts = activity.NewAlertLog(100)
	r.bus = events.New(8, r.m)
	bus := r.bus
	t.Cleanup(func() { _ = bus.Shutdown(context.Background()) })
	gate, err := providergate.Open(t.Context(), providergate.Config{Store: r.db, Hasher: r.hasher, Metrics: r.m, Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Setup: providergate.Open: %v", err)
	}
	label := func(id subflux.ProviderID) string { l, _ := r.reg.Schema(id); return l }
	gate.SetOnChange(server.NewProviderGateHook(r.alerts, label, server.NewProviderPublisher(r.bus, r.providerStatus)))
	r.gate = gate
	media, err := mediawrite.New(mediawrite.Config{
		Metrics: r.m, Alerts: r.alerts, MaxBytes: httpwire.MaxDownloadBytes, Write: r.dispatchWrite,
	})
	if err != nil {
		t.Fatalf("Setup: mediawrite.New: %v", err)
	}
	r.media = media
}

func (r *pipelineRig) providerStatus(id subflux.ProviderID) (subflux.ProviderStatus, bool) {
	st := r.live.Load()
	if st == nil {
		return subflux.ProviderStatus{}, false
	}
	all, enabled := st.res.Engine.ProviderStatus()
	return all[id], enabled
}

func (r *pipelineRig) setWrite(fn writeFn) { r.write.Store(&fn) }

func (r *pipelineRig) dispatchWrite(ctx context.Context, path string, data []byte) error {
	if isProbe(path) {
		r.probesMu.Lock()
		r.probes[filepath.Dir(path)]++
		r.probesMu.Unlock()
	}
	return (*r.write.Load())(ctx, path, data)
}

func (r *pipelineRig) probeWrites(folder string) int {
	r.probesMu.Lock()
	defer r.probesMu.Unlock()
	return r.probes[folder]
}

func (r *pipelineRig) config(passwords map[subflux.ProviderID]string) *config.Config {
	r.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "sonarr:\n  url: %q\n  api_key: %q\n", r.sonarr.srv.URL, arrKey)
	fmt.Fprintf(&b, "radarr:\n  url: %q\n  api_key: %q\n", r.radarr.srv.URL, arrKey)
	fmt.Fprintf(&b, "media_roots:\n  - %q\n", r.root)
	b.WriteString("languages:\n  default:\n    - code: en\n")
	b.WriteString("post_processing:\n  sync_subtitles: false\n")
	b.WriteString("providers:\n")
	for i, name := range r.order {
		fmt.Fprintf(&b, "  %s:\n    enabled: true\n    priority: %d\n    settings:\n      password: %q\n", name, i+1, passwords[name])
	}
	cfg, err := config.LoadFromBytes(r.t.Context(), []byte(b.String()))
	if err != nil {
		r.t.Fatalf("Setup: config.LoadFromBytes: %v\n%s", err, b.String())
	}
	r.t.Cleanup(func() { _ = cfg.Close() })
	return cfg
}

// activate builds an engine for passwords and makes it live the way the
// server's activation does: build, publish, gate Activate and Reconcile, then
// bind the media roots.
func (r *pipelineRig) activate(passwords map[subflux.ProviderID]string) *engineState {
	r.t.Helper()
	cfg := r.config(passwords)
	res, err := wiring.Build(r.t.Context(), cfg, r.db, r.m, r.reg, r.gate, wiring.Extras{
		SyncExec: syncing.InProcessExec{}, Tracks: search.NoopDetector{}, Media: r.media,
	})
	if err != nil {
		r.t.Fatalf("Setup: wiring.Build: %v", err)
	}
	st := &engineState{cfg: cfg, res: res}
	r.live.Store(st)
	r.gate.Activate(res.Binding)
	r.gate.Reconcile(r.t.Context())
	r.media.Bind(cfg.MediaRootDirs, cfg.ValidatePath)
	return st
}

func (r *pipelineRig) activateAll(password string) *engineState {
	r.t.Helper()
	passwords := map[subflux.ProviderID]string{}
	for _, name := range r.order {
		passwords[name] = password
	}
	return r.activate(passwords)
}

// runReport is one full scan: its result, its activity entry and the log
// lines written while it ran.
type runReport struct {
	entry activity.Entry
	logs  []string
	res   scanning.FullScanResult
}

// scan runs one scheduled full scan over st through the scheduler's entry.
func (r *pipelineRig) scan(st *engineState) runReport {
	r.t.Helper()
	deps := &scheduler.Deps{
		DB: r.db, ScanDB: r.db, Backoff: r.db, Metrics: r.m, ReconcileMetrics: r.m,
		Events: r.bus, Activity: r.activity, Alerts: r.alerts, Stops: &r.stops,
		ShowSkipCache: r.skip, Media: r.media, Presence: testsupport.MediaPresence(r.root),
		StateFunc: func() *scheduler.LiveState {
			ls := &scheduler.LiveState{
				Cfg: noDelayCfg{st.cfg}, Engine: st.res.Engine,
				Sonarr: r.sonarrC, Radarr: r.radarrC, Providers: st.res.Providers,
			}
			if st.res.Engine.HasShowCounter() {
				ls.ShowCounter = st.res.Engine
			}
			return ls
		},
		ScanningFlag:        new(atomic.Bool),
		DeleteSubtitleFiles: func([]string, string) {},
	}
	from := r.logs.offset()
	actID, run := scheduler.PrepareFullScan(deps, activity.SourceScheduled)
	res := run(r.t.Context())
	entry, ok := r.activity.Get(actID)
	if !ok {
		r.t.Fatalf("activity %s vanished after the scan", actID)
	}
	return runReport{res: res, entry: entry, logs: r.logs.since(from)}
}

// records returns the run's log lines at level that contain every needle.
func (rr *runReport) records(level string, needles ...string) []string {
	var out []string
	for _, line := range rr.logs {
		if !strings.Contains(line, "level="+level+" ") {
			continue
		}
		matched := true
		for _, n := range needles {
			matched = matched && strings.Contains(line, n)
		}
		if matched {
			out = append(out, line)
		}
	}
	return out
}

// episodeStat reads one counter of the run's "scan results: episodes" line,
// or "" when the run wrote none.
func (rr *runReport) episodeStat(key string) string {
	for _, line := range rr.logs {
		if !strings.Contains(line, `msg="scan results: episodes"`) {
			continue
		}
		for field := range strings.FieldsSeq(line) {
			if v, ok := strings.CutPrefix(field, key+"="); ok {
				return v
			}
		}
	}
	return ""
}

func (r *pipelineRig) metric(series string) string {
	r.t.Helper()
	rec := httptest.NewRecorder()
	r.m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(r.t.Context(), http.MethodGet, "/metrics", http.NoBody))
	for line := range strings.Lines(rec.Body.String()) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), series+" "); ok {
			return v
		}
	}
	return ""
}

func (r *pipelineRig) rootGauge() string {
	return r.metric(fmt.Sprintf("subflux_media_root_unwritable{root=%q}", r.root))
}

func (r *pipelineRig) alertSources(prefix string) []string {
	var out []string
	for _, a := range r.alerts.VisibleAlerts() {
		if strings.HasPrefix(a.Source, prefix) {
			out = append(out, a.Source)
		}
	}
	return out
}

func (r *pipelineRig) recentlyScanned() map[string]bool {
	r.t.Helper()
	recent, err := r.db.RecentlyScanned(r.t.Context(), time.Now().Add(-time.Hour))
	if err != nil {
		r.t.Fatalf("RecentlyScanned: %v", err)
	}
	return recent
}

func (r *pipelineRig) stateRows() []subflux.StateEntry {
	r.t.Helper()
	page, err := r.db.State(r.t.Context(), &subflux.StateQuery{Limit: 100})
	if err != nil {
		r.t.Fatalf("State: %v", err)
	}
	return page.Entries
}

func (r *pipelineRig) savedRelease(ep episodeFixture) (subflux.DownloadedRef, bool) {
	r.t.Helper()
	refs, err := r.db.DownloadedRefs(r.t.Context(), subflux.MediaTypeEpisode, ep.mediaID, "en")
	if err != nil {
		r.t.Fatalf("DownloadedRefs(%s): %v", ep.mediaID, err)
	}
	if len(refs) != 1 {
		return subflux.DownloadedRef{}, false
	}
	return refs[0], true
}

func fileText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// oneMatch answers every search with one release named for the requested
// episode under ID prefix+"-sNNeNN".
func oneMatch(prefix, quality string) func(title string, season, episode int) []wireSub {
	return func(title string, season, episode int) []wireSub {
		return []wireSub{{
			ID:      fmt.Sprintf("%s-s%02de%02d", prefix, season, episode),
			Release: fmt.Sprintf("%s.S%02dE%02d.%s", title, season, episode, quality),
		}}
	}
}

const (
	webQuality  = "1080p.WEB-DL.DDP5.1.H.264-NTb"
	hdtvQuality = "720p.HDTV.x264-ORGANiC"
)

// --- scenarios ---

// The automated scan must save what a manual search offers first for the
// same item. The provider answers like SubSource: every result carries the
// requested episode number, and a better-scoring S01E06 release rides along
// with each request, so only its name says it is the wrong episode.
func TestPipeline_the_scan_saves_what_manual_search_finds(t *testing.T) {
	rig := newPipelineRig(t, []show{{title: "Unforgotten", imdb: "tt4419684", tvdb: 301852, episodes: 3}},
		sourceSpec{name: primary, results: func(title string, season, episode int) []wireSub {
			return []wireSub{
				{ID: fmt.Sprintf("unforgotten-s01e%02d", episode), Release: fmt.Sprintf("%s.S%02dE%02d.%s", title, season, episode, hdtvQuality)},
				{ID: fmt.Sprintf("unforgotten-s01e06-for-e%02d", episode), Release: title + ".S01E06." + webQuality},
			}
		}})
	st := rig.activateAll(goodPassword)

	run := rig.scan(st)

	if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeCompleted}) {
		t.Fatalf("scan = %+v (detail %q), want completed", run.res, run.entry.Detail)
	}
	for _, ep := range rig.episodes {
		want := fmt.Sprintf("Unforgotten.S01E%02d.%s", ep.episode, hdtvQuality)
		if got := fileText(ep.subtitle); !strings.Contains(got, fmt.Sprintf("unforgotten-s01e%02d\r\n", ep.episode)) {
			t.Errorf("%s: subtitle file %s holds %q, want the S01E%02d download", ep.key, ep.subtitle, got, ep.episode)
		}
		ref, ok := rig.savedRelease(ep)
		if !ok || ref.ReleaseName != want || ref.Provider != primary {
			t.Errorf("%s: recorded download = %+v (found %v), want %s from %s", ep.key, ref, ok, want, primary)
		}

		req := scanning.EpisodeSearchRequest(&rig.sonarr.series[0], &rig.sonarr.eps[1][ep.episode-1], st.cfg.LanguageCodes())
		manual := manualops.RunSearch(t.Context(), &manualops.SearchDeps{DB: rig.db},
			&manualops.LiveState{Cfg: st.cfg, Engine: st.res.Engine, Scorer: st.res.Scorer},
			&req, "en", subflux.MediaTypeEpisode, ep.video)
		if len(manual.Results) == 0 {
			t.Errorf("%s: manual search returned no results (notices %+v)", ep.key, manual.Providers)
			continue
		}
		top := manual.Results[0]
		if top.ReleaseName != ref.ReleaseName || top.Provider != ref.Provider || !top.OnDisk {
			t.Errorf("%s: manual top pick = %s from %s (on disk %v), want the scan's %s from %s on disk",
				ep.key, top.ReleaseName, top.Provider, top.OnDisk, ref.ReleaseName, ref.Provider)
		}
	}
	if rows := rig.stateRows(); len(rows) != len(rig.episodes) {
		t.Errorf("subtitle_state rows = %d, want %d", len(rows), len(rig.episodes))
	}
	if got := rig.metric("subflux_scans_total"); got != "1" {
		t.Errorf("subflux_scans_total = %q, want 1", got)
	}
	if got := rig.metric("subflux_scan_found_total"); got != "3" {
		t.Errorf("subflux_scan_found_total = %q, want 3", got)
	}
	if got := rig.metric(`subflux_subtitles_saved_total{provider="primary"}`); got != "3" {
		t.Errorf("subflux_subtitles_saved_total for primary = %q, want 3", got)
	}
}

// readOnlyAfterFirstSave models a share that turns read-only once the scan
// is under way: the preflight passes, then the first subtitle write and
// everything after it, the confirming probe included, fails with EROFS.
func readOnlyAfterFirstSave() writeFn {
	var readOnly atomic.Bool
	return func(ctx context.Context, path string, data []byte) error {
		if !isProbe(path) {
			readOnly.Store(true)
		}
		if readOnly.Load() {
			return &fs.PathError{Op: "open", Path: path, Err: syscall.EROFS}
		}
		return realWrite(ctx, path, data)
	}
}

var twoShows = []show{
	{title: "Bodyguard", imdb: "tt7493974", tvdb: 350664, episodes: 2},
	{title: "Shetland", imdb: "tt2701582", tvdb: 268594, episodes: 2},
}

func TestPipeline_a_mid_run_write_failure_stops_the_scan(t *testing.T) {
	rig := newPipelineRig(t, twoShows, sourceSpec{name: primary, results: oneMatch("sub", webQuality)})
	rig.setWrite(readOnlyAfterFirstSave())
	st := rig.activateAll(goodPassword)
	first := rig.episodes[0]
	folder := filepath.Dir(first.video)

	run := rig.scan(st)

	if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeFailed, MediaUnwritable: true}) {
		t.Errorf("scan = %+v, want failed with MediaUnwritable", run.res)
	}
	calls := rig.sources[primary].calls()
	if calls.downloads != 1 || calls.searches != 1 {
		t.Errorf("provider calls = %+v, want one search and one download, both for %s", calls, first.key)
	}
	if d := run.entry.Detail; !strings.HasPrefix(d, "Stopped:") || !strings.Contains(d, folder) || !strings.Contains(d, "read-only file system") {
		t.Errorf("activity detail = %q, want it to start %q and name %s and the read-only error", d, "Stopped:", folder)
	}
	if got := rig.alertSources("media-unwritable"); len(got) != 1 || got[0] != "media-unwritable:"+folder {
		t.Errorf("media alerts = %q, want only media-unwritable:%s", got, folder)
	}
	if got := rig.rootGauge(); got != "1" {
		t.Errorf("media_root_unwritable for %s = %q, want 1", rig.root, got)
	}
	if errs := run.records("ERROR"); len(errs) != 1 || !strings.Contains(errs[0], folder) {
		t.Errorf("ERROR records = %q, want one naming %s", errs, folder)
	}
	if rig.recentlyScanned()[first.mediaID] {
		t.Errorf("%s is in RecentlyScanned after a stopped save, want it searched again", first.key)
	}
	if _, attempts, err := rig.db.Stats(t.Context()); err != nil || attempts != 0 {
		t.Errorf("search_attempts rows = %d (err %v), want 0", attempts, err)
	}
	if mark, err := rig.db.ScanCycleStart(t.Context()); err != nil || mark.IsZero() {
		t.Errorf("scan cycle mark = %v (err %v), want it still set", mark, err)
	}
}

func TestPipeline_an_unwritable_root_starts_nothing(t *testing.T) {
	rig := newPipelineRig(t, twoShows, sourceSpec{name: primary, countsShows: true, results: oneMatch("sub", webQuality)})
	rig.setWrite(func(_ context.Context, path string, _ []byte) error {
		return &fs.PathError{Op: "open", Path: path, Err: syscall.EROFS}
	})
	st := rig.activateAll(goodPassword)

	run := rig.scan(st)

	if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeFailed, MediaUnwritable: true}) {
		t.Errorf("scan = %+v, want failed with MediaUnwritable", run.res)
	}
	if got := rig.sonarr.callsTo("/api/v3/series"); got != 0 {
		t.Errorf("Sonarr series requests = %d, want 0", got)
	}
	if calls := rig.sources[primary].calls(); calls.total() != 0 {
		t.Errorf("provider calls = %+v, want none", calls)
	}
	if !strings.HasPrefix(run.entry.Detail, "Not started:") {
		t.Errorf("activity detail = %q, want it to start %q", run.entry.Detail, "Not started:")
	}
	if got := rig.alertSources("media-unwritable"); len(got) != 1 || got[0] != "media-unwritable:"+rig.root {
		t.Errorf("media alerts = %q, want only media-unwritable:%s", got, rig.root)
	}
	if got := rig.rootGauge(); got != "1" {
		t.Errorf("media_root_unwritable for %s = %q, want 1", rig.root, got)
	}
}

func TestPipeline_the_run_after_a_write_fix_saves_the_stopped_item(t *testing.T) {
	rig := newPipelineRig(t, twoShows, sourceSpec{name: primary, results: oneMatch("sub", webQuality)})
	rig.setWrite(readOnlyAfterFirstSave())
	if run := rig.scan(rig.activateAll(goodPassword)); !run.res.MediaUnwritable {
		t.Fatalf("Setup: first scan = %+v, want it stopped by the read-only share", run.res)
	}
	folder := filepath.Dir(rig.episodes[0].video)

	rig.setWrite(realWrite)
	run := rig.scan(rig.activateAll(goodPassword))

	if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeCompleted}) {
		t.Fatalf("scan after the fix = %+v (detail %q), want completed", run.res, run.entry.Detail)
	}
	if got := run.records("INFO", `msg="media folder writable again"`, folder); len(got) != 1 {
		t.Errorf("writable-again records for %s = %q, want one", folder, got)
	}
	if got := run.records("INFO", `msg="scan resume: continuing interrupted cycle"`); len(got) != 1 {
		t.Errorf("resume records = %q, want the scan to continue the interrupted cycle", got)
	}
	if got := rig.alertSources("media-unwritable"); len(got) != 0 {
		t.Errorf("media alerts after the fix = %q, want none", got)
	}
	if got := rig.rootGauge(); got != "0" {
		t.Errorf("media_root_unwritable for %s after the fix = %q, want 0", rig.root, got)
	}
	for _, ep := range rig.episodes {
		if _, ok := rig.savedRelease(ep); !ok || fileText(ep.subtitle) == "" {
			t.Errorf("%s: not saved after the fix (file %q)", ep.key, fileText(ep.subtitle))
		}
	}
}

// A provider out of download quota is skipped for every later item, so the
// next provider saves instead, and a target whose only candidates are the
// paused provider's is a failed download, not a no-result: its season is not
// written off and nothing is stamped.
func TestPipeline_a_download_quota_moves_to_the_next_provider(t *testing.T) {
	rig := newPipelineRig(t, []show{
		{title: "Broadchurch", imdb: "tt2249364", tvdb: 264776, episodes: 2},
		{title: "Endeavour", imdb: "tt2195722", tvdb: 259972, episodes: 4},
	},
		sourceSpec{name: primary, downloadStatus: http.StatusNotAcceptable, results: oneMatch("primary", webQuality)},
		sourceSpec{name: secondary, results: func(title string, season, episode int) []wireSub {
			if title != "Broadchurch" {
				return nil
			}
			return oneMatch("secondary", hdtvQuality)(title, season, episode)
		}},
	)

	run := rig.scan(rig.activateAll(goodPassword))

	if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeCompleted}) {
		t.Fatalf("scan = %+v (detail %q), want completed", run.res, run.entry.Detail)
	}
	if got := rig.sources[primary].calls().downloads; got != 1 {
		t.Errorf("primary downloads = %d, want 1: the 406 for Broadchurch S01E01 pauses it", got)
	}
	recent := rig.recentlyScanned()
	for _, ep := range rig.episodes {
		ref, saved := rig.savedRelease(ep)
		switch {
		case strings.HasPrefix(ep.key, "Broadchurch"):
			if !saved || ref.Provider != secondary {
				t.Errorf("%s: recorded download = %+v (found %v), want one from %s", ep.key, ref, saved, secondary)
			}
		default:
			if got := rig.sources[primary].searchesFor(ep.key); got != 1 {
				t.Errorf("%s: primary searches = %d, want 1 (the season must not be written off)", ep.key, got)
			}
			if saved || recent[ep.mediaID] {
				t.Errorf("%s: saved %v, stamped %v; want neither", ep.key, saved, recent[ep.mediaID])
			}
		}
	}
	if got, want := [2]string{run.episodeStat("download_failed"), run.episodeStat("no_result")}, [2]string{"4", "0"}; got != want {
		t.Errorf("scan stats download_failed, no_result = %v, want %v", got, want)
	}
}

// ladderRun advances the gate clock by d, scans, and reports the run plus the
// provider requests it caused.
func ladderRun(rig *pipelineRig, st *engineState, d time.Duration) (runReport, sourceCalls) {
	rig.clock.Advance(d)
	before := rig.sources[primary].calls()
	run := rig.scan(st)
	after := rig.sources[primary].calls()
	return run, sourceCalls{
		searches:  after.searches - before.searches,
		downloads: after.downloads - before.downloads,
		counts:    after.counts - before.counts,
	}
}

var ladderShow = []show{{title: "Shetland", imdb: "tt2701582", tvdb: 268594, episodes: 4}}

// ladderSteps are the gate-clock steps of the credential ladder: the first
// failure, one past the 5-minute pause, one past the 30-minute pause, and two
// days after the disable.
var ladderSteps = []time.Duration{0, 6 * time.Minute, 31 * time.Minute, 48 * time.Hour}

// A rejected credential reaches the provider three times in total, one
// request per run, and then never again; nothing about the items is recorded,
// so each run reaches the same items.
func TestPipeline_a_rejected_credential_walks_the_ladder_to_a_disable(t *testing.T) {
	rig := newPipelineRig(t, ladderShow, sourceSpec{name: primary, results: oneMatch("sub", webQuality)})
	st := rig.activateAll(wrongPassword)

	var logs []string
	for i, step := range ladderSteps {
		run, calls := ladderRun(rig, st, step)
		logs = append(logs, run.logs...)
		want := 1
		if i == len(ladderSteps)-1 {
			want = 0
		}
		if calls.total() != want {
			t.Errorf("run %d (+%s): provider requests = %+v, want %d", i+1, step, calls, want)
		}
		recent := rig.recentlyScanned()
		for _, ep := range rig.episodes {
			if recent[ep.mediaID] {
				t.Errorf("run %d: %s is in RecentlyScanned, want it unstamped (no provider answered)", i+1, ep.key)
			}
		}
		if got := run.episodeStat("skipped"); got != "0" {
			t.Errorf("run %d: episodes skipped = %q, want 0: neither resume nor the season tracker may skip an unanswered episode", i+1, got)
		}
	}
	if got := rig.metric(`subflux_provider_disabled{provider="primary"}`); got != "1" {
		t.Errorf("provider_disabled = %q, want 1", got)
	}
	if got := rig.alertSources("provider:primary"); len(got) != 1 {
		t.Errorf("provider alerts = %q, want provider:primary", got)
	}
	all := runReport{logs: logs}
	if got := all.records("ERROR", "provider=primary"); len(got) != 1 || !strings.Contains(got[0], `msg="provider disabled: credentials rejected"`) {
		t.Errorf("ERROR records naming the provider = %q, want the one disable record", got)
	}
	for _, line := range logs {
		if strings.Contains(line, wrongPassword) {
			t.Errorf("log line carries the password: %s", line)
		}
	}
}

// The show-level count shares the search's ladder: one request per run until
// the disable and none after it, and a refused count is never cached as a
// verdict about the series.
func TestPipeline_the_show_count_shares_the_ladder(t *testing.T) {
	shows := []show{
		{title: "Luther", imdb: "tt1474684", tvdb: 159391, episodes: 3},
		{title: "Marcella", imdb: "tt4790750", tvdb: 306502, episodes: 3},
		{title: "Shetland", imdb: "tt2701582", tvdb: 268594, episodes: 3},
	}
	rig := newPipelineRig(t, shows, sourceSpec{name: primary, countsShows: true, results: oneMatch("sub", webQuality)})
	st := rig.activateAll(wrongPassword)

	for i, step := range ladderSteps {
		run, calls := ladderRun(rig, st, step)
		want := sourceCalls{counts: 1}
		if i == len(ladderSteps)-1 {
			want = sourceCalls{}
		}
		if calls != want {
			t.Errorf("run %d (+%s): provider requests = %+v, want %+v", i+1, step, calls, want)
		}
		for _, sh := range shows {
			if skip, cached := rig.skip.Get(keyenc.Join(sh.imdb, "en")); cached {
				t.Errorf("run %d: show-skip cache holds %s = %v, want no entry", i+1, sh.title, skip)
			}
		}
		if got := run.episodeStat("series_skipped"); got != "0" {
			t.Errorf("run %d: series skipped = %q, want 0", i+1, got)
		}
		if got := run.records("INFO", `msg="skipping series: too few subtitles on OpenSubtitles"`); len(got) != 0 {
			t.Errorf("run %d: series skipped for too few subtitles: %q", i+1, got)
		}
	}
}

// A disable is persisted: after a restart the alert and gauge come back and
// the provider stays silent until its settings change, and the items, never
// stamped, are searched and saved once they do.
func TestPipeline_a_disable_survives_a_restart_until_the_settings_change(t *testing.T) {
	rig := newPipelineRig(t, ladderShow, sourceSpec{name: primary, results: oneMatch("sub", webQuality)})
	st := rig.activateAll(wrongPassword)
	for _, step := range ladderSteps[:3] {
		ladderRun(rig, st, step)
	}

	rig.startProcess()
	st = rig.activateAll(wrongPassword)

	if got := rig.alertSources("provider:primary"); len(got) != 1 {
		t.Errorf("provider alerts after the restart = %q, want provider:primary", got)
	}
	if got := rig.metric(`subflux_provider_disabled{provider="primary"}`); got != "1" {
		t.Errorf("provider_disabled after the restart = %q, want 1", got)
	}
	if _, calls := ladderRun(rig, st, time.Hour); calls.total() != 0 {
		t.Errorf("provider requests after the restart = %+v, want none", calls)
	}

	st = rig.activateAll(goodPassword)

	if got := rig.alertSources("provider:primary"); len(got) != 0 {
		t.Errorf("provider alerts after the settings change = %q, want none", got)
	}
	if got := rig.metric(`subflux_provider_disabled{provider="primary"}`); got != "0" {
		t.Errorf("provider_disabled after the settings change = %q, want 0", got)
	}
	run, calls := ladderRun(rig, st, 0)
	if calls.searches != len(rig.episodes) || run.res.Outcome != activity.OutcomeCompleted {
		t.Errorf("scan after the settings change = %+v with requests %+v, want completed with %d searches", run.res, calls, len(rig.episodes))
	}
	for _, ep := range rig.episodes {
		if _, ok := rig.savedRelease(ep); !ok {
			t.Errorf("%s: not saved after the settings change", ep.key)
		}
	}
}

// An engine built from settings that a newer activation replaced asks
// nothing of the provider and records nothing against it.
func TestPipeline_an_engine_from_replaced_settings_calls_nothing(t *testing.T) {
	rig := newPipelineRig(t, ladderShow, sourceSpec{name: primary, results: oneMatch("sub", webQuality)})
	stale := rig.activateAll(wrongPassword)
	fresh := rig.activateAll(goodPassword)

	if _, calls := ladderRun(rig, stale, 0); calls.total() != 0 {
		t.Errorf("provider requests from the replaced engine = %+v, want none", calls)
	}
	if recs, err := rig.db.ProviderAuthRecords(t.Context()); err != nil || len(recs) != 0 {
		t.Errorf("provider_auth records = %+v (err %v), want none", recs, err)
	}

	run, calls := ladderRun(rig, fresh, 0)
	if calls.searches != len(rig.episodes) || run.res.Outcome != activity.OutcomeCompleted {
		t.Errorf("scan with the live engine = %+v with requests %+v, want completed with %d searches", run.res, calls, len(rig.episodes))
	}
}

// refuseUnder fails every write below prefix with EROFS while broken is set.
func refuseUnder(prefix string, broken *atomic.Bool) writeFn {
	return func(ctx context.Context, path string, data []byte) error {
		if broken.Load() && strings.HasPrefix(path, prefix) {
			return &fs.PathError{Op: "open", Path: path, Err: syscall.EROFS}
		}
		return realWrite(ctx, path, data)
	}
}

// One series folder that refuses writes stops the first scan, then costs
// only its own episodes: the next scan skips them without a provider request
// and saves the rest, and the scan after the fix saves them too.
func TestPipeline_one_bad_series_folder_blocks_only_that_series(t *testing.T) {
	rig := newPipelineRig(t, twoShows, sourceSpec{name: primary, results: oneMatch("sub", webQuality)})
	bad := filepath.Join(rig.root, "Bodyguard") + string(filepath.Separator)
	var broken atomic.Bool
	broken.Store(true)
	rig.setWrite(refuseUnder(bad, &broken))
	st := rig.activateAll(goodPassword)
	src := rig.sources[primary]

	run := rig.scan(st)
	if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeFailed, MediaUnwritable: true}) {
		t.Fatalf("scan 1 = %+v, want failed with MediaUnwritable", run.res)
	}
	if got := src.calls(); got != (sourceCalls{searches: 1, downloads: 1}) {
		t.Errorf("scan 1: provider calls = %+v, want one search and one download for Bodyguard S01E01", got)
	}

	before := src.calls()
	searchedBefore := map[string]int{}
	for _, ep := range rig.episodes {
		searchedBefore[ep.key] = src.searchesFor(ep.key)
	}
	run = rig.scan(st)
	if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeCompleted}) {
		t.Fatalf("scan 2 = %+v (detail %q), want completed", run.res, run.entry.Detail)
	}
	for _, ep := range rig.episodes {
		_, saved := rig.savedRelease(ep)
		if strings.HasPrefix(ep.key, "Bodyguard") {
			if got := src.searchesFor(ep.key) - searchedBefore[ep.key]; got != 0 || saved {
				t.Errorf("scan 2: %s searched %d times, saved %v; want no search and no save", ep.key, got, saved)
			}
			continue
		}
		if !saved {
			t.Errorf("scan 2: %s not saved", ep.key)
		}
	}
	if got := src.calls().searches - before.searches; got != 2 {
		t.Errorf("scan 2: provider searches = %d, want 2 (Shetland only)", got)
	}
	if got := run.episodeStat("write_blocked"); got != "2" {
		t.Errorf("scan 2: write_blocked = %q, want 2", got)
	}

	broken.Store(false)
	run = rig.scan(st)
	if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeCompleted}) {
		t.Fatalf("scan 3 = %+v (detail %q), want completed", run.res, run.entry.Detail)
	}
	if got := run.records("INFO", `msg="media folder writable again"`, bad); len(got) != 1 {
		t.Errorf("scan 3: writable-again records = %q, want one for the Bodyguard folder", got)
	}
	for _, ep := range rig.episodes {
		if _, saved := rig.savedRelease(ep); !saved {
			t.Errorf("scan 3: %s not saved", ep.key)
		}
	}
}

// A series whose folder is already known to refuse writes costs no provider
// request, the show-level count included, and is left unstamped, so the scan
// after the fix counts, searches and saves it.
func TestPipeline_a_known_bad_folder_skips_the_show_count(t *testing.T) {
	rig := newPipelineRig(t, twoShows, sourceSpec{name: primary, countsShows: true, results: oneMatch("sub", webQuality)})
	bodyguard := twoShows[0]
	var broken atomic.Bool
	broken.Store(true)
	rig.setWrite(refuseUnder(filepath.Join(rig.root, bodyguard.title)+string(filepath.Separator), &broken))
	st := rig.activateAll(goodPassword)
	first := rig.episodes[0]
	if err := rig.media.WriteFile(t.Context(), first.subtitle, []byte("placeholder")); err == nil {
		t.Fatalf("Setup: WriteFile(%s) succeeded, want the folder marked unwritable", first.subtitle)
	} else if _, ok := errors.AsType[*mediawrite.UnwritableError](err); !ok {
		t.Fatalf("Setup: WriteFile(%s) = %v, want an *mediawrite.UnwritableError", first.subtitle, err)
	}
	src := rig.sources[primary]

	run := rig.scan(st)

	if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeCompleted}) {
		t.Fatalf("scan 1 = %+v (detail %q), want completed", run.res, run.entry.Detail)
	}
	if got := src.countsFor(bodyguard.imdb); got != 0 {
		t.Errorf("scan 1: show counts for %s = %d, want 0", bodyguard.title, got)
	}
	if got := src.calls(); got != (sourceCalls{searches: 2, downloads: 2, counts: 1}) {
		t.Errorf("scan 1: provider calls = %+v, want Shetland's count, two searches and two downloads only", got)
	}
	recent := rig.recentlyScanned()
	for _, ep := range rig.episodes[:bodyguard.episodes] {
		if _, saved := rig.savedRelease(ep); saved || recent[ep.mediaID] {
			t.Errorf("scan 1: %s saved %v, stamped %v; want neither", ep.key, saved, recent[ep.mediaID])
		}
	}
	if got := [2]string{run.episodeStat("write_blocked"), run.episodeStat("series_skipped")}; got != [2]string{"2", "0"} {
		t.Errorf("scan 1: write_blocked, series_skipped = %v, want [2 0]", got)
	}

	broken.Store(false)
	run = rig.scan(st)

	if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeCompleted}) {
		t.Fatalf("scan 2 = %+v (detail %q), want completed", run.res, run.entry.Detail)
	}
	if got := src.countsFor(bodyguard.imdb); got != 1 {
		t.Errorf("scan 2: show counts for %s = %d, want 1", bodyguard.title, got)
	}
	for _, ep := range rig.episodes {
		if _, saved := rig.savedRelease(ep); !saved || fileText(ep.subtitle) == "" {
			t.Errorf("scan 2: %s not saved (file %q)", ep.key, fileText(ep.subtitle))
		}
	}
}

// refusalShow is the per-file refusal fixture; its second episode is the
// refused one.
var refusalShow = []show{{title: "Shetland", imdb: "tt2701582", tvdb: 268594, episodes: 3}}

// runPerFileRefusal runs two full scans in which Write refuses exactly
// refused's subtitle, and checks that the refusal stays that file's: both
// scans complete, the other episodes are saved, and no folder is reported.
func runPerFileRefusal(t *testing.T, rig *pipelineRig, refused episodeFixture, refusal error) []runReport {
	t.Helper()
	rig.setWrite(func(ctx context.Context, path string, data []byte) error {
		if path == refused.subtitle {
			return refusal
		}
		return realWrite(ctx, path, data)
	})
	st := rig.activateAll(goodPassword)
	src := rig.sources[primary]
	refusedID := fmt.Sprintf("sub-s01e%02d", refused.episode)
	folder := filepath.Dir(refused.subtitle)

	var runs []runReport
	for i := range 2 {
		before := src.downloadsOf(refusedID)
		run := rig.scan(st)
		runs = append(runs, run)
		if run.res != (scanning.FullScanResult{Outcome: activity.OutcomeCompleted}) {
			t.Errorf("scan %d = %+v (detail %q), want completed", i+1, run.res, run.entry.Detail)
		}
		if got := src.downloadsOf(refusedID) - before; got != 1 {
			t.Errorf("scan %d: downloads of %s = %d, want 1", i+1, refusedID, got)
		}
		if got := run.episodeStat("download_failed"); got != "1" {
			t.Errorf("scan %d: download_failed = %q, want 1", i+1, got)
		}
		if got := run.records("WARN", refused.subtitle); len(got) != 1 {
			t.Errorf("scan %d: WARN records naming %s = %q, want one", i+1, refused.subtitle, got)
		}
		if got := run.records("ERROR", folder); len(got) != 0 {
			t.Errorf("scan %d: ERROR records naming the folder = %q, want none", i+1, got)
		}
		if got := rig.alertSources("media-unwritable"); len(got) != 0 {
			t.Errorf("scan %d: media alerts = %q, want none", i+1, got)
		}
		if got := rig.rootGauge(); got != "0" {
			t.Errorf("scan %d: media_root_unwritable = %q, want 0", i+1, got)
		}
		if i > 0 {
			continue
		}
		for _, ep := range rig.episodes {
			if ep.episode == refused.episode {
				continue
			}
			if _, saved := rig.savedRelease(ep); !saved {
				t.Errorf("scan 1: %s not saved", ep.key)
			}
		}
	}
	return runs
}

func TestPipeline_a_symlinked_subtitle_is_that_files_refusal(t *testing.T) {
	rig := newPipelineRig(t, refusalShow, sourceSpec{name: primary, results: oneMatch("sub", webQuality)})
	runPerFileRefusal(t, rig, rig.episodes[1], atomicfile.ErrSymlinkTarget)
}

func TestPipeline_a_name_too_long_is_that_files_refusal(t *testing.T) {
	rig := newPipelineRig(t, refusalShow, sourceSpec{name: primary, results: oneMatch("sub", webQuality)})
	refused := rig.episodes[1]
	runPerFileRefusal(t, rig, refused, &fs.PathError{Op: "rename", Path: refused.subtitle, Err: syscall.ENAMETOOLONG})
}

// An upgrade refused at the existing subtitle's own path is confirmed by a
// probe of its folder, which passes, so it stays that file's refusal.
func TestPipeline_a_refused_upgrade_is_that_files_refusal(t *testing.T) {
	rig := newPipelineRig(t, refusalShow, sourceSpec{name: primary, results: oneMatch("sub", webQuality)})
	refused := rig.episodes[1]
	if err := os.WriteFile(refused.subtitle, []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nold\r\n\r\n"), 0o600); err != nil {
		t.Fatalf("Setup: write the existing subtitle: %v", err)
	}
	if err := rig.db.SaveDownload(t.Context(), &subflux.DownloadRecord{
		MediaType: subflux.MediaTypeEpisode, MediaID: refused.mediaID, Language: "en", Variant: subflux.VariantStandard,
		ProviderName: primary, ReleaseName: "Shetland.S01E02.480p.DVDRip.XviD-OLD", Path: refused.subtitle, Score: 1,
		Meta: &subflux.DownloadMeta{Title: "Shetland", ImdbID: "tt2701582", VideoPath: refused.video, Season: 1, Episode: 2},
	}); err != nil {
		t.Fatalf("Setup: SaveDownload: %v", err)
	}
	folder := filepath.Dir(refused.subtitle)

	probes := rig.probeWrites(folder)
	runs := runPerFileRefusal(t, rig, refused, &fs.PathError{Op: "rename", Path: refused.subtitle, Err: syscall.EACCES})

	if got := rig.probeWrites(folder) - probes; got != len(runs) {
		t.Errorf("probe writes in %s over %d scans = %d, want one confirming probe per scan", folder, len(runs), got)
	}
	if got := fileText(refused.subtitle); !strings.Contains(got, "old") {
		t.Errorf("existing subtitle now holds %q, want it untouched", got)
	}
}
