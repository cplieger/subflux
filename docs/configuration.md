# Configuration

This page lists every setting and command of subflux, for readers who write `config.yaml` by hand or want to change more than the setup wizard asks.

The `dev` branch is being rebuilt, so its image reads `/config/config.yaml` at start but has no setup wizard or Settings dialog yet. The rest of this page describes subflux once the rebuild lands.

## Where settings live

Settings live in `/config/config.yaml`. The Settings dialog on the web page edits every one of them, and a save that passes its checks applies at once, with no restart. That includes login, passkey, OIDC and logging changes. A save that fails its checks is refused, and the running settings stay as they were.

With no `/config/config.yaml`, subflux starts unconfigured, and the first settings save writes the file. You can also write the file before the first start. The Default column below is the value used when a key is absent from the file.

## First start

Every first start runs one guided flow. You create the admin account, walk the setup wizard and can then add a passkey. The wizard steps are Sonarr and Radarr, media roots, subtitle sites, languages, search and post-processing.

- From scratch, every step is prefilled with defaults.
- With a `config.yaml` you wrote, every step the file already answers is prefilled and collapsed. Saved secrets show as present without their values. A fully valid file goes straight to a review screen with a **Finish** button, and collapsed steps stay open for review before you finish.

The Sonarr and Radarr step has a **Test connection** button. It checks the address and API key from the subflux server and reports the answer at the field. It is optional, but it catches a wrong address or a rejected key on the first step instead of at the final save. The same button is on both sections of the settings dialog later. Where a key is already saved, the test uses the stored one, so there is nothing to type again.

The test runs from subflux and not from your browser on purpose. `url` is the address subflux reaches the service at, while `public_url` is the address your browser uses for links. Only subflux can say whether its own scans will reach the service.

Reloading the page during setup returns you to the step you were on. Finishing saves the config and starts the sites, the Sonarr and Radarr connections, the background scans and the login methods in place, with no restart.

## Every setting

