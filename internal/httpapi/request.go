// request.go provides the HTTP handler prelude helpers every server handler
// opens with: the method gate and the JSON body decoder. They sit beside the
// response helpers in response.go and errors.go because the prelude writes the
// same {error,code,request_id} envelope those helpers define.
//
// The method gate and body decoder delegate to the webhttp prelude primitives
// (RequireMethod, LimitBody, MaxJSONBody) so subflux inherits the RFC-9110 Allow
// header on 405s and the MaxBytesReader overflow-to-400 behavior, while keeping
// subflux's own error-code taxonomy for the response envelope.

package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/webhttp/v2"
)

// MaxDefaultBodySize is the default JSON body size cap for most handlers (1 MiB),
// aliased to webhttp.MaxJSONBody so the limit has a single definition.
const MaxDefaultBodySize = webhttp.MaxJSONBody

// requireMethod checks that the request method matches the expected value. On a
// mismatch it delegates to webhttp.RequireMethod, which sets the RFC-9110 Allow
// header to the permitted method and writes subflux's {error,code,request_id}
// 405 envelope (code "method_not_allowed"), then returns false.
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	return webhttp.RequireMethod(w, r, method)
}

// RequireGET is sugar for requireMethod(w, r, http.MethodGet).
func RequireGET(w http.ResponseWriter, r *http.Request) bool {
	return requireMethod(w, r, http.MethodGet)
}

// RequirePOST is sugar for requireMethod(w, r, http.MethodPost).
func RequirePOST(w http.ResponseWriter, r *http.Request) bool {
	return requireMethod(w, r, http.MethodPost)
}

// DecodeJSONBody decodes a JSON request body into dst under a size cap, via
// webhttp.DecodeJSONInto (http.MaxBytesReader cap + single-value decode +
// trailing-data rejection): a body exceeding maxBytes, malformed JSON, or data
// after the first value fails the decode rather than being silently accepted. A
// maxBytes of 0 or less uses MaxDefaultBodySize. Writes subflux's 400 envelope
// (code "bad_request") and returns false on any decode failure.
func DecodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	if maxBytes <= 0 {
		maxBytes = MaxDefaultBodySize
	}
	if err := webhttp.DecodeJSONInto(w, r, dst, maxBytes); err != nil {
		slog.Debug("decode request body failed", "path", r.URL.Path, "error", err)
		BadRequestC(w, r, subflux.CodeBadRequest, "invalid json")
		return false
	}
	return true
}
