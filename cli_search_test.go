package main

// httptest-backed tests for the remote `subflux search` flow (R6.1):
// resolve happy/ambiguous/empty/conflict, golden-pinned result rendering,
// download+poll success/failure/timeout/ID-absent, API-key header
// propagation, exit codes (0/1/2), and the slow-search budget (the search
// leg must outwait the server's 60s search window, which the shared 30s
// client could not).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/apipaths"
	"github.com/cplieger/subflux/internal/cliparse"
)

// searchParams parses argv against the real search spec.
func searchParams(t *testing.T, args ...string) cliparse.Params {
	t.Helper()
	spec := cliSpecs[cmdSearch]
	p, err := cliparse.ParseAndValidate(args, &spec)
	if err != nil {
		t.Fatalf("ParseAndValidate(%v) error = %v, want nil", args, err)
	}
	return p
}

// testRunConfig returns a searchRunConfig writing to buf with millisecond
// poll budgets so terminal-case tests finish fast.
func testRunConfig(buf *bytes.Buffer) *searchRunConfig {
	return &searchRunConfig{
		out:           buf,
		searchTimeout: 5 * time.Second,
		pollInterval:  2 * time.Millisecond,
		pollTimeout:   250 * time.Millisecond,
	}
}

// startCLIServer serves mux and points SUBFLUX_URL at it (API key cleared
// unless a test sets one).
func startCLIServer(t *testing.T, mux *http.ServeMux) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("SUBFLUX_URL", srv.URL)
	t.Setenv("SUBFLUX_API_KEY", "")
	return srv
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode test response: %v", err)
	}
}

// fightClubResolve is a single-movie happy resolve response.
func fightClubResolve() map[string]any {
	return map[string]any{
		"resolved": true,
		"items": []map[string]any{{
			"media_type": "movie", "media_id": 7, "title": "Fight Club", "year": 1999,
			"search_ids": map[string]any{"imdb": "tt0137523", "tmdb": 550},
		}},
	}
}

// twoSearchResults is a two-row scored result payload (tiers computed
// server-side).
func twoSearchResults() map[string]any {
	return map[string]any{"results": []map[string]any{
		{
			"provider": "opensubtitles", "language": "fr",
			"release_name": "Fight.Club.1999.1080p.BluRay.x264-GRP",
			"matched_by":   "imdb", "subtitle_id": "sub-1",
			"tier": "excellent", "score": 93,
			"hearing_impaired": false, "forced": false, "on_disk": false,
		},
		{
			"provider": "subdl", "language": "fr",
			"release_name": "Fight.Club.1999.720p.WEB-DL",
			"matched_by":   "title", "subtitle_id": "sub-2",
			"tier": "acceptable", "score": 40,
			"hearing_impaired": true, "forced": false, "on_disk": false,
		},
	}}
}

// --- exit code 2: usage ---

func TestCLISearch_missing_criteria_is_usage_error(t *testing.T) {
	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t), testRunConfig(&buf))
	if code != 2 {
		t.Errorf("cliSearchRemote(no criteria) = %d, want 2", code)
	}
}

// --- resolve outcomes ---

