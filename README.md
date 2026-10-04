# subflux

[![Image Size](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/subflux/badges/size.json)](https://github.com/cplieger/subflux/pkgs/container/subflux) [![Platforms](https://img.shields.io/badge/platforms-amd64%20%7C%20arm64-blue)](https://github.com/cplieger/subflux/pkgs/container/subflux) [![base: Distroless](https://img.shields.io/badge/base-Distroless_nonroot-4285F4?logo=google)](https://github.com/cplieger/subflux/blob/main/Dockerfile) [![Mutation](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/subflux/badges/mutation.json)](https://github.com/cplieger/subflux/issues?q=label%3Agremlins-tracker) [![SBOM](https://img.shields.io/badge/SBOM-SPDX-1D4ED8)](https://github.com/cplieger/subflux/releases)

<!-- hub-overview BEGIN -->
subflux finds, downloads and times subtitles for the shows and movies in your Sonarr and Radarr library, and saves them next to each video. It works only through Sonarr and Radarr and does not scan folders on its own.

## ⚠️ Alpha software

subflux is under active development and has not reached version 1.0. Any update can change the config format, the saved state, the API or the behavior. Pin a specific image tag instead of `latest`, and read the release notes before you upgrade.

## What it does

subflux gets subtitles in your languages onto your whole library:

- Checks Sonarr and Radarr every 30 seconds and fetches subtitles for each new download.
- Scans the library once a day to fill gaps and upgrade recent subtitles.
- Fixes each download's timing to match a subtitle track in the video, or optionally its audio.
- Shows coverage on a web page, where you can search, pick and re-time subtitles by hand.

## Who it is for

subflux is built for people who run Sonarr, Radarr or both on a Docker host and want subtitles without picking them by hand. It picks languages from each file's audio track and can fetch forced and hearing-impaired versions. A 52,000-episode library runs in 1 GB of memory.

You need a Sonarr or Radarr instance with its API key, and your media mounted writable at the paths they use. subflux searches eight sites. Gestdown, AnimeTosho and YIFY Subtitles need no account. OpenSubtitles, SubDL, SubSource, BetaSeries and HDBits need an account or an API key.

Consider [Bazarr](https://github.com/morpheus65535/bazarr) if you want a native install on Windows, macOS or a Raspberry Pi, or a site such as Titlovi or Napiprojekt. It supports 184 subtitle languages and Whisper speech-to-text.

subflux is free software under the AGPL-3.0-or-later license.
<!-- hub-overview END -->

## Quick start

The image is on GitHub Container Registry and Docker Hub, for `amd64` and `arm64`. This is the [`compose.yaml`](compose.yaml) in this repository.

```yaml
services:
  subflux:
    image: ghcr.io/cplieger/subflux:latest
    container_name: subflux
    restart: unless-stopped
    # Run "sudo install -d -o 1000 -g 1000 /opt/appdata/subflux" before the first start,
    # or the container restarts in a loop. If .env sets PUID and PGID, use those numbers.
    user: "${PUID:-1000}:${PGID:-1000}"

    ports:
      - "8374:8374"

    volumes:
      - "/opt/appdata/subflux:/config"  # settings and saved state
      # Put the path Sonarr and Radarr show for your media on the right, writable by the user above.
      - "/path/to/media:/media"
```

1. Create the settings folder for user 1000 with `sudo install -d -o 1000 -g 1000 /opt/appdata/subflux`. If your `.env` sets `PUID` and `PGID`, use those numbers.
2. Replace `/path/to/media` with your media folder, and `/media` with the root folder Sonarr and Radarr show under Settings > Media Management. If they use two root folders, add one line for each.
3. Run `docker compose up -d`.
4. From another device on your network, open the host's address on port 8374 in a browser, for example `http://192.0.2.10:8374`.
5. Create the admin account.
6. In the setup wizard, enter Sonarr's address as you open it from another device, not `localhost`, and the API key from Sonarr's Settings > General page. Do the same for Radarr.
7. Enter the right-hand paths from step 2 as media roots, pick your subtitle sites and languages, then click **Finish**.

Run `docker logs subflux`. You should see `HTTP server listening`. If you see `failed to write default config`, the settings folder from step 1 is missing or belongs to another user.

## Configuration reference

Every setting is in the web page's Settings dialog and is saved to `/config/config.yaml`. A save applies at once, with no restart. To write the file yourself, start from the annotated [`config.example.yaml`](config.example.yaml).

| Key | Default | Description |
| --- | --- | --- |
| `sonarr.url`, `radarr.url` | required | The address subflux reaches Sonarr or Radarr at. Set at least one |
| `sonarr.api_key`, `radarr.api_key` | required | The API key from that app's Settings > General page |
| `media_roots` | _(unset)_ | The media folders, at the same paths Sonarr and Radarr use. The first-start file sets `/media` |
| `languages` | required | The subtitle languages to fetch for each audio language, plus a fallback list |
| `providers.<name>.enabled` | Gestdown, AnimeTosho and YIFY Subtitles on in the first-start file | Which subtitle sites to search, with their account settings |
| `poll_interval` | `30s` | How often subflux checks Sonarr and Radarr for new imports |
| `search.scan_interval` | `24h` | Time between the end of one full library scan and the start of the next |
| `search.upgrade_window_days` | `7` | How many days after a download subflux keeps looking for a better match |
| `search.exclude_arr_tags` | `no-subflux` | Sonarr or Radarr tags whose shows and movies subflux skips |
| `post_processing.audio_sync_fallback` | `false` | Re-time against the audio when the video has no subtitle track to compare with |
| `post_processing.strip_hi` | `false` | Remove hearing-impaired annotations such as `[music]` |
| `trusted_proxies` | `[]` | Your reverse proxy's address, so the real client address is logged and rate-limited |
| `allowed_hosts` | `[]` | The host names subflux answers for. Leave empty to accept any |
| `logging.level` | `info` | `debug`, `info`, `warn` or `error` |

Every other setting, the command line and environment references in `config.yaml` are in [Configuration](docs/configuration.md).

| Variable | Description | Default |
| --- | --- | --- |
| `PUID`, `PGID` | The user and group the container runs as, read by `compose.yaml` | `1000` |
| `SUBFLUX_URL` | The server address the `subflux` command line talks to | `http://127.0.0.1:8374` |
| `SUBFLUX_API_KEY` | An API key for the command line, created on the web page | _(unset)_ |

| Mount | Description |
| --- | --- |
| `/config` | `config.yaml` and the `subflux.bolt` database |
| `/media` | Your media, where subflux writes subtitle files next to each video |

| Port | Description |
| --- | --- |
| `8374` | The web page, the API and `/metrics` |

## Security

Create the admin account right after the first start. Until one exists, the first visitor to the page creates it. Keep port 8374 on your own network, or put a reverse proxy with HTTPS in front of it and set `trusted_proxies` and `allowed_hosts`. `/metrics` and `/api/health` answer without a login.

Logins use passwords, passkeys or OIDC, and an admin can create API keys for the command line. Saved site passwords and API keys are never sent back to the browser. Passkeys cover your whole domain, so with subflux at `subflux.example.com` the browser offers them on every `example.com` host. Setting `auth.disable_auth` turns login off and treats every request as an admin.

The image has no shell and runs as the user in `compose.yaml`. subflux checks each subtitle site's address before it connects. [Security](docs/hardening.md) covers reverse proxies, passkeys and what the image contains.

## Troubleshooting

The healthcheck runs `subflux health`, which passes while the web server is running, set up or not. Docker checks every 30 seconds after a 15-second start period and marks the container unhealthy after 3 failures in a row. A wrong API key or an unreachable Sonarr instance keeps it healthy, so you can fix it on the web page.

- The container restarts with `failed to write default config`. Create the settings folder for the container user, as in step 1.
- An alert says subtitles cannot be written in a folder. subflux pauses work there and retries every 5 minutes. Make the folder writable for the container user.
- Full scans do not start. A folder in `media_roots` is missing or not writable. Fix it or remove it from the list.
- An alert says a subtitle site was disabled. The site rejected your credentials three times. Save new ones or press the site's **Test** button.
- A share is unmounted. subflux keeps its records, shows an alert naming the path and holds new imports there until the share returns.

[How subflux works](docs/how-it-works.md) explains each case in full.

## Monitoring

subflux serves Prometheus metrics at `/metrics` and writes JSON logs. Eight Prometheus alert rules ship in [`alerts/promql.yaml`](alerts/promql.yaml). [Monitoring and alerts](docs/monitoring.md) lists the metrics and the rules and shows how to load them.

## Documentation

- [Configuration](docs/configuration.md) lists every setting and command, for anyone writing `config.yaml` by hand.
- [How subflux works](docs/how-it-works.md) covers scoring, timing, credential handling and the limits.
- [Security](docs/hardening.md) covers reverse proxies, passkeys and what the image contains.
- [Monitoring and alerts](docs/monitoring.md) lists the metrics and the alert rules.
- [Database maintenance](docs/database-maintenance.md) covers recovering and compacting the database.

## Credits

- [FFmpeg](https://ffmpeg.org/) and [x264](https://www.videolan.org/developers/x264.html) are built into the image to read subtitle and audio tracks and to stream the preview in the timing editor.
- The timing engine is a Go port of the alignment algorithm in [alass](https://github.com/kaegi/alass) by [@kaegi](https://github.com/kaegi).
- The audio timing uses a port of the voice-activity detector from the [WebRTC project](https://webrtc.org/), re-tuned for film audio.

## Contributing

Issues and pull requests are welcome. Open an issue first for a larger change, and see [CONTRIBUTING.md](CONTRIBUTING.md) for the layout and the checks.

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

AGPL-3.0-or-later. See [LICENSE](LICENSE). The image carries the license text of every bundled component under `/usr/share/licenses/`.

The image bundles two upstream programs built from source, both GPL-2.0-or-later: [FFmpeg](https://ffmpeg.org/) (the `FFMPEG_VERSION` pin in the Dockerfile, fetched as the `n<version>` tag archive of <https://github.com/FFmpeg/FFmpeg>) and [x264](https://www.videolan.org/developers/x264.html) (the `X264_COMMIT` pin, cloned from <https://code.videolan.org/videolan/x264.git>). The build applies no patches; this repository's Dockerfile, together with those two pinned sources, is the complete build recipe, which is how the GPL source offer for the shipped binaries is met.

Two upstream projects are ported into `internal/subsync` rather than bundled: the alignment algorithms of [alass](https://github.com/kaegi/alass) (GPL-3.0-or-later, which section 13 of the GPLv3 lets an AGPL-3.0 program contain) and the voice activity detector of [WebRTC](https://webrtc.googlesource.com/src/) (BSD-3-Clause). Both license texts, with the files that carry the ported code, are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
