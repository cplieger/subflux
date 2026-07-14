# Contributing to subflux

subflux is a subtitle search, download, and sync engine for Sonarr/Radarr:
one Go binary that is the server, the scan engine, and the CLI, with a
framework-free TypeScript web UI embedded at build time. Most of the tree is
discoverable by reading it, but several patterns are load-bearing and easy to
break by accident. This guide covers the architecture at a high level, the
invariants you must not violate, and how to build, run, and check your work
locally.

## Project overview

- The **engine** (`internal/search/`) drives everything automated: it polls
  the \*arr import history, runs scheduled full-library scans, queries
  providers, scores results, downloads the best one, syncs its timing, and
  saves it next to the media file.
- The **server** (`internal/server/`) exposes the JSON API and the embedded
  web UI, streams live updates over SSE, and hot-reloads config saves into a
  running engine.
- The **CLI is the same binary**: `subflux search` runs a local search;
  `subflux status`, `scan`, `locks`, and friends are remote commands against a
  running instance over HTTP.
- State is a single bbolt file (`/config/subflux.bolt`); subtitle files are
  written alongside the media.

## Architecture

This is a map of package boundaries, not a file manifest. Discover the real
tree with `go list ./...` or by browsing `internal/`.

### Composition roots (repo root)

The only files that import concrete implementations:

- `main.go` — server mode (and the `go:generate` directive for wire codegen).
- `cli.go`, `cli_remote.go`, `cli_specs.go` — the flat CLI.
- `providers.go` — table-driven provider registration (`providerEntries`
  drives both `Register` and `RegisterSchema`).
- `internal/wiring/` — composition-root types connecting concrete
  implementations across api/metrics/search/provider.
