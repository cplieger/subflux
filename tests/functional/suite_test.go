//go:build functional

package functional

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The suite mirrors run.sh: shared mutable state (last HTTP status/body,
// saved original config, pass/fail/skip counters) threaded through 26
// ordered sections. Output lines reproduce the bash suite's format without
// ANSI colors: "[TEST] msg", "  PASS msg", "  FAIL msg", "  SKIP msg" and
// the final summary block.

// defaultTimeout mirrors curl's `--max-time 60` on every _curl call.
const defaultTimeout = 60 * time.Second

type suite struct {
	t       *testing.T // current subtest; fail() marks it failed
	client  *http.Client
	baseURL string
	section string // SECTION env filter, default "all"

	lastStatus string // "000" on transport error, like curl's %{http_code}
	lastBody   []byte // raw last response body
	original   string // saved config, $()-style trailing-newline stripped

	passCount int
	failCount int
	skipCount int
	errors    []string
}

func newSuite() *suite {
	base := os.Getenv("SUBFLUX_URL")
	if base == "" {
		base = "http://192.0.2.77:8374"
	}
	sec := os.Getenv("SECTION")
	if sec == "" {
		sec = "all"
	}
	return &suite{
		client: &http.Client{
			Transport: &http.Transport{
				// curl --connect-timeout 5
				DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			},
			// curl without -L does not follow redirects.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		baseURL: base,
		section: sec,
	}
}

// --- Output helpers (bash prefixes, no ANSI) --------------------------------

func (s *suite) log(msg string) { fmt.Printf("[TEST] %s\n", msg) }

func (s *suite) logf(format string, args ...any) { s.log(fmt.Sprintf(format, args...)) }

func (s *suite) pass(msg string) {
	s.passCount++
	fmt.Printf("  PASS %s\n", msg)
}

func (s *suite) fail(msg string) {
	s.failCount++
	fmt.Printf("  FAIL %s\n", msg)
	s.errors = append(s.errors, msg)
	// The bash suite keeps going after a failure; Fail (never FailNow)
	// marks the subtest red without stopping it.
	s.t.Fail()
}

func (s *suite) skip(msg string) {
	s.skipCount++
	fmt.Printf("  SKIP %s\n", msg)
}

// --- HTTP helpers ------------------------------------------------------------

// doRequest performs one HTTP request, recording the status code (curl's
// "000" on transport errors) and raw body, and returning the body with
// trailing newlines stripped the way bash's $(api_get ...) captures do.
func (s *suite) doRequest(timeout time.Duration, method, url, contentType, body string) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var rdr io.Reader
	if contentType != "" || body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		s.lastStatus = "000"
		s.lastBody = nil
		return ""
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.lastStatus = "000"
		s.lastBody = nil
		return ""
	}
	defer resp.Body.Close()
	s.lastStatus = strconv.Itoa(resp.StatusCode)
	raw, _ := io.ReadAll(resp.Body) // a body-read error keeps the partial body
	s.lastBody = raw
	return strings.TrimRight(string(raw), "\n")
}

func (s *suite) apiGet(path string) string {
	return s.doRequest(defaultTimeout, http.MethodGet, s.baseURL+path, "", "")
}

func (s *suite) apiDelete(path string) string {
	return s.doRequest(defaultTimeout, http.MethodDelete, s.baseURL+path, "", "")
}

// apiDeleteJSON mirrors `_curl -X DELETE -H 'Content-Type: application/json' -d body`.
func (s *suite) apiDeleteJSON(path, body string) string {
	return s.doRequest(defaultTimeout, http.MethodDelete, s.baseURL+path, "application/json", body)
}

func (s *suite) apiPost(path, body string) string {
	if body != "" {
		return s.doRequest(defaultTimeout, http.MethodPost, s.baseURL+path, "application/json", body)
	}
	return s.doRequest(defaultTimeout, http.MethodPost, s.baseURL+path, "", "")
}

// apiPostText mirrors `_curl -X POST -H 'Content-Type: text/plain' -d body`
// (the alternate config-save method).
func (s *suite) apiPostText(path, body string) string {
	return s.doRequest(defaultTimeout, http.MethodPost, s.baseURL+path, "text/plain", body)
}

func (s *suite) apiPut(path, body string) string {
	return s.doRequest(defaultTimeout, http.MethodPut, s.baseURL+path, "text/plain", body)
}

// curlSF mirrors `curl -sf --max-time N url`: body and true on success,
// "" and false on transport error or HTTP status >= 400 (-f suppresses the
// body on failure). It does not touch lastStatus/lastBody.
func (s *suite) curlSF(timeout time.Duration, url string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", false
	}
	raw, _ := io.ReadAll(resp.Body)
	return string(raw), true
}

// sseProbe mirrors `curl -sf --max-time N -o /dev/null -w '%{http_code}'`
// against a streaming endpoint: it returns the status code as soon as the
// response headers arrive and closes the connection without draining the
// stream. "000" only on transport errors (curl still reports the status on
// -f failures and stream timeouts).
func (s *suite) sseProbe(timeout time.Duration, url string) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "000"
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "000"
	}
	resp.Body.Close()
	return strconv.Itoa(resp.StatusCode)
}

