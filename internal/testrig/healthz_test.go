package testrig

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

type healthBody struct {
	Status  string   `json:"status"`
	Reasons []string `json:"reasons"`
}

// healthz sends method /healthz to the ops listener and returns the status,
// Cache-Control and decoded body; a HEAD or POST answer's body stays raw.
func healthz(t *testing.T, r *Rig, method string) (code int, cacheControl string, body healthBody, raw []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, r.OpsURL+"/healthz", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if method == http.MethodGet {
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("GET /healthz body %q: %v", raw, err)
		}
	}
	return resp.StatusCode, resp.Header.Get("Cache-Control"), body, raw
}

func TestHealthzRoute(t *testing.T) {
	healthy := func(t *testing.T, r *Rig) {
		t.Helper()
		code, cc, b, _ := healthz(t, r, http.MethodGet)
		if code != http.StatusOK || b.Status != "ok" || len(b.Reasons) != 0 {
			t.Errorf("GET /healthz = %d %+v, want 200 ok", code, b)
		}
		if cc != "no-store" {
			t.Errorf("Cache-Control %q, want no-store", cc)
		}
		if code, _, _, raw := healthz(t, r, http.MethodHead); code != http.StatusOK || len(raw) != 0 {
			t.Errorf("HEAD /healthz = %d with body %q, want 200 and none", code, raw)
		}
	}
	unhealthy := func(t *testing.T, r *Rig, reason string) {
		t.Helper()
		code, _, b, _ := healthz(t, r, http.MethodGet)
		if code != http.StatusServiceUnavailable || b.Status != "unhealthy" || !slices.Equal(b.Reasons, []string{reason}) {
			t.Errorf("GET /healthz = %d %+v, want 503 with the reason %q alone", code, b, reason)
		}
		if code, _, _, raw := healthz(t, r, http.MethodHead); code != http.StatusServiceUnavailable || len(raw) != 0 {
			t.Errorf("HEAD /healthz = %d with body %q, want 503 and none", code, raw)
		}
	}
	t.Run("active", func(t *testing.T) {
		healthy(t, Start(t, Options{Config: []byte(mediaLanguages)}))
	})
	t.Run("unconfigured", func(t *testing.T) {
		healthy(t, Start(t, Options{}))
	})
	t.Run("failed_settings", func(t *testing.T) {
		r := Start(t, Options{Config: []byte("server:\n  log_level: info\n")})
		unhealthy(t, r, "The settings file did not load.")
		if _, _, _, raw := healthz(t, r, http.MethodGet); strings.Contains(string(raw), "languages") {
			t.Errorf("/healthz %q quotes the load error; it answers with no sign-in", raw)
		}
	})
	t.Run("draining", func(t *testing.T) {
		r, release := StartDraining(t)
		defer release()
		unhealthy(t, r, "subflux is starting or shutting down")
	})
	t.Run("methods_and_paths", func(t *testing.T) {
		r := Start(t, Options{})
		if code, _, _, _ := healthz(t, r, http.MethodPost); code != http.StatusMethodNotAllowed {
			t.Errorf("POST /healthz = %d, want 405", code)
		}
		for _, path := range []string{"/healthz", "/metrics", "/api/health"} {
			if code, _ := get(t, r, path); code != http.StatusNotFound {
				t.Errorf("the product listener's %s = %d, want 404", path, code)
			}
		}
		for _, path := range []string{"/", "/api/health"} {
			if code, _ := ops(t, r, path); code != http.StatusNotFound {
				t.Errorf("the ops listener's %s = %d, want 404", path, code)
			}
		}
		if err := r.Stop(); err != nil {
			t.Fatalf("Stop = %v", err)
		}
		if resp, err := r.Client.Do(mustRequest(t, r.OpsURL+"/healthz")); err == nil {
			_ = resp.Body.Close()
			t.Error("the ops listener still serves after Stop")
		}
	})
}

// TestOpsListenerAnswersEveryHost: the ops listener runs no host check,
// since /healthz and /metrics hold no secret, while the product listener
// keeps refusing a Host outside server.allowed_hosts.
func TestOpsListenerAnswersEveryHost(t *testing.T) {
	r := Start(t, Options{Config: []byte("server:\n  allowed_hosts: [nas.lan]\n" + mediaLanguages)})
	for _, host := range []string{"other.lan", "nas.lan", "192.0.2.9:8375", "[2001:db8::9]:8375"} {
		if code, body := fetch(t, r, r.OpsURL+"/healthz", host); code != http.StatusOK || !strings.Contains(body, `"status":"ok"`) {
			t.Errorf("/healthz with Host %s = %d %q, want 200 ok", host, code, body)
		}
		if code, body := fetch(t, r, r.OpsURL+"/metrics", host); code != http.StatusOK || !strings.Contains(body, "subflux_build_info{") {
			t.Errorf("/metrics with Host %s = %d, want 200 with build_info", host, code)
		}
	}
	if code, _ := fetch(t, r, r.URL+"/", "other.lan"); code != http.StatusForbidden {
		t.Errorf("the product listener with Host other.lan = %d, want 403", code)
	}
	if code, _ := fetch(t, r, r.URL+"/", "nas.lan"); code != http.StatusOK {
		t.Errorf("the product listener with Host nas.lan = %d, want 200", code)
	}
}

// TestOpsRequestsStayOutOfHTTPMetrics: a probe or scrape on the ops listener
// never moves subflux_http_requests_total, so an outage pages once, as the
// target or probe going down, not also as product 5xx.
func TestOpsRequestsStayOutOfHTTPMetrics(t *testing.T) {
	r := Start(t, Options{Config: []byte("server:\n  log_level: info\n")})
	_, before := ops(t, r, "/metrics")
	for range 3 {
		if code, _, _, _ := healthz(t, r, http.MethodGet); code != http.StatusServiceUnavailable {
			t.Fatalf("/healthz over a failed file = %d, want 503", code)
		}
		ops(t, r, "/metrics")
		ops(t, r, "/elsewhere")
	}
	_, after := ops(t, r, "/metrics")
	if counted(before) != counted(after) {
		t.Errorf("ops requests moved subflux_http_requests_total:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// counted is every subflux_http_requests_total line of an exposition.
func counted(body string) string {
	var b strings.Builder
	for line := range strings.Lines(body) {
		if strings.HasPrefix(line, "subflux_http_requests_total") {
			b.WriteString(line)
		}
	}
	return b.String()
}
