// Package authstore re-exports the composite store interface from the
// standalone github.com/cplieger/auth/v2 library so subflux call sites can
// continue to refer to authstore.AuthStore without depending on the
// library's import path directly.
//
// The auth library publishes store.Composite, composed of
// UserStore + SessionPersister + PasskeyStore + KeyStore + OIDCStateStore.
// Subflux uses the library's domain types (auth.User, auth.Session, auth.Key,
// auth.PasskeyCredential) directly, so AuthDB satisfies this interface with
// zero adapter glue.
//
// This tiny package also keeps the historical role of breaking a test-time
// import cycle:
//
//	auth/_test → store/ → store/authdb/ → authstore/
//
// authstore/ remains leaf and only imports the auth library.
package authstore

import authlibstore "github.com/cplieger/auth/v2/store"

// AuthStore is the composite store interface implemented by the concrete
// authdb persistence layer and consumed by auth/ and server/.
//
// This is a type alias of github.com/cplieger/auth/v2/store.Composite — the
// library is the single source of truth for the contract.
type AuthStore = authlibstore.Composite
