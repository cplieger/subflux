// http.go provides HTTP response helpers shared across every server handler.
// Consistent shape: all JSON responses go through WriteJSON/WriteJSONStatus,
// which set Content-Type automatically. All error responses go through the
// named helpers below.
//
// Wire format for errors:
//
//	{"error": "human message", "code": "machine_readable", "request_id": "..."}
//
// `error` is the historical field; existing clients that read only
// `error` continue to work unchanged. `code` and `request_id` are
// additive: callers that pass a code via the (w, r, code, msg)
// variants populate the field. `request_id` is auto-populated from
// r.Context() when the request was wrapped by the request-ID middleware.
//
// Hand-crafted JSON strings (`http.Error(w, ..., status)` for JSON,
// `fmt.Fprint(w, \`{"ok":true}\`)`, `w.Write([]byte(...))` for JSON) are
// forbidden. Use these helpers instead.
//
// Pattern mirrors apps/vibekit/web/internal/api/http.go and
// apps/vibecli/internal/api/middleware.go.

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Canonical JSON response keys and sentinel error messages shared
// across every helper below. Extracted to silence goconst warnings
// and to make a wire-format rename land in one place.
const (
	keyError         = "error"
	msgInternalError = "internal error"

	// contentTypeJSON is the MIME type for JSON responses. Matches
	// httputil.ContentTypeJSON but defined locally to avoid a circular import.
	contentTypeJSON = "application/json"

	// KeyStatus is the canonical JSON key for operation result status responses.
	// Used by server, scanning, and confighandlers packages.
	KeyStatus = "status"

	// ErrMsgSonarrNotConfigured is the canonical error message when Sonarr is nil.
	ErrMsgSonarrNotConfigured = "sonarr not configured"

	// ErrMsgRadarrNotConfigured is the canonical error message when Radarr is nil.
	ErrMsgRadarrNotConfigured = "radarr not configured"

	// HeaderXAPIKey is the canonical HTTP header name for API key authentication.
	// Used by both inbound (subflux API key verification) and outbound (Sonarr/Radarr) requests.
	HeaderXAPIKey = "X-Api-Key" //nolint:gosec // G101 false positive: header name, not a credential

	// QueryParamAPIKey is the canonical query parameter name for API key authentication.
	QueryParamAPIKey = "api_key"

	// HeaderXRequestID is the canonical X-Request-ID header. The middleware
	// reuses an inbound value when shape-valid; otherwise mints a fresh
	// 16-byte hex id and attaches it to the request context.
	HeaderXRequestID = "X-Request-ID"
)

// errorResponse is the canonical JSON error envelope for all HTTP error helpers.
// Wire format: {"error": msg, "code": ..., "request_id": ...}.
//
// `error` is always populated. `code` and `request_id` are emitted only
// when set (omitempty); legacy callers that don't pass a code or that
// run without the request-ID middleware emit only `error`, preserving
// existing wire compatibility.
type errorResponse struct {
	Error     string `json:"error"`
	Code      string `json:"code,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// jsonHeaders sets the standard JSON response headers.
func jsonHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// WriteJSON encodes v as JSON with status 200.
func WriteJSON(w http.ResponseWriter, v any) {
	WriteJSONStatus(w, http.StatusOK, v)
}

// WriteJSONStatus encodes v as JSON with the given status code.
func WriteJSONStatus(w http.ResponseWriter, code int, v any) {
	jsonHeaders(w)
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("writeJSON encode failed", "code", code, "error", err)
	}
}

// JSONError writes {"error": err.Error()} at the given status code.
//
// SECURITY: err.Error() is echoed verbatim to the client. Only use
// when the error text is author-controlled or explicitly user-safe
// (e.g. errors.New("short, static message")). For wrapped internal
// errors, use InternalError(w, err, ...) which logs the raw error and
// returns a generic message. For user-facing validation failures,
// prefer BadRequest(w, r, code, msg) with an author-controlled message.
func JSONError(w http.ResponseWriter, err error, code int) {
	WriteJSONStatus(w, code, errorResponse{Error: err.Error()})
}

// JSONErrorWithCode is JSONError + an error-code envelope. Use when the
// error string is safe to surface verbatim AND a stable machine-
// readable code is meaningful for the client.
func JSONErrorWithCode(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	WriteJSONStatus(w, status, errorResponse{
		Error:     msg,
		Code:      code,
		RequestID: requestIDFrom(r),
	})
}

// Ok writes a 200 {"ok": true} response — the standard
// "action succeeded" reply for endpoints that don't return data.
func Ok(w http.ResponseWriter) {
	WriteJSON(w, map[string]bool{"ok": true})
}

// requestIDFrom extracts the request id from r's context, if r is non-nil.
// Tolerates a nil request so legacy (w, msg) helpers work without a request.
func requestIDFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	return RequestIDFromContext(r.Context())
}

// writeError is the single place that builds an errorResponse and emits it.
// All public helpers funnel through this function.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	WriteJSONStatus(w, status, errorResponse{
		Error:     msg,
		Code:      code,
		RequestID: requestIDFrom(r),
	})
}
