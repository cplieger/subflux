package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cplieger/envx"
	"github.com/cplieger/runesafe"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/apipaths"
	"github.com/cplieger/subflux/internal/cliparse"
)

// cliClient is the shared HTTP client for the quick remote commands. Its
// timeout is the effective per-request budget (no per-request context
// timeout is layered on top).
var cliClient = &http.Client{Timeout: 30 * time.Second}

// Search-leg transport budgets. The server's manual search runs under a 60s
// budget, so the CLI's search request must wait LONGER than that — the
// shared 30s client would expire first and report a transport error while
// the server was still working. The client timeout exceeds the per-request
// context timeout so the context is the one that fires.
const (
	searchLegTimeout       = 90 * time.Second
	searchLegClientTimeout = 100 * time.Second
	downloadPollInterval   = time.Second
	// downloadPollTimeout mirrors the server's manualops.DownloadTimeout:
	// when the server would have given up on the download, so does the poll.
	downloadPollTimeout = 5 * time.Minute
)

// cliSearchClient is the search-leg HTTP client; see the budget comment
// above for why it is separate from cliClient.
var cliSearchClient = &http.Client{Timeout: searchLegClientTimeout}

// JSON body / query keys shared across the remote commands.
const (
	keyMediaType = "media_type"
	keyMediaID   = "media_id"
)

// serverURL returns the base URL for the running subflux server, or
// empty string if SUBFLUX_URL is malformed (caller decides how to fail;
// keeps the no-os.Exit-from-helpers contract).
func serverURL() (string, bool) {
	u := envx.String("SUBFLUX_URL", "http://127.0.0.1:8374")
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		fmt.Fprintf(os.Stderr, "Error: invalid SUBFLUX_URL %q (must be http:// or https://)\n", u)
		return "", false
	}
	return u, true
}

// --- Shared helpers ---

// cliRequest builds and executes an HTTP request against the running
// server using the shared 30-second client.
func cliRequest(method, path string, body io.Reader) (data []byte, status int, ok bool) {
	return cliRequestWith(cliClient, 0, method, path, body)
}

// cliRequestWith is cliRequest with the client and an optional per-request
// context timeout injected (the search leg needs a budget above the
// server's 60s search window). The request context is cancelled on
// SIGINT/SIGTERM so Ctrl+C aborts cleanly. Returns (body, status, ok).
// When ok is false, the helper has already written an explanatory message
// to stderr and the caller should return exit code 1.
func cliRequestWith(client *http.Client, timeout time.Duration, method, path string, body io.Reader) (data []byte, status int, ok bool) {
	base, ok := serverURL()
	if !ok {
		return nil, 0, false
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return nil, 0, false
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The remote endpoints sit behind session/API-key auth on a configured
	// server. SUBFLUX_API_KEY (generate one with `subflux generate-api-key`)
	// authenticates the CLI via the X-API-Key header the server's verifier
	// reads; without it, commands against an auth-enabled instance answer 401.
	if key := envx.String("SUBFLUX_API_KEY", ""); key != "" {
		req.Header.Set("X-API-Key", key)
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Is the subflux server running?")
		return nil, 0, false
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	resp.Body.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		return nil, 0, false
	}
	if len(data) == 10<<20 {
		fmt.Fprintln(os.Stderr, "Warning: response truncated at 10 MB")
	}
	return data, resp.StatusCode, true
}

// --- Remote CLI commands ---

// runCLIRemote fetches a JSON endpoint and prints the response to
// stdout. Default formatting indents the JSON for human readability;
// `--format json` (or `--format=json`) emits the server's response
// verbatim, suitable for piping to jq or scripted consumers.
//
// Non-2xx responses are printed to stderr; returns 1 on failure.
func runCLIRemote(p cliparse.Params, path string) int {
	data, status, ok := cliRequest(http.MethodGet, path, nil)
	if !ok {
		return 1
	}

	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		return 1
	}

	if rawJSONFormat(p) {
		// Verbatim passthrough: server already produces JSON.
		fmt.Println(string(data))
		return 0
	}

	var buf bytes.Buffer
	if json.Indent(&buf, data, "", "  ") == nil {
		fmt.Println(buf.String())
	} else {
		fmt.Println(string(data))
	}
	return 0
}