- `cmd/wire-codegen/` — build-time-only driver over the
  [`wiregen`](https://github.com/cplieger/wiregen) library; not a server
  runtime dependency.

### Domain packages (`internal/`)

- `api/` — interface contracts and pure utility types. Every other package
  depends only on this one. Auth domain types come directly from
  `github.com/cplieger/auth/v2` and arr DTOs from
  `github.com/cplieger/arrapi` (no local aliases or adapter package).
- `search/` — the scan engine: provider orchestration, retry policy, history
  polling, and the download pipeline. Subpackages: `scoring`, `syncing`
  (sync + post-process glue), `timeout`, `release`.
- `subsync/` — the subtitle sync library (alass port, cross-language anchors,
  framerate correction, split-aware DP, audio sync). Subpackages: `ffmpeg`
  (subprocess wrappers), `fft`, `framerate`, `crosslang`, `vad` (the WebRTC
  GMM port).
- `provider/` — shared provider primitives (registry, retry wrapper, the
  SSRF-hardened HTTP client) plus one subdirectory per provider
  implementation, and support packages `archive`, `classify`, `dlcache`,
  `anidb`.
- `scorer/` — release scoring with configurable weights.
- `boltstore/` — the bbolt store (implements `api.Store`); `store/kv` holds
  the engine-agnostic key/codec helpers and `store/storetest` the contract
  suite. `authstore/` implements the auth store (durable bbolt + ephemeral
  in-memory).
- `config/` — YAML loader, env expansion, validation, hot reload;
  `config/schema/` generates the settings-UI schema.
- `server/` — HTTP routing, SSE, middleware, and the embedded UI, split into
  focused subpackages (`authhandlers`, `confighandlers`, `synchandlers`,
  `manualops`, `scanning`, `scheduler`, `polling`, `events`, `coverage`, …).
- `arrsvc/`, `metrics/`, `cache/`, `httputil/`, `cliparse/`, `clisearch/`,
  `testsupport/` — focused helpers and thin wrappers over the shared
  `cplieger/*` libraries.

Dependencies flow one way: composition roots → `internal/server/` → domain
packages → `internal/api/`. There are no reverse imports.

### Frontend (`internal/server/static-src/`)

TypeScript compiled by tsc, served as native ES modules with an importmap.
There is no bundler and no framework; reactivity comes from
`@cplieger/reactive`, user-initiated mutations go through
`@cplieger/actions`, HTTP through `@cplieger/fetch`, and toasts/dialogs/
tooltips from `@cplieger/ui-primitives`. Generated wire types live under
`static-src/wire/` (see below). CSS is split per feature and concatenated
via `MANIFEST` files at image build.

## Invariants (do not break)

These exist because breaking them caused real bugs, or because a test fails
CI when they drift.

- **Dependencies flow one way.** New code depends on `internal/api`
  interfaces, never sideways into another domain package's concretes.
- **Providers are registry-driven.** No `init()`, no blank imports, no global
  state. A provider is one package plus one `providerEntries` row; the
  settings UI schema is discovered from the registry.
- **Provider HTTP goes through the shared client**
  (`internal/provider/http_client.go`). It validates every URL against SSRF
  and re-validates the connected IP through a hardened transport. Never
  construct a bare `http.Client` inside a provider.
- **Secrets stay redacted.** If a provider or config field is a secret, add
  it to `secretKeyNames` (`server/confighandlers/secrets.go`);
  `TestSecretKeysCoverProviderSchemas` fails CI if a `Secret: true` schema
  field has no entry. Secret values never appear in `GET /api/config`
  responses or logs.
- **Config is schema-driven.** Adding a config field means adding a schema
  entry (`internal/config/schema`); the UI renders from
  `GET /api/config/schema`, never from a hardcoded form.
- **File writes are crash-safe.** Subtitle, config, and backup writes go
  through `github.com/cplieger/atomicfile` (temp → fsync → rename); writes
  refuse symlink targets. Don't reach for `os.WriteFile`.
- **External input is bounded.** Downloads, archive extraction, and parsing
  paths cap sizes and validate content (`api.ValidateSubtitleData`, the
  zip-bomb guards, `maxAlignSpans`/`maxAlignEvents` in subsync). Keep new
  input paths bounded the same way.
- **Manual locks gate automation.** Every automated search checks
  `IsManuallyLocked` first and fails closed on a store error. Don't add an
  automated download path that skips it.
- **HTTP responses go through the `api` helpers.** Don't hand-craft JSON
  error strings.
- **Logs are UTC.** The `slogx` library forces every record's timestamp to
  UTC, so the container needs no `TZ` and the binary embeds no
  `time/tzdata`.
- **Generated code is generated.** Never hand-edit
  `static-src/wire/*.gen.ts`; change the Go wire types and regenerate.
- **Client mutations go through the actions framework.** Buttons and form
  submits dispatch `@cplieger/actions` definitions (loading states, error
  toasts, rollback); don't fire raw fetches from feature modules.

## Local development

Requires Go (see the `go` line in `go.mod`). The binary builds with no CGO:

```sh
CGO_ENABLED=0 go build .
```

Subtitle-sync and video-preview code paths shell out to `ffmpeg` /
`ffprobe`. The container builds a minimal static ffmpeg (see `Dockerfile`);
to exercise those paths locally, put `ffmpeg` and `ffprobe` on your `PATH`.

Run the server with no arguments; on first start it comes up in unconfigured
mode (only the web UI and config endpoints respond) until a valid config is
saved. `config.example.yaml` is the documented template. The CLI is the same
binary:

```sh
subflux --help            # full command list
subflux search ...        # local search against configured providers
subflux status            # remote command, talks to a running server
```

Remote subcommands (`status`, `scan`, `state`, `locks`, `providers`, …)
reach a running instance over HTTP via `SUBFLUX_URL` (default
`http://127.0.0.1:8374`). When the instance has auth enabled, set
`SUBFLUX_API_KEY` to an API key (created via `subflux generate-api-key` or
the web UI's Security dialog); the CLI sends it as `X-API-Key`.

A `go.work` file may exist locally to develop against unreleased sibling
libraries (for example `../webhttp`); it is gitignored, and the image build
resolves dependencies from `go.mod`/`go.sum` only.

### Generated wire types

`internal/server/static-src/wire/` (TypeScript types, validating decoders,
and the SSE event registry) is generated from the Go wire types by
`cmd/wire-codegen`. After changing wire types, enums, or SSE events,
regenerate and commit the output:

```sh
go generate ./...         # runs ./cmd/wire-codegen
```

### Frontend assets

The browser bundle is produced during the Docker build, not committed. The
builder stage compiles `static-src/*.ts` with tsc (the TypeScript native
compiler), fetches the `@cplieger/*` runtime packages from npm for the
importmap-based vendor directory, and concatenates the per-feature CSS
splits listed in the `MANIFEST` files (`MANIFEST` → `style.css`,
`login.MANIFEST` → `login.css`). The compiled output under
`internal/server/static/` is gitignored and embedded at build time. Go-only
changes need none of this; rebuild the image to pick up frontend edits.

To iterate on `static-src/` locally, install the dev toolchain and use the
package scripts:

```sh
cd internal/server/static-src
npm install
npm run typecheck          # tsc -project tsconfig.json (source)
npm run typecheck:tests    # tsc -project tsconfig.test.json (tests)
npm test                   # vitest --run (single pass)
npm run test:watch         # vitest watch mode
```

## Running checks

The one-shot option mirrors CI exactly (the same reusable-workflow logic the
GitHub `ci.yaml` runs, Go and frontend jobs included):

```sh
bash ci-local.sh subflux   # from a checkout of the cplieger/ci repo
```

The direct commands are the day-to-day path.

### Go

```sh
go build ./...             # compile
go test ./...              # unit + property tests
go test -race ./...        # race detector
golangci-lint run          # lint (config: .golangci.yaml, v2)
golangci-lint fmt          # apply gofumpt (+extra) and gci import grouping
```

`golangci-lint run` reports unformatted files as issues, so the lint step
also enforces formatting; `gocyclo` caps complexity at 18.

### Frontend (from `internal/server/static-src/`)

```sh
npm run lint:eslint        # eslint . (strict typed linting)
npm run lint:prettier      # prettier --check
npm run lint:knip          # unused-export / dependency check
npm run lint:fix           # eslint --fix + prettier --write
```

CSS is linted with stylelint (`.stylelintrc.json`) and HTML with
html-validate (`.htmlvalidate.json`); invoke them with `npx stylelint` and
`npx html-validate` when touching styles or markup.

CI and releases run through the centralized `cplieger/ci` reusable
workflows; there is no local Makefile, and the `ci.yaml` / `release.yaml`
workflow files are synced from that repo (do not edit them here).

## Testing conventions

Tests live beside the code they cover:

- **Go**: `foo.go` → `foo_test.go` in the same package. Property-based tests
  use `pgregory.net/rapid` and fuzz targets use native `testing.F`; most
  packages carry both, and parsers, decoders, and validators handling
  untrusted input should ship a fuzz target with a real invariant (not just
  "doesn't crash").
- **Store implementations** run the engine-agnostic contract suite in
  `internal/store/storetest` (see `boltstore/contract_test.go` and
  `authstore/contract_test.go`). A new store method means a new contract
  case.
- **TypeScript**: `foo.ts` → `foo.test.ts`, co-located; vitest with
  `fast-check` for property tests, happy-dom for DOM-dependent tests.
- Run `go test -race ./...` on changes to concurrent code (the engine,
  scheduler, polling, SSE events, store).
- The **mock provider** (`internal/provider/mock/`) generates realistic
  results with configurable failure modes (errors, timeouts, rate limits,
  flakiness, season packs) and no network calls; use it to exercise engine
  behavior without real provider accounts.

Add or update tests with every behavior change, and make sure the relevant
checks above pass before opening a PR.

## Functional tests

`tests/functional/run.sh` drives a live instance over the HTTP API: 27
sections covering config, providers, coverage, scoring, scans, sync,
manual downloads, hot reload, and the mock provider's failure modes. It
needs `jq`, a reachable subflux with auth disabled or an API key configured,
and reachable Sonarr/Radarr. It saves and restores the config around the
run.

```sh
bash tests/functional/run.sh                  # all sections
bash tests/functional/run.sh --section config # one section
bash tests/functional/run.sh --dry-run        # list sections
```

Override the target with `--url <url>` or `SUBFLUX_URL`.

## Conventions and gotchas

- **Adding a provider** is one package plus one row. Implement a package
  under `internal/provider/<name>/` exporting a `Factory` and
  `Search`/`Download`, then add a single entry to `providerEntries` in
  `providers.go`. If the provider has a secret field, add it to
  `secretKeyNames` (see Invariants).
- **Sync behavior is config-gated.** Reference-based sync runs when
  `sync_subtitles` is on; audio sync joins the automatic path only when
  `post_processing.audio_sync_fallback` is enabled (validation requires
  `sync_subtitles`), and runs only when reference sync changed nothing.
  `Engine.SyncAndPostProcess` is the shared entry point for the auto path,
  manual downloads, and the CLI, so all three honor the same config.
- **The media volume must not be mounted read-only** (`:ro`). subflux writes
  subtitle files alongside media; a read-only mount silently drops
  downloads.
- **Go `regexp` has no negative lookahead** (`(?!...)`). Use two regexes
  with combined boolean logic instead.
- **Frontend dialogs**: Safari mishandles `<dialog>` heights with flex; use
  `display: grid` with `grid-template-rows`, and isolate `allow-discrete`
  transitions behind `@supports (transition-behavior: allow-discrete)`.

## Commits and pull requests

Branch from `main` (named after the change, e.g. `feat/subdl-provider`) and
open a PR; changes land via the `validate` gate. Commits follow
[Conventional Commits](https://www.conventionalcommits.org/); git-cliff
parses them for release notes (`cliff.toml`), so the type drives the version
bump: `feat:`, `fix:`, and `sec:` are user-facing and release, while
`chore:`/`ci:`/`docs:`/`refactor:`/`test:` do not. Write the subject as the
changelog line a user would read. The project is pre-1.0, so versions stay
within `0.x`; there is no automatic `1.0` (see [ROADMAP.md](ROADMAP.md) for
what 1.0 requires).

## Conduct and security

By participating you agree to the
[Code of Conduct](https://github.com/cplieger/.github/blob/main/CODE_OF_CONDUCT.md).
Report vulnerabilities through the
[security policy](https://github.com/cplieger/.github/blob/main/SECURITY.md),
never in a public issue.
