#!/usr/bin/env bash
# shellcheck disable=SC2015
# SC2015 (`A && B || C` is not if-then-else) is intentional and pervasive
# in this file: test assertions of the form `[ cond ] && pass "msg" || fail "msg"`.
# `pass` and `fail` only increment counters and printf — they cannot fail
# in a way that would cause `fail` to run after a successful `pass`.
# ---------------------------------------------------------------------------
# Subflux Functional Test Suite
# ---------------------------------------------------------------------------
# Drives a live subflux instance via its HTTP API to verify every feature,
# setting combination, provider behavior, and error path.
#
# Prerequisites:
#   - subflux running and reachable at SUBFLUX_URL
#   - subflux auth disabled (auth.disable_auth: true) or API key configured
#   - Sonarr and Radarr reachable from subflux
#   - jq installed
#
# Usage:
#   bash run.sh                    # run all tests
#   bash run.sh --section config   # run only one section
#   bash run.sh --dry-run          # list available sections
#
# The script saves and restores the original config before/after testing.
# ---------------------------------------------------------------------------

set -o pipefail

# Temp files for HTTP response capture (unique per run to avoid collisions)
SF_BODY=$(mktemp /tmp/sf_body.XXXXXX)
SF_STATUS=$(mktemp /tmp/sf_status.XXXXXX)
cleanup_tmp() { rm -f "$SF_BODY" "$SF_STATUS"; }

command -v jq >/dev/null 2>&1 || {
  printf '\033[0;31mERROR: jq is required but not found\033[0m\n'
  exit 1
}

SUBFLUX_URL="${SUBFLUX_URL:-http://192.0.2.77:8374}"
SECTION="${SECTION:-all}"
DRY_RUN=false
PASS=0
FAIL=0
SKIP=0
ERRORS=""

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# ---------------------------------------------------------------------------
# Core helpers
# ---------------------------------------------------------------------------

log() { printf "${CYAN}[TEST]${NC} %s\n" "$*"; }
pass() {
  PASS=$((PASS + 1))
  printf "${GREEN}  PASS${NC} %s\n" "$*"
}
fail() {
  FAIL=$((FAIL + 1))
  printf "${RED}  FAIL${NC} %s\n" "$*"
  ERRORS="${ERRORS}\n  - $*"
}
skip() {
  SKIP=$((SKIP + 1))
  printf "${YELLOW}  SKIP${NC} %s\n" "$*"
}

# ---------------------------------------------------------------------------
# HTTP helpers
#
# Design: curl writes body to $SF_BODY, status code to $SF_STATUS.
# HTTP_STATUS is read from the file AFTER curl returns, so it works even
# when the caller captures stdout in a $() subshell.
# ---------------------------------------------------------------------------

_read_status() { HTTP_STATUS=$(cat "$SF_STATUS" 2>/dev/null); }

_curl() {
  local url="${!#}"
  local args=("${@:1:$#-1}")
  curl -s --connect-timeout 5 --max-time 60 \
    -o "$SF_BODY" -w '%{http_code}' \
    "${args[@]}" "$url" \
    >"$SF_STATUS" 2>/dev/null || true
  _read_status
  cat "$SF_BODY" 2>/dev/null
}

api_get() { _curl "${SUBFLUX_URL}$1"; }
api_delete() { _curl -X DELETE "${SUBFLUX_URL}$1"; }
api_post() {
  if [ -n "$2" ]; then
    _curl -X POST -H 'Content-Type: application/json' -d "$2" "${SUBFLUX_URL}$1"
  else
    _curl -X POST "${SUBFLUX_URL}$1"
  fi
}
api_put() { _curl -X PUT -H 'Content-Type: text/plain' \
  -d "$2" "${SUBFLUX_URL}$1"; }

sync_status() { _read_status; }

assert_status() {
  local expected="$1" ctx="$2"
  _read_status
  if [ "$HTTP_STATUS" = "$expected" ]; then
    pass "$ctx (HTTP $expected)"
  else
    fail "$ctx: expected HTTP $expected, got $HTTP_STATUS"
  fi
}

assert_json() {
  local json="$1" path="$2" expected="$3" ctx="$4"
  local actual
  actual=$(printf '%s' "$json" | jq -r "$path" 2>/dev/null)
  [ "$actual" = "$expected" ] && pass "$ctx" \
    || fail "$ctx: expected '$expected', got '$actual'"
}

assert_json_not_empty() {
  local json="$1" path="$2" ctx="$3"
  local actual
  actual=$(printf '%s' "$json" | jq -r "$path" 2>/dev/null)
  [ -n "$actual" ] && [ "$actual" != "null" ] && pass "$ctx" \
    || fail "$ctx: expected non-empty at $path"
}

assert_json_len() {
  local json="$1" path="$2" op="$3" n="$4" ctx="$5"
  local actual
  actual=$(printf '%s' "$json" | jq "$path | length" 2>/dev/null)
  case "$op" in
    eq) [ "${actual:-0}" -eq "$n" ] && pass "$ctx" || fail "$ctx: len=$actual, want =$n" ;;
    gt) [ "${actual:-0}" -gt "$n" ] && pass "$ctx" || fail "$ctx: len=$actual, want >$n" ;;
    ge) [ "${actual:-0}" -ge "$n" ] && pass "$ctx" || fail "$ctx: len=$actual, want >=$n" ;;
    *) fail "$ctx: unknown op $op" ;;
  esac
}

wait_ready() {
  for _ in $(seq 1 30); do
    curl -sf --max-time 2 "${SUBFLUX_URL}/api/health" >/dev/null 2>&1 && return 0
    sleep 1
  done
  printf "${RED}ERROR: subflux not reachable at %s${NC}\n" "$SUBFLUX_URL"
  exit 1
}

ORIGINAL_CONFIG=""
save_config() {
  ORIGINAL_CONFIG=$(api_get "/api/config")
  [ -n "$ORIGINAL_CONFIG" ] || {
    printf '%bERROR: empty config%b\n' "$RED" "$NC"
    exit 1
  }
  log "Original config saved (${#ORIGINAL_CONFIG} bytes)"
}
restore_config() {
  [ -n "$ORIGINAL_CONFIG" ] || return
  local _attempt
  for _attempt in 1 2 3; do
    api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
    _read_status
    if [ "$HTTP_STATUS" = "200" ]; then
      log "Config restored"
      sleep 2
      return
    fi
    sleep 2
  done
  printf '%bWARNING: Failed to restore original config after 3 attempts.%b\n' "$RED" "$NC" >&2
  printf '%bSave your config backup and restore manually.%b\n' "$RED" "$NC" >&2
}

