// Package api holds subflux's wire and domain types.
//
// It imports no subflux implementation packages — only stdlib and shared
// external libraries (cplieger/arrapi, cplieger/auth, cplieger/webhttp).
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
package api
