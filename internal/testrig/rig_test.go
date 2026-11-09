package testrig

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/app"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
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
	booted := storeRev(t, paths.Store)
	if booted == 0 {
		t.Fatal("a fresh directory's boot left the store at revision 0, so its activation wrote nothing")
	}
	second := Start(t, Options{Mutate: func(c *app.Config) { c.Paths = paths }})
	if err := second.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := storeRev(t, paths.Store); got != booted {
		t.Errorf("a restart with unchanged settings moved Rev from %d to %d", booted, got)
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
