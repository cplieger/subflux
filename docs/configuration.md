# Configuration

This page lists every setting and command of subflux, for readers who write `config.yaml` by hand or want to change more than the setup wizard asks.

The `dev` branch is being rebuilt, so its image reads `/config/config.yaml` at start but has no setup wizard or Settings dialog yet. The rest of this page describes subflux once the rebuild lands.

## Where settings live

Settings live in `/config/config.yaml`. The Settings dialog on the web page edits every one of them except `auth.disable_auth`, and a save that passes its checks applies at once, with no restart. That includes login, passkey, OIDC and log level changes. A save that fails its checks is refused, and the running settings stay as they were.

With no `/config/config.yaml`, subflux starts unconfigured, and the first settings save writes the file. You can also write the file before the first start. The Default column below is the value used when a key is absent from the file.

The file is one YAML mapping whose top-level keys are the sections below. A key subflux does not know is refused, so a typo is reported instead of ignored. Three YAML features are refused too: anchors and aliases (`&name`, `*name` and `<<`), explicit tags such as `!!str`, and a key written twice in one mapping. Repeat a value instead of sharing it, or put it in an environment variable.

Do not edit `config.yaml` while a settings save runs. A save checks that the file is still the one subflux last read, but an edit made during the save itself is overwritten. A file edited by hand while subflux runs is read at the next start, and until then a save from the Settings dialog is refused with "config.yaml changed on disk; restart to load it".

A file that does not load starts subflux with the errors shown on the web page and in the log, and no background work runs until a save fixes them. The `server` and `auth` sections stay in force when they loaded, so a typo in another section never turns password sign-in back on or drops your proxy list.

## First start