// runCLIState fetches subtitle state with optional filters from CLI flags.
func runCLIState(p cliparse.Params) int {
	qv := url.Values{}
	// The spec's declared filters map 1:1 onto /api/state query params;
	// `format` is a CLI-side display flag and stays out of the query.
	for _, k := range []string{cmdType, cmdLang, flagProvider, "limit"} {
		if v := p.String(k); v != "" {
			qv.Set(k, v)
		}
	}
	return runCLIRemote(p, apipaths.PathListState+"?"+qv.Encode())
}

// runCLIAction sends a POST to trigger an action. With `--format json`
// the server's response body is printed verbatim instead of the human
// success message.
func runCLIAction(p cliparse.Params, path, successMsg string) int {
	data, status, ok := cliRequest(http.MethodPost, path, nil)
	if !ok {
		return 1
	}
	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		return 1
	}
	if rawJSONFormat(p) {
		fmt.Println(string(data))
		return 0
	}
	fmt.Println(successMsg)
	return 0
}

// runCLIUnlock clears a manual lock via the server API. Locks are held per
// variant; omitting --variant clears every variant's lock for the language.
// Usage: subflux unlock --type episode --id tt0903747-s01e01 --lang fr [--variant forced]
func runCLIUnlock(p cliparse.Params) int {
	mediaType := p.String(cmdType)
	mediaID := p.String("id")
	lang := p.String(cmdLang)
	// The parse enforces flag presence; explicitly empty values (--id "")
	// are still a usage error.
	if mediaType == "" || mediaID == "" || lang == "" {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr,
			"  subflux unlock --type episode --id tt0903747-s01e01 --lang fr [--variant forced]")
		// 2: usage error per POSIX convention.
		return 2
	}

	payload := map[string]string{
		keyMediaType: mediaType, keyMediaID: mediaID, "language": lang,
	}
	if v := p.String("variant"); v != "" {
		payload["variant"] = v
	}
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	data, status, ok := cliRequest(http.MethodPost,
		apipaths.PathClearLock, bytes.NewReader(body))
	if !ok {
		return 1
	}
	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		return 1
	}
	if rawJSONFormat(p) {
		fmt.Println(string(data))
		return 0
	}
	fmt.Println("Lock cleared")
	return 0
}

