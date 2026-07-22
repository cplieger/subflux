//go:build functional

package functional

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// This file ports run.sh's operational sections: scans, sync, poster_proxy,
// manual_download, real_providers, plus the start_scan/poll_scan_terminal
// helpers backing the 202 + activity-monitor contract.

// startScan mirrors run.sh's start_scan: POST a scan start, assert the
// 202 + activity_id contract, and return the activity id ("" on failure,
// the bash helper's SCAN_ACT_ID + non-zero return).
func (s *suite) startScan(label, path, body string) string {
	resp := s.apiPost(path, body)
	if s.lastStatus != "202" {
		s.fail(fmt.Sprintf("%s: HTTP %s, want 202", label, s.lastStatus))
		return ""
	}
	id := fieldRawOrEmpty(resp, ".activity_id")
	if id == "" {
		s.fail(fmt.Sprintf("%s: 202 body carries no activity_id: %s", label, resp))
		return ""
	}
	s.pass(fmt.Sprintf("%s: 202 + activity_id=%s", label, id))
	return id
}

// pollScanTerminal mirrors run.sh's poll_scan_terminal: poll
// GET /api/activity (the 202 contract's status monitor) at 1s cadence until
// the entry reaches a terminal state. Returns cancelled/failed
// ("true"/"false") and whether a terminal state was observed.
func (s *suite) pollScanTerminal(label, id string, timeout int) (cancelled, failed string, ok bool) {
	cancelled, failed = "false", "false"
	for waited := range timeout {
		body, _ := s.curlSF(10*time.Second, s.baseURL+"/api/activity")
		row := activityRow(body, id)
		if row == "" {
			s.fail(fmt.Sprintf("%s: activity entry %s vanished before terminal state", label, id))
			return cancelled, failed, false
		}
		if fieldRaw(row, ".done") == "true" {
			cancelled = fieldRawOrFalse(row, ".cancelled")
			failed = fieldRawOrFalse(row, ".failed")
			s.pass(fmt.Sprintf("%s: terminal after %ds (cancelled=%s failed=%s)", label, waited, cancelled, failed))
			return cancelled, failed, true
		}
		time.Sleep(time.Second)
	}
	s.fail(fmt.Sprintf("%s: not terminal after %ds", label, timeout))
	return cancelled, failed, false
}

// runningProbe counts live (not-done) activity entries for id — the bash
// `[.[] | select(.id == $id and (.done | not))] | length` idempotency probe.
func (s *suite) runningProbe(id string) string {
	body, _ := s.curlSF(10*time.Second, s.baseURL+"/api/activity")
	return runningLen(body, id)
}

func (s *suite) sectionScans() {
	s.log("=== Scan Operations (202 + activity monitor + explicit stop) ===")

	s.applyMockConfig("static", `      result_count: "1"`, "", "")
	time.Sleep(3 * time.Second)

	seriesBody, _ := s.curlSF(30*time.Second, s.baseURL+"/api/media/series")
	sid := seriesPick(seriesBody)
	if sid == "" || sid == "null" {
		seriesBody, _ = s.curlSF(30*time.Second, s.baseURL+"/api/media/series")
		sid = fieldJSONOrEmpty(seriesBody, ".[0].id")
	}

	if sid != "" && sid != "null" {
		// Series scan: 202 immediately, completion observed via activity
		// polling.
		if actID := s.startScan("Scan series/"+sid, "/api/scan/series/"+sid, ""); actID != "" {
			// Idempotent same-scope start: while running/queued, a re-POST
			// returns the SAME activity id (a completed first scan
			// legitimately mints a new one, so only assert when the first
			// is still live).
			running := s.runningProbe(actID)
			rerunID := fieldRawOrEmpty(s.apiPost("/api/scan/series/"+sid, ""), ".activity_id")
			if running == "1" {
				if rerunID == actID {
					s.pass("Idempotent same-scope start returns running id")
				} else {
					s.fail(fmt.Sprintf("Idempotent start: got id %s, want %s", rerunID, actID))
				}
			} else {
				s.log("Idempotency probe skipped (first scan already terminal)")
			}
			s.pollScanTerminal("Scan series/"+sid, actID, 180)
		}

		if actID := s.startScan("Scan season/"+sid+"/1", "/api/scan/season/"+sid+"/1", ""); actID != "" {
			s.pollScanTerminal("Scan season/"+sid+"/1", actID, 180)
		}

		episodesBody, _ := s.curlSF(30*time.Second, s.baseURL+"/api/media/series/"+sid+"/episodes")
		eid := fieldJSONOrEmpty(episodesBody, ".[0].id")
		if eid != "" && eid != "null" {
			body := fmt.Sprintf(`{"media_type":"episode","media_id":%s,"season":1,"episode":1}`, sid)
			if actID := s.startScan("Scan item: episode", "/api/scan/item", body); actID != "" {
				s.pollScanTerminal("Scan item: episode", actID, 180)
			}
		}
	} else {
		s.skip("No series for scan tests")
	}

	moviesBody, _ := s.curlSF(30*time.Second, s.baseURL+"/api/media/movies")
	mid := fieldJSONOrEmpty(moviesBody, ".[0].id")
	if mid != "" && mid != "null" {
		if actID := s.startScan("Scan movie/"+mid, "/api/scan/movie/"+mid, ""); actID != "" {
			s.pollScanTerminal("Scan movie/"+mid, actID, 180)
		}
		body := fmt.Sprintf(`{"media_type":"movie","media_id":%s}`, mid)
		if actID := s.startScan("Scan item: movie", "/api/scan/item", body); actID != "" {
			s.pollScanTerminal("Scan item: movie", actID, 180)
		}
	} else {
		s.skip("No movies for scan tests")
	}

	s.fullScanStopFlow()

	// Edge cases
	s.apiPost("/api/scan/series/0", "")
	s.assertStatus("400", "Scan series/0 rejected")
	s.apiPost("/api/scan/movie/0", "")
	s.assertStatus("400", "Scan movie/0 rejected")
	s.apiPost("/api/scan/series/99999999", "")
	s.assertStatus("404", "Scan unknown series: synchronous preflight 404")
	s.apiPost("/api/scan/item", "{}")
	s.assertStatus("400", "Scan item empty rejected")
	s.apiPost("/api/scan/item", `{"media_type":"invalid","media_id":1}`)
	s.assertStatus("400", "Scan item invalid rejected")
	s.apiPost("/api/activity/999999/cancel", "")
	s.assertStatus("404", "Cancel unknown activity")

	s.apiPut("/api/config", s.original)
	time.Sleep(2 * time.Second)
}

