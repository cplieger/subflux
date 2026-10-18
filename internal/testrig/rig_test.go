package testrig

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/app"
	"go.yaml.in/yaml/v3"
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

func TestConfigModeFollowsSave(t *testing.T) {
	r := Start(t, Options{})
	for _, line := range []string{`subflux_config_mode{mode="unconfigured"} 1`, `subflux_config_mode{mode="active"} 0`, `subflux_config_mode{mode="failed"} 0`} {
		if !metric(t, r, line) {
			t.Errorf("an unconfigured boot lacks %s", line)
		}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("server:\n  log_level: info\n"), &doc); err != nil {
		t.Fatal(err)
	}
	if _, errs, err := r.Settings.Save(t.Context(), doc.Content[0], r.Settings.Snapshot().Revision()); err != nil || len(errs) != 0 {
		t.Fatalf("Save = %v, %v", errs, err)
	}
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
	if code, body := get(t, r, "/api/health"); code != http.StatusOK || !strings.Contains(body, `"status":"OK"`) {
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

func eventually(cond func() bool) bool {
	deadline := time.Now().Add(readyBudget)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}
