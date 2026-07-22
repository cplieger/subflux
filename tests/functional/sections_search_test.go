//go:build functional

package functional

import (
	"fmt"
	"time"
)

// This file ports run.sh's search-focused sections: manual_search,
// search_resolve, scoring, backoff, mock_provider, provider_errors.

func (s *suite) sectionManualSearch() {
	s.log("=== Manual Search ===")

	r := s.apiGet("/api/search?title=Inception&year=2010&lang=en&type=movie&imdb=tt1375666")
	s.assertStatus("200", "Search: movie (Inception)")
	s.assertJSONNotEmpty(r, ".results", "Movie results array")

	s.apiGet("/api/search?title=Inception&year=2010&lang=en&type=movie&tmdb=27205")
	s.assertStatus("200", "Search: movie with TMDB")

	r = s.apiGet("/api/search?title=Breaking+Bad&season=1&episode=1&lang=en&type=episode&imdb=tt0903747&tvdb=81189")
	s.assertStatus("200", "Search: TV episode")
	s.assertJSONNotEmpty(r, ".results", "Episode results array")

	s.apiGet("/api/search?title=Bleach&season=1&episode=1&lang=en&type=episode&absolute_episode=1&tvdb=74796")
	s.assertStatus("200", "Search: anime absolute ep")

	s.apiGet("/api/search?title=Bleach&season=9&episode=15&lang=en&type=episode&scene_season=9&scene_episode=15&tvdb=74796")
	s.assertStatus("200", "Search: scene numbering")

	for _, lang := range []string{"fr", "de", "es", "pt", "ja", "zh", "ar", "ko", "pb"} {
		s.apiGet("/api/search?title=Inception&year=2010&lang=" + lang + "&type=movie")
		s.assertStatus("200", "Search: lang="+lang)
	}

	s.apiGet("/api/search?lang=en&type=movie")
	s.assertStatus("200", "Search: no title")
	s.apiGet("/api/search?title=Test&lang=xx&type=movie")
	s.logf("Invalid lang: HTTP %s", s.lastStatus)
	s.apiGet("/api/search?title=Test&lang=en&type=invalid")
	s.logf("Invalid type: HTTP %s", s.lastStatus)

	s.apiGet("/api/search/targets?orig_lang=en&audio_langs=en")
	s.assertStatus("200", "Targets: en audio")
	s.apiGet("/api/search/targets?orig_lang=ja&audio_langs=ja")
	s.assertStatus("200", "Targets: ja audio")
	s.apiGet("/api/search/targets?orig_lang=fr&audio_langs=fr")
	s.assertStatus("200", "Targets: fr audio")
}

// sectionSearchResolve is the remote CLI search's resolution leg: user
// query -> arr media items. Arr-dependent (lists the live Sonarr/Radarr
// libraries).
func (s *suite) sectionSearchResolve() {
	s.log("=== Search Resolve ===")

	r := s.apiGet("/api/search/resolve?title=Inception&type=movie")
	s.assertStatus("200", "Resolve: movie by title")
	s.assertJSON(r, ".resolved", "true", "Movie resolves")
	s.assertJSONLen(r, ".items", "eq", 1, "Movie yields one item")
	s.assertJSON(r, ".items[0].media_type", "movie", "Movie item type")
	s.assertJSONNotEmpty(r, ".items[0].media_id", "Movie item carries arr id")

	r = s.apiGet("/api/search/resolve?title=Breaking+Bad&type=series&season=1&episode=1")
	s.assertStatus("200", "Resolve: series narrowed to S01E01")
	s.assertJSON(r, ".resolved", "true", "Series resolves")
	s.assertJSONLen(r, ".items", "eq", 1, "Season+episode narrowing yields one item")
	s.assertJSON(r, ".items[0].media_type", "episode", "Series expands to episode items")

	r = s.apiGet("/api/search/resolve?title=Breaking+Bad")
	s.assertStatus("200", "Resolve: absent type falls back series-then-movie")
	s.assertJSON(r, ".resolved", "true", "Fallback resolves the series")

	r = s.apiGet("/api/search/resolve?title=No-Such-Title-Zzz")
	s.assertStatus("200", "Resolve: unknown title answers 200")
	s.assertJSON(r, ".resolved", "false", "Unknown title is an empty result")

	s.apiGet("/api/search/resolve?type=movie")
	s.assertStatus("400", "Resolve: missing identifiers rejected")
	s.apiGet("/api/search/resolve?title=x&type=album")
	s.assertStatus("400", "Resolve: invalid type rejected")
	s.apiGet("/api/search/resolve?tmdb=abc")
	s.assertStatus("400", "Resolve: non-integer tmdb rejected")
}

