// Package authstore defines the composite store interface used by the
// authentication subsystem (auth/, server/) and implemented by the
// authdb/ persistence layer.
//
// This tiny package exists to break a test-time import cycle:
//
//	auth/_test → store/ → store/authdb/ → authstore/
//
// vs the prior arrangement where store/authdb/'s compile-time assertion
// referenced auth.AuthStore, which created the cycle when auth/_test
// transitively pulled in store/authdb/. authstore/ is leaf and
// only imports api/ for the sub-interface symbols, breaking the cycle.
//
// The composite interface is defined here. The narrower per-call-site
// interfaces (auth.SessionStore, server.authHandlerStore, etc.) remain
// at their consumer packages; authstore.AuthStore is specifically the
// "passes one thing that satisfies all of them" composition type.
package authstore

import "subflux/internal/api"

// AuthStore is the composite store interface implemented by the
// concrete authdb persistence layer and consumed by auth/ and server/.
type AuthStore interface {
	api.UserStore
	api.SessionPersister
	api.PasskeyStore
	api.KeyStore
	api.TOTPStore
	api.OIDCStateStore
}
