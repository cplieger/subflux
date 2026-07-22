//go:build functional

package functional

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// This file ports run.sh's config-focused sections: config_validation,
// post_processing, language_rules, hot_reload, exclude_tags,
// embedded_settings, adaptive_config. The inline YAML documents are
// byte-for-byte copies of the bash strings (no trailing newline — the bash
// quoted strings end at the last character).

func (s *suite) sectionConfigValidation() {
	s.log("=== Config Validation ===")

	// No arr endpoints.
	s.apiPut("/api/config", `languages:
  default:
    - code: en
providers:
  mock:
    enabled: true
    priority: 1
media_roots:
  - /media
poll_interval: 30s
search:
  scan_interval: 24h
  scan_delay: 5s
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
logging:
  level: info
  format: json`)
	if s.lastStatus != "200" {
		s.pass(fmt.Sprintf("Rejects: no arr (HTTP %s)", s.lastStatus))
	} else {
		s.fail("Accepted config without arr")
	}

	// No languages.
	s.apiPut("/api/config", `sonarr:
  enabled: true
  url: "http://sonarr:8989"
  api_key: "REDACTED"
media_roots:
  - /media
poll_interval: 30s
providers:
  mock:
    enabled: true
    priority: 1
search:
  scan_interval: 24h
  scan_delay: 5s
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
logging:
  level: info
  format: json`)
	if s.lastStatus != "200" {
		s.pass(fmt.Sprintf("Rejects: no languages (HTTP %s)", s.lastStatus))
	} else {
		s.fail("Accepted config without languages")
	}

	// Invalid YAML.
	s.apiPut("/api/config", "this is: [not: valid yaml")
	if s.lastStatus != "200" {
		s.pass(fmt.Sprintf("Rejects: invalid YAML (HTTP %s)", s.lastStatus))
	} else {
		s.fail("Accepted invalid YAML")
	}

	// Empty body.
	s.apiPut("/api/config", "")
	if s.lastStatus != "200" {
		s.pass(fmt.Sprintf("Rejects: empty body (HTTP %s)", s.lastStatus))
	} else {
		s.fail("Accepted empty config")
	}

	s.apiPut("/api/config", s.original)
	time.Sleep(time.Second)
}

func (s *suite) sectionPostProcessing() {
	s.log("=== Post-Processing Combinations ===")
	combos := [][4]string{
		{"false", "false", "false", "false"},
		{"true", "false", "false", "false"},
		{"false", "true", "false", "false"},
		{"true", "true", "false", "false"},
		{"false", "false", "true", "false"},
		{"true", "true", "true", "false"},
		{"false", "false", "true", "true"},
		{"true", "true", "true", "true"},
	}
	for _, c := range combos {
		topExtra := fmt.Sprintf(`post_processing:
  strip_hi: %s
  strip_tags: %s
  sync_subtitles: %s
  audio_sync_fallback: %s
  normalize_utf8: true
  normalize_endings: true
  clean_whitespace: true
  remove_empty: true`, c[0], c[1], c[2], c[3])
		s.applyMockConfig("static", "", topExtra, "")
		if s.lastStatus == "200" {
			s.pass(fmt.Sprintf("PP: hi=%s tags=%s sync=%s audio=%s", c[0], c[1], c[2], c[3]))
		} else {
			s.fail(fmt.Sprintf("PP: hi=%s tags=%s sync=%s audio=%s (HTTP %s)", c[0], c[1], c[2], c[3], s.lastStatus))
		}
	}
	s.apiPut("/api/config", s.original)
	time.Sleep(time.Second)
}