func (s *suite) sectionScoring() {
	s.log("=== Score Simulation ===")

	sc := s.apiPost("/api/score", `{"media_type":"movie","video_release":"Inception.2010.1080p.BluRay.x264-SPARKS","sub_release":"Inception.2010.1080p.BluRay.x264-SPARKS","matched_by":"title"}`)
	s.assertStatus("200", "Score: exact match")
	s.assertJSONNotEmpty(sc, ".score", "Has score")
	s.assertJSONNotEmpty(sc, ".tier", "Has tier")

	sc = s.apiPost("/api/score", `{"media_type":"movie","video_release":"X","sub_release":"X","matched_by":"hash"}`)
	s.assertStatus("200", "Score: hash match")
	hs := fieldJSONOrZero(sc, ".score")
	if n, ok := shellInt(hs); ok && n >= 100 {
		s.pass(fmt.Sprintf("Hash score >= 100 (%s)", hs))
	} else {
		s.fail(fmt.Sprintf("Hash score %s < 100", hs))
	}

	s.apiPost("/api/score", `{"media_type":"episode","video_release":"Show.S01E01.1080p.BluRay.x264-DEMAND","sub_release":"Show.S01E01.720p.HDTV.x264-LOL","matched_by":"title"}`)
	s.assertStatus("200", "Score: different releases")

	s.apiPost("/api/score", `{"media_type":"movie","video_release":"","sub_release":"","matched_by":"title"}`)
	s.assertStatus("200", "Score: empty releases")

	s.apiPost("/api/score", `{"media_type":"episode","video_release":"Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.x264-GROUP","sub_release":"Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.x264-GROUP","matched_by":"title"}`)
	s.assertStatus("200", "Score: streaming service")

	s.apiPost("/api/score", `{"media_type":"movie","video_release":"Movie.2024.Directors.Cut.2160p.UHD.BluRay.HDR.x265-GROUP","sub_release":"Movie.2024.Directors.Cut.2160p.UHD.BluRay.HDR.x265-GROUP","matched_by":"title"}`)
	s.assertStatus("200", "Score: edition + HDR")
}

func (s *suite) sectionBackoff() {
	s.log("=== Backoff & Locks ===")
	s.apiGet("/api/backoff")
	s.assertStatus("200", "GET /api/backoff")
	s.apiGet("/api/backoff/prefix?type=episode&prefix=tvdb-81189-")
	s.assertStatus("200", "Backoff prefix: episode")
	s.apiGet("/api/backoff/prefix?type=movie&prefix=tmdb-27205")
	s.assertStatus("200", "Backoff prefix: movie")
	s.apiGet("/api/locks")
	s.assertStatus("200", "GET /api/locks")
	s.apiPost("/api/search/clear-lock", `{"media_type":"movie","media_id":"tmdb-0","language":"en"}`)
	s.logf("Clear lock (nonexistent): HTTP %s", s.lastStatus)
}

