# Configuration

This page lists every setting and command of subflux, for readers who write `config.yaml` by hand or want to change more than the setup wizard asks.

## Where settings live

Settings live in `/config/config.yaml`. The Settings dialog on the web page edits every one of them, and a save that passes its checks applies at once, with no restart. That includes login, passkey, OIDC and logging changes. A save that fails its checks is refused, and the running settings stay as they were.

On its first start, subflux writes the annotated [`config.example.yaml`](../config.example.yaml) as `/config/config.yaml` and opens the setup wizard. You can also write the file before the first start. The Default column below is the value used when a key is absent from the file.

## First start

Every first start runs one guided flow. You create the admin account, walk the setup wizard and can then add a passkey. The wizard steps are Sonarr and Radarr, media roots, subtitle sites, languages, search, scoring and post-processing.

- From scratch, every step is prefilled with defaults.
- With a `config.yaml` you wrote, every step the file already answers is prefilled and collapsed. Saved secrets show as present without their values. A fully valid file goes straight to a review screen with a **Finish** button, and collapsed steps stay open for review before you finish.

The Sonarr and Radarr step has a **Test connection** button. It checks the address and API key from the subflux server and reports the answer at the field. It is optional, but it catches a wrong address or a rejected key on the first step instead of at the final save. The same button is on both sections of the settings dialog later. Where a key is already saved, the test uses the stored one, so there is nothing to type again.

The test runs from subflux and not from your browser on purpose. `url` is the address subflux reaches the service at, while `public_url` is the address your browser uses for links. Only subflux can say whether its own scans will reach the service.

Reloading the page during setup returns you to the step you were on. Finishing saves the config and starts the sites, the Sonarr and Radarr connections, the background scans and the login methods in place, with no restart.

## Every setting

| Key | Default | Description |
| --- | --- | --- |
| `sonarr.url`, `radarr.url` | required | The address subflux reaches Sonarr or Radarr at. Set at least one |
| `sonarr.api_key`, `radarr.api_key` | required | The API key from that app's Settings > General page |
| `sonarr.enabled`, `radarr.enabled` | `true` in the first-start file | Turn one app off without deleting its settings |
| `sonarr.public_url`, `radarr.public_url` | _(unset)_ | The address your browser opens the app at, for links on the web page. Falls back to `url` |
| `media_roots` | _(unset)_ | The media folders, at the same paths Sonarr and Radarr use. The first-start file sets `/media` |
| `languages` | required | The subtitle languages to fetch for each audio language, plus a fallback list |
| `languages.rules[].audio` | required | A two-letter ISO 639-1 audio language, such as `en`. `pb` is Brazilian Portuguese |
| `languages.rules[].subtitles[]` | required | Targets with a `code`, optional `variants` (`standard`, `forced`, `hi`), `min_score`, `providers` and `exclude`. An empty list fetches nothing |
| `languages.default` | _(unset)_ | The targets for audio languages no rule matches. Remove it to skip them |
| `providers.<name>.enabled` | Gestdown, AnimeTosho and YIFY Subtitles on in the first-start file | Which subtitle sites to search, with their account settings |
| `providers.<name>.priority` | per site | Breaks a tie between equal scores. Lower is more trusted |
| `providers.<name>.settings` | per site | Account settings. [`config.example.yaml`](../config.example.yaml) lists each site's keys and where to get them |
| `poll_interval` | `30s` | How often subflux checks Sonarr and Radarr for new imports |
| `search.scan_interval` | `24h` | Time between the end of one full library scan and the start of the next |
| `search.upgrade_enabled` | `true` | Look for better subtitles during scans |
| `search.upgrade_window_days` | `7` | How many days after a download subflux keeps looking for a better match |
| `search.exclude_arr_tags` | `no-subflux` | Sonarr or Radarr tags whose shows and movies subflux skips |
| `search.scan_delay` | `5s` | Pause after each scan item that asked a site. Minimum `5s` |
| `search.provider_timeout` | `1h` | How long a site rests after repeated failures. Minimum `1h` |
| `search.min_score` | `0` | The lowest score a result may have to be downloaded, from 0 to 100 |
| `search.download_max_attempts` | `3` | Download attempts per result before subflux moves to the next one |
| `search.max_provider_concurrency` | `4` | How many sites subflux asks at the same time |
| `search.max_sse_clients` | `32` | How many open browser tabs can receive live updates |
| `adaptive.enabled` | `true` | Wait longer between searches for an item that keeps finding nothing |
| `adaptive.initial_delay` | `7D` | The first wait after a search with no result |
| `adaptive.max_delay` | `3M` | The longest wait between searches |
| `adaptive.backoff_multiplier` | `2` | How much each wait grows |
| `adaptive.max_attempts` | `0` | Searches before subflux gives up on an item. `0` retries forever |
| `embedded_subtitles.ignore_pgs` | `true` | PGS picture subtitles from Blu-ray do not count as a subtitle |
| `embedded_subtitles.ignore_vobsub` | `true` | VobSub picture subtitles from DVD do not count as a subtitle |
| `embedded_subtitles.ignore_ass` | `false` | ASS styled subtitles count as a subtitle |
| `post_processing.sync_subtitles` | `true` | Re-time automatic downloads against a subtitle track inside the video |
| `post_processing.audio_sync_fallback` | `false` | Re-time against the audio when the video has no subtitle track to compare with |
| `post_processing.sync_min_confidence` | `0.6` | The confidence, from 0 to 1, a timing change needs before it is applied |
| `post_processing.strip_hi` | `false` | Remove hearing-impaired annotations such as `[music]` |
| `post_processing.strip_tags` | `true` | Remove `<i>`, `<b>`, `<u>` and `<font>` tags |
| `post_processing.normalize_utf8` | `true` | Convert UTF-16 and Windows-1252 text to UTF-8 |
| `post_processing.normalize_endings` | `true` | Convert line endings to CRLF, the SRT standard |
| `post_processing.clean_whitespace` | `true` | Trim lines and remove empty lines |
| `post_processing.remove_empty` | `true` | Drop subtitle lines left with no text |
| `scoring.weights` | see the example file | How much each release property adds to a score. A hash match scores 100 |
| `trusted_proxies` | `[]` | Your reverse proxy's address, so the real client address is logged and rate-limited |
| `allowed_hosts` | `[]` | The host names subflux answers for. Leave empty to accept any |
| `auth.basic_enabled` | `true` | Password login. It can be turned off only while OIDC is on |
| `auth.check_breached_passwords` | `true` | Refuse passwords found in known breaches |
| `auth.oidc_enabled`, `auth.oidc` | `false` | Single sign-on through OIDC, with `issuer_url`, `client_id`, `client_secret` and `redirect_uri` |
| `auth.oidc_auto_redirect` | `false` | Send the login page straight to the OIDC provider |
| `auth.webauthn_rp_id` | _(unset)_ | The passkey domain. subflux fills it in on the first settings save. See [Security](security.md) |
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

