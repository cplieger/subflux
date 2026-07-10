# Contributing to subflux

Notes on the architecture and local workflow. subflux is a single Go binary
(a Bazarr replacement for subtitle search/download); most of the tree is
discoverable, but a few patterns are load-bearing and easy to miss.

## Layout

The repository root holds the composition roots — the only files that import
concrete implementations:

- `main.go` — server mode (and `go generate` directive).
- `cli.go`, `cli_remote.go`, `cli_specs.go` — the flat CLI.
- `providers.go` — table-driven provider registration.

Everything else lives under `internal/`. Dependencies flow one way: the
composition roots import `internal/server/` (split into subpackages) and
the domain packages (`search`, `store`, `config`, `provider`, `subsync`, …),
which in turn depend only on `internal/api/` (interface contracts + pure
types). There are no reverse imports. `cmd/wire-codegen/` is a build-time-only
driver, not a server runtime dependency.

## Local development

Requires Go (see the `go` line in `go.mod`). The binary builds with no CGO:

```sh
CGO_ENABLED=0 go build .
```

Subtitle-sync and video-preview code paths shell out to `ffmpeg` / `ffprobe`.
The container builds a minimal static ffmpeg (see `Dockerfile`); to exercise
those paths locally, put `ffmpeg` and `ffprobe` on your `PATH`.

Run the server with no arguments; it writes a default config on first start
and otherwise reads `config.example.yaml` as the documented template. The CLI
is the same binary:

```sh
subflux --help            # full command list
subflux search ...        # local search against configured providers
subflux status            # remote command, talks to a running server
```

Remote subcommands (`status`, `scan`, `state`, `locks`, `providers`, …) reach
a running instance over HTTP via `SUBFLUX_URL` (default
`http://127.0.0.1:8374`).

## Running checks

```sh
go test ./...             # unit + property tests (pgregory.net/rapid)
golangci-lint run         # lint (config: .golangci.yaml, v2)
golangci-lint fmt         # apply gofumpt (+extra) and gci import grouping
```

`golangci-lint run` reports unformatted files as issues, so the lint step
also enforces formatting; `gocyclo` caps complexity at 18. CI and releases run
through the centralized `cplieger/ci` reusable workflows — there is no local
Makefile, and the `ci.yaml` / `release.yaml` workflow files are synced from
that repo (do not edit them here).

### Generated wire types

`internal/server/static-src/wire/` (TypeScript types/decoders for the SSE and
config wire shapes) is generated. After changing wire types, enums, or SSE
events, regenerate:

```sh
go generate ./...         # runs ./cmd/wire-codegen
```

### Frontend assets

The TypeScript under `internal/server/static-src/` is compiled with tsc and
the CSS is concatenated from per-feature splits via `MANIFEST` files — both
happen inside the Docker build, and the compiled output
(`internal/server/static/*.js`, `style.css`, …) is gitignored and embedded at
build time. Go-only changes need none of this; rebuild the image to pick up
frontend edits.

## Functional tests

`tests/functional/run.sh` drives a live instance over the HTTP API. It needs `jq`, a reachable subflux with auth
disabled or an API key configured, and reachable Sonarr/Radarr. It saves and
restores the config around the run.

```sh
bash tests/functional/run.sh                  # all sections
bash tests/functional/run.sh --section config # one section
bash tests/functional/run.sh --dry-run        # list sections
```

Override the target with `--url <url>` or `SUBFLUX_URL`.

## Conventions and gotchas

- **Adding a provider** is one package plus one row. Implement a package under
  `internal/provider/<name>/` exporting a `Factory` and `Search`/`Download`,
  then add a single entry to `providerEntries` in `providers.go` (it drives
  both `Register` and `RegisterSchema`, and the settings UI schema is
  discovered from the registry). No `init()`, no blank imports, no global
  state. If the provider has a secret field, add it to `secretKeyNames` —
  `TestSecretKeysCoverProviderSchemas` in `providers_test.go` fails CI
  otherwise.
- **Config is schema-driven.** Adding a config field is one entry in the
  config schema (`internal/config/schema`); the UI renders from
  `GET /api/config/schema` rather than a hardcoded form.
- **HTTP responses go through the `api` helpers.** Don't hand-craft JSON error
  strings.
- **The media volume must not be mounted read-only** (`:ro`). subflux writes
  subtitle files alongside media; a read-only mount silently drops downloads.
- **Go `regexp` has no negative lookahead** (`(?!...)`). Use two regexes with
  combined boolean logic instead.
- **Logs are UTC.** The `slogx` library (its `UTCTime` `ReplaceAttr`) forces every
  record's timestamp to UTC, so the container needs no `TZ` and the binary
  embeds no `time/tzdata`.

## Commits and PRs

Branch from `main` and open a PR. Commits follow
[Conventional Commits](https://www.conventionalcommits.org/); git-cliff parses
them for release notes (`cliff.toml`), so the type drives the version bump:
`feat:`, `fix:`, `sec:` are user-facing, while `chore:`/`ci:`/`docs:`/
`refactor:`/`test:` do not trigger a release. Write the subject as the
changelog line a user would read.

## Conduct & security

By participating you agree to the
[Code of Conduct](https://github.com/cplieger/.github/blob/main/CODE_OF_CONDUCT.md).
Report vulnerabilities through the
[security policy](https://github.com/cplieger/.github/blob/main/SECURITY.md),
never in a public issue.
