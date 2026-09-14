package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// stampHeader carries the JSON events.Stamp of the subject a GET serves.
const stampHeader = "Subject-Stamp"

// stamp returns the middleware that labels a GET's response with the
// version of the subject it serves. The counter is read BEFORE next runs,
// so a bump racing the handler's body labels the payload with the older
// version: one spurious changed on the next digest, never a false unchanged.
// ref derives the subject ref from the request, nil for a ref-less kind; a
// request naming no registered subject is served unstamped, and a handler
// that then answers 404 still carries the stamp, which the client ignores
// along with the body.
func (s *Server) stamp(kind string, ref func(*http.Request) string) middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var sub string
			if ref != nil {
				sub = ref(r)
			}
			if st, ok := s.events.Versions().Stamp(kind, sub); ok {
				if b, err := json.Marshal(st); err != nil {
					slog.Warn("SSE: failed to marshal stamp", "kind", kind, "ref", sub, "error", err)
				} else {
					w.Header().Set(stampHeader, string(b))
				}
			}
			next(w, r)
		}
	}
}

// seriesDetailRef is the detail prefix route's tail as a tvdb root; a tail
// that is not a positive integer names no subject.
func seriesDetailRef(r *http.Request) string {
	return "tvdb-" + strings.TrimPrefix(r.URL.Path, "/api/coverage/series/")
}

// movieDetailRef is the movie subs route's {tmdbId} as a tmdb root.
func movieDetailRef(r *http.Request) string {
	return "tmdb-" + r.PathValue("tmdbId")
}
