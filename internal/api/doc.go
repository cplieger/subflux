// Package api holds subflux's wire and domain types, and nothing else.
//
// Every remaining declaration is a type, a constant, or a method on one of this
// package's own types. There are no interfaces, no free functions, and no
// behaviour over the world: the last of that left in the C11 pass — the HTTP
// request prelude and response envelope (internal/httpapi), the language code
// space (internal/langcode), the media identifier builders (internal/mediaid),
// the subtitle filename and its content check (internal/subtitlefile), the arr
// DTO helpers (internal/arrsvc), config drift detection (internal/server) and
// the auth request context (internal/server/authhandlers).
//
// It imports no subflux package at all, and only three stdlib packages plus
// cplieger/auth for the user and session types the auth response bodies carry.
// Implementation packages import api, never the reverse. That acyclicity is
// what the package is for; it is NOT a place to declare contracts, and the
// testability/swappability rationale this doc used to give was measured and
// found false — there is one store implementation, one config implementation,
// and every consumer declares the surface it calls at its own site.
//
// No config contract is left here. ConfigProvider was the last composite, and
// what removed it was measuring the width it would need at its own consumer:
// internal/server composes the configuration, so an interface there had to
// carry 35 of *config.Config's 37 exported methods, and the 28 it actually
// carried were so far short that the server type-asserted back to the concrete
// type at six sites to reach the rest.
//
// Two things keep the package honest about its own contents. api.ErrorCode's
// ~70 constants and every json-tagged struct here are discovered by
// internal/wirespec and emitted as TypeScript, so a type that leaves changes a
// cross-language contract. And SchemaFunc is a func type, not an interface —
// the last contract-shaped declaration still here, flagged rather than moved.
package api