func TestCLISearch_happy_flow_renders_results(t *testing.T) {
	var searchQuery map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+apipaths.PathSearchResolve, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, fightClubResolve())
	})
	mux.HandleFunc("GET "+apipaths.PathManualSearch, func(w http.ResponseWriter, r *http.Request) {
		searchQuery = map[string]string{}
		for k := range r.URL.Query() {
			searchQuery[k] = r.URL.Query().Get(k)
		}
		writeTestJSON(t, w, twoSearchResults())
	})
	startCLIServer(t, mux)

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Fight Club"), testRunConfig(&buf))
	if code != 0 {
		t.Fatalf("cliSearchRemote(happy) = %d, want 0\noutput:\n%s", code, buf.String())
	}

	out := buf.String()
	for _, want := range []string{
		"Found 1 item(s) to search for fr subtitles",
		"--- Fight Club (1999) ---",
		"2 result(s) ranked by release quality:",
		"[ 93 excellent ]",
		"[HI]",
	} {
		// The bracket assertion below is format-sensitive; build it exactly.
		if want == "[ 93 excellent ]" {
			want = "[ 93 excellent "
		}
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output missing %q\noutput:\n%s", want, out)
		}
	}

	// The search leg forwards the typed identifiers from resolve.
	wantQuery := map[string]string{
		"lang": "fr", "type": "movie", "media_id": "7",
		"title": "Fight Club", "year": "1999", "imdb": "tt0137523", "tmdb": "550",
	}
	for k, v := range wantQuery {
		if searchQuery[k] != v {
			t.Errorf("search query %s = %q, want %q (full query: %v)", k, searchQuery[k], v, searchQuery)
		}
	}
	if _, ok := searchQuery["season"]; ok {
		t.Errorf("movie search query carries season: %v", searchQuery)
	}
}

func TestCLISearch_episode_expansion_searches_each_item(t *testing.T) {
	var seasons, episodes []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+apipaths.PathSearchResolve, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, map[string]any{
			"resolved": true,
			"items": []map[string]any{
				{
					"media_type": "episode", "media_id": 11, "title": "Breaking Bad", "year": 2008,
					"season": 1, "episode": 1,
					"search_ids": map[string]any{"imdb": "tt0903747", "tvdb": 81189},
				},
				{
					"media_type": "episode", "media_id": 11, "title": "Breaking Bad", "year": 2008,
					"season": 1, "episode": 2,
					"search_ids": map[string]any{"imdb": "tt0903747", "tvdb": 81189},
				},
			},
		})
	})
	mux.HandleFunc("GET "+apipaths.PathManualSearch, func(w http.ResponseWriter, r *http.Request) {
		seasons = append(seasons, r.URL.Query().Get("season"))
		episodes = append(episodes, r.URL.Query().Get("episode"))
		writeTestJSON(t, w, map[string]any{"results": []map[string]any{}})
	})
	startCLIServer(t, mux)

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Breaking Bad"), testRunConfig(&buf))
	if code != 0 {
		t.Fatalf("cliSearchRemote(episodes) = %d, want 0\noutput:\n%s", code, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"Found 2 item(s) to search for fr subtitles",
		"--- Breaking Bad S01E01 ---",
		"--- Breaking Bad S01E02 ---",
		"No results found.",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output missing %q\noutput:\n%s", want, out)
		}
	}
	if len(seasons) != 2 || seasons[0] != "1" || episodes[0] != "1" || episodes[1] != "2" {
		t.Errorf("per-item search params: seasons=%v episodes=%v, want two narrowed searches", seasons, episodes)
	}
}

func TestCLISearch_ambiguous_lists_candidates_exit2(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+apipaths.PathSearchResolve, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, map[string]any{
			"resolved": false,
			"candidates": []map[string]any{
				{"media_type": "movie", "media_id": 1, "title": "Dune", "year": 1984},
				{"media_type": "movie", "media_id": 2, "title": "Dune", "year": 2021},
			},
		})
	})
	startCLIServer(t, mux)

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Dune"), testRunConfig(&buf))
	if code != 2 {
		t.Fatalf("cliSearchRemote(ambiguous) = %d, want 2\noutput:\n%s", code, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"Multiple matches", "Dune (1984)", "Dune (2021)", "[id 1]", "[id 2]"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("candidate listing missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestCLISearch_empty_resolution_exit1(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+apipaths.PathSearchResolve, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, map[string]any{"resolved": false})
	})
	startCLIServer(t, mux)

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Nope"), testRunConfig(&buf))
	if code != 1 {
		t.Errorf("cliSearchRemote(empty resolution) = %d, want 1", code)
	}
}