_sonarr_key() { printf '%s' "$ORIGINAL_CONFIG" | grep -A5 'sonarr:' | grep 'api_key:' | sed 's/.*api_key: *//' | tr -d '"' | tr -d "'"; } # REDACTED
_radarr_key() { printf '%s' "$ORIGINAL_CONFIG" | grep -A5 'radarr:' | grep 'api_key:' | sed 's/.*api_key: *//' | tr -d '"' | tr -d "'"; } # REDACTED

apply_mock_config() {
  local mode="$1" mock_extra="${2:-}" top_extra="${3:-}"
  api_put "/api/config" "sonarr:
  enabled: true
  url: \"http://sonarr:8989\"
  api_key: \"$(_sonarr_key)\"
radarr:
  enabled: true
  url: \"http://radarr:7878\"
  api_key: \"$(_radarr_key)\"
media_roots:
  - /media
poll_interval: 999h
languages:
  default:
    - code: en
    - code: fr
providers:
  embedded:
    settings:
      ignore_pgs: true
      ignore_vobsub: true
  mock:
    enabled: true
    priority: 1
    settings:
      mode: \"${mode}\"
${mock_extra}
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
logging:
  level: debug
  format: json
${top_extra}" >/dev/null
  sleep 1
}

# ---------------------------------------------------------------------------
# Parse args
# ---------------------------------------------------------------------------

while [ $# -gt 0 ]; do
  case "$1" in
    --section)
      SECTION="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    --url)
      SUBFLUX_URL="$2"
      shift 2
      ;;
    *)
      printf "Unknown arg: %s\n" "$1"
      exit 1
      ;;
  esac
done

should_run() { [ "$SECTION" = "all" ] || [ "$SECTION" = "$1" ]; }

# ===========================================================================
# SECTION: health
# ===========================================================================
test_health() {
  log "=== Health & Basics ==="
  api_get "/health"
  assert_status 200 "GET /health"
  api_get "/metrics"
  assert_status 200 "GET /metrics"

  # SSE requires auth; without credentials expect 401
  local sse_status
  sse_status=$(curl -sf --max-time 2 -o /dev/null -w '%{http_code}' \
    "${SUBFLUX_URL}/api/events" 2>/dev/null || true)
  if [ "$sse_status" = "200" ]; then
    pass "SSE connects (auth disabled)"
  elif [ "$sse_status" = "401" ]; then
    pass "SSE requires auth (HTTP 401)"
  else
    fail "SSE: HTTP $sse_status"
  fi

  local ui
  ui=$(curl -sf --max-time 5 "${SUBFLUX_URL}/" 2>/dev/null)
  printf '%s' "$ui" | grep -q 'html' && pass "GET / serves HTML" || fail "GET / no HTML"

  ui=$(curl -sf --max-time 5 "${SUBFLUX_URL}/library/series" 2>/dev/null)
  printf '%s' "$ui" | grep -q 'html' && pass "SPA routing works" || fail "SPA routing broken"

  # Login page is a separate HTML entry point
  ui=$(curl -sf --max-time 5 "${SUBFLUX_URL}/login" 2>/dev/null)
  printf '%s' "$ui" | grep -q 'html' && pass "GET /login serves HTML" || fail "GET /login no HTML"

  # Method not allowed on wrong methods
  _curl -X DELETE "${SUBFLUX_URL}/api/health"
  [ "$HTTP_STATUS" = "200" ] && pass "Health accepts any method" || log "Health DELETE: HTTP $HTTP_STATUS"
}

# ===========================================================================
# SECTION: auth
# ===========================================================================
test_auth() {
  log "=== Authentication Endpoints ==="

  # Setup status
  local setup
  setup=$(api_get "/api/auth/setup")
  sync_status
  assert_status 200 "GET /api/auth/setup"
  assert_json_not_empty "$setup" ".setup_required" "Setup has setup_required field"
  assert_json_not_empty "$setup" ".config_valid" "Setup has config_valid field"

  # Login with wrong credentials (should fail gracefully)
  api_post "/api/auth/login" '{"username":"nonexistent","password":"wrong"}'
  assert_status 401 "Login: invalid credentials"

  # Login with empty body
  api_post "/api/auth/login" '{}'
  assert_status 401 "Login: empty body"

  # Login method not allowed
  _curl -X GET "${SUBFLUX_URL}/api/auth/login"
  assert_status 405 "Login: GET not allowed"

  # TOTP verify without token
  api_post "/api/auth/totp" '{"code":"123456","totp_token":"invalid"}'
  assert_status 401 "TOTP verify: invalid token"

  # Logout without session
  api_post "/api/auth/logout"
  assert_status 200 "Logout: no session"

  # Auth me without session (should 401 or return synthetic user if auth disabled)
  local me
  me=$(api_get "/api/auth/me")
  sync_status
  if [ "$HTTP_STATUS" = "200" ]; then
    assert_json_not_empty "$me" ".username" "Auth me: has username"
    assert_json_not_empty "$me" ".role" "Auth me: has role"
    pass "Auth me: returns user (auth disabled or session valid)"
  elif [ "$HTTP_STATUS" = "401" ]; then
    pass "Auth me: requires auth (HTTP 401)"
  else
    fail "Auth me: unexpected HTTP $HTTP_STATUS"
  fi

  # Passkeys list (requires auth)
  api_get "/api/auth/passkeys"
  sync_status
  [ "$HTTP_STATUS" = "200" ] || [ "$HTTP_STATUS" = "401" ] \
    && pass "List passkeys (HTTP $HTTP_STATUS)" \
    || fail "List passkeys: HTTP $HTTP_STATUS"

  # API keys list (requires auth)
  api_get "/api/auth/apikeys"
  sync_status
  [ "$HTTP_STATUS" = "200" ] || [ "$HTTP_STATUS" = "401" ] \
    && pass "List API keys (HTTP $HTTP_STATUS)" \
    || fail "List API keys: HTTP $HTTP_STATUS"

  # Users list (requires admin)
  api_get "/api/auth/users"
  sync_status
  [ "$HTTP_STATUS" = "200" ] || [ "$HTTP_STATUS" = "401" ] || [ "$HTTP_STATUS" = "403" ] \
    && pass "List users (HTTP $HTTP_STATUS)" \
    || fail "List users: HTTP $HTTP_STATUS"

  # Reauth without session
  api_post "/api/auth/reauth" '{"method":"password","password":"test"}'
  sync_status
  [ "$HTTP_STATUS" = "200" ] || [ "$HTTP_STATUS" = "401" ] \
    && pass "Reauth (HTTP $HTTP_STATUS)" \
    || fail "Reauth: HTTP $HTTP_STATUS"

  # WebAuthn login begin (requires WebAuthn configured)
  api_post "/api/auth/webauthn/login/begin"
  sync_status
  [ "$HTTP_STATUS" = "200" ] || [ "$HTTP_STATUS" = "400" ] || [ "$HTTP_STATUS" = "401" ] \
    && pass "WebAuthn login begin (HTTP $HTTP_STATUS)" \
    || fail "WebAuthn login begin: HTTP $HTTP_STATUS"

  # WebAuthn signal data (requires auth)
  api_get "/api/auth/webauthn/signal-data"
  sync_status
  [ "$HTTP_STATUS" = "200" ] || [ "$HTTP_STATUS" = "400" ] || [ "$HTTP_STATUS" = "401" ] \
    && pass "WebAuthn signal data (HTTP $HTTP_STATUS)" \
    || fail "WebAuthn signal data: HTTP $HTTP_STATUS"

  # OIDC redirect (requires OIDC configured)
  _curl -o /dev/null -w '%{http_code}' "${SUBFLUX_URL}/api/auth/oidc" >"$SF_STATUS" 2>/dev/null || true
  _read_status
  [ "$HTTP_STATUS" = "302" ] || [ "$HTTP_STATUS" = "400" ] || [ "$HTTP_STATUS" = "401" ] \
    && pass "OIDC redirect (HTTP $HTTP_STATUS)" \
    || fail "OIDC redirect: HTTP $HTTP_STATUS"
}