| Key | Default | Description |
| --- | --- | --- |
| `sources[]` | _(none)_ | The Sonarr and Radarr instances subflux reads your library from, at most 16. A save checks each added entry, and each entry whose type, address or API key changed, against its instance, and is refused if one does not answer |
| `sources[].id` | required | A name for the entry, unique in the list: 1 to 32 lowercase letters, digits and `-`. Alerts and metrics name the source by it |
| `sources[].type` | required | `sonarr` or `radarr` |
| `sources[].url` | required | The address subflux reaches the instance at |
| `sources[].api_key` | required | The API key from the General page of that app's Settings |
| `sources[].public_url` | _(unset)_ | The address your browser opens the instance at, for links on the web page |
| `sources[].enabled` | `true` | Set `false` to stop reading from the instance while keeping its entry, which is still checked as written |
| `sources[].path_mappings[]` | `[]` | Up to 32 `{from, to}` pairs, for media mounted at another path in subflux than in Sonarr or Radarr: a path the instance reports under `from` is read under `to`. subflux writes subtitles only under each mapping's `to`, or with no mappings under the instance's root folders |
| `sources[].exclude_tags[]` | `[no-subflux]` | Up to 32 tag labels, matched ignoring case. subflux skips the shows and movies that carry one. An empty list skips nothing |
| `languages` | required | The subtitle languages to fetch. List at least one target in `default` or a rule |
| `languages.close_varieties` | `false` | Download a close variety automatically when the exact one is missing, such as `pt-PT` or bare `pt` for a `pt-BR` target |
| `languages.default[]` | `[]` | The targets for items no rule matches, at most 16. Each has a `tag` and optional `variants` (`standard`, `hi`, `forced`), default `[standard]` |
| `languages.rules[].audio` | required | A language tag, such as `ja` or `pt-BR`. subflux tries an item's original language first, then each audio track, and the rule whose tag is closest wins |
| `languages.rules[].targets[]` | required | The targets for an item the rule matches, at most 16. An empty list fetches nothing and skips `default` |
| `providers.<name>` | _(none)_ | The subtitle sites to search, by name: `opensubtitles`, `gestdown`, `subsource`, `subdl`, `betaseries`, `animetosho`, `yifysubtitles`, `hdbits`, `tsukihime`, `jimaku`, `subsro`, `assrt` or `subx`. Until a site is listed, subflux searches none |
| `providers.<name>.enabled` | `true` | Set `false` to stop searching a listed site while keeping its settings. A site that is off may leave its required keys empty |
| `providers.<name>.priority` | `0` | From 0 to 100. Among equally good results, the site with the lower number goes first, then the site whose name sorts first |
| `providers.<name>.settings` | per site | Account settings: each site's keys, listed in its section of the Settings dialog |
| `scan.change_interval` | `30s` | How often subflux checks each source for imports, renames and deletes, from `10s` to `1h` |
| `scan.whole_interval` | `24h` | Time between the end of one full library scan and the start of the next, from `1h` to `720h` |
| `search.min_fit` | `other` | The lowest fit class downloaded automatically: `other`, `same_source`, `same_release` or `exact`. [How subflux works](how-it-works.md) explains each |
| `search.min_fit_by_target` | _(none)_ | A minimum fit per target language, replacing `min_fit` for it, such as `{ja: same_release}`. Each key is a target listed in `languages` |
| `search.upgrade_window_days` | `7` | For how many days after a download a better fitting subtitle replaces it, from 0 to 365. `0` turns these upgrades off |
| `search.scan_delay` | `5s` | The least time between two automatic searches on one site, from `5s` to `10m` |
| `embedded_subtitles.ignore_codecs` | `[pgs, vobsub]` | The formats of subtitle tracks inside a video that do not count as a subtitle it has, so subflux still downloads one: any of `srt`, `ass`, `ssa`, `webvtt`, `mov_text`, `pgs`, `vobsub`, `dvbsub`, `teletext`, `cea608` and `ttml` |
| `timing.sync` | `true` | Re-time each downloaded subtitle against a subtitle track inside the video |
| `timing.audio_fallback` | `false` | Also try the video's speech when its subtitle tracks cannot verify a download's timing. It is slower, and it can spot a different cut only when the video also has a usable subtitle track |
| `post_processing.strip_hi` | `false` | Remove hearing-impaired annotations such as `[music]` and speaker labels such as `JOHN:`. Overlapping annotations end at the first closing bracket, so `(a[b)c]` keeps `c]`, and a speaker label never takes the next line |
| `post_processing.strip_tags` | `true` | Remove `<i>`, `<b>`, `<u>`, `<s>` and `<font>` tags. Any other tag, such as `<iframe>`, stays as text |
| `post_processing.normalize_utf8` | `true` | Convert the text to UTF-8 from the encoding subflux detects, with the subtitle's language as a hint, and drop a byte order mark; text in an encoding subflux cannot name is written as it arrived |
| `post_processing.normalize_line_endings` | `true` | Convert line endings to CRLF, the SRT standard |
| `post_processing.clean_whitespace` | `true` | Trim lines and remove empty lines |
| `post_processing.remove_empty` | `true` | Drop subtitle lines left with no text |
| `trusted_proxies` | `[]` | Your reverse proxy's address, so the real client address is logged and rate-limited |
| `allowed_hosts` | `[]` | The host names subflux answers for. Leave empty to accept any |
| `auth.basic_enabled` | `true` | Password login. It can be turned off only while OIDC is on |
| `auth.check_breached_passwords` | `true` | Refuse passwords found in known breaches |
| `auth.oidc_enabled`, `auth.oidc` | `false` | Single sign-on through OIDC, with `issuer_url`, `client_id`, `client_secret` and `redirect_uri` |
| `auth.oidc_auto_redirect` | `false` | Send the login page straight to the OIDC provider |
| `auth.webauthn_rp_id` | _(unset)_ | The passkey domain. subflux fills it in on the first settings save. See [Security](hardening.md) |
| `auth.session_idle_timeout` | `24h` | Sign a session out after this long without use |
| `auth.session_absolute_timeout` | `168h` | Sign a session out after this long in any case |
| `auth.disable_auth` | `false` | Turn login off and treat every request as an admin |
| `backup.enabled` | `false` | Write scheduled copies of the database |
| `backup.path` | `/config` | Where the copies go |
| `backup.frequency` | `24h` | Time between copies. Minimum `1h` |
| `backup.retention` | `7` | How many copies to keep |
| `logging.level` | `info` | `debug`, `info`, `warn` or `error` |
| `logging.format` | `json` | `json` or `text` |

Durations accept `s`, `m` and `h`, plus `D` for days, `M` for 730-hour months and `Y` for years. Fractions work, such as `0.5D`, and `s`, `m` and `h` combine, such as `1h30m`.

Language tags are checked when the file loads, so a typo is reported at the start instead of matching nothing. Write each tag in its standard form, such as `en`, `pt-BR`, `es-419` or `zh-Hant`. The error names the form to use: `eng` answers "use en" and `pb` answers "use pt-BR". A Chinese target names its script, `zh-Hans` or `zh-Hant`. A file may hold at most 64 rules and 32 distinct target languages.

Subtitle files named the way other tools name them still count. `movie.pb.srt` reads as `pt-BR`, and `movie.cht.srt` as `zh-Hant`. That is also why `cht`, `spl` and `es-LA` are refused as targets: a file named with them reads as Traditional Chinese or Latin American Spanish, so name those targets `zh-Hant` and `es-419`.

## Environment variables in config.yaml

String values in `config.yaml` can read environment variables with the braced `${VAR}` form, so secrets stay in the container environment while the file holds the structure:

```yaml
api_key: ${SUBFLUX_OPENSUBTITLES_KEY}
```

Only `SUBFLUX_*` names and `CONFIG_ROOT`, `MEDIA_FOLDER`, `PUID`, `PGID`, `TZ`, `LAN_IP` and `HOSTNAME` are read. Any other name, and the unbraced `$VAR` form, stays as written. A listed variable that is not set logs a warning at the start naming it, and its `${VAR}` text stays as written. A variable that is set but empty becomes an empty string. Variables are read after the YAML is parsed, and only in string values, so an environment value cannot change the file's structure.

## Command line

With no arguments, `subflux` runs the server. `subflux health` is the probe the image's `HEALTHCHECK` runs: it exits 0 while the server reports itself healthy and 1 otherwise. `subflux --help` prints the usage line.
