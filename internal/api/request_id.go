// request_id.go re-exports the webhttp request-id context setter. subflux
// threads the request id under webhttp's context key — the same key
// webhttp.Logging writes on every request and webhttp.WriteError reads when it
// populates the request_id field of the error envelope. The thin local wrapper
// lets subflux tests use api.WithRequestID without importing webhttp directly.
// The id minting, validation, access logging, and context READS all live in
// webhttp (RequestLogger / Logging / WriteError).

package api

import (
	"context"

	"github.com/cplieger/webhttp"
)

// WithRequestID attaches the given id to ctx under webhttp's request-id key.
// Production requests receive the id transparently from webhttp.Logging; tests
// use this to inject a known id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return webhttp.WithRequestID(ctx, id)
}
