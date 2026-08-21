// Package subflux is the application's wire and domain type vocabulary.
//
// It is named for the app because that is what it holds: the nouns every other
// package speaks in. Read at a use site, the qualifier says which vocabulary a
// type belongs to — subflux.Subtitle, subflux.SearchRequest, subflux.MediaType,
// subflux.ProviderID.
//
// Every declaration is a type, a constant, or a method on one of this package's
// own types. There are no interfaces, no free functions, and no behaviour over
// the world. A consumer that needs a contract declares it at its own site,
// sized to what it calls; behaviour lives in the package named for the job it
// does.
//
// The package imports no subflux package at all — only errors, fmt and time,
// plus cplieger/auth for the user and session types the auth response bodies
// carry. Implementation packages import it, never the reverse, and that
// acyclicity is what the package is for.
//
// Two of its contents are a published contract rather than an internal detail.
// ErrorCode's constants and every json-tagged struct here are discovered by
// internal/wirespec and emitted as TypeScript, so a type that leaves or a field
// that is renamed changes a cross-language contract. SchemaFunc is a func type,
// and the one contract-shaped declaration still here.
package subflux