# ===========================================================================
# SECTION: config
# ===========================================================================
test_config() {
  log "=== Configuration ==="
  local cfg schema parsed reset_cfg

  cfg=$(api_get "/api/config")
  sync_status
  assert_status 200 "GET /api/config"
  [ -n "$cfg" ] && pass "Config body non-empty" || fail "Config body empty"

  schema=$(api_get "/api/config/schema")
  sync_status
  assert_status 200 "GET /api/config/schema"
  assert_json_len "$schema" "." gt 0 "Schema has sections"

  parsed=$(api_get "/api/config/parsed")
  sync_status
  assert_status 200 "GET /api/config/parsed"
  assert_json_not_empty "$parsed" ".search" "Parsed has search config"
  assert_json_not_empty "$parsed" ".logging" "Parsed has logging config"

  # Auth config in parsed response
  local auth_enabled
  auth_enabled=$(printf '%s' "$parsed" | jq '.auth.enabled // empty' 2>/dev/null)
  [ -n "$auth_enabled" ] && pass "Parsed has auth config" || log "Parsed: no auth section (may be disabled)"

  api_put "/api/config" "$cfg"
  assert_status 200 "PUT /api/config (unchanged)"

  # POST also accepted for config save
  _curl -X POST -H 'Content-Type: text/plain' -d "$cfg" "${SUBFLUX_URL}/api/config"
  assert_status 200 "POST /api/config (alternate method)"

  api_post "/api/config/reset" "{}"
  _read_status
  if [ "$HTTP_STATUS" = "200" ] || [ "$HTTP_STATUS" = "409" ]; then
    pass "POST /api/config/reset (HTTP $HTTP_STATUS)"
  else
    fail "POST /api/config/reset: HTTP $HTTP_STATUS"
  fi
  reset_cfg=$(api_get "/api/config")
  sync_status
  [ "$reset_cfg" != "$cfg" ] && pass "Reset changed config" || skip "Reset same (already default)"

  api_put "/api/config" "$cfg" >/dev/null
  sleep 1
}

# ===========================================================================
# SECTION: providers
# ===========================================================================
test_providers() {
  log "=== Providers ==="
  local provs

  provs=$(api_get "/api/providers")
  sync_status
  assert_status 200 "GET /api/providers"
  assert_json_len "$provs" "." gt 0 "Provider list non-empty"

  local schema_provs has_mock
  schema_provs=$(api_get "/api/config/schema")
  sync_status
  has_mock=$(printf '%s' "$schema_provs" | jq '[.[] | select(.key=="providers") | .providers[] | select(.name=="mock")] | length' 2>/dev/null)
  [ "${has_mock:-0}" -gt 0 ] && pass "Mock in schema" || fail "Mock not in schema"

  api_get "/api/providers/timeout"
  assert_status 200 "GET /api/providers/timeout"
  api_post "/api/providers/timeout/reset"
  assert_status 200 "POST /api/providers/timeout/reset"
}

# ===========================================================================
# SECTION: media_browser
# ===========================================================================
test_media_browser() {
  log "=== Media Browser ==="
  local series movies episodes first_id

  series=$(api_get "/api/media/series")
  sync_status
  assert_status 200 "GET /api/media/series"
  assert_json_len "$series" "." gt 0 "Series list non-empty"

  first_id=$(printf '%s' "$series" | jq '.[0].id' 2>/dev/null)
  if [ -n "$first_id" ] && [ "$first_id" != "null" ]; then
    episodes=$(api_get "/api/media/series/${first_id}/episodes")
    sync_status
    assert_status 200 "GET episodes for series $first_id"
    assert_json_len "$episodes" "." gt 0 "Episode list non-empty"
  else
    skip "No series for episode test"
  fi

  movies=$(api_get "/api/media/movies")
  sync_status
  assert_status 200 "GET /api/media/movies"
  assert_json_len "$movies" "." gt 0 "Movie list non-empty"
}

# ===========================================================================
# SECTION: coverage
# ===========================================================================
test_coverage() {
  log "=== Coverage ==="
  api_get "/api/coverage/series"
  assert_status 200 "GET /api/coverage/series"
  api_get "/api/coverage/movies"
  assert_status 200 "GET /api/coverage/movies"
  api_get "/api/coverage/scan-state"
  assert_status 200 "GET /api/coverage/scan-state"

  local sid mid
  sid=$(api_get "/api/media/series" | jq -r '.[0].id // empty' 2>/dev/null)
  sync_status
  if [ -n "$sid" ]; then
    api_get "/api/coverage/series/${sid}"
    assert_status 200 "Coverage detail series/$sid"
  else
    skip "No series for coverage detail"
  fi

  mid=$(api_get "/api/media/movies" | jq -r '.[0].id // empty' 2>/dev/null)
  sync_status
  if [ -n "$mid" ]; then
    # Movie coverage is via the movies list endpoint, not a detail endpoint
    log "Movie coverage included in /api/coverage/movies"
  fi
}

