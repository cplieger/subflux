# subflux

[![Image Size](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/subflux/badges/size.json)](https://github.com/cplieger/subflux/pkgs/container/subflux)
![Platforms](https://img.shields.io/badge/platforms-amd64%20%7C%20arm64-blue)
![base: Distroless](https://img.shields.io/badge/base-Distroless_nonroot-4285F4?logo=google)
[![Test coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/subflux/badges/coverage.json)](https://github.com/cplieger/subflux/actions/workflows/coverage.yml)
[![Mutation](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/subflux/badges/mutation.json)](https://github.com/cplieger/subflux/issues?q=label%3Agremlins-tracker)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/13221/badge)](https://www.bestpractices.dev/projects/13221)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cplieger/subflux/badge)](https://scorecard.dev/viewer/?uri=github.com/cplieger/subflux)
[![SBOM](https://img.shields.io/badge/SBOM-SPDX-1D4ED8)](https://github.com/cplieger/subflux/releases)

A fast, small subtitle search, download, and sync engine for Sonarr and Radarr. A Go-based Bazarr alternative that ships as a ~14 MB container.

> **Alpha.** Subflux is pre-1.0 and moving fast. It is functional and runs a 52k-episode production library today, but the UI, config format, and API can still change between releases, and rough edges remain. See [ROADMAP.md](ROADMAP.md) for the path to 1.0.

## What it does

Subflux finds, scores, downloads, and time-syncs subtitles for your Sonarr/Radarr library. It watches the \*arr import history (default 30 s poll) so new downloads get subtitles within moments of importing, and runs scheduled full-library scans (default 24 h) to fill gaps and upgrade what's already there. Every result passes an identity check and a release-quality score; the best one is downloaded, synced against the video's own timing, cleaned up, and saved next to the media file. A web UI shows per-show coverage at a glance and lets you search, pick, and visually sync subtitles by hand when you want control.

## Why subflux

Subflux was born from debugging Bazarr consuming 15-20 GB of RAM on a 52,000-episode library (a CPython allocator fragmentation problem, architectural rather than fixable). The answer was a rewrite with resource discipline as a design goal rather than an optimization pass:

- **~14 MB compressed image, ffmpeg included.** Distroless base, one static Go binary, no Python, no runtime dependencies.
- **The library that broke Bazarr runs in a 1 GB container limit.** Arr responses are batch-fetched then iterated (the largest payload, 4,360 movies, decodes to 24 MB); goroutine pools are bounded; media probing streams instead of buffering.
- **A purpose-built ffmpeg** (~5 MB, plus ~2 MB ffprobe): decoders for every mainstream video, audio, and subtitle codec, a single x264 encoder for the 360p preview, statically linked, no network support compiled in. It does track detection, subtitle/audio extraction, and the sync editor's live preview.
- **One file of state.** Pure-Go bbolt (no SQLite, no CGO): crash-durable on commit, hot-backed-up on schedule, and reconciled against the filesystem so it heals itself after manual file changes.
- amd64 + arm64 images, cosign-signed, with SBOM attestations.

## Features

### Search and scoring

- **Eight online providers** (OpenSubtitles, Gestdown, SubSource, SubDL, BetaSeries, AnimeTosho, YIFY Subtitles, HDBits) behind one interface, plus always-on embedded-track detection (local ffprobe inspection, not a provider).
- **Two-phase scoring:** a hard identity gate (IMDB/TVDB/TMDB id, season/episode, title validation) before a 0-100 release-quality score used for ranking and upgrades. Upgrades replace an existing subtitle only when a strictly better release shows up.
- **Language rules keyed on the audio track:** map detected audio languages to subtitle targets (Japanese audio can want different subs than English audio), with `standard`/`forced`/`hi` variants and per-target provider or min-score overrides.
- **Anime-aware numbering:** searches run with aired, scene (TheXEM), and absolute (TVDB) numbering and merge the results, so long-running shows with weird episode orders still match.
- **Embedded-subtitle awareness:** coverage counts text and bitmap tracks already in the container (SRT/ASS, PGS, VobSub, DVB), with per-codec ignore settings (top-level `embedded_subtitles` config section) so an unwanted PGS track doesn't stop the search for a text alternative.
- **Adaptive backoff:** per-provider exponential backoff for no-result media, season-level early termination, and per-provider timeouts, so failing or empty providers don't get hammered.
- **Manual override with locks:** manual downloads are saved as numbered siblings (`movie.fr.1.srt`) and lock the item from automation; locks clear automatically when the files are deleted or the video is replaced.

### The sync engine

Downloaded subtitles rarely match your exact file, so subflux syncs every download before it reaches the disk. The engine is a from-scratch Go port of [alass](https://github.com/kaegi/alass) plus subflux's own additions:

- **Constant-offset and split-aware alignment** (the alass algorithms): a piecewise-linear overlap rating finds the global shift, and a dynamic-programming pass finds split points where the offset changes abruptly (commercial breaks, different cuts) and aligns each segment independently.
- **Framerate correction:** drift detection via linear regression, snapping to known ratio pairs (23.976, 24, 25, 29.97, ...) with a golden-section search for non-standard ratios.
- **Cross-language anchor alignment** (subflux's own): aligns subtitles across languages by matching language-independent anchors (numbers, proper nouns, cognates by edit distance) through a monotonic DP pass. This is what lets a French subtitle sync against the English track embedded in the file.
- **Audio sync with a re-tuned VAD:** a pure-Go port of WebRTC's GMM voice-activity detector, reconfigured for film audio. The stock threshold under-detects movie dialogue; subflux runs a two-pass classification (a safe pass and a precise pass that must agree within 500 ms), which raised the benchmark pass rate from 82.5% to 91.4% across 326 files with ~120 ms residual error. Neural VADs (Silero, FunASR) were benchmarked and brought no improvement; the tuning is what matters. Voice activity is FFT-cross-correlated with the subtitle's dialogue signal, and ASS inputs are filtered for karaoke/signs/SDH first.
- **Confidence-weighted voting:** the strategies run concurrently, results cluster by offset, agreeing clusters get boosted, and the winner is applied only above a confidence threshold. A sync that isn't confident is a sync that doesn't happen.
- **Knowing when not to sync:** hash-matched subtitles and same-release matches skip sync (their timing is already right); forced subtitles skip it (too few cues to align reliably).
- **Post-processing:** encoding normalization to UTF-8 (UTF-16, Windows-1252), hearing-impaired annotation removal, tag stripping, and whitespace cleanup, each step logged.

Auto-downloads sync against an embedded subtitle reference when the file has one (audio-based sync as an automatic fallback is opt-in). Audio sync and manual offset adjustment are always available from the sync dialog.

### The web UI

A single-page app served by the same binary: framework-free TypeScript compiled to ~380 KB of first-party JS, loaded as native ES modules (no bundler), updating live over SSE.

- **Coverage table:** every series and movie against your language rules, with per-target have/total badges, embedded-track counts, a missing-only filter, and text search.
- **Visual sync editor:** subflux transcodes the actual video to a 360p stream on the fly (fMP4 over MSE, using the bundled ffmpeg) and renders the subtitle as a live caption track. Scrub the offset with a timecode control and the captions reload in place, so you verify timing with your own eyes; or run any sync strategy (embedded reference, external file, audio VAD) and preview its computed result before a byte is written.
- **Manual search:** query all providers for any item, see each result's score breakdown and tier, and grab a specific pick. Downloading anything other than the top pick locks the item from automation until you release it.
- **Schema-driven settings:** the entire config renders from a server-generated schema with tooltips; saves are validated, hot-reload the engine without a restart, and never echo secrets back to the browser.
- **History:** every download and search attempt, filterable by type, language, and provider.

First run lands in unconfigured mode: the settings dialog auto-opens and the instance serves nothing else until a valid config is saved.

### Auth, API, and operations

- **Multi-user auth (optional):** local passwords (Argon2id), passkeys/WebAuthn, OIDC with PKCE, per-user API keys, and login rate limiting.
- **API and CLI:** the UI drives a JSON API you can use too; every CLI subcommand is a remote command against a live instance (`subflux search`, `scan`, `status`, `locks`, `backoff`, `score`, ...) authenticated via `SUBFLUX_URL` / `SUBFLUX_API_KEY`.
- **Operations:** Prometheus metrics at `/metrics`, structured `slog` logging (UTC), a distroless file-marker healthcheck, graceful shutdown, scheduled bbolt hot backups with a staleness metric, database reconciliation before each scan, and scan resume after a restart.

## Run it

Images are published to both `ghcr.io/cplieger/subflux` and `docker.io/cplieger/subflux`; use whichever registry you prefer.

```yaml
# compose.yaml
services:
  subflux:
    image: ghcr.io/cplieger/subflux:latest
    container_name: subflux
    restart: unless-stopped
    user: "1000:1000"  # match your host user
    ports:
      - "8374:8374"
    volumes:
      - "/opt/appdata/subflux:/config"  # config.yaml + bbolt state
      - "/path/to/media:/media"         # must NOT be read-only; subflux writes subtitle files
```

### First run

Open `http://localhost:8374`. Every first boot runs ONE guided flow: create the admin account, then walk the setup wizard (Sonarr/Radarr, media roots, providers, languages, and tunable defaults), then an optional passkey enrollment. The wizard adapts to what it finds:

- **From scratch** — subflux wrote a placeholder config on first boot, so the wizard walks every step with sensible defaults prefilled.
- **Pre-authored `config.yaml`** — every step the file already answers is prefilled and collapsed (saved secrets show as present without exposing values); a fully valid config fast-forwards straight to a review screen with a "Finish" button. Collapsed steps stay reviewable and editable before finishing.

Finishing saves the config and activates everything in place — providers, arr clients, background scans, and auth capabilities — with no restart. The same holds for later edits in the settings dialog: saving a valid config hot-activates it, including WebAuthn/OIDC and logging changes.

## Configuration

All settings are editable in the web UI (schema-driven form) and persist to `config.yaml`. The CLI's subcommands — including manual search — run against a running instance via the `SUBFLUX_URL` env var; set `SUBFLUX_API_KEY` (created with `subflux generate-api-key` or in the web UI) to authenticate them when auth is enabled.

### Running behind a reverse proxy

When subflux runs behind a reverse proxy (nginx, Caddy, Traefik, HAProxy, ...), the network peer subflux sees is the proxy, not the browser. Set `trusted_proxies` to the proxy's IP or CIDR so the real client IP, resolved from a trusted `X-Forwarded-For` header, is used for the audit log, the login rate limiter, the session `IPAddress`, and the request access log, instead of the proxy's address:

```yaml
trusted_proxies:
  - 10.0.0.0/8
  - 192.168.0.0/16
```

Entries are CIDR ranges; write a single proxy as a `/32` (IPv4) or `/128` (IPv6). Only when the direct peer is one of these ranges is `X-Forwarded-For` consulted (walked right-to-left, spoof-safe); invalid CIDRs are rejected at config load. Leave `trusted_proxies` empty (the default) when subflux is directly exposed: the socket peer is used and `X-Forwarded-For` is ignored.

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

Distroless `gcr.io/distroless/static-debian13:nonroot` (UID 65532, no shell). Provider URLs are validated against SSRF before every fetch; secrets are redacted from config API responses; archive extraction is zip-bomb-guarded; all external input is size-capped and validated. Images are published with cosign signatures and SBOM attestations.

## Known limitations

- The media volume must be writable. Subflux saves subtitle files next to the media, so mounting `/media` read-only silently prevents downloads from being saved.
- Cloudflare-protected providers (subf2m, AvistaZ, CinemaZ) are not implemented.
- Long-running anime with colliding aired/absolute numbering has a rare false-positive window: results matched by a stable ID skip title validation, so an aired SxxEyy that collides with another episode's absolute number can slip through.

## Credits

- [alass](https://github.com/kaegi/alass) by [@kaegi](https://github.com/kaegi): the subtitle alignment algorithm (constant-offset rating and split-aware DP) that subflux ports to Go.
- The [WebRTC project](https://webrtc.org/): the GMM voice-activity detector that subflux ports and re-tunes for film audio.
- [Bazarr](https://github.com/morpheus65535/bazarr): the project that defined this category; subflux is an original engine, not a fork, but Bazarr set the bar for what it has to do.

## Contributing

Issues and pull requests are welcome; please open an issue first for larger changes so the approach can be discussed. Architecture notes and local build/test instructions are in [CONTRIBUTING.md](CONTRIBUTING.md); operational runbooks live in [docs/OPERABILITY.md](docs/OPERABILITY.md).

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude Opus](https://www.anthropic.com/claude) and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

GPL-3.0. See [LICENSE](LICENSE).