// fullScanStopFlow mirrors run.sh's full scan + explicit stop block: start
// it, assert the idempotent duplicate start (R1.5: a second POST answers
// 202 with the RUNNING scan's id, never the old 409), stop it via the
// cancel endpoint, and observe the terminal cancelled state (graceful: the
// item in flight completes first, so allow real polling time).
func (s *suite) fullScanStopFlow() {
	fullResp := s.apiPost("/api/scan", "")
	if s.lastStatus != "202" {
		// 409 is no longer a duplicate-start answer (R1.5): a start while a
		// scan runs must return 202 + the running scan's id.
		s.fail(fmt.Sprintf("Full scan: HTTP %s, want 202", s.lastStatus))
		return
	}
	fullID := fieldRawOrEmpty(fullResp, ".activity_id")
	if fullID == "" {
		s.fail(fmt.Sprintf("Full scan 202 body carries no activity_id: %s", fullResp))
		return
	}
	s.pass(fmt.Sprintf("Full scan trigger: 202 + activity_id=%s", fullID))

	s.dupFullScanProbe(fullID)

	s.apiPost("/api/activity/"+fullID+"/cancel", "")
	switch s.lastStatus {
	case "204":
		s.pass("Stop full scan: 204")
		// Idempotent second stop.
		s.apiPost("/api/activity/"+fullID+"/cancel", "")
		if s.lastStatus == "204" || s.lastStatus == "409" {
			s.pass(fmt.Sprintf("Repeated stop idempotent (HTTP %s)", s.lastStatus))
		} else {
			s.fail(fmt.Sprintf("Repeated stop: HTTP %s", s.lastStatus))
		}
		if cancelled, failed, ok := s.pollScanTerminal("Stopped full scan", fullID, 300); ok {
			if cancelled == "true" {
				s.pass("Stopped full scan reports cancelled terminal state")
			} else {
				s.fail(fmt.Sprintf("Stopped full scan: cancelled=%s failed=%s, want cancelled=true", cancelled, failed))
			}
		}
	case "409":
		// The scan finished before the stop landed (tiny library): not
		// cancellable is the honest answer.
		s.pass("Stop full scan raced completion (HTTP 409, acceptable)")
	default:
		s.fail(fmt.Sprintf("Stop full scan: HTTP %s", s.lastStatus))
	}
}

// dupFullScanProbe mirrors the idempotent duplicate full-scan start: only
// probed while the first scan is still live (a completed scan legitimately
// mints a new id).
func (s *suite) dupFullScanProbe(fullID string) {
	if s.runningProbe(fullID) != "1" {
		s.log("Duplicate full-scan probe skipped (scan already terminal)")
		return
	}
	dupResp := s.apiPost("/api/scan", "")
	dupStatus := s.lastStatus
	dupID := fieldRawOrEmpty(dupResp, ".activity_id")
	if dupStatus == "202" && dupID == fullID {
		s.pass("Duplicate full-scan start returns running id (202)")
		return
	}
	// The scan may have finished between the probe and the POST; in that
	// case the duplicate legitimately started a fresh scan.
	if s.runningProbe(fullID) == "1" {
		s.fail(fmt.Sprintf("Duplicate full-scan start: HTTP %s id=%s, want 202 + running id %s", dupStatus, dupID, fullID))
	} else {
		s.log("Duplicate full-scan probe inconclusive (scan finished mid-probe)")
		if dupID != "" {
			s.apiPost("/api/activity/"+dupID+"/cancel", "")
		}
	}
}