func (s *suite) sectionLanguageRules() {
	s.log("=== Language Rule Combinations ===")

	s.applyMockConfig("static", "", "", `  rules:
    - audio: en
      subtitles:
        - code: fr`)
	t := s.apiGet("/api/search/targets?orig_lang=en&audio_langs=en")
	s.assertStatus("200", "Lang: en -> fr")
	tc := lengthAt(t, ".")
	if n, ok := shellInt(tc); ok && n >= 1 {
		s.pass(fmt.Sprintf("Lang: %s targets", tc))
	} else {
		s.fail("Lang: 0 targets")
	}

	s.applyMockConfig("static", "", "", `  rules:
    - audio: en
      subtitles:
        - code: fr
        - code: de
        - code: es`)
	s.apiGet("/api/search/targets?orig_lang=en&audio_langs=en")
	s.assertStatus("200", "Lang: en -> fr,de,es")

	s.applyMockConfig("static", "", "", `  rules:
    - audio: fr
      subtitles:
        - code: en
          variants: [standard, forced]`)
	s.apiGet("/api/search/targets?orig_lang=fr&audio_langs=fr")
	s.assertStatus("200", "Lang: fr -> en std+forced")

	s.applyMockConfig("static", "", "", `  rules:
    - audio: en
      subtitles:
        - code: en
          variant: hi`)
	s.apiGet("/api/search/targets?orig_lang=en&audio_langs=en")
	s.assertStatus("200", "Lang: en -> en HI")

	s.applyMockConfig("static", "", "", `  rules:
    - audio: ja
      subtitles:
        - code: en
          providers: [mock]`)
	s.apiGet("/api/search/targets?orig_lang=ja&audio_langs=ja")
	s.assertStatus("200", "Lang: provider filter")

	s.applyMockConfig("static", "", "", `  rules:
    - audio: en
      subtitles:
        - code: fr
          min_score: 80`)
	s.apiGet("/api/search/targets?orig_lang=en&audio_langs=en")
	s.assertStatus("200", "Lang: min_score=80")

	s.apiPut("/api/config", s.original)
	time.Sleep(time.Second)
}

// scanDelayRe mirrors run.sh's `sed 's/scan_delay: .*/scan_delay: 7s/'`:
// `.*` stops at end of line, so at most one match per line, like sed's
// unflagged substitution.
var scanDelayRe = regexp.MustCompile(`scan_delay: .*`)

func (s *suite) sectionHotReload() {
	s.log("=== Config Hot Reload ===")

	// Flip scan_delay and verify the parsed (live) config reflects it.
	// (.logging is not exposed in /api/config/parsed, so a log-level flip
	// could not be verified through the API.)
	modified := scanDelayRe.ReplaceAllString(s.original, "scan_delay: 7s")
	if modified == s.original {
		s.skip("Hot reload: config has no explicit scan_delay line")
		return
	}
	s.apiPut("/api/config", modified)
	s.assertStatus("200", "Hot reload: scan_delay change accepted")

	parsed := s.apiGet("/api/config/parsed")
	delay := fieldRawOrEmpty(parsed, ".search.ScanDelay")
	if delay == "7000000000" {
		s.pass("Hot reload: live scan_delay=7s")
	} else {
		s.fail(fmt.Sprintf("Hot reload: live scan_delay=%s, want 7000000000 (7s)", delay))
	}

	s.apiPut("/api/config", s.original)
	time.Sleep(time.Second)
}

func (s *suite) sectionExcludeTags() {
	s.log("=== Exclude Tags ===")
	s.apiGet("/api/config/parsed")
	// Bash reads the raw body file ($SF_BODY), not the $()-stripped capture.
	tc := lengthAt(string(s.lastBody), ".search.ExcludeArrTags")
	if n, ok := shellInt(tc); ok && n >= 1 {
		s.pass(fmt.Sprintf("Exclude tags: %s configured", tc))
	} else {
		s.fail(fmt.Sprintf("Exclude tags: %s (expected >= 1)", tc))
	}
	s.apiPut("/api/config", s.original)
	time.Sleep(time.Second)
}

// embeddedConfigTemplate is the byte-exact YAML from test_embedded_settings.
// Slots: sonarr key, radarr key, ignore_pgs, ignore_vobsub, ignore_ass.
const embeddedConfigTemplate = `sonarr:
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
embedded_subtitles:
  ignore_pgs: %s
  ignore_vobsub: %s
  ignore_ass: %s
providers:
  mock:
    enabled: true
    priority: 1
search:
  scan_interval: 999h
  scan_delay: 5s
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
  format: json`