// runCLIScore simulates scoring via the server API.
// Usage: subflux score --type episode --video "Name.S01E01.1080p" --sub "Name.S01E01.720p"
func runCLIScore(p cliparse.Params) int {
	mediaType := p.String(cmdType)
	videoRelease := p.String("video")
	subRelease := p.String("sub")
	matchedBy := p.String("match")

	if videoRelease == "" || subRelease == "" {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr,
			"  subflux score --video \"Show.S01E01.1080p.WEB-DL\" "+
				"--sub \"Show.S01E01.720p.HDTV\"")
		fmt.Fprintln(os.Stderr,
			"  subflux score --type movie --video \"Movie.2024.BluRay\" "+
				"--sub \"Movie.2024.WEB-DL\" --match imdb")
		return 2
	}

	body, err := json.Marshal(map[string]string{
		keyMediaType: mediaType, "release_name": videoRelease,
		"sub_release": subRelease, "matched_by": matchedBy,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	data, status, ok := cliRequest(http.MethodPost,
		apipaths.PathScoreRelease, bytes.NewReader(body))
	if !ok {
		return 1
	}
	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		return 1
	}

	if rawJSONFormat(p) {
		// Pass through the server response unchanged for scripted use.
		fmt.Println(string(data))
		return 0
	}

	var result struct {
		Tier        string `json:"tier"`
		Score       int    `json:"score"`
		ScoreNoHash int    `json:"score_no_hash"`
		MinScore    int    `json:"min_score"`
		WouldAccept bool   `json:"would_accept"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		return 1
	}

	accept := "REJECT"
	if result.WouldAccept {
		accept = "ACCEPT"
	}
	fmt.Printf("Score: %d (%s) | No-hash: %d | Min: %d | %s\n",
		result.Score, result.Tier, result.ScoreNoHash,
		result.MinScore, accept)
	return 0
}

// rawJSONFormat reports whether the user asked for `--format json` on the
// current command. The flag is registered in each remote command's spec so
// the single ParseAndValidate pass accepts it; the value arrives with the
// rest of the parsed params (no reparse).
func rawJSONFormat(p cliparse.Params) bool {
	return strings.EqualFold(p.String("format"), "json")
}

// --- Remote search (`subflux search`) ---
//
// The search subcommand drives the running server over HTTP like every
// other remote command: resolve the query onto arr media items
// (GET /api/search/resolve), search each item (GET /api/search), and with
// --download post the pick (POST /api/search/download) and poll the
// activity feed for the outcome. The wire mirror structs below are
// decode-only views of the server's typed DTOs.

// cliResolvedItem mirrors the resolve endpoint's item shape: the API-wide
// (media_type, media_id) identity (arr ID) plus the stable search IDs the
// search leg forwards.
type cliResolvedItem struct {
	MediaType api.MediaType `json:"media_type"`
	Title     string        `json:"title"`
	SearchIDs struct {
		Imdb string `json:"imdb,omitempty"`
		Tvdb int    `json:"tvdb,omitempty"`
		Tmdb int    `json:"tmdb,omitempty"`
	} `json:"search_ids"`
	MediaID int `json:"media_id"`
	Year    int `json:"year,omitempty"`
	Season  int `json:"season,omitempty"`
	Episode int `json:"episode,omitempty"`
}

// cliResolveCandidate mirrors one ambiguity candidate (year disambiguates
// equal titles).
type cliResolveCandidate struct {
	Title     string        `json:"title"`
	MediaType api.MediaType `json:"media_type"`
	MediaID   int           `json:"media_id"`
	Year      int           `json:"year,omitempty"`
}

// cliResolveResponse mirrors the resolve endpoint's typed response.
type cliResolveResponse struct {
	Items      []cliResolvedItem     `json:"items"`
	Candidates []cliResolveCandidate `json:"candidates"`
	Resolved   bool                  `json:"resolved"`
}

// cliSearchResult mirrors the manual-search result fields the CLI renders;
// Tier is computed server-side (the CLI has no scorer).
type cliSearchResult struct {
	Provider    string `json:"provider"`
	Language    string `json:"language"`
	ReleaseName string `json:"release_name"`
	MatchedBy   string `json:"matched_by"`
	SubtitleID  string `json:"subtitle_id"`
	Tier        string `json:"tier"`
	Score       int    `json:"score"`
	HearingImp  bool   `json:"hearing_impaired"`
	Forced      bool   `json:"forced"`
}

// cliActivityEntry mirrors the activity-feed fields the download poll
// reads. Detail carries the saved subtitle path once the server completes
// the download. Terminal states follow the activity outcome shape:
// done+failed, done+cancelled (a terminally cancelled entry), or done alone
// (success).
type cliActivityEntry struct {
	ID        string `json:"id"`
	Detail    string `json:"detail"`
	Done      bool   `json:"done"`
	Failed    bool   `json:"failed"`
	Cancelled bool   `json:"cancelled"`
}

// searchRunConfig carries the search flow's output writer and transport
// budgets. Production values come from runCLISearchRemote; tests inject a
// buffer and millisecond budgets.
type searchRunConfig struct {
	out           io.Writer
	searchTimeout time.Duration
	pollInterval  time.Duration
	pollTimeout   time.Duration
}

// runCLISearchRemote runs `subflux search` against the running server.
func runCLISearchRemote(p cliparse.Params) int {
	return cliSearchRemote(p, &searchRunConfig{
		out:           os.Stdout,
		searchTimeout: searchLegTimeout,
		pollInterval:  downloadPollInterval,
		pollTimeout:   downloadPollTimeout,
	})
}

// cliSearchRemote is the testable body of runCLISearchRemote: resolve →
// per-item search (+ optional download & poll). Exit codes: 0 success, 1
// runtime/no-match/download failure, 2 usage error or non-interactive
// disambiguation required.
func cliSearchRemote(p cliparse.Params, rc *searchRunConfig) int {
	if p.String("imdb") == "" && p.String("tmdb") == "" && p.String("title") == "" {
		fmt.Fprintln(os.Stderr, "Error: no search criteria provided (need --imdb, --tmdb, or --title)")
		return 2
	}
	lang := p.String(cmdLang)

	items, code := resolveSearchItems(p, rc)
	if items == nil {
		return code
	}

	fmt.Fprintf(rc.out, "Found %d item(s) to search for %s subtitles\n\n", len(items), lang)
	exit := 0
	for i := range items {
		if c := searchOneItem(rc, &items[i], lang, p.Bool("download"), p.Int("pick")); c != 0 {
			exit = c
		}
	}
	return exit
}

// resolveSearchItems calls the resolve endpoint and maps its outcome onto
// the CLI contract: items to search (nil items + exit code otherwise), the
// candidate list on ambiguity (exit 2), "no match" on an empty resolution
// (exit 1), and a 400 (contradictory or invalid identifiers) as a usage
// error (exit 2).
func resolveSearchItems(p cliparse.Params, rc *searchRunConfig) (items []cliResolvedItem, exit int) {
	qv := url.Values{}
	for _, k := range []string{"title", "imdb", "tmdb", cmdType} {
		if v := p.String(k); v != "" {
			qv.Set(k, v)
		}
	}
	if n := p.Int(flagSeason); n > 0 {
		qv.Set(flagSeason, strconv.Itoa(n))
	}
	if n := p.Int(flagEpisode); n > 0 {
		qv.Set(flagEpisode, strconv.Itoa(n))
	}

	data, status, ok := cliRequest(http.MethodGet, apipaths.PathSearchResolve+"?"+qv.Encode(), nil)
	if !ok {
		return nil, 1
	}
	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		if status == http.StatusBadRequest {
			// Contradictory or malformed identifiers are a usage error.
			return nil, 2
		}
		return nil, 1
	}

	var resolved cliResolveResponse
	if err := json.Unmarshal(data, &resolved); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing resolve response: %v\n", err)
		return nil, 1
	}
	if len(resolved.Candidates) > 0 {
		fmt.Fprintln(rc.out, "Multiple matches; narrow the query with --imdb/--tmdb or --type:")
		for i := range resolved.Candidates {
			c := &resolved.Candidates[i]
			fmt.Fprintf(rc.out, "  %-6s %s  [id %d]\n", c.MediaType, titleWithYear(c.Title, c.Year), c.MediaID)
		}
		return nil, 2
	}
	if len(resolved.Items) == 0 {
		fmt.Fprintln(os.Stderr, "not found in Sonarr or Radarr; add the media first")
		return nil, 1
	}
	return resolved.Items, 0
}

// titleWithYear renders "Title (Year)", omitting a zero year.
func titleWithYear(title string, year int) string {
	if year > 0 {
		return fmt.Sprintf("%s (%d)", title, year)
	}
	return title
}

// itemLabel returns the human-readable heading for one resolved item,
// matching the pre-remote CLI's labels.
func itemLabel(item *cliResolvedItem) string {
	if item.MediaType == api.MediaTypeEpisode {
		return fmt.Sprintf("%s S%02dE%02d", item.Title, item.Season, item.Episode)
	}
	return fmt.Sprintf("%s (%d)", item.Title, item.Year)
}

// searchOneItem searches the server for one resolved item, renders the
// result table, and optionally downloads the picked result. Returns 0 on
// success (including "no results"), 1 on a failed search or download leg.
func searchOneItem(rc *searchRunConfig, item *cliResolvedItem, lang string, download bool, pick int) int {
	fmt.Fprintf(rc.out, "--- %s ---\n", itemLabel(item))

	qv := url.Values{}
	qv.Set(cmdLang, lang)
	qv.Set(cmdType, string(item.MediaType))
	// media_id (the arr ID) lets the server resolve the video path for
	// hash-based search and release-name defaulting (S7: no client paths).
	qv.Set(keyMediaID, strconv.Itoa(item.MediaID))
	if item.MediaType == api.MediaTypeEpisode {
		qv.Set(flagSeason, strconv.Itoa(item.Season))
		qv.Set(flagEpisode, strconv.Itoa(item.Episode))
	}
	if item.Title != "" {
		qv.Set("title", item.Title)
	}
	if item.Year > 0 {
		qv.Set("year", strconv.Itoa(item.Year))
	}
	if item.SearchIDs.Imdb != "" {
		qv.Set("imdb", item.SearchIDs.Imdb)
	}
	if item.SearchIDs.Tvdb > 0 {
		qv.Set("tvdb", strconv.Itoa(item.SearchIDs.Tvdb))
	}
	if item.SearchIDs.Tmdb > 0 {
		qv.Set("tmdb", strconv.Itoa(item.SearchIDs.Tmdb))
	}

	// The search leg gets its own budget above the server's 60s search
	// window (see searchLegTimeout).
	data, status, ok := cliRequestWith(cliSearchClient, rc.searchTimeout,
		http.MethodGet, apipaths.PathManualSearch+"?"+qv.Encode(), nil)
	if !ok {
		return 1
	}
	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		return 1
	}

	var resp struct {
		Results []cliSearchResult `json:"results"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing search response: %v\n", err)
		return 1
	}
	if len(resp.Results) == 0 {
		fmt.Fprintln(rc.out, "  No results found.")
		fmt.Fprintln(rc.out)
		return 0
	}

	// The full table always prints; --pick only selects the download index.
	renderSearchResults(rc.out, resp.Results)

	code := 0
	if download {
		code = downloadPick(rc, item, resp.Results, pick, lang)
	}
	fmt.Fprintln(rc.out)
	return code
}

