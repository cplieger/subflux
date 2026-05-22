// http_errors.go contains typed HTTP error response helpers.
// Each helper maps a single HTTP status code to a JSON error envelope.
// This file changes only when a new status code needs a helper.
// Core response infrastructure lives in http.go.

package api

import (
	"log/slog"
	"net/http"
)

// --- Typed-code variants ---
//
// These accept *http.Request so the request id flows into the response
// envelope automatically. Pass an empty string for code when no
// machine-readable code applies; the wire format then matches the
// legacy (w, msg) variants exactly.

// BadRequestC writes a 400 with the given code and message.
func BadRequestC(w http.ResponseWriter, r *http.Request, code, msg string) {
	writeError(w, r, http.StatusBadRequest, code, msg)
}

// UnauthorizedC writes a 401 with the given code and message.
func UnauthorizedC(w http.ResponseWriter, r *http.Request, code, msg string) {
	writeError(w, r, http.StatusUnauthorized, code, msg)
}

// ForbiddenC writes a 403 with the given code and message.
func ForbiddenC(w http.ResponseWriter, r *http.Request, code, msg string) {
	writeError(w, r, http.StatusForbidden, code, msg)
}

// NotFoundC writes a 404 with the given code and message.
func NotFoundC(w http.ResponseWriter, r *http.Request, code, msg string) {
	writeError(w, r, http.StatusNotFound, code, msg)
}

// MethodNotAllowedC writes a 405 with the given code.
func MethodNotAllowedC(w http.ResponseWriter, r *http.Request, code string) {
	writeError(w, r, http.StatusMethodNotAllowed, code, "method not allowed")
}

// ConflictC writes a 409 with the given code and message.
func ConflictC(w http.ResponseWriter, r *http.Request, code, msg string) {
	writeError(w, r, http.StatusConflict, code, msg)
}

// PayloadTooLargeC writes a 413 with the given code and message.
func PayloadTooLargeC(w http.ResponseWriter, r *http.Request, code, msg string) {
	writeError(w, r, http.StatusRequestEntityTooLarge, code, msg)
}

// TooManyRequestsC writes a 429 with the given code and message. The caller
// is responsible for setting the Retry-After header when appropriate.
func TooManyRequestsC(w http.ResponseWriter, r *http.Request, code, msg string) {
	writeError(w, r, http.StatusTooManyRequests, code, msg)
}

// BadGatewayC writes a 502 with the given code and message.
func BadGatewayC(w http.ResponseWriter, r *http.Request, code, msg string) {
	writeError(w, r, http.StatusBadGateway, code, msg)
}

// ServiceUnavailableC writes a 503 with the given code and message.
func ServiceUnavailableC(w http.ResponseWriter, r *http.Request, code, msg string) {
	writeError(w, r, http.StatusServiceUnavailable, code, msg)
}

// InternalErrorC writes a 500 with the given code and a generic message,
// and logs the raw error at ERROR level. The raw error is never echoed
// to the client to avoid leaking internal details.
func InternalErrorC(w http.ResponseWriter, r *http.Request, err error, code string, logAttrs ...any) {
	if err != nil {
		attrs := append([]any{keyError, err}, logAttrs...)
		slog.Error(msgInternalError, attrs...)
	}
	writeError(w, r, http.StatusInternalServerError, code, msgInternalError)
}