// --- Assertions ---------------------------------------------------------------

func (s *suite) assertStatus(expected, ctx string) {
	if s.lastStatus == expected {
		s.pass(fmt.Sprintf("%s (HTTP %s)", ctx, expected))
	} else {
		s.fail(fmt.Sprintf("%s: expected HTTP %s, got %s", ctx, expected, s.lastStatus))
	}
}

func (s *suite) assertJSON(body, path, expected, ctx string) {
	actual := fieldRaw(body, path)
	if actual == expected {
		s.pass(ctx)
	} else {
		s.fail(fmt.Sprintf("%s: expected '%s', got '%s'", ctx, expected, actual))
	}
}

func (s *suite) assertJSONNotEmpty(body, path, ctx string) {
	actual := fieldRaw(body, path)
	if actual != "" && actual != "null" {
		s.pass(ctx)
	} else {
		s.fail(fmt.Sprintf("%s: expected non-empty at %s", ctx, path))
	}
}

func (s *suite) assertJSONLen(body, path, op string, n int, ctx string) {
	actual := lengthAt(body, path)
	val, numOK := shellInt(actual)
	var cond bool
	var want string
	switch op {
	case "eq":
		cond, want = numOK && val == n, "="
	case "gt":
		cond, want = numOK && val > n, ">"
	case "ge":
		cond, want = numOK && val >= n, ">="
	default:
		s.fail(fmt.Sprintf("%s: unknown op %s", ctx, op))
		return
	}
	if cond {
		s.pass(ctx)
	} else {
		// The bash fail message interpolates the raw `jq length` output
		// (possibly empty), not the defaulted value.
		s.fail(fmt.Sprintf("%s: len=%s, want %s%d", ctx, actual, want, n))
	}
}

