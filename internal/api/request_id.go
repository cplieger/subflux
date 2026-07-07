// request_id.go provides request-ID lifecycle middleware: minting,
// validation, context threading, and response-header echo.

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/cplieger/webhttp"
)

type ctxKey struct{}

// RequestIDFromContext returns the request ID stored by RequestLogger,
// or "" if the context does not carry one. Helpers use this to populate
// the `request_id` field automatically when the caller passes *http.Request.
func RequestIDFromContext(ctx context.Context) string {
	v, ok := ctx.Value(ctxKey{}).(string)
	if !ok {
		return ""
	}
	return v
}

// WithRequestID attaches the given id to ctx. Used by RequestLogger and
// by tests; production handlers receive the id transparently via the
// middleware-decorated context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// NewRequestID returns a fresh 16-byte hex id, suitable for use as a
// request id. Falls back to a timestamp on the (effectively impossible)
// rand.Read failure rather than returning empty.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// validRequestID enforces a conservative shape on inbound X-Request-ID
// headers: 1..64 chars, alphanumeric + dash + underscore. Defends
// against header-injection that would echo into log lines.
func validRequestID(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// RequestLogger wraps next with method/path/status/latency/request-id
// logging at slog.Info, and threads a request id through r.Context()
// so handlers can pull it via RequestIDFromContext (or implicitly via
// the typed-code helpers).
//
// Inbound X-Request-Id is reused when shape-valid; otherwise a fresh
// 16-byte hex id is minted. The id is echoed back via the response
// header so a reverse proxy / client can correlate without parsing
// logs.
//
// Wraps the writer in webhttp.StatusRecorder so downstream handlers can
// still type-assert to http.Flusher / http.Hijacker (via
// http.ResponseController's Unwrap chain) — the previous local
// statusRecorder lacked Unwrap and broke SSE streaming under the
// middleware.
func RequestLogger(next http.Handler, record func(method, path string, status int, d time.Duration)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderXRequestID)
		if !validRequestID(id) {
			id = NewRequestID()
		}
		w.Header().Set(HeaderXRequestID, id)
		ctx := WithRequestID(r.Context(), id)

		start := time.Now()
		rec := webhttp.NewStatusRecorder(w)
		next.ServeHTTP(rec, r.WithContext(ctx))

		dur := time.Since(start)
		status := rec.Status()
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"latency", dur,
			"request_id", id,
		)
		if record != nil {
			record(r.Method, r.URL.Path, status, dur)
		}
	})
}