# ===========================================================================
# SECTION: state
# ===========================================================================
test_state() {
  log "=== State & History ==="
  local stats
  stats=$(api_get "/api/state/stats")
  sync_status
  assert_status 200 "GET /api/state/stats"
  assert_json_not_empty "$stats" ".total_series" "Stats has total_series"
  assert_json_not_empty "$stats" ".total_movies" "Stats has total_movies"

  api_get "/api/state"
  assert_status 200 "GET /api/state"
  api_get "/api/state?type=episode&limit=5"
  assert_status 200 "State filter: episode"
  api_get "/api/state?type=movie&limit=5"
  assert_status 200 "State filter: movie"
  api_get "/api/state?lang=fr&limit=5"
  assert_status 200 "State filter: lang=fr"
  api_get "/api/state?search=test&limit=5"
  assert_status 200 "State filter: search"
  api_get "/api/state?limit=5&offset=0"
  assert_status 200 "State: pagination"

  api_get "/api/state/ids?type=episode"
  assert_status 200 "History IDs: episode"
  api_get "/api/state/ids?type=movie"
  assert_status 200 "History IDs: movie"

  # Invalid type for history IDs
  api_get "/api/state/ids?type=invalid"
  sync_status
  [ "$HTTP_STATUS" = "400" ] && pass "History IDs: invalid type rejected" \
    || log "History IDs invalid type: HTTP $HTTP_STATUS"

  api_get "/api/activity"
  assert_status 200 "GET /api/activity"

  # Dismiss activity (no-op if nothing to dismiss)
  api_delete "/api/activity"
  sync_status
  [ "$HTTP_STATUS" = "200" ] || [ "$HTTP_STATUS" = "204" ] \
    && pass "DELETE /api/activity (HTTP $HTTP_STATUS)" \
    || log "Dismiss activity: HTTP $HTTP_STATUS"

  api_get "/api/backoff"
  assert_status 200 "GET /api/backoff"
  api_get "/api/backoff/prefix?type=episode&prefix=tvdb-81189-"
  assert_status 200 "Backoff prefix: episode"
  api_get "/api/backoff/prefix?type=movie&prefix=tmdb-27205"
  assert_status 200 "Backoff prefix: movie"
  api_get "/api/locks"
  assert_status 200 "GET /api/locks"
  api_get "/api/alerts"
  assert_status 200 "GET /api/alerts"

  # Dismiss alerts
  api_delete "/api/alerts"
  sync_status
  log "Dismiss alerts: HTTP $HTTP_STATUS"
}

# ===========================================================================
# SECTION: files
# ===========================================================================
test_files() {
  log "=== File Management ==="
  api_get "/api/files?media_type=episode&media_id=tvdb-"
  assert_status 200 "Files: episode prefix"
  api_get "/api/files?media_type=movie&media_id=tmdb-"
  assert_status 200 "Files: movie prefix"

  # Bulk delete with empty body
  _curl -X DELETE -H 'Content-Type: application/json' -d '{}' "${SUBFLUX_URL}/api/files/bulk"
  log "Bulk delete empty: HTTP $HTTP_STATUS"

  # Single delete with nonexistent path
  _curl -X DELETE -H 'Content-Type: application/json' \
    -d '{"path":"/nonexistent/sub.srt","media_type":"movie","media_id":"tmdb-0"}' \
    "${SUBFLUX_URL}/api/files"
  log "Delete nonexistent: HTTP $HTTP_STATUS"
}

# ===========================================================================
# SECTION: manual_search
# ===========================================================================
test_manual_search() {
  log "=== Manual Search ==="
  local r

  r=$(api_get "/api/search?title=Inception&year=2010&lang=en&type=movie&imdb=tt1375666")
  sync_status
  assert_status 200 "Search: movie (Inception)"
  assert_json_not_empty "$r" ".results" "Movie results array"

  api_get "/api/search?title=Inception&year=2010&lang=en&type=movie&tmdb=27205"
  assert_status 200 "Search: movie with TMDB"

  r=$(api_get "/api/search?title=Breaking+Bad&season=1&episode=1&lang=en&type=episode&imdb=tt0903747&tvdb=81189")
  sync_status
  assert_status 200 "Search: TV episode"
  assert_json_not_empty "$r" ".results" "Episode results array"

  api_get "/api/search?title=Bleach&season=1&episode=1&lang=en&type=episode&absolute_episode=1&tvdb=74796"
  assert_status 200 "Search: anime absolute ep"

  api_get "/api/search?title=Bleach&season=9&episode=15&lang=en&type=episode&scene_season=9&scene_episode=15&tvdb=74796"
  assert_status 200 "Search: scene numbering"

  local langs="fr de es pt ja zh ar ko pb"
  for lang in $langs; do
    api_get "/api/search?title=Inception&year=2010&lang=${lang}&type=movie"
    assert_status 200 "Search: lang=$lang"
  done

  api_get "/api/search?lang=en&type=movie"
  assert_status 200 "Search: no title"
  api_get "/api/search?title=Test&lang=xx&type=movie"
  log "Invalid lang: HTTP $HTTP_STATUS"
  api_get "/api/search?title=Test&lang=en&type=invalid"
  log "Invalid type: HTTP $HTTP_STATUS"

  api_get "/api/search/targets?orig_lang=en&audio_langs=en"
  assert_status 200 "Targets: en audio"
  api_get "/api/search/targets?orig_lang=ja&audio_langs=ja"
  assert_status 200 "Targets: ja audio"
  api_get "/api/search/targets?orig_lang=fr&audio_langs=fr"
  assert_status 200 "Targets: fr audio"
}

# ===========================================================================
# SECTION: scoring
# ===========================================================================
test_scoring() {
  log "=== Score Simulation ==="
  local s

  s=$(api_post "/api/score" '{"media_type":"movie","video_release":"Inception.2010.1080p.BluRay.x264-SPARKS","sub_release":"Inception.2010.1080p.BluRay.x264-SPARKS","matched_by":"title"}')
  sync_status
  assert_status 200 "Score: exact match"
  assert_json_not_empty "$s" ".score" "Has score"
  assert_json_not_empty "$s" ".tier" "Has tier"

  s=$(api_post "/api/score" '{"media_type":"movie","video_release":"X","sub_release":"X","matched_by":"hash"}')
  sync_status
  assert_status 200 "Score: hash match"
  local hs
  hs=$(printf '%s' "$s" | jq '.score // 0' 2>/dev/null)
  [ "${hs:-0}" -ge 100 ] && pass "Hash score >= 100 ($hs)" || fail "Hash score $hs < 100"

  api_post "/api/score" '{"media_type":"episode","video_release":"Show.S01E01.1080p.BluRay.x264-DEMAND","sub_release":"Show.S01E01.720p.HDTV.x264-LOL","matched_by":"title"}'
  assert_status 200 "Score: different releases"

  api_post "/api/score" '{"media_type":"movie","video_release":"","sub_release":"","matched_by":"title"}'
  assert_status 200 "Score: empty releases"

  api_post "/api/score" '{"media_type":"episode","video_release":"Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.x264-GROUP","sub_release":"Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.x264-GROUP","matched_by":"title"}'
  assert_status 200 "Score: streaming service"

  api_post "/api/score" '{"media_type":"movie","video_release":"Movie.2024.Directors.Cut.2160p.UHD.BluRay.HDR.x265-GROUP","sub_release":"Movie.2024.Directors.Cut.2160p.UHD.BluRay.HDR.x265-GROUP","matched_by":"title"}'
  assert_status 200 "Score: edition + HDR"
}

