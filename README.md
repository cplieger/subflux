# subflux

[![Image Size](https://ghcr-badge.egpl.dev/cplieger/subflux/size)](https://github.com/cplieger/subflux/pkgs/container/subflux)
![Platforms](https://img.shields.io/badge/platforms-amd64%20%7C%20arm64-blue)
![base: Distroless](https://img.shields.io/badge/base-Distroless_nonroot-4285F4?logo=google)
[![Go Report Card](https://goreportcard.com/badge/github.com/cplieger/subflux)](https://goreportcard.com/report/github.com/cplieger/subflux)
[![Test coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/subflux/badges/coverage.json)](https://github.com/cplieger/subflux/actions/workflows/coverage.yml)
[![Mutation](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/subflux/badges/mutation.json)](https://github.com/cplieger/subflux/issues?q=label%3Agremlins-tracker)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/13221/badge)](https://www.bestpractices.dev/projects/13221)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cplieger/subflux/badge)](https://scorecard.dev/viewer/?uri=github.com/cplieger/subflux)
[![SBOM](https://img.shields.io/badge/SBOM-SPDX-1D4ED8)](https://github.com/cplieger/subflux/releases)

A fast, low-memory subtitle search and download engine for Sonarr/Radarr libraries — a Go-based Bazarr replacement.

## What it does

Subflux finds, scores, downloads, and time-syncs subtitles for your Plex/Sonarr/Radarr library. It watches the *arr import history in real time and runs scheduled full-library scans, querying multiple providers concurrently, ranking results by release quality, and syncing subtitle timing to the video before saving.

It was born from diagnosing Bazarr's 15–20 GB memory consumption on a 52k-episode library (CPython arena fragmentation during long Sonarr sync loops). Subflux processes items in a batch-fetch-then-iterate model with bounded goroutine pools and no Python runtime — a single static binary in a distroless image.

## Features

- **Providers** — OpenSubtitles, Gestdown, embedded (ffprobe), SubSource, SubDL, BetaSeries, AnimeTosho, YIFY Subtitles, HDBits — behind one interface, each registered declaratively.
- **Two-phase scoring** — an identity gate (IMDB/TVDB/TMDB + season/episode + title validation) followed by a 0–100 release-quality score for ranking and upgrades.
- **Audio-language-based language rules** — map detected audio to subtitle targets, with `standard`/`forced`/`hi` variants and per-target provider/min-score overrides.
- **Subtitle sync** — a Go port of the alass algorithm plus cross-language anchor alignment, framerate correction, split-aware DP, and GMM audio VAD, with confidence-scored voting.
- **Adaptive backoff** — per-provider exponential backoff for no-result media, season-level early termination, and per-provider timeout to avoid hammering failing APIs.
- **Manual override** — numbered manual downloads lock an item from automation; locks clear automatically when files are deleted or the video is replaced.
- **Web UI** — a zero-dependency, zero-build vanilla-ESM SPA: library coverage table, manual search, sync dialog with video preview, history, schema-driven settings, and live SSE updates.
- **Multi-user auth** — local password + WebAuthn/passkeys + OIDC + TOTP, API keys, and rate limiting.
- **Prometheus metrics** at `/metrics`, structured `slog` logging, and a distroless file-marker healthcheck.

## Run it

```yaml
# compose.yaml
services:
  subflux:
    image: ghcr.io/cplieger/subflux:latest
    ports:
      - "8374:8374"
    volumes:
      - ./config:/config        # config.yaml + SQLite state
      - /media:/media           # must NOT be read-only — subflux writes .srt files
    restart: unless-stopped
```

Open `http://localhost:8374`; the settings dialog auto-opens on first run (unconfigured mode) until a valid config is saved. Point it at your Sonarr/Radarr instances and configure providers + language rules.

> The media volume must not be mounted `:ro` — subflux writes subtitle files alongside the media.

## Configuration

All settings are editable in the web UI (schema-driven form) and persist to `config.yaml`. The CLI also supports manual search and remote commands against a running instance (`subflux search`, `subflux scan`, `subflux status`, `subflux locks`, …) via the `SUBFLUX_URL` env var.

## Security

Distroless `gcr.io/distroless/static:nonroot` (UID 65534, no shell). Provider URLs are validated against SSRF before every fetch; secrets are redacted from config API responses; archive extraction is zip-bomb-guarded; all external input is size-capped and validated. Images are published with cosign signatures and SBOM attestations.

## License

GPL-3.0. See [LICENSE](LICENSE).