// renderSearchResults writes the scored result table. The line format is
// pinned by a golden test and must stay byte-identical to the pre-remote
// CLI's rendering: index, [score tier], provider, matched-by, release name
// sanitized and truncated to a 42-byte budget (rune-boundary safe, "..."
// marker), and the [HI] suffix. Tier arrives on the wire (computed by the
// server's scorer).
func renderSearchResults(w io.Writer, results []cliSearchResult) {
	fmt.Fprintf(w, "  %d result(s) ranked by release quality:\n", len(results))
	for i := range results {
		r := &results[i]
		hi := ""
		if r.HearingImp {
			hi = " [HI]"
		}
		fmt.Fprintf(w, "  %2d. [%3d %-10s] %-14s %-6s %s%s\n",
			i+1, r.Score, r.Tier, r.Provider, r.MatchedBy, truncateRelease(r.ReleaseName), hi)
	}
}

// truncateRelease bounds a release name for the fixed-width result table:
// runesafe's single-line preset replaces control/bidi runes (an
// upstream-controlled name must not forge table rows or reorder terminal
// output) and names over 42 bytes are cut on a rune boundary with the "..."
// marker appended (at most 45 bytes total). For pure-ASCII names within the
// budget the output is byte-identical to the historical rendering (pinned
// by the golden test); names of 43-45 bytes, which the historical cut
// passed through marker-free, now truncate like everything over budget.
func truncateRelease(name string) string {
	return runesafe.SanitizeSingleLineBounded(name, 42)
}

