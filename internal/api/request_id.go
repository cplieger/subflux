// request_id.go re-exports the webhttp request-id context accessors. subflux
// threads and reads the request id under webhttp's context key — the same key
// webhttp.Logging writes on every request and webhttp.WriteError reads when it
// populates the request_id field of the error envelope. Thin local wrappers let
// subflux code and tests use api.WithRequestID / api.RequestIDFromContext
// without importing webhttp directly. The id minting, validation, and access
// logging themselves live in webhttp (RequestLogger / Logging).

package api

import (
	"context"

	"github.com/cplieger/webhttp"
)

// RequestIDFromContext returns the request ID threaded by webhttp.Logging, or ""
// if the context does not carry one. The typed-code error helpers use it (via
// webhttp.WriteError) to auto-populate the `request_id` field of the envelope.
func RequestIDFromContext(ctx context.Context) string {
	return webhttp.RequestIDFromContext(ctx)
}

// WithRequestID attaches the given id to ctx under webhttp's request-id key.
// Production requests receive the id transparently from webhttp.Logging; tests
// use this to inject a known id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return webhttp.WithRequestID(ctx, id)
}
