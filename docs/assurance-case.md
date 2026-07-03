# Security assurance case — subflux

This extends the shared
[default assurance case](https://github.com/cplieger/.github/blob/main/assurance-case.md)
with the threat model specific to `subflux`. Read that first. subflux is
**pre-1.0**; this case reflects the current posture, and the
[roadmap](../ROADMAP.md) lists the hardening still in progress (notably the auth
stack and first-run flow).

## What this is

A self-hosted subtitle search/download engine with a web UI and an auth stack.
It fetches from many third-party subtitle providers, extracts subtitle archives,
and writes files alongside the user's media. The notable security surfaces are
outbound provider fetches, archive extraction, authentication, and file writes.

## Threats and mitigations

| Threat                                                                            | Mitigation                                                                                                                                                                           | Evidence                                                                 |
| --------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------ |
| SSRF via attacker-influenced provider URLs                                        | every provider URL is validated and the connected IP re-checked via the `ssrf` library, wired through `internal/provider/http_client.go`                                             | ssrf integration, provider tests                                         |
| Malicious subtitle archives (zip/rar bombs, path traversal, season-pack mismatch) | episode-matched extraction with no "first file" fallback; multi-episode false-positive guards                                                                                        | `internal/provider/archive`, archive tests                               |
| Path traversal on subtitle writes                                                 | path sanitisation + atomic writes in `internal/fsutil` (CodeQL-friendly sanitiser)                                                                                                   | `fsutil`, tests                                                          |
| Auth bypass / weak auth                                                           | the `auth` library (Argon2id, passkeys/WebAuthn, OIDC+PKCE, API keys, rate limiting); see [auth's assurance case](https://github.com/cplieger/auth/blob/main/docs/assurance-case.md) | `internal/server/authhandlers`, auth integration tests                   |
| Secret leakage via the config API                                                 | secret fields redacted in `GET /api/config`; empty secrets merged server-side on save                                                                                                | `server/confighandlers/secrets.go`, `TestSecretKeysCoverProviderSchemas` |
| Stale/empty embedded web UI shipped                                               | the build embeds compiled assets; a CI image smoke test starts the container and asserts it serves                                                                                   | image smoke test (CI docker job)                                         |
| Resource exhaustion / parser bugs                                                 | extensive property-based + fuzz suite (300+ test files, 100+ fuzz targets)                                                                                                           | weekly fuzz + gremlins                                                   |

## Residual risks

- Pre-1.0: the auth stack (especially OIDC) and the first-run flow are still
  under focused hardening (see the roadmap).
- The media volume must be writable (subtitles are written beside media); this
  is inherent to the function, not a defect.

Report vulnerabilities privately per
[SECURITY.md](https://github.com/cplieger/.github/blob/main/SECURITY.md).