// downloadPick posts the picked result to the download endpoint and polls
// for the outcome. A pick beyond the result count prints a notice and
// keeps exit 0 (matching the pre-remote CLI); a non-positive pick falls
// back to 1 (top pick).
func downloadPick(rc *searchRunConfig, item *cliResolvedItem, results []cliSearchResult, pick int, lang string) int {
	if pick < 1 {
		pick = 1
	}
	idx := pick - 1
	if idx >= len(results) {
		fmt.Fprintf(rc.out, "  --pick %d but only %d result(s)\n", pick, len(results))
		return 0
	}
	chosen := &results[idx]
	fmt.Fprintf(rc.out, "  Downloading #%d from %s (score=%d, %s)...\n",
		pick, chosen.Provider, chosen.Score, chosen.Tier)

	payload := map[string]any{
		flagProvider:       chosen.Provider,
		"subtitle_id":      chosen.SubtitleID,
		"language":         lang,
		"release_name":     chosen.ReleaseName,
		keyMediaType:       item.MediaType,
		keyMediaID:         item.MediaID,
		"score":            chosen.Score,
		"top_pick":         idx == 0,
		"hearing_impaired": chosen.HearingImp,
		"forced":           chosen.Forced,
	}
	if item.MediaType == api.MediaTypeEpisode {
		payload[flagSeason] = item.Season
		payload[flagEpisode] = item.Episode
	}
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	data, status, ok := cliRequest(http.MethodPost, apipaths.PathDownloadSubtitle, bytes.NewReader(body))
	if !ok {
		return 1
	}
	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		return 1
	}

	var accepted struct {
		ActivityID string `json:"activity_id"`
	}
	if err := json.Unmarshal(data, &accepted); err != nil || accepted.ActivityID == "" {
		fmt.Fprintf(os.Stderr, "Error parsing download response: %v\n", err)
		return 1
	}
	return pollDownloadOutcome(rc, accepted.ActivityID)
}

