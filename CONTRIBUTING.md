# Contributing to subflux

The [shared rules](https://github.com/cplieger/.github/blob/main/CONTRIBUTING.md) for commits, releases, synced files and checks apply here.

## Rules

- Every provider builds its HTTP client with `provider.NewHTTPClient` or `provider.NewHTTPClientNoClientTimeout`. A client built any other way skips the SSRF checks, the redirect policy and the response size cap, and no test notices.
- Rename or relabel a metric in the same commit as the alert rules in `alerts/promql.yaml` that read it. A rule over a series subflux no longer exports stays silent, and `TestAlertRulesNameExportedSeries` fails on it.
- Write a file the server keeps with `github.com/cplieger/go/filesafe`, never `os.WriteFile`. A plain write can leave a half-written file after a crash.
