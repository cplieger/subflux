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
	"strings"
	"syscall"
	"time"

	"subflux/internal/cliparse"
)

// cliClient is a shared HTTP client for CLI commands with a reasonable timeout.
var cliClient = &http.Client{Timeout: 30 * time.Second}

// serverURL returns the base URL for the running subflux server, or
// empty string if SUBFLUX_URL is malformed (caller decides how to fail;
// keeps the no-os.Exit-from-helpers contract).
func serverURL() (string, bool) {
	u := envOr("SUBFLUX_URL", "http://127.0.0.1:8374")
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
// server. The request context is cancelled on SIGINT/SIGTERM so Ctrl+C
// aborts cleanly. Returns (body, status, ok). When ok is false, the
// helper has already written an explanatory message to stderr and the
// caller should return exit code 1.
func cliRequest(method, path string, body io.Reader) (data []byte, status int, ok bool) {
	base, ok := serverURL()
	if !ok {
		return nil, 0, false
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		stop()
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return nil, 0, false
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cliClient.Do(req)
	if err != nil {
		stop()
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Is the subflux server running?")
		return nil, 0, false
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	resp.Body.Close()
	stop()
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
func runCLIRemote(path string) int {
	data, status, ok := cliRequest(http.MethodGet, path, nil)
	if !ok {
		return 1
	}

	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		return 1
	}

	if rawJSONFormat() {
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

// runCLIState fetches subtitle state with optional filters from CLI args.
func runCLIState() int {
	params, _ := cliparse.ParseArgs(os.Args[2:])
	qv := url.Values{}
	for k, v := range params {
		// `format` is a CLI-side display flag, not a query parameter.
		if k == "format" {
			continue
		}
		qv.Set(k, v)
	}
	if qv.Get("limit") == "" {
		qv.Set("limit", "20")
	}
	return runCLIRemote("/api/state?" + qv.Encode())
}

// runCLIAction sends a POST to trigger an action. With `--format json`
// the server's response body is printed verbatim instead of the human
// success message.
func runCLIAction(path, successMsg string) int {
	data, status, ok := cliRequest(http.MethodPost, path, nil)
	if !ok {
		return 1
	}
	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		return 1
	}
	if rawJSONFormat() {
		fmt.Println(string(data))
		return 0
	}
	fmt.Println(successMsg)
	return 0
}

// runCLIUnlock clears a manual lock via the server API.
// Usage: subflux unlock --type episode --id tt0903747-s01e01 --lang fr
func runCLIUnlock() int {
	params, _ := cliparse.ParseArgs(os.Args[2:])
	mediaType := params["type"]
	mediaID := params["id"]
	lang := params["lang"]
	if mediaType == "" || mediaID == "" || lang == "" {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr,
			"  subflux unlock --type episode --id tt0903747-s01e01 --lang fr")
		// 2: usage error per POSIX convention.
		return 2
	}

	body, err := json.Marshal(map[string]string{
		"media_type": mediaType, "media_id": mediaID, "language": lang,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	data, status, ok := cliRequest(http.MethodPost,
		"/api/search/clear-lock", bytes.NewReader(body))
	if !ok {
		return 1
	}
	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		return 1
	}
	if rawJSONFormat() {
		fmt.Println(string(data))
		return 0
	}
	fmt.Println("Lock cleared")
	return 0
}

// runCLIScore simulates scoring via the server API.
// Usage: subflux score --type episode --video "Name.S01E01.1080p" --sub "Name.S01E01.720p"
func runCLIScore() int {
	params, _ := cliparse.ParseArgs(os.Args[2:])
	mediaType := params["type"]
	if mediaType == "" {
		mediaType = "episode"
	}
	videoRelease := params["video"]
	subRelease := params["sub"]
	matchedBy := params["match"]

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
		"media_type": mediaType, "release_name": videoRelease,
		"sub_release": subRelease, "matched_by": matchedBy,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	data, status, ok := cliRequest(http.MethodPost,
		"/api/score", bytes.NewReader(body))
	if !ok {
		return 1
	}
	if status >= 300 {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", status, string(data))
		return 1
	}

	if rawJSONFormat() {
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

// rawJSONFormat reports whether the user asked for `--format json` on
// the current command. The flag is registered in each remote command's
// spec so cliparse's strict-validation accepts it. Local commands that
// don't request it ignore the flag. Subflux's CLI uses the
// space-separated `--key value` form throughout (see internal/clisearch
// ParseArgs); the GNU-style `--key=value` form is not supported here.
func rawJSONFormat() bool {
	params, _ := cliparse.ParseArgs(os.Args[2:])
	return strings.EqualFold(params["format"], "json")
}