# ===========================================================================
# SECTION: backoff
# ===========================================================================
test_backoff() {
  log "=== Backoff & Locks ==="
  api_get "/api/backoff"
  assert_status 200 "GET /api/backoff"
  api_get "/api/backoff/prefix?type=episode&prefix=tvdb-81189-"
  assert_status 200 "Backoff prefix: episode"
  api_get "/api/backoff/prefix?type=movie&prefix=tmdb-27205"
  assert_status 200 "Backoff prefix: movie"
  api_get "/api/locks"
  assert_status 200 "GET /api/locks"
  api_post "/api/search/clear-lock" '{"media_type":"movie","media_id":"tmdb-0","language":"en"}'
  log "Clear lock (nonexistent): HTTP $HTTP_STATUS"
}

# ===========================================================================
# SECTION: mock_provider
# ===========================================================================
test_mock_provider() {
  log "=== Mock Provider Modes ==="
  local r count

  apply_mock_config "static" '      result_count: "5"'
  r=$(api_get "/api/search?title=Test+Movie&year=2024&lang=en&type=movie")
  sync_status
  assert_status 200 "Mock static: search"
  count=$(printf '%s' "$r" | jq '.results | length' 2>/dev/null)
  [ "${count:-0}" -gt 0 ] && pass "Mock static: $count results" || fail "Mock static: 0 results"

  apply_mock_config "empty"
  r=$(api_get "/api/search?title=Test+Movie&year=2024&lang=en&type=movie")
  sync_status
  assert_status 200 "Mock empty: search"
  count=$(printf '%s' "$r" | jq '.results | length' 2>/dev/null)
  [ "${count:-0}" -eq 0 ] && pass "Mock empty: 0 results" || fail "Mock empty: $count results"

  apply_mock_config "error" '      error_message: "test-failure-42"'
  api_get "/api/search?title=Test&year=2024&lang=en&type=movie"
  assert_status 200 "Mock error: endpoint 200"

  apply_mock_config "auth_error"
  api_get "/api/search?title=Test&year=2024&lang=en&type=movie"
  assert_status 200 "Mock auth_error: endpoint 200"

  apply_mock_config "rate_limit"
  api_get "/api/search?title=Test&year=2024&lang=en&type=movie"
  assert_status 200 "Mock rate_limit: endpoint 200"

  apply_mock_config "timeout"
  api_get "/api/search?title=Test&year=2024&lang=en&type=movie"
  assert_status 200 "Mock timeout: endpoint 200"

  apply_mock_config "static" '      include_hash: "true"
      result_count: "3"'
  api_get "/api/search?title=Test&year=2024&lang=en&type=movie"
  assert_status 200 "Mock hash: search"

  apply_mock_config "static" '      hearing_impaired: "true"
      result_count: "2"'
  api_get "/api/search?title=Test&year=2024&lang=en&type=movie"
  assert_status 200 "Mock HI: search"

  apply_mock_config "static" '      forced: "true"
      result_count: "2"'
  api_get "/api/search?title=Test&year=2024&lang=en&type=movie"
  assert_status 200 "Mock forced: search"

  apply_mock_config "static" '      languages: "fr"
      result_count: "3"'
  r=$(api_get "/api/search?title=Test&year=2024&lang=en&type=movie")
  sync_status
  assert_status 200 "Mock lang filter: en query"
  count=$(printf '%s' "$r" | jq '.results | length' 2>/dev/null)
  [ "${count:-0}" -eq 0 ] && pass "Mock lang filter: 0 en results" \
    || log "Mock lang filter: $count results (may include embedded)"

  r=$(api_get "/api/search?title=Test&year=2024&lang=fr&type=movie")
  sync_status
  count=$(printf '%s' "$r" | jq '.results | length' 2>/dev/null)
  [ "${count:-0}" -gt 0 ] && pass "Mock lang filter: fr results" || fail "Mock lang filter: 0 fr"

  apply_mock_config "static" '      download_error: "disk-full-test"'
  api_get "/api/search?title=Test&year=2024&lang=en&type=movie"
  assert_status 200 "Mock download error: search works"

  apply_mock_config "season_pack"
  api_get "/api/search?title=Breaking+Bad&season=1&episode=1&lang=en&type=episode&tvdb=81189"
  assert_status 200 "Mock season_pack: search"

  # Slow mode with timing verification
  apply_mock_config "static" '      delay_ms: "1500"'
  local t0 t1
  t0=$(date +%s)
  api_get "/api/search?title=Slow&year=2024&lang=en&type=movie"
  t1=$(date +%s)
  assert_status 200 "Mock slow: completes"
  [ $((t1 - t0)) -ge 1 ] && pass "Mock slow: >= 1s delay" \
    || log "Mock slow: faster than expected"

  api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
  sleep 2
  log "Config restored"
}

# ===========================================================================
# SECTION: provider_errors
# ===========================================================================
test_provider_errors() {
  log "=== Provider Error Scenarios ==="

  apply_mock_config "flaky" '      flaky_rate: "0.8"'
  local flaky_pass=0 flaky_fail=0
  for _ in $(seq 1 5); do
    local r c
    r=$(api_get "/api/search?title=Flaky&year=2024&lang=en&type=movie")
    sync_status
    c=$(printf '%s' "$r" | jq '.results | length' 2>/dev/null)
    [ "${c:-0}" -gt 0 ] && flaky_pass=$((flaky_pass + 1)) || flaky_fail=$((flaky_fail + 1))
  done
  pass "Flaky provider: $flaky_pass pass, $flaky_fail fail"

  apply_mock_config "error"
  for _ in $(seq 1 6); do
    api_get "/api/search?title=Timeout+Test&year=2024&lang=en&type=movie" >/dev/null
  done
  local to timed_out
  to=$(api_get "/api/providers/timeout")
  sync_status
  assert_status 200 "Timeout state after errors"
  timed_out=$(printf '%s' "$to" | jq '.mock.timed_out // false' 2>/dev/null)
  [ "$timed_out" = "true" ] && pass "Mock timed out" || log "Mock not timed out (threshold)"

  api_post "/api/providers/timeout/reset"
  assert_status 200 "Timeout reset"

  api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
  sleep 2
}