// legacyEmbeddedConfigTemplate is the byte-exact legacy providers.embedded
// config that must be rejected with the targeted cutover error. Slot:
// sonarr key.
const legacyEmbeddedConfigTemplate = `sonarr:
  enabled: true
  url: "http://sonarr:8989"
  api_key: "%s"
media_roots:
  - /media
languages:
  default:
    - code: en
providers:
  embedded:
    settings:
      ignore_pgs: true
  mock:
    enabled: true
    priority: 1
auth:
  disable_auth: true`

func (s *suite) sectionEmbeddedSettings() {
	s.log("=== Embedded Subtitle Settings ===")
	combos := [][3]string{
		{"true", "true", "false"},
		{"false", "false", "false"},
		{"true", "false", "true"},
		{"false", "true", "true"},
		{"true", "true", "true"},
	}
	for _, c := range combos {
		s.apiPut("/api/config", fmt.Sprintf(embeddedConfigTemplate,
			s.arrKey("sonarr"), s.arrKey("radarr"), c[0], c[1], c[2]))
		time.Sleep(time.Second)
		if s.lastStatus == "200" {
			s.pass(fmt.Sprintf("Embedded: pgs=%s vobsub=%s ass=%s", c[0], c[1], c[2]))
		} else {
			s.fail(fmt.Sprintf("Embedded: pgs=%s vobsub=%s ass=%s (HTTP %s)", c[0], c[1], c[2], s.lastStatus))
		}
	}

	// Legacy providers.embedded must be rejected with the targeted cutover
	// error (alpha hard cutover, no migration path).
	r := s.apiPut("/api/config", fmt.Sprintf(legacyEmbeddedConfigTemplate, s.arrKey("sonarr")))
	if s.lastStatus != "200" {
		if strings.Contains(r, "embedded_subtitles") {
			s.pass(fmt.Sprintf("Rejects providers.embedded with targeted error (HTTP %s)", s.lastStatus))
		} else {
			s.fail(fmt.Sprintf("providers.embedded rejected but error lacks embedded_subtitles pointer: %s", r))
		}
	} else {
		s.fail("Accepted legacy providers.embedded config")
	}

	s.apiPut("/api/config", s.original)
	time.Sleep(time.Second)
}

// adaptiveConfigTemplate is the byte-exact YAML from test_adaptive_config.
// Slots: sonarr key, radarr key, initial_delay, max_delay,
// backoff_multiplier, max_attempts.
const adaptiveConfigTemplate = `sonarr:
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
embedded_subtitles:
  ignore_pgs: true
  ignore_vobsub: true
providers:
  mock:
    enabled: true
    priority: 1
search:
  scan_interval: 999h
  scan_delay: 5s
adaptive:
  initial_delay: %s
  max_delay: %s
  backoff_multiplier: %s
  max_attempts: %s
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
  format: json`

func (s *suite) sectionAdaptiveConfig() {
	s.log("=== Adaptive Backoff Config ===")
	configs := [][4]string{
		{"1s", "5s", "2", "3"},
		{"1h", "30D", "3", "10"},
		{"7D", "90D", "2", "0"},
	}
	for _, c := range configs {
		s.apiPut("/api/config", fmt.Sprintf(adaptiveConfigTemplate,
			s.arrKey("sonarr"), s.arrKey("radarr"), c[0], c[1], c[2], c[3]))
		time.Sleep(time.Second)
		if s.lastStatus == "200" {
			s.pass(fmt.Sprintf("Adaptive: init=%s max=%s mult=%s att=%s", c[0], c[1], c[2], c[3]))
		} else {
			s.fail(fmt.Sprintf("Adaptive: init=%s max=%s mult=%s att=%s (HTTP %s)", c[0], c[1], c[2], c[3], s.lastStatus))
		}
	}
	s.apiPut("/api/config", s.original)
	time.Sleep(time.Second)
}