func TestCLISearch_conflicting_identifiers_exit2(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+apipaths.PathSearchResolve, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"conflicting identifiers: tmdb and title match different items","code":"resolve_conflict"}`))
	})
	startCLIServer(t, mux)

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Alien", "--tmdb", "841"), testRunConfig(&buf))
	if code != 2 {
		t.Errorf("cliSearchRemote(conflict 400) = %d, want 2", code)
	}
}

// --- download + poll terminal cases ---

// downloadFixture wires the four endpoints for a download flow. The
// activity handler is swappable per test.
func downloadFixture(t *testing.T, activityHandler http.HandlerFunc) (downloadBody *map[string]any) {
	t.Helper()
	var body map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+apipaths.PathSearchResolve, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, fightClubResolve())
	})
	mux.HandleFunc("GET "+apipaths.PathManualSearch, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, twoSearchResults())
	})
	mux.HandleFunc("POST "+apipaths.PathDownloadSubtitle, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode download body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"activity_id":"17","status":"accepted"}`))
	})
	mux.HandleFunc("GET "+apipaths.PathListActivity, activityHandler)
	startCLIServer(t, mux)
	return &body
}

func TestCLISearch_download_success_reports_saved_path(t *testing.T) {
	savedPath := "/media/movies/Fight Club (1999)/Fight Club.fr.1.srt"
	body := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, []map[string]any{
			{"id": "17", "action": "Manual Download", "detail": savedPath, "done": true},
		})
	})

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Fight Club", "--download"), testRunConfig(&buf))
	if code != 0 {
		t.Fatalf("cliSearchRemote(download success) = %d, want 0\noutput:\n%s", code, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"Downloading #1 from opensubtitles (score=93, excellent)...",
		"Saved: " + savedPath,
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output missing %q\noutput:\n%s", want, out)
		}
	}

	// Top pick rides auto mode; the wire carries typed refs, never paths.
	b := *body
	if b["top_pick"] != true {
		t.Errorf("download body top_pick = %v, want true (pick 1 = top pick)", b["top_pick"])
	}
	if b["provider"] != "opensubtitles" || b["subtitle_id"] != "sub-1" {
		t.Errorf("download body pick = (%v, %v), want top result", b["provider"], b["subtitle_id"])
	}
	if b["media_type"] != "movie" || b["media_id"] != float64(7) {
		t.Errorf("download body MediaRef = (%v, %v), want (movie, 7)", b["media_type"], b["media_id"])
	}
	if _, hasPath := b["file_path"]; hasPath {
		t.Error("download body carries file_path; the wire must be path-free (S7)")
	}
}

func TestCLISearch_download_non_top_pick(t *testing.T) {
	body := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, []map[string]any{
			{"id": "17", "detail": "/somewhere/x.srt", "done": true},
		})
	})

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Fight Club", "--download", "--pick", "2"), testRunConfig(&buf))
	if code != 0 {
		t.Fatalf("cliSearchRemote(pick 2) = %d, want 0\noutput:\n%s", code, buf.String())
	}
	b := *body
	if b["top_pick"] != false {
		t.Errorf("download body top_pick = %v, want false (manual lock semantics preserved)", b["top_pick"])
	}
	if b["subtitle_id"] != "sub-2" {
		t.Errorf("download body subtitle_id = %v, want sub-2", b["subtitle_id"])
	}
}

func TestCLISearch_download_failure_exit1(t *testing.T) {
	downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, []map[string]any{
			{"id": "17", "detail": "opensubtitles sub-1", "done": true, "failed": true},
		})
	})

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Fight Club", "--download"), testRunConfig(&buf))
	if code != 1 {
		t.Fatalf("cliSearchRemote(download failed) = %d, want 1\noutput:\n%s", code, buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("Download failed")) {
		t.Errorf("output missing failure report\noutput:\n%s", buf.String())
	}
}

