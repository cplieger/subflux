# Contributing to subflux

The [shared rules](https://github.com/cplieger/.github/blob/main/CONTRIBUTING.md) for commits, releases, synced files and checks apply here.

## Rules

- The scripts and styles the server embeds from `internal/server/static/` are build output and not committed. After you edit `internal/server/static-src/`, run `npm install` there once, then `go run ./cmd/bundle` from the repository root before `go run .`, or the server embeds the old bundle.
- A new setting needs its field in `internal/config`, its default in `internal/config/defaults` when it has one, an entry in the matching `internal/config/schema/sections_*.go` builder, a line in `config.example.yaml` and a row in `docs/configuration.md`. The settings dialog shows only schema fields.
- Every provider builds its HTTP client with `provider.NewHTTPClient` or `provider.NewHTTPClientNoClientTimeout`. A client built any other way skips the SSRF checks, the redirect policy and the response size cap, and no test notices.
- Write a file the server keeps with `github.com/cplieger/atomicfile/v4`, never `os.WriteFile`, and a subtitle only through `internal/mediawrite`. A plain write can leave a half-written file after a crash, and a subtitle written elsewhere skips the check for unwritable media folders.
- Automated searches go through `Engine.SearchTargets`, which skips each target locked by a manual download and treats a store error as locked. A new automated path that calls providers directly keeps downloading for items the user chose by hand.
- A button or form mutation in the web page dispatches an `@cplieger/actions` definition, or it loses the pending state, the error toast with retry and the rollback. Raw `fetch` is kept for the non-JSON, streaming and custom-header flows `api-client.ts` lists.

## Checks

The functional suite in `tests/functional/` drives a running server over its API, and `go test ./...` skips it. CI runs only its 13 sections that need no Sonarr, Radarr or media files.

After you change scans, sync, coverage or manual downloads, run all 27 sections against a test instance with Sonarr, Radarr and media files. The suite changes its config and scans its first series and movie, so never use your own library:

```sh
SUBFLUX_URL=http://127.0.0.1:8374 go test -tags functional -count=1 -timeout 30m -run 'TestFunctional$' ./tests/functional/
```

Tests in the root package and `internal/server` bind a Unix socket under `t.TempDir()`, which fails with `bind: invalid argument` past 108 bytes, or 104 on macOS. The default macOS `TMPDIR` is too long, so run them there with `TMPDIR=/tmp`.