func (s *suite) sectionMockProvider() {
	s.log("=== Mock Provider Modes ===")

	s.applyMockConfig("static", `      result_count: "5"`, "", "")
	r := s.apiGet("/api/search?title=Test+Movie&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock static: search")
	count := resultsLen(r)
	if n, ok := shellInt(count); ok && n > 0 {
		s.pass(fmt.Sprintf("Mock static: %s results", count))
	} else {
		s.fail("Mock static: 0 results")
	}

	s.applyMockConfig("empty", "", "", "")
	r = s.apiGet("/api/search?title=Test+Movie&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock empty: search")
	count = resultsLen(r)
	if n, ok := shellInt(count); ok && n == 0 {
		s.pass("Mock empty: 0 results")
	} else {
		s.fail(fmt.Sprintf("Mock empty: %s results", count))
	}

	s.applyMockConfig("error", `      error_message: "test-failure-42"`, "", "")
	s.apiGet("/api/search?title=Test&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock error: endpoint 200")

	s.applyMockConfig("auth_error", "", "", "")
	s.apiGet("/api/search?title=Test&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock auth_error: endpoint 200")

	s.applyMockConfig("rate_limit", "", "", "")
	s.apiGet("/api/search?title=Test&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock rate_limit: endpoint 200")

	s.applyMockConfig("timeout", "", "", "")
	s.apiGet("/api/search?title=Test&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock timeout: endpoint 200")

	s.applyMockConfig("static", `      include_hash: "true"
      result_count: "3"`, "", "")
	s.apiGet("/api/search?title=Test&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock hash: search")

	s.applyMockConfig("static", `      hearing_impaired: "true"
      result_count: "2"`, "", "")
	s.apiGet("/api/search?title=Test&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock HI: search")

	s.applyMockConfig("static", `      forced: "true"
      result_count: "2"`, "", "")
	s.apiGet("/api/search?title=Test&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock forced: search")

	s.applyMockConfig("static", `      languages: "fr"
      result_count: "3"`, "", "")
	r = s.apiGet("/api/search?title=Test&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock lang filter: en query")
	count = resultsLen(r)
	if n, ok := shellInt(count); ok && n == 0 {
		s.pass("Mock lang filter: 0 en results")
	} else {
		s.logf("Mock lang filter: %s results", count)
	}

	r = s.apiGet("/api/search?title=Test&year=2024&lang=fr&type=movie")
	count = resultsLen(r)
	if n, ok := shellInt(count); ok && n > 0 {
		s.pass("Mock lang filter: fr results")
	} else {
		s.fail("Mock lang filter: 0 fr")
	}

	s.applyMockConfig("static", `      download_error: "disk-full-test"`, "", "")
	s.apiGet("/api/search?title=Test&year=2024&lang=en&type=movie")
	s.assertStatus("200", "Mock download error: search works")

	s.applyMockConfig("season_pack", "", "", "")
	s.apiGet("/api/search?title=Breaking+Bad&season=1&episode=1&lang=en&type=episode&tvdb=81189")
	s.assertStatus("200", "Mock season_pack: search")

	// Slow mode with timing verification (epoch-second diff, like `date +%s`).
	s.applyMockConfig("static", `      delay_ms: "1500"`, "", "")
	t0 := time.Now().Unix()
	s.apiGet("/api/search?title=Slow&year=2024&lang=en&type=movie")
	t1 := time.Now().Unix()
	s.assertStatus("200", "Mock slow: completes")
	if t1-t0 >= 1 {
		s.pass("Mock slow: >= 1s delay")
	} else {
		s.log("Mock slow: faster than expected")
	}

	s.apiPut("/api/config", s.original)
	time.Sleep(2 * time.Second)
	s.log("Config restored")
}

func (s *suite) sectionProviderErrors() {
	s.log("=== Provider Error Scenarios ===")

	s.applyMockConfig("flaky", `      flaky_rate: "0.8"`, "", "")
	flakyPass, flakyFail := 0, 0
	for range 5 {
		r := s.apiGet("/api/search?title=Flaky&year=2024&lang=en&type=movie")
		c := resultsLen(r)
		if n, ok := shellInt(c); ok && n > 0 {
			flakyPass++
		} else {
			flakyFail++
		}
	}
	s.pass(fmt.Sprintf("Flaky provider: %d pass, %d fail", flakyPass, flakyFail))

	s.applyMockConfig("error", "", "", "")
	for range 6 {
		s.apiGet("/api/search?title=Timeout+Test&year=2024&lang=en&type=movie")
	}
	to := s.apiGet("/api/providers/timeout")
	s.assertStatus("200", "Timeout state after errors")
	timedOut := fieldJSONOrFalse(to, ".mock.timed_out")
	if timedOut == "true" {
		s.pass("Mock timed out")
	} else {
		s.log("Mock not timed out (threshold)")
	}

	s.apiPost("/api/providers/timeout/reset", "")
	s.assertStatus("200", "Timeout reset")

	s.apiPut("/api/config", s.original)
	time.Sleep(2 * time.Second)
}
