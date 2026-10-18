# Monitoring and alerts

This page lists what subflux reports about itself and the alert rules that ship with it, for readers who run Prometheus or a similar ruler.

## What subflux emits

subflux writes structured logs to the container log, as JSON by default (`server.log_format`) and with UTC timestamps. It serves Prometheus metrics at `/metrics` on port 8374, with no login.

The `dev` branch is being rebuilt, so its image exports only the platform series below. The series for searches, downloads, providers, media roots, backups and the database return with the parts of subflux that own them.

| Metric | Meaning |
| --- | --- |
| `subflux_http_requests_total{method,path,status}` | requests the web server answered, labelled with the matched route |
| `subflux_http_request_duration_seconds` | how long each request took, in buckets up to 30 seconds |
| `subflux_panics_total` | request handlers that crashed and were answered with a 500 |
| `subflux_config_mode{mode}` | 1 for the current state of `config.yaml` (`unconfigured`, `failed` or `active`), 0 for the others |
| `subflux_build_info{version}` | always 1, labelled with the running version |

The `go_*` and `process_*` series, such as `process_start_time_seconds`, describe the subflux process itself.

## Alerting

Load the rules in [`alerts/promql.yaml`](../alerts/promql.yaml) into Prometheus or the Mimir ruler, as [Loading metric alert rules](https://github.com/cplieger/docs/blob/main/docs/monitoring.md#loading-metric-alert-rules) shows. They cover:

| Alert | Fires when | Severity |
| --- | --- | --- |
| `SubfluxTargetDown` | no successful scrape for 15m, so every other rule here is blind | warning |
| `SubfluxTargetAbsent` | there is no `up` series at all, so subflux is no longer a configured target | warning |
| `SubfluxHTTP5xx` | more than 5 server errors in 10m | warning |

`SubfluxTargetDown` and `SubfluxTargetAbsent` are two rules because neither covers the other. `up == 0` catches a configured target that is failing and keeps its labels. `absent(up{job="subflux"})` catches a target that no longer exists, such as a dropped scrape target, a removed scrape config, or a deleted Kubernetes pod or ServiceMonitor. Keep the job matcher exact. A regex `absent()` asks whether any matching target is up, so one healthy replica hides every failed one, and its result carries no `job` label to route on.

The two target rules pin `job="subflux"`, because they read the synthetic `up` series and a bare `up == 0` would fire on every unrelated target in your Prometheus. Set that matcher to whatever your scrape config calls subflux. The rules that read subflux's own metrics carry no job matcher, so add one if you run more than one instance.

Thresholds and the `severity` labels are starting points. Route by whatever labels your Alertmanager uses.