// pollDownloadOutcome polls the activity feed for the download's terminal
// state at rc.pollInterval, capped at rc.pollTimeout. Terminal cases: done
// (report the saved path from the entry detail), failed, and ID absent
// from the feed (activity ring cap, 15-minute prune, or server restart) —
// reported as unknown-outcome. Never an infinite loop.
func pollDownloadOutcome(rc *searchRunConfig, activityID string) int {
	deadline := time.Now().Add(rc.pollTimeout)
	for {
		data, status, ok := cliRequest(http.MethodGet, apipaths.PathListActivity, nil)
		if !ok {
			return 1
		}
		if status >= 300 {
			fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
			return 1
		}
		var entries []cliActivityEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing activity feed: %v\n", err)
			return 1
		}
		if code, terminal := reportDownloadEntry(rc.out, entries, activityID); terminal {
			return code
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(rc.out, "  Download timed out after %s (may still finish server-side; check the activity feed)\n",
				rc.pollTimeout)
			return 1
		}
		// Plain sleep is fine here: this is a foreground CLI loop, and
		// Ctrl+C terminates the process with the default disposition
		// between requests.
		time.Sleep(rc.pollInterval)
	}
}

// reportDownloadEntry maps one activity-feed snapshot onto the download's
// terminal outcome, writing the report line. terminal is false while the
// entry exists but has not completed (keep polling).
func reportDownloadEntry(w io.Writer, entries []cliActivityEntry, activityID string) (code int, terminal bool) {
	var entry *cliActivityEntry
	for i := range entries {
		if entries[i].ID == activityID {
			entry = &entries[i]
			break
		}
	}
	switch {
	case entry == nil:
		fmt.Fprintln(w, "  Download outcome unknown (activity entry gone)")
		return 1, true
	case entry.Done && entry.Failed:
		fmt.Fprintf(w, "  Download failed (%s)\n", entry.Detail)
		return 1, true
	case entry.Done && entry.Cancelled:
		fmt.Fprintf(w, "  Download cancelled (%s)\n", entry.Detail)
		return 1, true
	case entry.Done:
		fmt.Fprintf(w, "  Saved: %s\n", entry.Detail)
		return 0, true
	default:
		return 0, false
	}
}
