// wizard-example.ts — The embedded example config (config.example.yaml) in
// the structured-sections form GET /api/config/structured serves (secrets
// redacted to ""). Fresh boots auto-write this file, so a step section that
// EQUALS its example default is placeholder data, not a user decision: the
// wizard's collapse gate requires config_valid AND differs-from-example
// (wizard-state.ts), closing the fast-forward-past-real-steps trap.
//
// Keep in sync with config.example.yaml; the wizard-state tests pin the
// full-walk behavior of this exact shape.

import type { Sections } from "./wizard-state.js";

/** Example-config sections owned by wizard steps. Sections the wizard never
 *  edits (logging, backup, trusted_proxies, embedded_subtitles, ...) are
 *  irrelevant to collapse gating and omitted. */
export const EXAMPLE_SECTIONS: Sections = {
  sonarr: {
    enabled: true,
    url: "http://sonarr:8989",
    api_key: "",
    public_url: "",
  },
  radarr: {
    enabled: true,
    url: "http://radarr:7878",
    api_key: "",
    public_url: "",
  },
  media_roots: ["/media"],
  languages: {
    rules: [
      { audio: "en", subtitles: [{ code: "fr", min_score: 80 }] },
      { audio: "fr", subtitles: [{ code: "en", variants: ["standard", "forced"] }] },
      {
        audio: "ja",
        subtitles: [
          {
            code: "en",
            providers: ["opensubtitles", "animetosho"],
            exclude: ["yifysubtitles"],
          },
        ],
      },
      { audio: "es", subtitles: [] },
    ],
    default: [{ code: "en" }, { code: "fr" }],
  },
  providers: {
    hdbits: { enabled: false, priority: 1, settings: { username: "", passkey: "" } },
    opensubtitles: {
      enabled: false,
      priority: 2,
      settings: {
        username: "",
        password: "",
        api_key: "",
        use_hash: true,
        include_ai_translated: false,
      },
    },
    betaseries: { enabled: false, priority: 3, settings: { token: "" } },
    gestdown: { enabled: true, priority: 4 },
    subsource: { enabled: false, priority: 5, settings: { api_key: "" } },
    subdl: { enabled: false, priority: 7, settings: { api_key: "" } },
    animetosho: { enabled: true, priority: 8, settings: { anidb_client_key: "" } },
    yifysubtitles: { enabled: true, priority: 9 },
  },
  search: {
    scan_interval: "24h",
    provider_timeout: "1h",
    scan_delay: "5s",
    upgrade_enabled: true,
    upgrade_window_days: 7,
    exclude_arr_tags: ["no-subflux"],
  },
  adaptive: {
    initial_delay: "7D",
    max_delay: "90D",
    backoff_multiplier: 2,
    max_attempts: 10,
  },
  scoring: {
    weights: {
      hash: 100,
      source: 28,
      release_group: 23,
      streaming_service: 14,
      edition: 15,
      video_codec: 10,
      hdr: 8,
      season_pack: 15,
    },
  },
  post_processing: {
    sync_subtitles: true,
    audio_sync_fallback: false,
    strip_hi: false,
    strip_tags: true,
    normalize_utf8: true,
    normalize_endings: true,
    clean_whitespace: true,
    remove_empty: true,
  },
};