// curlEncode mirrors curl --data-urlencode's byte encoding: QueryEscape's
// set with %20 for spaces (curl never emits '+').
func curlEncode(v string) string {
	return strings.ReplaceAll(url.QueryEscape(v), "+", "%20")
}

func (s *suite) sectionSync() {
	s.log("=== Sync & Preview ===")

	state := s.apiGet("/api/state?limit=1")
	firstPath := fieldRawOrEmpty(state, ".[0].path")

	if firstPath != "" {
		s.apiGet("/api/preview/start?subtitle=" + curlEncode(firstPath))
		s.assertStatus("200", "Preview start")

		s.apiGet("/api/preview/subtitle?path=" + curlEncode(firstPath) + "&start=0&shift=0")
		s.assertStatus("200", "Preview subtitle")

		s.apiPost("/api/sync/offset", fmt.Sprintf(`{"subtitle_path":"%s","offset_ms":0}`, firstPath))
		s.assertStatus("200", "Sync offset no-op")
	} else {
		s.skip("No subtitles for sync/preview")
	}

	s.apiPost("/api/sync/audio", `{"subtitle_path":"/nonexistent.srt","video_path":"/nonexistent.mkv"}`)
	s.logf("Sync audio nonexistent: HTTP %s", s.lastStatus)
	s.apiPost("/api/sync/offset", "{}")
	s.logf("Sync offset empty: HTTP %s", s.lastStatus)
}

func (s *suite) sectionPosterProxy() {
	s.log("=== Poster Proxy ===")

	moviesBody, _ := s.curlSF(30*time.Second, s.baseURL+"/api/media/movies")
	mid := fieldJSONOrEmpty(moviesBody, ".[0].id")
	if mid != "" && mid != "null" {
		s.apiGet("/api/preview/poster?type=movie&id=" + mid)
		s.assertStatus("200", "Poster: movie")
		s.apiGet("/api/preview/poster?type=movie&id=" + mid + "&style=fanart")
		s.logf("Poster fanart: HTTP %s", s.lastStatus)
	} else {
		s.skip("No movies for poster")
	}

	seriesBody, _ := s.curlSF(30*time.Second, s.baseURL+"/api/media/series")
	sid := fieldJSONOrEmpty(seriesBody, ".[0].id")
	if sid != "" && sid != "null" {
		s.apiGet("/api/preview/poster?type=series&id=" + sid)
		s.assertStatus("200", "Poster: series")
	} else {
		s.skip("No series for poster")
	}

	s.apiGet("/api/preview/poster?type=movie")
	s.logf("Poster no id: HTTP %s", s.lastStatus)
	s.apiGet("/api/preview/poster?type=invalid&id=1")
	s.logf("Poster invalid type: HTTP %s", s.lastStatus)
}

func (s *suite) sectionManualDownload() {
	s.log("=== Manual Download Validation ===")
	s.apiPost("/api/search/download", `{"provider":"nonexistent","subtitle_id":"x","file_path":"/media/test.mkv","language":"en"}`)
	s.logf("Download invalid provider: HTTP %s", s.lastStatus)

	s.apiPost("/api/search/download", `{"provider":"mock"}`)
	s.logf("Download missing fields: HTTP %s", s.lastStatus)

	s.apiPost("/api/search/download", `{"provider":"mock","subtitle_id":"x","file_path":"/etc/passwd","language":"en"}`)
	s.logf("Download path traversal: HTTP %s", s.lastStatus)

	s.apiPost("/api/search/download", `{"provider":"mock","subtitle_id":"x","file_path":"/media/test.mkv","language":"xx"}`)
	s.logf("Download invalid lang: HTTP %s", s.lastStatus)
}

func (s *suite) sectionRealProviders() {
	s.log("=== Real Provider Smoke Tests ===")

	r := s.apiGet("/api/search?title=Inception&year=2010&lang=en&type=movie&imdb=tt1375666&tmdb=27205")
	s.assertStatus("200", "Real: movie (Inception)")
	total := resultsLen(r)
	s.logf("Real: movie returned %s results", total)

	r = s.apiGet("/api/search?title=Breaking+Bad&season=1&episode=1&lang=en&type=episode&imdb=tt0903747&tvdb=81189")
	s.assertStatus("200", "Real: TV (Breaking Bad)")
	total = resultsLen(r)
	s.logf("Real: TV returned %s results", total)

	r = s.apiGet("/api/search?title=Inception&year=2010&lang=fr&type=movie&imdb=tt1375666")
	s.assertStatus("200", "Real: French movie")
	total = resultsLen(r)
	s.logf("Real: French returned %s results", total)

	r = s.apiGet("/api/search?title=Bleach&season=1&episode=1&lang=en&type=episode&tvdb=74796")
	s.assertStatus("200", "Real: anime (Bleach)")
	total = resultsLen(r)
	s.logf("Real: anime returned %s results", total)
	if n, ok := shellInt(total); ok && n > 0 {
		s.logf("Per-provider: %s", providerBreakdown(r))
	}
}
