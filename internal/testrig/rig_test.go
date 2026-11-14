package testrig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/subflux/internal/adminsock"
	"github.com/cplieger/subflux/internal/app"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
)

func get(t *testing.T, r *Rig, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, r.URL+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func metric(t *testing.T, r *Rig, line string) bool {
	t.Helper()
	_, body := get(t, r, "/metrics")
	return strings.Contains(body, line+"\n")
}

func TestRequestRecordedByRoute(t *testing.T) {
	r := Start(t, Options{})
	const series = `subflux_http_requests_total{method="GET",path="/api/health",status="200"}`
	_, before := get(t, r, "/metrics")
	if code, _ := get(t, r, "/api/health"); code != http.StatusOK {
		t.Fatalf("/api/health = %d", code)
	}
	_, after := get(t, r, "/metrics")
	if count(before, series)+1 != count(after, series) {
		t.Errorf("the health request did not raise %s by one:\n%s", series, after)
	}
	if !strings.Contains(after, "subflux_http_request_duration_seconds_count ") {
		t.Errorf("no duration observed:\n%s", after)
	}
}

// count reads series' value from an exposition, 0 when absent.
func count(body, series string) int {
	for line := range strings.SplitSeq(body, "\n") {
		if v, ok := strings.CutPrefix(line, series+" "); ok {
			n := 0
			for _, c := range v {
				n = n*10 + int(c-'0')
			}
			return n
		}
	}
	return 0
}

func TestRecoveredPanicCounted(t *testing.T) {
	r := Start(t, Options{Mutate: func(c *app.Config) {
		c.Routes = func(mux *http.ServeMux) {
			mux.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
		}
	}})
	if code, _ := get(t, r, "/panic"); code != http.StatusInternalServerError {
		t.Errorf("a panicking route answered %d, want 500", code)
	}
	if !metric(t, r, "subflux_panics_total 1") {
		t.Error("subflux_panics_total did not reach 1")
	}
}

func TestBootOverRealStore(t *testing.T) {
	first := Start(t, Options{Config: []byte("languages:\n  default: [{tag: en}]\n")})
	paths := first.Paths
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	if storeRev(t, paths.Store) == 0 {
		t.Fatal("a fresh directory's boot left the store at revision 0, so its activation wrote nothing")
	}
	// A boot over a store with no user mints a setup token, a write of its own.
	addUser(t, paths.Store)
	booted := storeRev(t, paths.Store)
	second := Start(t, Options{Mutate: func(c *app.Config) { c.Paths = paths }})
	if err := second.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := storeRev(t, paths.Store); got != booted {
		t.Errorf("a restart with unchanged settings moved Rev from %d to %d", booted, got)
	}
}

// addUser adds an admin to the stopped rig's store.
func addUser(t *testing.T, path string) {
	t.Helper()
	s, err := statedb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now()
	u := &access.User{Username: "admin", Role: access.RoleAdmin, Enabled: true, WebAuthnHandle: access.GenerateWebAuthnHandle(), CreatedAt: now, UpdatedAt: now}
	if err := s.Accounts().CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
}

// storeRev opens the stopped rig's store and reads its revision.
func storeRev(t *testing.T, path string) uint64 {
	t.Helper()
	s, err := statedb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	in, err := s.Inspect(t.Context(), statedb.InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return in.Rev
}

// save commits text through the rig's settings service.
func save(t *testing.T, r *Rig, text string) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	if _, errs, err := r.Settings.Save(t.Context(), doc.Content[0], r.Settings.Snapshot().Revision()); err != nil || len(errs) != 0 {
		t.Fatalf("Save(%q) = %v, %v", text, errs, err)
	}
}

func TestConfigModeFollowsSave(t *testing.T) {
	r := Start(t, Options{})
	for _, line := range []string{`subflux_config_mode{mode="unconfigured"} 1`, `subflux_config_mode{mode="active"} 0`, `subflux_config_mode{mode="failed"} 0`} {
		if !metric(t, r, line) {
			t.Errorf("an unconfigured boot lacks %s", line)
		}
	}
	save(t, r, "server:\n  log_level: info\nlanguages:\n  default: [{tag: en}]\n")
	want := []string{`subflux_config_mode{mode="active"} 1`, `subflux_config_mode{mode="unconfigured"} 0`, `subflux_config_mode{mode="failed"} 0`}
	if !eventually(func() bool {
		for _, line := range want {
			if !metric(t, r, line) {
				return false
			}
		}
		return true
	}) {
		_, body := get(t, r, "/metrics")
		t.Errorf("after a save the mode gauge reads:\n%s", body)
	}
}

// scriptish matches a script element, an inline event handler or a
// javascript: URL.
var scriptish = regexp.MustCompile(`(?i)<script|\son[a-z]+\s*=|javascript:`)

func TestPlaceholderHasNoScript(t *testing.T) {
	r := Start(t, Options{})
	code, body := get(t, r, "/")
	if code != http.StatusOK || !strings.Contains(body, "being rebuilt") {
		t.Fatalf("/ = %d %q", code, body)
	}
	if scriptish.MatchString(body) {
		t.Errorf("the placeholder carries script:\n%s", body)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, r.URL+"/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP %q, want default-src 'none'", csp)
	}
	if code, _ := get(t, r, "/elsewhere"); code != http.StatusNotFound {
		t.Errorf("/elsewhere = %d, want 404", code)
	}
}