func TestCLISearch_download_cancelled_exit1(t *testing.T) {
	// The activity outcome shape (S12): a terminally cancelled entry carries
	// done+cancelled — the poll must report it distinctly, never as "Saved".
	downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, []map[string]any{
			{"id": "17", "detail": "opensubtitles sub-1", "done": true, "cancelled": true},
		})
	})

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Fight Club", "--download"), testRunConfig(&buf))
	if code != 1 {
		t.Fatalf("cliSearchRemote(download cancelled) = %d, want 1\noutput:\n%s", code, buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("Download cancelled")) {
		t.Errorf("output missing cancelled report\noutput:\n%s", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("Saved:")) {
		t.Errorf("cancelled entry reported as saved\noutput:\n%s", buf.String())
	}
}

func TestCLISearch_download_id_absent_exit1(t *testing.T) {
	// Ring cap 50 / 15-minute prune / server restart: the entry is gone.
	downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, []map[string]any{})
	})

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Fight Club", "--download"), testRunConfig(&buf))
	if code != 1 {
		t.Fatalf("cliSearchRemote(entry gone) = %d, want 1\noutput:\n%s", code, buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("Download outcome unknown (activity entry gone)")) {
		t.Errorf("output missing unknown-outcome report\noutput:\n%s", buf.String())
	}
}

func TestCLISearch_download_poll_timeout_exit1(t *testing.T) {
	// The entry exists but never completes; the poll cap must terminate the
	// loop (never an infinite loop).
	downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, []map[string]any{
			{"id": "17", "detail": "opensubtitles sub-1", "done": false},
		})
	})

	var buf bytes.Buffer
	rc := testRunConfig(&buf)
	rc.pollTimeout = 20 * time.Millisecond
	start := time.Now()
	code := cliSearchRemote(searchParams(t, "--title", "Fight Club", "--download"), rc)
	if code != 1 {
		t.Fatalf("cliSearchRemote(poll timeout) = %d, want 1\noutput:\n%s", code, buf.String())
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("poll loop ran %s despite a 20ms cap", elapsed)
	}
	if !bytes.Contains(buf.Bytes(), []byte("Download timed out")) {
		t.Errorf("output missing timeout report\noutput:\n%s", buf.String())
	}
}

func TestCLISearch_pick_out_of_range_keeps_exit0(t *testing.T) {
	downloadHit := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+apipaths.PathSearchResolve, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, fightClubResolve())
	})
	mux.HandleFunc("GET "+apipaths.PathManualSearch, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, twoSearchResults())
	})
	mux.HandleFunc("POST "+apipaths.PathDownloadSubtitle, func(w http.ResponseWriter, r *http.Request) {
		downloadHit = true
	})
	startCLIServer(t, mux)

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Fight Club", "--download", "--pick", "9"), testRunConfig(&buf))
	if code != 0 {
		t.Fatalf("cliSearchRemote(pick out of range) = %d, want 0 (pre-remote behavior)\noutput:\n%s", code, buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("--pick 9 but only 2 result(s)")) {
		t.Errorf("output missing out-of-range notice\noutput:\n%s", buf.String())
	}
	if downloadHit {
		t.Error("download endpoint was called despite the out-of-range pick")
	}
}

// --- auth header ---