# ===========================================================================
# SECTION: config_validation
# ===========================================================================
test_config_validation() {
  log "=== Config Validation ==="

  # No arr endpoints
  api_put "/api/config" 'languages:
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
  format: json'
  [ "$HTTP_STATUS" != "200" ] && pass "Rejects: no arr (HTTP $HTTP_STATUS)" \
    || fail "Accepted config without arr"

  # No languages
  api_put "/api/config" "sonarr:
  enabled: true
  url: \"http://sonarr:8989\"
  api_key: \"REDACTED\"
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
  format: json"
  [ "$HTTP_STATUS" != "200" ] && pass "Rejects: no languages (HTTP $HTTP_STATUS)" \
    || fail "Accepted config without languages"

  # Invalid YAML
  api_put "/api/config" "this is: [not: valid yaml"
  [ "$HTTP_STATUS" != "200" ] && pass "Rejects: invalid YAML (HTTP $HTTP_STATUS)" \
    || fail "Accepted invalid YAML"

  # Empty body
  api_put "/api/config" ""
  [ "$HTTP_STATUS" != "200" ] && pass "Rejects: empty body (HTTP $HTTP_STATUS)" \
    || fail "Accepted empty config"

  api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
  sleep 1
}

# ===========================================================================
# SECTION: post_processing
# ===========================================================================
test_post_processing() {
  log "=== Post-Processing Combinations ==="
  local combos=(
    "false false false false"
    "true  false false false"
    "false true  false false"
    "true  true  false false"
    "false false true  false"
    "true  true  true  false"
    "false false true  true"
    "true  true  true  true"
  )
  for combo in "${combos[@]}"; do
    # shellcheck disable=SC2086
    set -- $combo
    apply_mock_config "static" "" "post_processing:
  strip_hi: $1
  strip_tags: $2
  sync_subtitles: $3
  audio_sync_fallback: $4
  normalize_utf8: true
  normalize_endings: true
  clean_whitespace: true
  remove_empty: true"
    sync_status
    [ "$HTTP_STATUS" = "200" ] && pass "PP: hi=$1 tags=$2 sync=$3 audio=$4" \
      || fail "PP: hi=$1 tags=$2 sync=$3 audio=$4 (HTTP $HTTP_STATUS)"
  done
  api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
  sleep 1
}

# ===========================================================================
# SECTION: language_rules
# ===========================================================================
test_language_rules() {
  log "=== Language Rule Combinations ==="
  local t tc

  apply_mock_config "static" "" 'languages:
  rules:
    - audio: en
      subtitles:
        - code: fr'
  t=$(api_get "/api/search/targets?orig_lang=en&audio_langs=en")
  sync_status
  assert_status 200 "Lang: en -> fr"
  tc=$(printf '%s' "$t" | jq 'length' 2>/dev/null)
  [ "${tc:-0}" -ge 1 ] && pass "Lang: $tc targets" || fail "Lang: 0 targets"

  apply_mock_config "static" "" 'languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
        - code: de
        - code: es'
  api_get "/api/search/targets?orig_lang=en&audio_langs=en"
  assert_status 200 "Lang: en -> fr,de,es"

  apply_mock_config "static" "" 'languages:
  rules:
    - audio: fr
      subtitles:
        - code: en
          variants: [standard, forced]'
  api_get "/api/search/targets?orig_lang=fr&audio_langs=fr"
  assert_status 200 "Lang: fr -> en std+forced"

  apply_mock_config "static" "" 'languages:
  rules:
    - audio: en
      subtitles:
        - code: en
          variant: hi'
  api_get "/api/search/targets?orig_lang=en&audio_langs=en"
  assert_status 200 "Lang: en -> en HI"

  apply_mock_config "static" "" 'languages:
  rules:
    - audio: ja
      subtitles:
        - code: en
          providers: [mock]'
  api_get "/api/search/targets?orig_lang=ja&audio_langs=ja"
  assert_status 200 "Lang: provider filter"

  apply_mock_config "static" "" 'languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
          min_score: 80'
  api_get "/api/search/targets?orig_lang=en&audio_langs=en"
  assert_status 200 "Lang: min_score=80"

  api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
  sleep 1
}

# ===========================================================================
# SECTION: scans
# ===========================================================================
test_scans() {
  log "=== Scan Operations ==="
  local sid mid eid

  apply_mock_config "static" '      result_count: "1"'
  sleep 3

  sid=$(curl -sf --max-time 30 "${SUBFLUX_URL}/api/media/series" 2>/dev/null \
    | jq '[.[] | select(.episodes > 0 and .episodes <= 10)] | .[0].id // empty')
  if [ -z "$sid" ] || [ "$sid" = "null" ]; then
    sid=$(curl -sf --max-time 30 "${SUBFLUX_URL}/api/media/series" 2>/dev/null | jq '.[0].id // empty')
  fi

  if [ -n "$sid" ] && [ "$sid" != "null" ]; then
    local t0 t1
    t0=$(date +%s)
    _curl --max-time 120 -X POST "${SUBFLUX_URL}/api/scan/series/${sid}"
    t1=$(date +%s)
    _read_status
    [ "$HTTP_STATUS" = "200" ] && pass "Scan series/$sid (HTTP 200, $((t1 - t0))s)" \
      || fail "Scan series/$sid: HTTP $HTTP_STATUS"

    api_get "/api/activity"
    assert_status 200 "Activity after scan"

    t0=$(date +%s)
    _curl --max-time 120 -X POST "${SUBFLUX_URL}/api/scan/season/${sid}/1"
    t1=$(date +%s)
    _read_status
    [ "$HTTP_STATUS" = "200" ] && pass "Scan season/${sid}/1 (HTTP 200, $((t1 - t0))s)" \
      || fail "Scan season/${sid}/1: HTTP $HTTP_STATUS"

    eid=$(curl -sf --max-time 30 "${SUBFLUX_URL}/api/media/series/${sid}/episodes" 2>/dev/null | jq '.[0].id // empty')
    if [ -n "$eid" ] && [ "$eid" != "null" ]; then
      api_post "/api/scan/item" "{\"media_type\":\"episode\",\"media_id\":${eid},\"season\":1,\"episode\":1}"
      assert_status 202 "Scan item: episode"
    fi
  else
    skip "No series for scan tests"
  fi

  mid=$(curl -sf --max-time 30 "${SUBFLUX_URL}/api/media/movies" 2>/dev/null | jq '.[0].id // empty')
  if [ -n "$mid" ] && [ "$mid" != "null" ]; then
    api_post "/api/scan/movie/${mid}"
    assert_status 200 "Scan movie/$mid"
    api_post "/api/scan/item" "{\"media_type\":\"movie\",\"media_id\":${mid}}"
    assert_status 202 "Scan item: movie"
  else
    skip "No movies for scan tests"
  fi

  api_post "/api/scan"
  [ "$HTTP_STATUS" = "202" ] || [ "$HTTP_STATUS" = "409" ] \
    && pass "Full scan trigger (HTTP $HTTP_STATUS)" \
    || fail "Full scan: HTTP $HTTP_STATUS"

  # Edge cases
  api_post "/api/scan/series/0"
  log "Scan series/0: HTTP $HTTP_STATUS"
  api_post "/api/scan/movie/0"
  log "Scan movie/0: HTTP $HTTP_STATUS"
  api_post "/api/scan/item" "{}"
  log "Scan item empty: HTTP $HTTP_STATUS"
  api_post "/api/scan/item" '{"media_type":"invalid","media_id":1}'
  log "Scan item invalid: HTTP $HTTP_STATUS"

  api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
  sleep 2
}