func TestPublicHealthAndMetrics(t *testing.T) {
	r := Start(t, Options{Config: []byte("server:\n  allowed_hosts: [nas.lan]\n")})
	if code, body := get(t, r, "/api/health"); code != http.StatusOK || !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("/api/health = %d %q", code, body)
	}
	if code, body := get(t, r, "/metrics"); code != http.StatusOK || !strings.Contains(body, "subflux_build_info{") {
		t.Errorf("/metrics = %d, body lacks build_info", code)
	}
	if err := r.Stop(); err != nil {
		t.Errorf("Stop = %v", err)
	}
	if resp, err := r.Client.Do(mustGet(t, r.URL+"/api/health")); err == nil {
		_ = resp.Body.Close()
		t.Error("the rig still serves after Stop")
	}
}

func mustGet(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.WithoutCancel(t.Context()), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func eventually(cond func() bool) bool { return within(readyBudget, cond) }

// within polls cond until it holds or d passes.
func within(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// sonarrRecording is the recorded Sonarr v5 the source rigs serve.
var sonarrRecording = filepath.Join(moduleRoot, "internal", "mediasource", "sonarr", "testdata", "v5-api-v5")

// sourceRig is a rig whose settings hold one Sonarr source, tv, at url, and
// scan, a scan section; it hands back the Scanner and the status store. The
// aired order join is sourcetest.OwnPlaces, so no rig reaches SkyHook.
func sourceRig(t *testing.T, url, scanSection string) (*Rig, *scan.Scanner, *status.Store) {
	t.Helper()
	scanners, stores := make(chan *scan.Scanner, 1), make(chan *status.Store, 1)
	r := Start(t, Options{
		Config: []byte("languages:\n  default: [{tag: en}]\nsources:\n  - {id: tv, type: sonarr, url: '" + url + "', api_key: " + arrtest.Key + "}\nscan:\n" + scanSection),
		Mutate: func(c *app.Config) {
			c.Scanner = func(s *scan.Scanner) { scanners <- s }
			c.Status = func(s *status.Store) { stores <- s }
			c.Orders = sourcetest.OwnPlaces{}
		},
	})
	select {
	case sc := <-scanners:
		return r, sc, <-stores
	default:
		t.Fatal("the boot's Active settings built no Scanner")
		return nil, nil, nil
	}
}

// endedJob waits up to d for a job of kind on source tv to end, and answers
// its outcome.
func endedJob(t *testing.T, st *status.Store, kind status.Kind, d time.Duration) status.Outcome {
	t.Helper()
	var out status.Outcome
	if !within(d, func() bool {
		snap, _ := st.Snapshot()
		i := slices.IndexFunc(snap.Jobs, func(j status.Job) bool { return j.Kind == kind && j.Source == "tv" && j.State == status.Done })
		if i >= 0 {
			out = snap.Jobs[i].Outcome
		}
		return i >= 0
	}) {
		t.Fatalf("no %v job on tv ended within %v", kind, d)
	}
	return out
}

// importRecord is Sonarr's history answer holding one folder import into
// Firefly (series 2188) dated at, shaped as the recording's own import.
func importRecord(t *testing.T, srv *arrtest.Server, at time.Time) []byte {
	t.Helper()
	var recs []map[string]any
	if err := json.Unmarshal(srv.Body("history-since.json"), &recs); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(recs, func(r map[string]any) bool { return r["eventType"] == "downloadFolderImported" })
	if i < 0 {
		t.Fatal("the recording holds no folder import")
	}
	rec := recs[i]
	rec["id"], rec["seriesId"], rec["episodeId"], rec["date"] = 970001, 2188, 180639, at.UTC().Format(time.RFC3339)
	b, err := json.Marshal([]map[string]any{rec})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fireflyFiles counts the recorded Firefly episodes that have a file.
func fireflyFiles(t *testing.T, srv *arrtest.Server) int {
	t.Helper()
	var eps []struct{ HasFile bool }
	if err := json.Unmarshal(srv.Body("episode-2188.json"), &eps); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range eps {
		if e.HasFile {
			n++
		}
	}
	return n
}

func TestRigSourceImportFlow(t *testing.T) {
	srv := arrtest.Load(t, sonarrRecording)
	imports := importRecord(t, srv, time.Now())
	var imported atomic.Bool
	srv.Handle("/api/v5/history/since", func(w http.ResponseWriter, _ *http.Request) {
		if imported.Load() {
			srv.Write(w, http.StatusOK, imports)
			return
		}
		srv.Write(w, http.StatusOK, []byte("[]"))
	})
	files := fireflyFiles(t, srv)
	r, sc, st := sourceRig(t, srv.Start(t), "  change_interval: 10s\n")
	sc.PassNow()
	if o := endedJob(t, st, status.Scan, readyBudget); o != status.Succeeded {
		t.Fatalf("the whole pass ended %v", o)
	}
	imported.Store(true)
	if o := endedJob(t, st, status.Import, 3*readyBudget); o != status.Succeeded {
		t.Fatalf("the import batch ended %v", o)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	db, err := statedb.Open(r.Paths.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ws, err := db.NextWork(t.Context(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	insts := map[statedb.InstanceID]bool{}
	for _, w := range ws {
		if w.Cause == statedb.Import {
			insts[w.Inst] = true
		}
	}
	if len(insts) != files {
		t.Errorf("Import work names %d instances, want Firefly's %d files in English", len(insts), files)
	}
}

// closedURL is an http URL on a loopback port nothing listens on.
func closedURL(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return url
}

func TestRigBootWithArrDown(t *testing.T) {
	r, sc, _ := sourceRig(t, closedURL(t), "  change_interval: 10s\n")
	sc.PassNow()
	if !eventually(func() bool { return metric(t, r, `subflux_alerts_active{id="tv",kind="source"} 1`) }) {
		_, body := get(t, r, "/metrics")
		t.Errorf("an unreachable source raised no source alert:\n%s", body)
	}
	if code, _ := get(t, r, "/api/health"); code != http.StatusOK {
		t.Errorf("/api/health = %d with the source down, want 200", code)
	}
}

func TestRigSaveRefusesBadKey(t *testing.T) {
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	t.Cleanup(arr.Close)
	const before = "languages:\n  default: [{tag: en}]\n"
	r := Start(t, Options{Config: []byte(before)})
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(before+"sources:\n  - {id: tv, type: sonarr, url: '"+arr.URL+"', api_key: wrong}\n"), &doc); err != nil {
		t.Fatal(err)
	}
	_, errs, err := r.Settings.Save(t.Context(), doc.Content[0], r.Settings.Snapshot().Revision())
	if err != nil {
		t.Fatal(err)
	}
	if want := "(" + mediasource.Unauthorized.String() + ")"; len(errs) != 1 || errs[0].Path != "sources[0]" || !strings.Contains(errs[0].Message, want) {
		t.Errorf("Save with a refused key = %+v, want one error at sources[0] naming %s", errs, want)
	}
	if a, ok := r.Settings.Snapshot().Active(); !ok || len(a.Sources) != 0 {
		t.Errorf("after the refused save the live settings hold %d sources, want the previous none", len(a.Sources))
	}
	if b, err := os.ReadFile(r.Paths.Config); err != nil || string(b) != before {
		t.Errorf("config.yaml = %q, %v; want it unchanged", b, err)
	}
}

// fireflyVideo is the recorded Firefly S01E01's file, as Sonarr reports it.
const fireflyVideo = "/media/tv/Firefly (2002) {imdb-tt0303461}/Season 01/Firefly (2002) - S01E01 - Serenity [DSNP][WEBDL-1080p][EAC3 5.1][h264]-FLUX.mkv"

// mediaRig is a rig whose Sonarr source, tv, maps /media/tv to a temp media
// root holding Firefly S01E01's video, with the parts of the app the media
// cases drive.
type mediaRig struct {
	*Rig
	scanners chan *scan.Scanner
	scanned  *scan.Scanner
	status   *status.Store
	store    *statedb.Store
	writer   *mediawrite.Writer
	// root is the local media root; video the local Firefly S01E01.
	root, video string
}

const mediaLanguages = "languages:\n  default: [{tag: en}]\n"

// sourceSection is a sources section naming tv at url, /media/tv mapped to
// root.
func sourceSection(url, root string) string {
	return "sources:\n  - {id: tv, type: sonarr, url: '" + url + "', api_key: " + arrtest.Key + ", path_mappings: [{from: /media/tv, to: '" + root + "'}]}\n"
}

// newMediaRig boots the rig with tv at url configured when withSource is
// set, else with languages alone.
func newMediaRig(t *testing.T, url string, withSource bool) *mediaRig {
	t.Helper()
	return startMediaRig(t, url, withSource, nil)
}

// startMediaRig is newMediaRig with mutate, when set, applied to the
// Config after the rig's own hooks.
func startMediaRig(t *testing.T, url string, withSource bool, mutate func(*app.Config)) *mediaRig {
	t.Helper()
	m := &mediaRig{root: filepath.Join(t.TempDir(), "tv"), scanners: make(chan *scan.Scanner, 1)}
	m.video = filepath.Join(m.root, strings.TrimPrefix(fireflyVideo, "/media/tv/"))
	if err := os.MkdirAll(filepath.Dir(m.video), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := mediaLanguages
	if withSource {
		cfg += sourceSection(url, m.root)
	}
	stores, statuses, writers := make(chan *statedb.Store, 1), make(chan *status.Store, 1), make(chan *mediawrite.Writer, 1)
	m.Rig = Start(t, Options{
		Config: []byte(cfg),
		Mutate: func(c *app.Config) {
			c.Scanner = func(s *scan.Scanner) { m.scanners <- s }
			c.Status = func(s *status.Store) { statuses <- s }
			c.Store = func(s *statedb.Store) { stores <- s }
			c.Writer = func(w *mediawrite.Writer) { writers <- w }
			c.Orders = sourcetest.OwnPlaces{}
			if mutate != nil {
				mutate(c)
			}
		},
	})
	m.store, m.status, m.writer = <-stores, <-statuses, <-writers
	return m
}

// firefly runs a whole pass of tv and answers Firefly S01E01's instance.
func (m *mediaRig) firefly(t *testing.T) statedb.InstanceID {
	t.Helper()
	if m.scanned == nil {
		select {
		case m.scanned = <-m.scanners:
		case <-time.After(readyBudget):
			t.Fatal("no Active settings built a Scanner")
		}
	}
	m.scanned.PassNow()
	if o := endedJob(t, m.status, status.Scan, readyBudget); o != status.Succeeded {
		t.Fatalf("the whole pass ended %v", o)
	}
	k, ok := media.Choose(media.Episode, media.ExternalIDs{TVDB: 78874}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1})
	if !ok {
		t.Fatal("no key for Firefly S01E01")
	}
	row, found, err := m.store.ItemRow(t.Context(), k)
	if err != nil || !found || row.Inst == 0 {
		t.Fatalf("ItemRow(%s) = %v, %v, %v; want Firefly S01E01's instance", k, row.Inst, found, err)
	}
	return row.Inst
}

// deliver delivers an English SRT saying text for inst through the writer.
func (m *mediaRig) deliver(t *testing.T, inst statedb.InstanceID, text string) statedb.FileRecord {
	t.Helper()
	cues, err := timing.ParseCues([]byte("1\n00:00:01,000 --> 00:00:02,000\n" + text + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	en, _ := language.Parse("en")
	ctx, cancel := context.WithTimeout(t.Context(), readyBudget)
	defer cancel()
	rec, err := m.writer.Deliver(ctx, mediawrite.Delivery{
		Inst: inst, Tag: en, Target: en, Variant: language.Standard, Cues: cues, Timing: timing.Identity{},
		Provenance: statedb.Provenance{Provider: "opensubtitles", Release: "Firefly.S01E01"},
		Run:        statedb.RunID{1}, Attempt: 1, Candidate: sha256.Sum256([]byte(text)),
	})
	if err != nil {
		t.Fatalf("Deliver = %v", err)
	}
	return rec
}

// rescans counts the RescanSeries commands srv receives per series, each
// answered by answer when set, else accepted.
type rescans struct {
	answer func(w http.ResponseWriter, r *http.Request)
	got    map[int]int
	mu     sync.Mutex
}

func countRescans(srv *arrtest.Server, answer func(http.ResponseWriter, *http.Request)) *rescans {
	c := &rescans{got: map[int]int{}, answer: answer}
	srv.Handle("/api/v5/command", func(w http.ResponseWriter, r *http.Request) {
		var cmd struct {
			Name     string `json:"name"`
			SeriesID int    `json:"seriesId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&cmd); err == nil && cmd.Name == "RescanSeries" {
			c.mu.Lock()
			c.got[cmd.SeriesID]++
			c.mu.Unlock()
		}
		if c.answer != nil {
			c.answer(w, r)
			return
		}
		srv.Write(w, http.StatusCreated, []byte(`{"id":7}`))
	})
	return c
}

func (c *rescans) of(series int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.got[series]
}

func TestRigDeliveryRescansSource(t *testing.T) {
	srv := arrtest.Load(t, sonarrRecording)
	sent := countRescans(srv, nil)
	m := newMediaRig(t, srv.Start(t), true)
	m.deliver(t, m.firefly(t), "Hello")
	if !eventually(func() bool { return sent.of(2188) > 0 }) {
		t.Fatal("Sonarr received no RescanSeries for Firefly after the delivery")
	}
	// A duplicate could only come from a later batch, after mediarefresh's
	// 5 s debounce.
	time.Sleep(7 * time.Second)
	if n := sent.of(2188); n != 1 {
		t.Errorf("Sonarr received %d RescanSeries for Firefly, want 1", n)
	}
}

func TestRigWriteNeverWaitsOnSink(t *testing.T) {
	srv := arrtest.Load(t, sonarrRecording)
	hung := make(chan struct{}, 1)
	sent := countRescans(srv, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case hung <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	})
	m := newMediaRig(t, srv.Start(t), true)
	inst := m.firefly(t)
	m.deliver(t, inst, "Hello")
	select {
	case <-hung:
	case <-time.After(readyBudget):
		t.Fatal("the rescan never reached the hung Sonarr")
	}
	start := time.Now()
	m.deliver(t, inst, "Hello again")
	if d := time.Since(start); d > 2*time.Second || sent.of(2188) != 1 {
		t.Errorf("a delivery with the rescan hung took %v with %d rescans sent, want it at once beside the one", d, sent.of(2188))
	}
}

// agrees fails t unless rec's file, rec and its baseline agree: the file
// holds rec's bytes, and they are the baseline rendered under rec's timing,
// want.
func (m *mediaRig) agrees(t *testing.T, rec statedb.FileRecord, want timing.Timing, text string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(m.video), rec.Name))
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(b) != rec.Identity.SHA256 || int64(len(b)) != rec.Identity.Size {
		t.Errorf("%s holds %d bytes the record (%d bytes) does not name", rec.Name, len(b), rec.Identity.Size)
	}
	ctx, cancel := context.WithTimeout(t.Context(), readyBudget)
	defer cancel()
	cur, err := m.writer.Current(ctx, mediawrite.RefOf(rec), mediawrite.Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256})
	if err != nil {
		t.Fatalf("Current = %v", err)
	}
	if cur.Timing != want || rec.Timing != want {
		t.Errorf("timing: record %v, Current %v; want %v", rec.Timing, cur.Timing, want)
	}
	if ev := cur.Cues.Events(); len(ev) != 1 || ev[0].Text != text || ev[0].StartMs != 1000 {
		t.Errorf("baseline events = %+v, want the delivered cue %q at 1000 ms", ev, text)
	}
	if rendered, err := timing.Render(cur.Cues, cur.Timing); err != nil || !bytes.Equal(rendered, b) {
		t.Errorf("the baseline under %v renders %q, %v; the file holds %q", cur.Timing, rendered, err, b)
	}
}

func TestRigWriteJourney(t *testing.T) {
	srv := arrtest.Load(t, sonarrRecording)
	countRescans(srv, nil)
	m := newMediaRig(t, srv.Start(t), true)
	inst := m.firefly(t)
	rec := m.deliver(t, inst, "Hello")
	m.agrees(t, rec, timing.Identity{}, "Hello")

	shift := timing.Shift{Ms: 500}
	ctx, cancel := context.WithTimeout(t.Context(), readyBudget)
	defer cancel()
	retimed, err := m.writer.Retime(ctx, mediawrite.RefOf(rec), mediawrite.Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256}, shift, nil)
	if err != nil {
		t.Fatalf("Retime = %v", err)
	}
	m.agrees(t, retimed, shift, "Hello")

	if err := m.writer.Delete(ctx, mediawrite.RefOf(retimed), mediawrite.Expectation{Generation: retimed.Generation, SHA256: retimed.Identity.SHA256}); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.video), retimed.Name)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after Delete, stat %s = %v; want it gone", retimed.Name, err)
	}
	if files, err := m.store.Files(ctx, inst); err != nil || len(files) != 0 {
		t.Errorf("after Delete the store holds %+v, %v; want no record", files, err)
	}
}

func TestRigSaveAddsWritableRoot(t *testing.T) {
	srv := arrtest.Load(t, sonarrRecording)
	countRescans(srv, nil)
	url := srv.Start(t)
	m := newMediaRig(t, url, false)
	save(t, m.Rig, mediaLanguages+sourceSection(url, m.root))
	inst := m.firefly(t)
	m.deliver(t, inst, "Hello")
}

func TestRigSaveAppliesToNextDelivery(t *testing.T) {
	srv := arrtest.Load(t, sonarrRecording)
	countRescans(srv, nil)
	url := srv.Start(t)
	// Every apply after boot is held until the test returns, so the delivery
	// below can see the saved section only through the settings commit.
	held, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	m := startMediaRig(t, url, true, func(c *app.Config) {
		c.SourcesApply = func(ctx context.Context) {
			select {
			case held <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	})
	inst := m.firefly(t)
	read := func(rec statedb.FileRecord) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(filepath.Dir(m.video), rec.Name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read(m.deliver(t, inst, "<i>Hello</i>")); strings.Contains(got, "<i>") || !strings.Contains(got, "Hello") {
		t.Errorf("under the default section the delivery reads %q, want its tags stripped", got)
	}
	save(t, m.Rig, mediaLanguages+sourceSection(url, m.root)+"post_processing:\n  strip_tags: false\n")
	select {
	case <-held:
	case <-time.After(readyBudget):
		t.Fatal("the save woke no media sources apply")
	}
	if got := read(m.deliver(t, inst, "<i>Hello again</i>")); !strings.Contains(got, "<i>Hello again</i>") {
		t.Errorf("the delivery right after a strip_tags: false save, its sources apply held, reads %q, want its tags kept", got)
	}
}

func TestRigSocketAfterUncleanExit(t *testing.T) {
	dir, err := os.MkdirTemp("", "sfx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "admin.sock")
	// A crashed process leaves its socket file behind.
	stale, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("the stale socket is gone before boot: %v", err)
	}
	Start(t, Options{Mutate: func(c *app.Config) { c.Paths.SocketDir, c.Paths.Socket = dir, path }})
	reply, err := adminsock.Call(t.Context(), path, &adminsock.Request{Command: adminsock.SetupToken})
	if err != nil || reply.Secret == "" {
		t.Errorf("setup-token over the rebound socket = %+v, %v; want a token", reply, err)
	}
}