Every first start runs one guided flow. You create the admin account, walk the setup wizard and can then add a passkey. The wizard steps are Sonarr and Radarr, media roots, subtitle sites, languages, search and post-processing. Creating the admin account from another network, through a reverse proxy, or at a host name `server.allowed_hosts` does not list, asks for the setup token that subflux writes to its log, as [Security](hardening.md#the-first-admin-account) explains.

- From scratch, every step is prefilled with defaults.
- With a `config.yaml` you wrote, every step the file already answers is prefilled and collapsed. Saved secrets show as present without their values. A fully valid file goes straight to a review screen with a **Finish** button, and collapsed steps stay open for review before you finish.

The Sonarr and Radarr step has a **Test connection** button. It checks the address and API key from the subflux server and reports the answer at the field. It is optional, but it catches a wrong address or a rejected key on the first step instead of at the final save. The same button is on both sections of the settings dialog later. Where a key is already saved, the test uses the stored one, so there is nothing to type again.

The test runs from subflux and not from your browser on purpose. `url` is the address subflux reaches the service at, while `public_url` is the address your browser uses for links. Only subflux can say whether its own scans will reach the service.

Reloading the page during setup returns you to the step you were on. Finishing saves the config and starts the sites, the Sonarr and Radarr connections, the background scans and the login methods in place, with no restart.

## Every setting

Durations accept `s`, `m` and `h`, plus `D` for days, `M` for 730-hour months and `Y` for years. Fractions work, such as `0.5D`, and `s`, `m` and `h` combine, such as `1h30m`.

### `sources`

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
| `sources[].path_mappings[].from` | required | An absolute path as the instance reports it |
| `sources[].path_mappings[].to` | required | The absolute path subflux reads it at |
| `sources[].exclude_tags[]` | `[no-subflux]` | Up to 32 tag labels, matched ignoring case. subflux skips the shows and movies that carry one. An empty list skips nothing |

Each mapping's `to`, or each root folder when there are no mappings, is one media root: subflux checks that it is mounted before it writes there, and pauses that root alone when it is not. When one root folder holds several separately mounted shares, give each share its own mapping with the same path on both sides, such as `{from: /media/tv, to: /media/tv}` and `{from: /media/anime, to: /media/anime}`. Each share is then its own media root, so an unmounted share pauses its own folder instead of reading as an empty folder under a root that still looks mounted.

### `languages`

| Key | Default | Description |
| --- | --- | --- |
| `languages` | required | The subtitle languages to fetch. List at least one target in `default` or a rule |
| `languages.close_varieties` | `false` | Download a close variety automatically when the exact one is missing, such as `pt-PT` or bare `pt` for a `pt-BR` target |
| `languages.default[]` | `[]` | The targets for items no rule matches, at most 16 |
| `languages.default[].tag` | required | The target's language tag, such as `en`, `pt-BR` or `zh-Hant` |
| `languages.default[].variants` | `[standard]` | Which versions to fetch: any of `standard`, `hi` (hearing-impaired) and `forced` |
| `languages.rules[].audio` | required | A language tag, such as `ja` or `pt-BR`. subflux tries an item's original language first, then each audio track, and the rule whose tag is closest wins |
| `languages.rules[].targets[]` | required | The targets for an item the rule matches, at most 16, each with a `tag` and `variants` as in `default`. An empty list fetches nothing and skips `default` |

Language tags are checked when the file loads, so a typo is reported at the start instead of matching nothing. Write each tag in its standard form, such as `en`, `pt-BR`, `es-419` or `zh-Hant`. The error names the form to use: `eng` answers "use en" and `pb` answers "use pt-BR". A Chinese target names its script, `zh-Hans` or `zh-Hant`. A file may hold at most 64 rules and 32 distinct target languages.

Subtitle files named the way other tools name them still count. `movie.pb.srt` reads as `pt-BR`, and `movie.cht.srt` as `zh-Hant`. That is also why `cht`, `spl` and `es-LA` are refused as targets: a file named with them reads as Traditional Chinese or Latin American Spanish, so name those targets `zh-Hant` and `es-419`.

### `providers`

| Key | Default | Description |
| --- | --- | --- |
| `providers.<name>` | _(none)_ | The subtitle sites to search, by name: `opensubtitles`, `gestdown`, `subsource`, `subdl`, `betaseries`, `animetosho`, `yifysubtitles`, `hdbits`, `tsukihime`, `jimaku`, `subsro`, `assrt` or `subx`. Until a site is listed, subflux searches none |
| `providers.<name>.enabled` | `true` | Set `false` to stop searching a listed site while keeping its settings. A site that is off may leave its required keys empty |
| `providers.<name>.priority` | `0` | From 0 to 100. Among equally good results, the site with the lower number goes first, then the site whose name sorts first |
| `providers.<name>.settings` | per site | The site's account keys, listed below. Gestdown, AnimeTosho, TsukiHime and YIFY Subtitles take none |
| `providers.opensubtitles.settings.username` | required | Your opensubtitles.com account name |
| `providers.opensubtitles.settings.password` | required | Your opensubtitles.com password |
| `providers.opensubtitles.settings.api_key` | required | An API key from opensubtitles.com/consumers |
| `providers.opensubtitles.settings.use_hash` | `true` | Match by the video's file hash, which is fast and exact when OpenSubtitles knows the file |
| `providers.opensubtitles.settings.include_ai_translated` | `false` | Also offer subtitles from OpenSubtitles' own AI translation |
| `providers.opensubtitles.settings.include_machine_translated` | `false` | Also offer older machine-translated uploads |
| `providers.subsource.settings.api_key` | required | An API key from subsource.net |
| `providers.subdl.settings.api_key` | required | An API key from subdl.com |
| `providers.betaseries.settings.token` | required | An API key from betaseries.com/en/account/api |
| `providers.hdbits.settings.username` | required | Your hdbits.org account name |
| `providers.hdbits.settings.passkey` | required | The passkey from your hdbits.org user settings |
| `providers.jimaku.settings.api_key` | required | An API key from your jimaku.cc account page |
| `providers.subsro.settings.api_key` | required | An API key from your subs.ro account |
| `providers.assrt.settings.token` | required | The API token from your assrt.net user panel |
| `providers.subx.settings.api_key` | required | An API key from your SubX profile's API keys |

### `scan`

| Key | Default | Description |
| --- | --- | --- |
| `scan.change_interval` | `30s` | How often subflux checks each source for imports, renames and deletes, from `10s` to `1h` |
| `scan.whole_interval` | `24h` | Time between the end of one full library scan and the start of the next, from `1h` to `720h` |

### `search`

| Key | Default | Description |
| --- | --- | --- |
| `search.min_fit` | `other` | The lowest fit class downloaded automatically: `other`, `same_source`, `same_release` or `exact`. [How subflux works](how-it-works.md) explains each |
| `search.min_fit_by_target` | _(none)_ | A minimum fit per target language, replacing `min_fit` for it, such as `{ja: same_release}`. Each key is a target listed in `languages` |
| `search.upgrade_window_days` | `7` | For how many days after a download a better fitting subtitle replaces it, from 0 to 365. `0` turns these upgrades off |
| `search.scan_delay` | `5s` | The least time between two automatic searches on one site, from `5s` to `10m` |

### `timing`

| Key | Default | Description |
| --- | --- | --- |
| `timing.sync` | `true` | Re-time each downloaded subtitle against a subtitle track inside the video |
| `timing.audio_fallback` | `false` | Also try the video's speech when its subtitle tracks cannot verify a download's timing. It is slower, and it can spot a different cut only when the video also has a usable subtitle track |

### `post_processing`

| Key | Default | Description |
| --- | --- | --- |
| `post_processing.strip_hi` | `false` | Remove hearing-impaired annotations such as `[music]` and speaker labels such as `JOHN:`. Overlapping annotations end at the first closing bracket, so `(a[b)c]` keeps `c]`, and a speaker label never takes the next line |
| `post_processing.strip_tags` | `true` | Remove `<i>`, `<b>`, `<u>`, `<s>` and `<font>` tags. Any other tag, such as `<iframe>`, stays as text |
| `post_processing.normalize_utf8` | `true` | Convert the text to UTF-8 from the encoding subflux detects, with the subtitle's language as a hint, and drop a byte order mark; text in an encoding subflux cannot name is written as it arrived |
| `post_processing.normalize_line_endings` | `true` | Convert line endings to CRLF, the SRT standard |
| `post_processing.clean_whitespace` | `true` | Trim lines and remove empty lines |
| `post_processing.remove_empty` | `true` | Drop subtitle lines left with no text |

### `embedded_subtitles`

| Key | Default | Description |
| --- | --- | --- |
| `embedded_subtitles.ignore_codecs` | `[pgs, vobsub]` | The formats of subtitle tracks inside a video that do not count as a subtitle it has, so subflux still downloads one: any of `srt`, `ass`, `ssa`, `webvtt`, `mov_text`, `pgs`, `vobsub`, `dvbsub`, `teletext`, `cea608` and `ttml` |

### `server`

| Key | Default | Description |
| --- | --- | --- |
| `server.trusted_proxies` | `[]` | Your reverse proxies, each an address or a CIDR range, whose `X-Forwarded-For` subflux believes. See [Security](hardening.md#running-behind-a-reverse-proxy) |
| `server.allowed_hosts` | `[]` | The host names or addresses subflux answers for, each with an optional port, such as `subflux.example.com` or `192.0.2.5:8374`. Leave empty to accept any. A host listed here also lets the first admin account be created from your network without the setup token |
| `server.log_level` | `info` | `debug`, `info`, `warn` or `error` |
| `server.log_format` | `json` | `json` or `text`. A change takes effect at the next restart |

### `auth`

| Key | Default | Description |
| --- | --- | --- |
| `auth.password_login` | `true` | Password sign-in. A save that would leave a user with no way to sign in, such as turning this off while an admin has only a password, is refused |
| `auth.session_idle_timeout` | `24h` | Sign a session out after this long without use, from `5m` to 30 days |
| `auth.session_absolute_timeout` | `168h` | Sign a session out after this long in any case, from the idle timeout to 90 days |
| `auth.check_breached_passwords` | `false` | Refuse a new password found in known data breaches. When the check cannot be reached, the password is accepted |
| `auth.disable_auth` | `false` | Turn login off and treat every request as an admin. Read only at start: edit `config.yaml` and restart, since a settings save cannot change it |
| `auth.passkey_origins` | `[]` | The addresses passkeys sign in from, such as `https://subflux.example.com`: each an `https` origin with a host name, or `http://localhost` with an optional port, and no path. Empty turns passkeys off. See [Security](hardening.md#passkeys) |
| `auth.oidc_enabled` | `false` | Single sign-on through an OIDC provider |
| `auth.oidc_issuer` | _(unset)_ | The provider's issuer URL, over `https` or `http` to a loopback address. Required while `oidc_enabled` is on, at most 255 bytes |
| `auth.oidc_client_id` | _(unset)_ | The client ID the provider gave subflux. Required while `oidc_enabled` is on, at most 255 bytes |
| `auth.oidc_client_secret` | _(unset)_ | The client secret. Required while `oidc_enabled` is on |
| `auth.oidc_redirect_url` | _(unset)_ | subflux's own address followed by `/api/auth/oidc/callback`, such as `https://subflux.example.com/api/auth/oidc/callback`, over `https` or `http` to a loopback address. Required while `oidc_enabled` is on, at most 255 bytes |
| `auth.oidc_label` | `Single sign-on` | The sign-in button's text, at most 64 characters |
| `auth.oidc_auto_provision` | `false` | Create an account at a person's first OIDC sign-in, when their `groups` claim holds one of `oidc_allowed_groups`. Without it, an OIDC identity signs in only once it is linked to an existing account |
| `auth.oidc_allowed_groups` | `[]` | The `groups` claim values that may create an account. Required while `oidc_auto_provision` is on |

### `backup`

| Key | Default | Description |
| --- | --- | --- |
| `backup.enabled` | `false` | Write scheduled copies of the database |
| `backup.path` | `/config/backups` | The folder the copies go in, created when missing. An absolute path other than the database's own folder, so a tool backing it up never reads the live database |
| `backup.frequency` | `24h` | Time between copies, from `1h` to 30 days. A restart never skips a copy that is due, and a failed copy is retried after 15 minutes |
| `backup.retention` | `7` | How many copies to keep, from 1 to 365. Only files named like `subflux-20060102T150405Z.bolt` are counted or deleted |

## Environment variables in config.yaml

String values in `config.yaml` can read environment variables with the braced `${VAR}` form, so secrets stay in the container environment while the file holds the structure:

```yaml
providers:
  opensubtitles:
    settings:
      username: alice
      password: ${SUBFLUX_OPENSUBTITLES_PASSWORD}
      api_key: ${SUBFLUX_OPENSUBTITLES_KEY}
```

Only `SUBFLUX_*` names are read, so a file cannot read the container's other variables. A reference to any other name, and a `SUBFLUX_*` variable that is not set, is an error naming it, and the section holding it does not load. A variable that is set but empty becomes an empty string. Variables are read after the YAML is parsed, and only in values, so an environment value cannot change the file's structure. A value read from a variable may itself hold `${`, but a value written in the file cannot hold a literal `${`, since there is no escape for it.

## Command line

With no arguments, `subflux` runs the server. `subflux health` is the probe the image's `HEALTHCHECK` runs: it exits 0 while the server reports itself healthy and 1 otherwise. `subflux --help` prints the usage line.

The other commands ask the running server to act, through a socket only processes inside the container can open. Run each with `docker exec -it subflux subflux <command>`. Each acts as the admin socket, which the security log names as the actor. A refused command prints a one-line JSON error and exits 1, and a command line that is not one of these exits 2 after printing its usage.

| Command | What it does |
| --- | --- |
| `reset-password <user>` | Sets a new password, read twice from the terminal or once from piped input, and signs the user out everywhere: their sessions and API keys are revoked and their sign-in locks cleared |
| `recovery-link <user>` | Prints a `/recover#<token>` link, valid for 15 minutes, that lets the user set a new password. Open it at subflux's own address |
| `create-user <user> [--role admin\|user]` | Creates a user, `user` by default, with a password read as `reset-password` reads one |
| `create-key <user> [--label <text>] [--scope <scope>]...` | Creates an API key for the user and prints it once. Each `--scope` is one of `read`, `operate`, `admin`, `manage_users` and `create_keys`; with none, the key has the user's full access |
| `setup-token` | Prints a fresh setup token while no account exists, replacing the one in the log |
| `enable-password-login` | Saves `auth.password_login: true` into the settings in force, for when no other sign-in method works. It prints the errors instead when the file would still not load, and changes nothing when there is no `config.yaml`, since password sign-in is then already on |
| `store [--verify [--repair]]` | Prints the database's counts and sizes, as [Database maintenance](database-maintenance.md#inspecting-the-database) describes |

A user name is matched ignoring case, so `Alice` and `alice` are one user.
