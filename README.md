# subflux

[![Image Size](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/subflux/badges/size.json)](https://github.com/cplieger/subflux/pkgs/container/subflux)
![Platforms](https://img.shields.io/badge/platforms-amd64%20%7C%20arm64-blue)
![base: Distroless](https://img.shields.io/badge/base-Distroless_nonroot-4285F4?logo=google)
[![Test coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/subflux/badges/coverage.json)](https://github.com/cplieger/subflux/actions/workflows/coverage.yml)
[![Mutation](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/subflux/badges/mutation.json)](https://github.com/cplieger/subflux/issues?q=label%3Agremlins-tracker)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/13221/badge)](https://www.bestpractices.dev/projects/13221)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cplieger/subflux/badge)](https://scorecard.dev/viewer/?uri=github.com/cplieger/subflux)
[![SBOM](https://img.shields.io/badge/SBOM-SPDX-1D4ED8)](https://github.com/cplieger/subflux/releases)

A fast, low-memory subtitle search and download engine for Sonarr/Radarr libraries — a Go-based Bazarr replacement.

## What it does

Subflux finds, scores, downloads, and time-syncs subtitles for your Plex/Sonarr/Radarr library. It watches the \*arr import history in real time and runs scheduled full-library scans, querying multiple providers concurrently, ranking results by release quality, and syncing subtitle timing to the video before saving.

Where Bazarr can consume 15-20 GB of memory during long Sonarr sync loops, subflux stays lean: it processes items in a batch-fetch-then-iterate model with bounded goroutine pools and no Python runtime, shipping as a single static binary in a distroless image.

## Features

- **Providers** — OpenSubtitles, Gestdown, embedded (ffprobe), SubSource, SubDL, BetaSeries, AnimeTosho, YIFY Subtitles, HDBits — behind one interface, each registered declaratively.
- **Two-phase scoring** — an identity gate (IMDB/TVDB/TMDB + season/episode + title validation) followed by a 0–100 release-quality score for ranking and upgrades.
- **Audio-language-based language rules** — map detected audio to subtitle targets, with `standard`/`forced`/`hi` variants and per-target provider/min-score overrides.
- **Subtitle sync** — a Go port of the alass algorithm plus cross-language anchor alignment, framerate correction, split-aware DP, and GMM audio VAD, with confidence-scored voting.
- **Adaptive backoff** — per-provider exponential backoff for no-result media, season-level early termination, and per-provider timeout to avoid hammering failing APIs.
- **Manual override** — numbered manual downloads lock an item from automation; locks clear automatically when files are deleted or the video is replaced.
- **Web UI** — a zero-dependency, zero-build vanilla-ESM SPA: library coverage table, manual search, sync dialog with video preview, history, schema-driven settings, and live SSE updates.
- **Multi-user auth** — local password + WebAuthn/passkeys + OIDC + TOTP, API keys, and rate limiting.
- **Prometheus metrics** at `/metrics`, structured `slog` logging (UTC timestamps), and a distroless file-marker healthcheck.

## Run it

```yaml
# compose.yaml
services:
  subflux:
    image: ghcr.io/cplieger/subflux:latest
    ports:
      - "8374:8374"
    volumes:
      - ./config:/config        # config.yaml + bbolt state
      - /media:/media           # must NOT be read-only — subflux writes .srt files
    restart: unless-stopped
```

Open `http://localhost:8374`; the settings dialog auto-opens on first run (unconfigured mode) until a valid config is saved. Point it at your Sonarr/Radarr instances and configure providers + language rules.

## Configuration

All settings are editable in the web UI (schema-driven form) and persist to `config.yaml`. The CLI also supports manual search and remote commands against a running instance (`subflux search`, `subflux scan`, `subflux status`, `subflux locks`, …) via the `SUBFLUX_URL` env var.

### Running behind a reverse proxy

When subflux runs behind a reverse proxy (nginx, Caddy, Traefik, HAProxy, …), the network peer subflux sees is the proxy, not the browser. Set `trusted_proxies` to the proxy's IP or CIDR so the real client IP — resolved from a trusted `X-Forwarded-For` header — is used for the audit log, the login rate limiter, the session `IPAddress`, and the request access log, instead of the proxy's address:

```yaml
trusted_proxies:
  - 10.0.0.0/8
  - 192.168.0.0/16
```

Entries are CIDR ranges; write a single proxy as a `/32` (IPv4) or `/128` (IPv6). Only when the direct peer is one of these ranges is `X-Forwarded-For` consulted (walked right-to-left, spoof-safe); invalid CIDRs are rejected at config load. Leave `trusted_proxies` empty (the default) when subflux is directly exposed — the socket peer is used and `X-Forwarded-For` is ignored.

## Alerting

subflux exposes Prometheus metrics on `/metrics`. Scrape it and evaluate these
with Prometheus or the Mimir ruler; delivery is through your Alertmanager.

```yaml
groups:
  - name: subflux
    rules:
      - alert: SubfluxHTTP5xx
        expr: sum(increase(subflux_http_requests_total{status=~"5.."}[10m])) > 5
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "subflux is returning HTTP 5xx"
          description: >
            subflux returned more than 5 server errors in 10m. Check upstream
            connectivity, provider config, and the subflux logs.
      - alert: SubfluxBackupStale
        expr: >
          subflux_backup_last_success_timestamp > 0
          and (time() - subflux_backup_last_success_timestamp) > 172800
        for: 1h
        labels:
          severity: warning
        annotations:
          summary: "subflux backup is stale"
          description: >
            No successful subflux backup recorded in over 48h. Check the backup
            task and the /config volume.
```

Thresholds are starting points; add your scrape `job` label to the selectors if
you run more than one instance, and route by whatever labels your Alertmanager
uses.

## Security

Distroless `gcr.io/distroless/static:nonroot` (UID 65534, no shell). Provider URLs are validated against SSRF before every fetch; secrets are redacted from config API responses; archive extraction is zip-bomb-guarded; all external input is size-capped and validated. Images are published with cosign signatures and SBOM attestations.

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude Opus](https://www.anthropic.com/claude) and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

GPL-3.0. See [LICENSE](LICENSE).