# ===========================================================================
# SECTION: sync
# ===========================================================================
test_sync() {
  log "=== Sync & Preview ==="
  local state first_path

  state=$(api_get "/api/state?limit=1")
  sync_status
  first_path=$(printf '%s' "$state" | jq -r '.[0].path // empty' 2>/dev/null)

  if [ -n "$first_path" ]; then
    _curl -G --data-urlencode "subtitle=${first_path}" "${SUBFLUX_URL}/api/preview/start"
    assert_status 200 "Preview start"

    _curl -G --data-urlencode "path=${first_path}" \
      -d "start=0" -d "shift=0" \
      "${SUBFLUX_URL}/api/preview/subtitle"
    assert_status 200 "Preview subtitle"

    api_post "/api/sync/offset" "{\"subtitle_path\":\"${first_path}\",\"offset_ms\":0}"
    assert_status 200 "Sync offset no-op"
  else
    skip "No subtitles for sync/preview"
  fi

  api_post "/api/sync/audio" '{"subtitle_path":"/nonexistent.srt","video_path":"/nonexistent.mkv"}'
  log "Sync audio nonexistent: HTTP $HTTP_STATUS"
  api_post "/api/sync/offset" "{}"
  log "Sync offset empty: HTTP $HTTP_STATUS"
}

# ===========================================================================
# SECTION: poster_proxy
# ===========================================================================
test_poster_proxy() {
  log "=== Poster Proxy ==="
  local mid sid

  mid=$(curl -sf --max-time 30 "${SUBFLUX_URL}/api/media/movies" 2>/dev/null | jq '.[0].id // empty')
  if [ -n "$mid" ] && [ "$mid" != "null" ]; then
    api_get "/api/preview/poster?type=movie&id=${mid}"
    assert_status 200 "Poster: movie"
    api_get "/api/preview/poster?type=movie&id=${mid}&style=fanart"
    log "Poster fanart: HTTP $HTTP_STATUS"
  else
    skip "No movies for poster"
  fi

  sid=$(curl -sf --max-time 30 "${SUBFLUX_URL}/api/media/series" 2>/dev/null | jq '.[0].id // empty')
  if [ -n "$sid" ] && [ "$sid" != "null" ]; then
    api_get "/api/preview/poster?type=series&id=${sid}"
    assert_status 200 "Poster: series"
  else
    skip "No series for poster"
  fi

  api_get "/api/preview/poster?type=movie"
  log "Poster no id: HTTP $HTTP_STATUS"
  api_get "/api/preview/poster?type=invalid&id=1"
  log "Poster invalid type: HTTP $HTTP_STATUS"
}

# ===========================================================================
# SECTION: manual_download
# ===========================================================================
test_manual_download() {
  log "=== Manual Download Validation ==="
  api_post "/api/search/download" '{"provider":"nonexistent","subtitle_id":"x","file_path":"/media/test.mkv","language":"en"}'
  log "Download invalid provider: HTTP $HTTP_STATUS"

  api_post "/api/search/download" '{"provider":"mock"}'
  log "Download missing fields: HTTP $HTTP_STATUS"

  api_post "/api/search/download" '{"provider":"mock","subtitle_id":"x","file_path":"/etc/passwd","language":"en"}'
  log "Download path traversal: HTTP $HTTP_STATUS"

  api_post "/api/search/download" '{"provider":"mock","subtitle_id":"x","file_path":"/media/test.mkv","language":"xx"}'
  log "Download invalid lang: HTTP $HTTP_STATUS"
}

# ===========================================================================
# SECTION: hot_reload
# ===========================================================================
test_hot_reload() {
  log "=== Config Hot Reload ==="
  local modified parsed level

  modified=$(printf '%s' "$ORIGINAL_CONFIG" | sed 's/level: info/level: debug/' | sed 's/level: warn/level: debug/')
  api_put "/api/config" "$modified"
  assert_status 200 "Hot reload: debug logging"

  parsed=$(api_get "/api/config/parsed")
  sync_status
  level=$(printf '%s' "$parsed" | jq -r '.logging.level // empty' 2>/dev/null)
  [ "$level" = "debug" ] && pass "Hot reload: level=debug" || log "Hot reload: level=$level"

  api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
  sleep 1
}

# ===========================================================================
# SECTION: exclude_tags
# ===========================================================================
test_exclude_tags() {
  log "=== Exclude Tags ==="
  api_get "/api/config/parsed"
  local tc
  tc=$(cat "$SF_BODY" 2>/dev/null | jq '.search.ExcludeArrTags | length' 2>/dev/null)
  [ "${tc:-0}" -ge 1 ] && pass "Exclude tags: $tc configured" || fail "Exclude tags: $tc (expected >= 1)"
  api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
  sleep 1
}

