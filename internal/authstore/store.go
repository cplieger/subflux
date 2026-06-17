// Package authstore re-exports the composite store interface from the
// standalone github.com/cplieger/auth library so subflux call sites can
// continue to refer to authstore.AuthStore without depending on the
// library's import path directly.
//
// The auth library publishes store.Composite, composed of
// UserStore + SessionPersister + PasskeyStore + KeyStore + OIDCStateStore.
// Subflux's domain types (api.User, api.Session, api.Key,
// api.PasskeyCredential) are type aliases of the library's types
// (see internal/api/auth_types.go), so AuthDB satisfies this interface
// directly with zero adapter glue.
//
// This tiny package also keeps the historical role of breaking a test-time
// import cycle:
//
//	auth/_test → store/ → store/authdb/ → authstore/
//
// authstore/ remains leaf and only imports the auth library.
package authstore

import authlibstore "github.com/cplieger/auth/store"

// AuthStore is the composite store interface implemented by the concrete
// authdb persistence layer and consumed by auth/ and server/.
//
// This is a type alias of github.com/cplieger/auth/store.Composite — the
// library is the single source of truth for the contract.
type AuthStore = authlibstore.Composite
