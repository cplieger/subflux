package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/webhttp"
)

// accessLogMW returns subflux's request-id + access-log middleware for use in
// the global webhttp.Chain. skipPath (exact r.URL.Path match) is served without
// a recorder, access line, or metric — matching webhttp.WithSkipPaths — for
// long-lived streams like SSE whose open-to-close duration would mislead.
func (s *Server) accessLogMW(skipPath string) webhttp.Middleware {
	return func(next http.Handler) http.Handler {
		return s.accessLog(next, skipPath)
	}
}

// accessLog mirrors webhttp.Logging (mint/echo/thread a request id, record the
// status via a streaming-safe StatusRecorder, emit one deferred access line at
// Info, fire the RecordHTTP metric hook) and adds a client_ip field resolved
// through the trusted-proxy set — a field webhttp.Logging's fixed shape does
// not carry. It reuses webhttp's exported request-id + recorder primitives so
// the recorder / http.ResponseController behavior is identical to the library:
// the deferred emit means a panicking handler is still logged, and a skipPath
// request is served through the raw writer (no recorder) so SSE streaming is
// untouched while its id is still minted, echoed, and threaded.
func (s *Server) accessLog(next http.Handler, skipPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		id := r.Header.Get(webhttp.HeaderRequestID)
		if !webhttp.ValidRequestID(id) {
			id = webhttp.NewRequestID()
		}
		w.Header().Set(webhttp.HeaderRequestID, id)
		r = r.WithContext(webhttp.WithRequestID(r.Context(), id))

		path := r.URL.Path
		if path == skipPath {
			next.ServeHTTP(w, r)
			return
		}

		rec := webhttp.NewStatusRecorder(w)
		defer func() {
			d := time.Since(start)
			slog.Info("http",
				"method", r.Method,
				"path", path,
				"status", rec.Status(),
				"duration_ms", d.Milliseconds(),
				"request_id", id,
				"client_ip", authhandlers.ClientIP(r),
			)
			s.metrics.RecordHTTP(r.Method, path, rec.Status(), d)
		}()
		next.ServeHTTP(rec, r)
	})
}
