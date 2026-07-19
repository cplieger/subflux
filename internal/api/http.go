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
// r.Context() when the request was wrapped by webhttp.Logging.
//
// The JSON/error funnel is layered on the webhttp library
// (github.com/cplieger/webhttp): jsonHeaders/WriteJSON/WriteJSONStatus/Ok and
// the writeError envelope delegate to webhttp's mechanism, so the wire shape is
// defined once in the library and stays byte-identical across every consumer.
// subflux keeps its ~70-code taxonomy and the BadRequestC-style named helpers
// on top; they just delegate now.
//
// Hand-crafted JSON strings (`http.Error(w, ..., status)` for JSON,
// `fmt.Fprint(w, \`{"ok":true}\`)`, `w.Write([]byte(...))` for JSON) are
// forbidden. Use these helpers instead.
//
// Pattern mirrors apps/vibekit/web/internal/api/http.go and
// apps/web-terminal-kiro/internal/api/middleware.go.

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/cplieger/webhttp"
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

	// KeyStatus is the canonical JSON key for operation result status
	// responses (StatusResponse is the typed carrier).
	// Used by server, scanning, and confighandlers packages.
	KeyStatus = "status"

	// ErrMsgSonarrNotConfigured is the canonical error message when Sonarr is nil.
	ErrMsgSonarrNotConfigured = "sonarr not configured"

	// ErrMsgRadarrNotConfigured is the canonical error message when Radarr is nil.
	ErrMsgRadarrNotConfigured = "radarr not configured"

	// HeaderXAPIKey is the canonical HTTP header name for API key authentication.
	// Used by both inbound (subflux API key verification) and outbound (Sonarr/Radarr) requests.
	HeaderXAPIKey = "X-Api-Key" //nolint:gosec // G101 false positive: header name, not a credential
)

// errorResponse is the canonical JSON error envelope for all HTTP error helpers.
// It aliases webhttp.ErrorResponse so the wire format ({"error": msg, "code":
// ..., "request_id": ...}) is defined once, in the library, and stays
// byte-identical across every consumer. `error` is always populated; `code` and
// `request_id` are emitted only when set (omitempty), preserving the legacy
// error-only shape for callers that pass no code and run without the request-id
// middleware.
type errorResponse = webhttp.ErrorResponse

// jsonHeaders sets the standard JSON response headers (application/json +
// nosniff) via webhttp.JSONHeaders, so the header set matches every other
// webhttp consumer.
func jsonHeaders(w http.ResponseWriter) {
	webhttp.JSONHeaders(w)
}

// WriteJSON encodes v as JSON with status 200.
func WriteJSON(w http.ResponseWriter, v any) {
	WriteJSONStatus(w, http.StatusOK, v)
}

// WriteJSONStatus encodes v as JSON with the given status code. The encode
// failure is logged at Debug (deliberately quiet: the status line is already on
// the wire and an app JSON payload that fails to encode is exceptional) rather
// than delegating to webhttp.WriteJSONStatus, which logs at Warn.
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
// readable code is meaningful for the client. Delegates to webhttp.WriteError
// so the envelope and its request-id population match writeError.
func JSONErrorWithCode(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, msg string) {
	webhttp.WriteError(w, r, status, string(code), msg)
}

// Ok writes a 200 {"ok": true} response — the standard "action succeeded" reply
// for endpoints that don't return data. Delegates to webhttp.Ok so the body
// matches the {"ok":true} every webhttp consumer emits.
func Ok(w http.ResponseWriter) {
	webhttp.Ok(w)
}

// StatusResponse is the canonical {"status": "..."} operation-result body
// used by action endpoints (scan triggers, resets, logout).
type StatusResponse struct {
	Status string `json:"status"`
}

// writeError is the single place all named error helpers funnel through. It
// delegates to webhttp.WriteError, which builds the {error,code,request_id}
// envelope and pulls the request id from r's context (nil-safe on r). The wire
// shape is identical to the previous hand-built errorResponse.
func writeError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, msg string) {
	webhttp.WriteError(w, r, status, string(code), msg)
}