// shellInt mirrors bash's `[ "${x:-0}" -op n ]`: empty defaults to 0, a
// non-numeric value makes the comparison itself fail (ok=false).
func shellInt(v string) (int, bool) {
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// --- Lifecycle -----------------------------------------------------------------

func (s *suite) waitReady(t *testing.T) {
	for range 30 {
		if _, ok := s.curlSF(2*time.Second, s.baseURL+"/api/health"); ok {
			return
		}
		time.Sleep(time.Second)
	}
	fmt.Printf("ERROR: subflux not reachable at %s\n", s.baseURL)
	t.FailNow()
}

func (s *suite) saveConfig(t *testing.T) {
	s.original = s.apiGet("/api/config")
	if s.original == "" {
		fmt.Printf("ERROR: empty config\n")
		t.FailNow()
	}
	s.logf("Original config saved (%d bytes)", len(s.original))
}

func (s *suite) restoreConfig() {
	if s.original == "" {
		return
	}
	for range 3 {
		s.apiPut("/api/config", s.original)
		if s.lastStatus == "200" {
			s.log("Config restored")
			time.Sleep(2 * time.Second)
			return
		}
		time.Sleep(2 * time.Second)
	}
	fmt.Fprintf(os.Stderr, "WARNING: Failed to restore original config after 3 attempts.\n")
	fmt.Fprintf(os.Stderr, "Save your config backup and restore manually.\n")
}

// arrKey extracts the api_key value from one top-level arr section of the
// saved original config -- a port of run.sh's section-scoped awk state
// machine (a naive grep would also match URL values like http://sonarr:8989
// and capture both keys). The value is the redaction placeholder; the
// server merges the real key back from disk on save.
func (s *suite) arrKey(section string) string {
	insec := false
	for line := range strings.SplitSeq(s.original, "\n") {
		if strings.HasPrefix(line, section+":") {
			insec = true
			continue
		}
		if line != "" && line[0] != ' ' && line[0] != '#' {
			insec = false
		}
		if !insec {
			continue
		}
		rest := strings.TrimLeft(line, " ")
		if v, ok := strings.CutPrefix(rest, "api_key:"); ok {
			v = strings.TrimLeft(v, " ")
			v = strings.ReplaceAll(v, `"`, "")
			v = strings.ReplaceAll(v, "'", "")
			return v
		}
	}
	return ""
}

// mockConfigTemplate is the byte-for-byte YAML template from run.sh's
// apply_mock_config. Slots: sonarr key, radarr key, lang rules (inside the
// languages: block), mock mode, mock extra (indented under providers:),
// top-level extra (appended last, no trailing newline after it).
const mockConfigTemplate = `sonarr:
  enabled: true
  url: "http://sonarr:8989"
  api_key: "%s"
radarr:
  enabled: true
  url: "http://radarr:7878"
  api_key: "%s"
media_roots:
  - /media
poll_interval: 999h
languages:
  default:
    - code: en
    - code: fr
%s
embedded_subtitles:
  ignore_pgs: true
  ignore_vobsub: true
providers:
  mock:
    enabled: true
    priority: 1
    settings:
      mode: "%s"
%s
search:
  scan_interval: 999h
  scan_delay: 5s
  upgrade_enabled: false
adaptive:
  initial_delay: 1s
  max_delay: 5s
  backoff_multiplier: 2
  max_attempts: 3
scoring:
  weights:
    hash: 100
    source: 28
    release_group: 23
    streaming_service: 14
    edition: 15
    video_codec: 10
    hdr: 8
    season_pack: 15
auth:
  disable_auth: true
logging:
  level: debug
  format: json
%s`

// applyMockConfig mirrors `apply_mock_config MODE [MOCK_EXTRA] [TOP_EXTRA]
// [LANG_RULES]`. langRules is emitted inside the languages: block; a
// top-level languages: key in topExtra would be a duplicate mapping key the
// YAML parser rejects. auth.disable_auth is pinned so hot-reloading this
// config never re-enables auth mid-suite.
func (s *suite) applyMockConfig(mode, mockExtra, topExtra, langRules string) {
	cfg := fmt.Sprintf(mockConfigTemplate,
		s.arrKey("sonarr"), s.arrKey("radarr"), langRules, mode, mockExtra, topExtra)
	s.apiPut("/api/config", cfg)
	time.Sleep(time.Second)
}

// --- Test entry points ------------------------------------------------------------

type section struct {
	name string
	fn   func(*suite)
}

// sections lists the 26 sections in the exact order run.sh executes them.
var sections = []section{
	{"health", (*suite).sectionHealth},
	{"auth", (*suite).sectionAuth},
	{"config", (*suite).sectionConfig},
	{"providers", (*suite).sectionProviders},
	{"media_browser", (*suite).sectionMediaBrowser},
	{"coverage", (*suite).sectionCoverage},
	{"state", (*suite).sectionState},
	{"files", (*suite).sectionFiles},
	{"manual_search", (*suite).sectionManualSearch},
	{"search_resolve", (*suite).sectionSearchResolve},
	{"scoring", (*suite).sectionScoring},
	{"backoff", (*suite).sectionBackoff},
	{"mock_provider", (*suite).sectionMockProvider},
	{"provider_errors", (*suite).sectionProviderErrors},
	{"config_validation", (*suite).sectionConfigValidation},
	{"post_processing", (*suite).sectionPostProcessing},
	{"language_rules", (*suite).sectionLanguageRules},
	{"scans", (*suite).sectionScans},
	{"sync", (*suite).sectionSync},
	{"poster_proxy", (*suite).sectionPosterProxy},
	{"manual_download", (*suite).sectionManualDownload},
	{"hot_reload", (*suite).sectionHotReload},
	{"exclude_tags", (*suite).sectionExcludeTags},
	{"embedded_settings", (*suite).sectionEmbeddedSettings},
	{"adaptive_config", (*suite).sectionAdaptiveConfig},
	{"real_providers", (*suite).sectionRealProviders},
}

// TestFunctional drives a live subflux instance through all 26 sections in
// order, sharing one suite state, exactly like `bash run.sh`. Sections can
// be filtered with the SECTION env var (bash parity) or with
// -run 'TestFunctional/<name>'.
func TestFunctional(t *testing.T) {
	s := newSuite()
	s.waitReady(t)
	s.saveConfig(t)
	// Bash installs `trap restore_config EXIT` right after save_config; the
	// cleanup runs whether the suite finishes or aborts on the auth guard.
	t.Cleanup(s.restoreConfig)
	// The bash EXIT trap also fired on Ctrl-C, but t.Cleanup does not run
	// on SIGINT (golang/go#41891); an explicit handler keeps the
	// interrupted-run config restore.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		s.restoreConfig()
		os.Exit(1)
	}()

	// Verify auth is disabled or credentials are configured.
	s.apiGet("/api/config")
	if s.lastStatus == "401" {
		fmt.Printf("ERROR: subflux requires authentication. Set auth.disable_auth: true in config or provide credentials.\n")
		t.FailNow()
	}

	for _, sec := range sections {
		t.Run(sec.name, func(t *testing.T) {
			if s.section != "all" && s.section != sec.name {
				t.Skip() // bash should_run: silently skipped, no output
			}
			s.t = t
			sec.fn(s)
		})
	}

	s.printSummary()
}

// TestFunctionalSectionList mirrors `bash run.sh --dry-run`: it prints the
// section list and never dials the server.
func TestFunctionalSectionList(_ *testing.T) {
	names := make([]string, 0, len(sections))
	for _, sec := range sections {
		names = append(names, sec.name)
	}
	fmt.Printf("Sections: %s\n", strings.Join(names, " "))
}

func (s *suite) printSummary() {
	fmt.Printf("\n========================================\n")
	fmt.Printf("PASS: %d  FAIL: %d  SKIP: %d\n", s.passCount, s.failCount, s.skipCount)
	fmt.Printf("Total: %d\n", s.passCount+s.failCount+s.skipCount)
	if s.failCount > 0 {
		fmt.Printf("\nFailures:")
		for _, e := range s.errors {
			fmt.Printf("\n  - %s", e)
		}
		fmt.Printf("\n")
		fmt.Printf("\n")
		return // the failed subtests already made the run exit non-zero
	}
	fmt.Printf("\nAll tests passed.\n")
}
