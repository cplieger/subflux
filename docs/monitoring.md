# Monitoring and alerts

This page lists what subflux reports about itself and the alert rules that ship with it, for readers who run Prometheus or a similar ruler.

## What you get

subflux serves Prometheus metrics at `/metrics` on port 8374, with no login.

The `dev` branch is being rebuilt, so its image exports only the series below. The series for searches, downloads, providers, media roots and the database return with the parts of subflux that own them.

| Metric | Owner | Meaning |
| --- | --- | --- |
| `subflux_http_requests_total{method,path,status}` | web server | requests the web server answered, labelled with the matched route |
| `subflux_http_request_duration_seconds` | web server | how long each request took, in buckets up to 30 seconds |
| `subflux_panics_total` | web server | request handlers that crashed and were answered with a 500 |
| `subflux_config_mode{mode}` | settings | 1 for the current state of `config.yaml` (`unconfigured`, `failed` or `active`), 0 for the others |
| `subflux_alerts_active{kind,id}` | alerts | 1 for each problem subflux reports, labelled with its kind and id; a cleared problem's series goes away |
| `subflux_build_info{version}` | build | always 1, labelled with the running version |
| `subflux_auth_events_dropped_total` | accounts | sign-in audit lines (failed sign-ins, refusals and rate limits) dropped because their log queue was full; a rising count means a burst of refused requests outran the log |
| `subflux_scan_last_complete_timestamp_seconds{source}` | scan | when the media source's latest full scan completed, or when its scan loop started before one has; a removed source's series goes away |
| `subflux_scan_whole_interval_seconds` | scan | the `scan.whole_interval` setting in force |
| `subflux_backup_enabled` | backup | 1 while `backup.enabled` is on, else 0 |
| `subflux_backup_interval_seconds` | backup | the `backup.frequency` setting in force |
| `subflux_backup_last_success_timestamp_seconds` | backup | when the newest backup in `backup.path` was made, read from its file name; 0 before any |
| `subflux_backup_failures_total` | backup | scheduled backup runs that failed |

The `go_*` and `process_*` series, such as `process_start_time_seconds`, describe the subflux process itself.

The `id` label of `subflux_alerts_active` names what a problem is about: a subtitle site, media source or media server by the name you gave it, or a media root by its path. Anyone who can reach `/metrics` can read those names and paths.

## Logs

subflux writes structured logs to the container log, with UTC timestamps. `server.log_format` picks JSON, the default, or text, and takes effect at the next restart; until then subflux reports a `restart_pending` problem. `server.log_level` takes effect as soon as it is saved.

## Knowing the app is alive

The container's healthcheck runs `subflux health`, so Docker marks the container unhealthy when the web server stops. Docker's health state notifies nobody. To be told when subflux is down, load the alert rules below, whose `SubfluxTargetDown` and `SubfluxTargetAbsent` fire when Prometheus cannot scrape it, or point an uptime prober at `GET /api/health`. That endpoint answers without a login: `200` with `{"status":"ok"}` while the web server runs, and `503` with the reasons otherwise.

## Alerting

Load the rules in [`alerts/promql.yaml`](../alerts/promql.yaml) into Prometheus or the Mimir ruler, as [Loading metric alert rules](https://github.com/cplieger/docs/blob/main/docs/monitoring.md#loading-metric-alert-rules) shows. They cover:

| Alert | Fires when | Severity |
| --- | --- | --- |
| `SubfluxTargetDown` | no successful scrape for 15m, so every other rule here is blind | warning |
| `SubfluxTargetAbsent` | there is no `up` series at all, so subflux is no longer a configured target | warning |
| `SubfluxHTTP5xx` | more than 5 server errors in 10m | warning |
| `SubfluxProviderUnusable` | a subtitle site's problem has stood for 65m, longer than one cooldown and the retry after it | warning |
| `SubfluxMediaUnavailable` | a media root could not be read for 5m | warning |
| `SubfluxSourceUnreachable` | a Sonarr or Radarr instance could not be reached for 15m | warning |
| `SubfluxScanStalled` | a media source has gone two full scan intervals with no completed full scan, for 2h | warning |
| `SubfluxStoreWriteFailing` | writes to the database have failed for 5m | critical |
| `SubfluxBackupStale` | backups are on and none has succeeded for two backup intervals, on an instance that has run for an hour, for 15m | warning |
| `SubfluxConditionActive` | any other problem has stood for 15m | warning |

`SubfluxTargetDown` and `SubfluxTargetAbsent` are two rules because neither covers the other. `up == 0` catches a configured target that is failing and keeps its labels. `absent(up{job="subflux"})` catches a target that no longer exists, such as a dropped scrape target, a removed scrape config, or a deleted Kubernetes pod or ServiceMonitor. Keep the job matcher exact. A regex `absent()` asks whether any matching target is up, so one healthy replica hides every failed one, and its result carries no `job` label to route on.

The two target rules pin `job="subflux"`, because they read the synthetic `up` series and a bare `up == 0` would fire on every unrelated target in your Prometheus. Set that matcher to whatever your scrape config calls subflux. The rules that read subflux's own metrics carry no job matcher, so add one if you run more than one instance.

### Kinds of problem

Each problem subflux reports is one `subflux_alerts_active` series. Its `kind` decides which rule pages for it:

| Kind | What it means | Rule |
| --- | --- | --- |
| `config` | `config.yaml` holds settings that are not live | `SubfluxConditionActive` |
| `restart_pending` | a saved log format waits for a restart, or `config.yaml` sets `auth.disable_auth: true` while login is on | none |
| `job_records` | finished jobs are not yet stored | `SubfluxConditionActive`, unless `store` is active on the same instance |
| `store` | a write to the database failed | `SubfluxStoreWriteFailing` |
| `backup` | a scheduled backup failed | none; `SubfluxBackupStale` pages when no backup succeeds for two backup intervals |
| `auth_disabled` | login is turned off by `auth.disable_auth` | none |
| `admin_socket` | the admin socket could not be opened | `SubfluxConditionActive` |
| `pending_writes` | subtitle changes wait to be written | `SubfluxConditionActive`, unless `media_root` is active on the same instance |
| `provider` | a subtitle site rejected its credentials or cannot be reached | `SubfluxProviderUnusable` |
| `provider_data` | data a subtitle site needs, such as AnimeTosho's anime map, could not be refreshed | `SubfluxConditionActive` |
| `source` | a Sonarr or Radarr instance cannot be reached | `SubfluxSourceUnreachable` |
| `source_overlap` | a media source's folders overlap another source's | `SubfluxConditionActive` |
| `media_server` | a media server cannot be reached or refuses subflux | `SubfluxConditionActive` |
| `media_root` | a media root cannot be read | `SubfluxMediaUnavailable` |

`SubfluxConditionActive` covers every kind not named in its exclusion list, so a kind added in a later release pages with no new rule. It leaves out `restart_pending` and `auth_disabled`, since one is a restart request and the other a setting you chose, and `backup`, since `SubfluxBackupStale` reads the backup series instead: one failed run that the next retry repairs pages nobody, and an instance that never made a backup pages once it has run an hour. `job_records` and `pending_writes` usually follow from a database or media root fault, so each stays quiet while its cause is active on the same subflux instance, and that cause pages once. On `dev`, today's build raises every kind but `media_server`, which returns with media server support.
