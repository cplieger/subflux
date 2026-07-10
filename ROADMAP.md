# Roadmap

Subflux is **pre-1.0** (currently `v0.x`). It is functional and in active use,
but the road to a stable `1.0` is about hardening and validation, not new
scope. This document supersedes the
[default shared roadmap](https://github.com/cplieger/.github/blob/main/ROADMAP.md)
for this repository.

## Toward 1.0

- **Full manual test pass.** Work through the browser UI, settings flows,
  sync dialog, coverage badges, and CLI end to end before declaring 1.0.
- **First-run / setup flow.** Deep testing and refinement of the initial
  configuration experience (unconfigured mode, the settings wizard, "reset to
  defaults", and the transition to fully-configured/hot-reload). This is the
  first thing a new user touches and the highest-leverage place to remove
  friction.
- **Authentication stack hardening.** Focused testing and refinement of the
  auth stack — sessions, API keys, passkeys/WebAuthn, and **especially the
  OIDC + PKCE flow** (provider compatibility, token handling, edge cases).

## Ongoing

- Incorporate fixes surfaced by the weekly central fuzzing and
  [gremlins](https://gremlins.dev/) mutation-testing runs (subflux has an
  extensive property-based and fuzz suite; these runs are the main source of
  hardening work).
- Dependency and base-image currency via Renovate; security findings
  (CodeQL / Trivy / Scorecard) addressed as they arise.
- Bug and security response per
  [SECURITY.md](https://github.com/cplieger/.github/blob/main/SECURITY.md).

## Out of scope (for now)

Cloudflare-protected providers (subf2m, AvistaZ, CinemaZ) are not implemented;
see the "Known Limitations" section of the README.