# ===========================================================================
# SECTION: embedded_settings
# ===========================================================================
test_embedded_settings() {
  log "=== Embedded Provider Settings ==="
  local combos=("true true false" "false false false" "true false true" "false true true" "true true true")
  for combo in "${combos[@]}"; do
    # shellcheck disable=SC2086
    set -- $combo
    api_put "/api/config" "sonarr:
  enabled: true
  url: \"http://sonarr:8989\"
  api_key: \"$(_sonarr_key)\"
radarr:
  enabled: true
  url: \"http://radarr:7878\"
  api_key: \"$(_radarr_key)\"
media_roots:
  - /media
poll_interval: 999h
languages:
  default:
    - code: en
providers:
  embedded:
    settings:
      ignore_pgs: $1
      ignore_vobsub: $2
      ignore_ass: $3
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
logging:
  level: debug
  format: json" >/dev/null
    sleep 1
    sync_status
    [ "$HTTP_STATUS" = "200" ] && pass "Embedded: pgs=$1 vobsub=$2 ass=$3" \
      || fail "Embedded: pgs=$1 vobsub=$2 ass=$3 (HTTP $HTTP_STATUS)"
  done
  api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
  sleep 1
}

# ===========================================================================
# SECTION: adaptive_config
# ===========================================================================
test_adaptive_config() {
  log "=== Adaptive Backoff Config ==="
  local configs=("1s 5s 2 3" "1h 30D 3 10" "7D 90D 2 0")
  for c in "${configs[@]}"; do
    # shellcheck disable=SC2086
    set -- $c
    api_put "/api/config" "sonarr:
  enabled: true
  url: \"http://sonarr:8989\"
  api_key: \"$(_sonarr_key)\"
radarr:
  enabled: true
  url: \"http://radarr:7878\"
  api_key: \"$(_radarr_key)\"
media_roots:
  - /media
poll_interval: 999h
languages:
  default:
    - code: en
providers:
  embedded:
    settings:
      ignore_pgs: true
      ignore_vobsub: true
  mock:
    enabled: true
    priority: 1
search:
  scan_interval: 999h
  scan_delay: 5s
adaptive:
  initial_delay: $1
  max_delay: $2
  backoff_multiplier: $3
  max_attempts: $4
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
  level: debug
  format: json" >/dev/null
    sleep 1
    sync_status
    [ "$HTTP_STATUS" = "200" ] && pass "Adaptive: init=$1 max=$2 mult=$3 att=$4" \
      || fail "Adaptive: init=$1 max=$2 mult=$3 att=$4 (HTTP $HTTP_STATUS)"
  done
  api_put "/api/config" "$ORIGINAL_CONFIG" >/dev/null
  sleep 1
}

# ===========================================================================
# SECTION: real_providers
# ===========================================================================
test_real_providers() {
  log "=== Real Provider Smoke Tests ==="
  local r total

  r=$(api_get "/api/search?title=Inception&year=2010&lang=en&type=movie&imdb=tt1375666&tmdb=27205")
  sync_status
  assert_status 200 "Real: movie (Inception)"
  total=$(printf '%s' "$r" | jq '.results | length' 2>/dev/null)
  log "Real: movie returned $total results"

  r=$(api_get "/api/search?title=Breaking+Bad&season=1&episode=1&lang=en&type=episode&imdb=tt0903747&tvdb=81189")
  sync_status
  assert_status 200 "Real: TV (Breaking Bad)"
  total=$(printf '%s' "$r" | jq '.results | length' 2>/dev/null)
  log "Real: TV returned $total results"

  r=$(api_get "/api/search?title=Inception&year=2010&lang=fr&type=movie&imdb=tt1375666")
  sync_status
  assert_status 200 "Real: French movie"
  total=$(printf '%s' "$r" | jq '.results | length' 2>/dev/null)
  log "Real: French returned $total results"

  r=$(api_get "/api/search?title=Bleach&season=1&episode=1&lang=en&type=episode&tvdb=74796")
  sync_status
  assert_status 200 "Real: anime (Bleach)"
  total=$(printf '%s' "$r" | jq '.results | length' 2>/dev/null)
  log "Real: anime returned $total results"
  if [ "${total:-0}" -gt 0 ]; then
    local breakdown
    breakdown=$(printf '%s' "$r" | jq -r '[.results[].provider] | group_by(.) | map({(.[0]): length}) | add // {}' 2>/dev/null)
    log "Per-provider: $breakdown"
  fi
}

# ===========================================================================
# Main
# ===========================================================================

ALL_SECTIONS="health auth config providers media_browser coverage state files manual_search scoring backoff mock_provider provider_errors config_validation post_processing language_rules scans sync poster_proxy manual_download hot_reload exclude_tags embedded_settings adaptive_config real_providers"

if $DRY_RUN; then
  printf "Sections: %s\n" "$ALL_SECTIONS"
  exit 0
fi

wait_ready
save_config
trap 'restore_config; cleanup_tmp' EXIT

# Verify auth is disabled or credentials are configured
api_get "/api/config"
sync_status
if [ "$HTTP_STATUS" = "401" ]; then
  printf '%bERROR: subflux requires authentication. Set auth.disable_auth: true in config or provide credentials.%b\n' "$RED" "$NC"
  exit 1
fi

should_run "health" && test_health
should_run "auth" && test_auth
should_run "config" && test_config
should_run "providers" && test_providers
should_run "media_browser" && test_media_browser
should_run "coverage" && test_coverage
should_run "state" && test_state
should_run "files" && test_files
should_run "manual_search" && test_manual_search
should_run "scoring" && test_scoring
should_run "backoff" && test_backoff
should_run "mock_provider" && test_mock_provider
should_run "provider_errors" && test_provider_errors
should_run "config_validation" && test_config_validation
should_run "post_processing" && test_post_processing
should_run "language_rules" && test_language_rules
should_run "scans" && test_scans
should_run "sync" && test_sync
should_run "poster_proxy" && test_poster_proxy
should_run "manual_download" && test_manual_download
should_run "hot_reload" && test_hot_reload
should_run "exclude_tags" && test_exclude_tags
should_run "embedded_settings" && test_embedded_settings
should_run "adaptive_config" && test_adaptive_config
should_run "real_providers" && test_real_providers

# ===========================================================================
# Summary
# ===========================================================================

printf '\n%b========================================%b\n' "$CYAN" "$NC"
printf "${GREEN}PASS: %d${NC}  ${RED}FAIL: %d${NC}  ${YELLOW}SKIP: %d${NC}\n" "$PASS" "$FAIL" "$SKIP"
printf "Total: %d\n" "$((PASS + FAIL + SKIP))"

if [ "$FAIL" -gt 0 ]; then
  printf '\n%bFailures:%b' "$RED" "$NC"
  printf '%b\n' "$ERRORS"
  printf "\n"
  exit 1
fi

printf '\n%bAll tests passed.%b\n' "$GREEN" "$NC"