func TestCLISearch_sends_api_key_on_every_leg(t *testing.T) {
	var missing []string
	check := func(name string, r *http.Request) {
		if r.Header.Get("X-API-Key") != "sekret-key" {
			missing = append(missing, name)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+apipaths.PathSearchResolve, func(w http.ResponseWriter, r *http.Request) {
		check("resolve", r)
		writeTestJSON(t, w, fightClubResolve())
	})
	mux.HandleFunc("GET "+apipaths.PathManualSearch, func(w http.ResponseWriter, r *http.Request) {
		check("search", r)
		writeTestJSON(t, w, twoSearchResults())
	})
	mux.HandleFunc("POST "+apipaths.PathDownloadSubtitle, func(w http.ResponseWriter, r *http.Request) {
		check("download", r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"activity_id":"17","status":"accepted"}`))
	})
	mux.HandleFunc("GET "+apipaths.PathListActivity, func(w http.ResponseWriter, r *http.Request) {
		check("activity", r)
		writeTestJSON(t, w, []map[string]any{{"id": "17", "detail": "/x.srt", "done": true}})
	})
	startCLIServer(t, mux)
	t.Setenv("SUBFLUX_API_KEY", "sekret-key")

	var buf bytes.Buffer
	code := cliSearchRemote(searchParams(t, "--title", "Fight Club", "--download"), testRunConfig(&buf))
	if code != 0 {
		t.Fatalf("cliSearchRemote(api key) = %d, want 0\noutput:\n%s", code, buf.String())
	}
	if len(missing) > 0 {
		t.Errorf("X-API-Key header missing on legs: %v", missing)
	}
}

// --- slow search: the per-request budget, not the shared client ---

func TestCLISearch_slow_search_budget(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+apipaths.PathSearchResolve, func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, fightClubResolve())
	})
	mux.HandleFunc("GET "+apipaths.PathManualSearch, func(w http.ResponseWriter, r *http.Request) {
		// Slower than the injected search budget below, far faster than the
		// production one.
		time.Sleep(250 * time.Millisecond)
		writeTestJSON(t, w, twoSearchResults())
	})
	startCLIServer(t, mux)

	// A search slower than the leg budget fails the leg...
	var buf bytes.Buffer
	rc := testRunConfig(&buf)
	rc.searchTimeout = 40 * time.Millisecond
	if code := cliSearchRemote(searchParams(t, "--title", "Fight Club"), rc); code != 1 {
		t.Errorf("cliSearchRemote(search slower than budget) = %d, want 1", code)
	}

	// ...and succeeds when the budget accommodates the server's work.
	buf.Reset()
	rc.searchTimeout = 5 * time.Second
	if code := cliSearchRemote(searchParams(t, "--title", "Fight Club"), rc); code != 0 {
		t.Errorf("cliSearchRemote(budget above server time) = %d, want 0\noutput:\n%s", code, buf.String())
	}
}

// The production budgets must keep their ordering: the search leg waits
// out the server's 60s search window, and its client timeout stays above
// the per-request context so the context is the effective bound.
func TestSearchLegBudgets(t *testing.T) {
	if searchLegTimeout <= 60*time.Second {
		t.Errorf("searchLegTimeout = %s, must exceed the server's 60s search budget", searchLegTimeout)
	}
	if searchLegClientTimeout <= searchLegTimeout {
		t.Errorf("searchLegClientTimeout = %s, must exceed searchLegTimeout %s", searchLegClientTimeout, searchLegTimeout)
	}
	if cliClient.Timeout >= searchLegTimeout {
		t.Errorf("shared cliClient timeout %s unexpectedly >= search leg budget %s (the dedicated client exists because it is smaller)",
			cliClient.Timeout, searchLegTimeout)
	}
	if downloadPollTimeout != 5*time.Minute {
		t.Errorf("downloadPollTimeout = %s, want 5m (mirrors the server's DownloadTimeout)", downloadPollTimeout)
	}
}

// --- golden: result table rendering ---

// TestRenderSearchResults_golden pins the result-table byte format (tier
// labels, provider column, matched-by, 45-char release truncation, [HI]
// suffix) against testdata/cli-search-render.golden.
// Regenerate: UPDATE_GOLDEN=1 go test . -run TestRenderSearchResults_golden
func TestRenderSearchResults_golden(t *testing.T) {
	results := []cliSearchResult{
		{
			Provider: "opensubtitles", ReleaseName: "Movie.2024.1080p.BluRay.x264-GRP",
			MatchedBy: "imdb", Tier: "excellent", Score: 93,
		},
		{
			Provider: "subdl", ReleaseName: "Movie.2024.720p.WEB-DL.DDP5.1.Atmos.H.264-VeryLongGroup",
			MatchedBy: "title", Tier: "good", Score: 51, HearingImp: true,
		},
		{
			Provider: "yifysubtitles", ReleaseName: "Movie.2024.HDTV",
			MatchedBy: "tmdb", Tier: "none", Score: 0,
		},
	}
	var buf bytes.Buffer
	renderSearchResults(&buf, results)

	golden := filepath.Join("testdata", "cli-search-render.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
			t.Fatalf("create testdata dir: %v", err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s (run UPDATE_GOLDEN=1 go test . -run TestRenderSearchResults_golden): %v", golden, err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("rendered table drifted from golden (regenerate with UPDATE_GOLDEN=1 go test . -run TestRenderSearchResults_golden)\n--- want\n%s\n+++ got\n%s",
			want, buf.Bytes())
	}
}

// TestRenderSearchResults_unicode_boundary: a multibyte rune crossing the
// 42-byte truncation point must never be split (pre-fix, the byte-index
// cut emitted invalid UTF-8 into the rendered row). The truncation keeps
// the largest prefix within the byte budget that ends on a rune boundary,
// then the "..." marker; pure-ASCII truncation stays byte-identical.
// truncateRelease is runesafe.SanitizeSingleLineBounded(name, 42), so the
// cap applies to every name over 42 bytes (the historical cut passed
// 43-45-byte names through marker-free) and unsafe runes become spaces.
func TestRenderSearchResults_unicode_boundary(t *testing.T) {
	cases := []struct {
		name        string
		release     string
		wantRelease string
	}{
		{
			// Historical pass-through window: 43-45-byte names used to render
			// unmarked; the bounded form truncates everything over the 42-byte
			// budget.
			name:        "43-byte name truncates under the bounded form",
			release:     strings.Repeat("a", 43),
			wantRelease: strings.Repeat("a", 42) + "...",
		},
		{
			// Single-line sanitization: an upstream-controlled name must not
			// forge a second table row; the raw newline renders as a space.
			name:        "newline in name is sanitized to a space",
			release:     "Movie.\n2024",
			wantRelease: "Movie. 2024",
		},
		{
			name: "3-byte rune split at the cut",
			// Bytes 41..43 are 日: the naive [:42] kept one of its 3 bytes.
			release:     strings.Repeat("a", 41) + "日本語テスト",
			wantRelease: strings.Repeat("a", 41) + "...",
		},
		{
			name: "4-byte rune split at the cut",
			// Bytes 40..43 are 😀: the cut lands two bytes in.
			release:     strings.Repeat("a", 40) + "😀😀😀",
			wantRelease: strings.Repeat("a", 40) + "...",
		},
		{
			name: "cut aligned on a rune boundary keeps the full budget",
			// Byte 42 starts 日, so the 42-byte prefix is already valid.
			release:     strings.Repeat("a", 42) + "日本語テ",
			wantRelease: strings.Repeat("a", 42) + "...",
		},
		{
			name:        "ascii truncation unchanged",
			release:     strings.Repeat("a", 50),
			wantRelease: strings.Repeat("a", 42) + "...",
		},
		{
			name:        "short multibyte name passes through",
			release:     "映画.2024.1080p",
			wantRelease: "映画.2024.1080p",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			renderSearchResults(&buf, []cliSearchResult{{
				Provider: "opensubtitles", ReleaseName: c.release,
				MatchedBy: "imdb", Tier: "excellent", Score: 93,
			}})
			out := buf.String()
			if !utf8.ValidString(out) {
				t.Errorf("rendered row is not valid UTF-8: %q", out)
			}
			if !strings.Contains(out, c.wantRelease+"\n") {
				t.Errorf("rendered row = %q, want release column %q", out, c.wantRelease)
			}
		})
	}
}