Language codes are checked when the file loads, so a typo is reported at the start instead of matching nothing. Use the two-letter form: `en`, not `eng` or `EN`, and `pb` rather than `pt-BR`. The error names the code to use.

## Environment variables in config.yaml

String values in `config.yaml` can read environment variables with the braced `${VAR}` form, so secrets stay in the container environment while the file holds the structure:

```yaml
api_key: ${SUBFLUX_OPENSUBTITLES_KEY}
```

Only `SUBFLUX_*` names and `CONFIG_ROOT`, `MEDIA_FOLDER`, `PUID`, `PGID`, `TZ`, `LAN_IP` and `HOSTNAME` are read. Any other name, and the unbraced `$VAR` form, stays as written. A listed variable that is not set logs a warning at the start naming it, and its `${VAR}` text stays as written. A variable that is set but empty becomes an empty string. Variables are read after the YAML is parsed, and only in string values, so an environment value cannot change the file's structure.

## Command line

The `subflux` command talks to a running server. It reads the server address from `SUBFLUX_URL`, which defaults to `http://127.0.0.1:8374`, and an API key from `SUBFLUX_API_KEY` when login is on. An admin creates a key in the web page's Security dialog. Run `subflux --help` for every flag.

| Command | What it does |
| --- | --- |
| `subflux search --title "The Wire"` | Searches every site for an item and prints scored results. `--download` saves the top result, or the one `--pick N` names |
| `subflux scan` | Starts a full library scan |
| `subflux status` | Prints download and search counts |
| `subflux state` | Prints saved downloads, filtered by `--type`, `--lang` or `--provider` |
| `subflux locks`, `subflux unlock` | List manual locks and clear one |
| `subflux backoff` | Lists items waiting between searches |
| `subflux providers` | Lists the sites and whether each is on |
| `subflux timeouts`, `subflux timeouts-reset` | Show site timeouts and reset every site's state |
| `subflux score --video <name> --sub <name>` | Shows how a subtitle release would score against a video release |

Two account commands use a private socket inside the container instead of the network, so run them inside it with `docker exec -it subflux /subflux <command>`:

- `reset-password --user <name>` asks for a new password for an existing user.
- `generate-api-key --user <name> --label <label>` prints a new API key once.

If password login was turned off and OIDC is unavailable, `docker exec subflux /subflux enable-password-login` sets `auth.basic_enabled: true` in the file. Restart the container to apply it.
