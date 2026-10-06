# Monitoring and alerts

This page lists what subflux reports about itself and the alert rules that ship with it, for readers who run Prometheus or a similar ruler.

## What subflux emits

subflux writes structured logs to the container log, as JSON by default (`logging.format`) and with UTC timestamps. It serves Prometheus metrics at `/metrics` on port 8374, with no login.

These series tell a fetched subtitle from a saved one, and carry the state the alert rules below read:

| Metric | Meaning |
| --- | --- |
| `subflux_downloads_total{provider}` | subtitle files fetched from a site, saved or not |
| `subflux_subtitles_saved_total{provider}` | subtitle files the automatic search wrote next to the media |
| `subflux_subtitle_write_errors_total` | failed subtitle writes and failed media folder write tests |
| `subflux_media_root_unwritable{root}` | 1 while subtitles cannot be written in that media root or a folder below it |
| `subflux_media_root_unavailable{root}` | 1 while that media root cannot be read, so nothing under it is treated as deleted |
| `subflux_provider_disabled{provider}` | 1 while a site is disabled because it rejected its credentials |
| `subflux_provider_setting_rejected{provider,setting}` | 1 after a site refuses an optional setting, until the setting is accepted or changed, a Test passes, site state is reset or subflux restarts |
| `subflux_provider_auth_failures_total{provider}` | credential rejections counted toward a disable |
| `subflux_provider_rate_limited_total{provider,op}` | rate-limit answers that paused a site's `search` or `download` |
| `subflux_scans_total` | completed full library scans |
| `subflux_http_requests_total{method,path,status}` | requests the web server answered |
| `subflux_backup_last_success_timestamp` | the time of the last successful database backup |
| `subflux_store_file_bytes`, `subflux_store_freelist_bytes` | the database file size and its reusable free space |
| `subflux_configured` | 1 while a valid configuration is active |

[Database maintenance](database-maintenance.md) explains when the two database sizes call for a compaction.

## Alerting

Load the rules in [`alerts/promql.yaml`](../alerts/promql.yaml) into Prometheus or the Mimir ruler, as [Loading metric alert rules](https://github.com/cplieger/docs/blob/main/docs/monitoring.md#loading-metric-alert-rules) shows. They cover:

| Alert | Fires when | Severity |
| --- | --- | --- |
| `SubfluxTargetDown` | no successful scrape for 15m, so every other rule here is blind | warning |
| `SubfluxTargetAbsent` | there is no `up` series at all, so subflux is no longer a configured target | warning |
| `SubfluxScanStalled` | no scheduled scan has completed in 26h while the process has been up that long | warning |
| `SubfluxHTTP5xx` | more than 5 server errors in 10m | warning |
| `SubfluxBackupStale` | no successful backup recorded in over 48h | warning |
| `SubfluxProviderCredentialsRejected` | a site rejected its credentials and was disabled, or rejected an optional setting, for 5m | warning |
| `SubfluxMediaUnwritable` | subtitle files could not be written under a media root for 5m (`subflux_media_root_unwritable{root}` reads 1) | warning |
| `SubfluxMediaUnavailable` | a media root could not be read for 5m (`subflux_media_root_unavailable{root}` reads 1) | warning |

More on individual rules:

- `SubfluxTargetDown` and `SubfluxTargetAbsent` are two rules because neither covers the other. `up == 0` catches a configured target that is failing and keeps its labels. `absent(up{job="subflux"})` catches a target that no longer exists, such as a dropped scrape target, a removed scrape config, or a deleted Kubernetes pod or ServiceMonitor. Keep the job matcher exact. A regex `absent()` asks whether any matching target is up, so one healthy replica hides every failed one, and its result carries no `job` label to route on.
- `SubfluxScanStalled` is the deadman for the scan loop. subflux can be up, scraped and answering HTTP while its scheduled scan has been stuck for days. A range selector does not need the series to span the whole range, so after a restart `increase(subflux_scans_total[26h])` reads 0 as soon as there are two samples. The uptime guard `(time() - process_start_time_seconds) > 93600` stops the rule firing after every restart. `subflux_configured == 1` keeps it quiet in unconfigured mode, where there is no scan to miss. A scan that normally runs longer than 26h also trips it, so widen the range and the uptime guard together.
- `SubfluxProviderCredentialsRejected` covers two cases. A site that rejects its credentials three times is disabled, so it is not pushed into a forced password reset. A disabled site never re-enables on its own. A rejected optional setting, such as AnimeTosho's AniDB client key, leaves the site working. It also clears when the site accepts the setting on a later lookup.
- While `SubfluxMediaUnwritable` fires, scans and downloads for files in the affected folder are paused. The failure can be a folder write test or a subtitle save. The web UI alert and the subflux log name the folder and the error.
- `SubfluxMediaUnavailable` fires when the root is missing or empty, or the filesystem refused or timed out. New imports under it wait until it can be read again, and subflux rechecks the failed path every 5 minutes. The web UI alert and the subflux log name the path and the error.

The two target rules pin `job="subflux"`, because they read the synthetic `up` series and a bare `up == 0` would fire on every unrelated target in your Prometheus. Set that matcher to whatever your scrape config calls subflux. The rules that read subflux's own metrics carry no job matcher, so add one if you run more than one instance.

Thresholds and the `severity` labels are starting points. `SubfluxScanStalled`'s window tracks `search.scan_interval`, 24h by default, so move both together if you change it. Route by whatever labels your Alertmanager uses.
