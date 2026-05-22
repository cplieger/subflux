// Package httphelpers provides shared HTTP handler prelude helpers used
// across server sub-packages. This breaks the import cycle between server/
// and its sub-packages (queryhandlers, confighandlers, etc.) that previously
// required copy-pasting these helpers.
package httphelpers

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"subflux/internal/api"
)

// MaxDefaultBodySize is the default JSON body size cap for most handlers (1 MiB).
const MaxDefaultBodySize = 1 << 20

// RequireMethod checks that the request method matches the expected
// value. Writes 405 and returns false if not.
func RequireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return false
	}
	return true
}

// RequireGET is sugar for RequireMethod(w, r, http.MethodGet).
func RequireGET(w http.ResponseWriter, r *http.Request) bool {
	return RequireMethod(w, r, http.MethodGet)
}

// RequirePOST is sugar for RequireMethod(w, r, http.MethodPost).
func RequirePOST(w http.ResponseWriter, r *http.Request) bool {
	return RequireMethod(w, r, http.MethodPost)
}

// RequireDELETE is sugar for RequireMethod(w, r, http.MethodDelete).
func RequireDELETE(w http.ResponseWriter, r *http.Request) bool {
	return RequireMethod(w, r, http.MethodDelete)
}

// DecodeJSONBody decodes a JSON request body into dst with a size cap.
// Writes 400 and returns false on decode failure.
func DecodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	if maxBytes <= 0 {
		maxBytes = MaxDefaultBodySize
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBytes))
	if err := dec.Decode(dst); err != nil {
		slog.Debug("decode request body failed", "path", r.URL.Path, "error", err)
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid json")
		return false
	}
	return true
}

// ParseMediaTypeParam extracts and validates the "type" query parameter as an
// api.MediaType. Defaults to api.MediaTypeEpisode when empty. Writes 400 and
// returns ("", false) if the value is invalid.
func ParseMediaTypeParam(w http.ResponseWriter, r *http.Request) (api.MediaType, bool) {
	raw := r.URL.Query().Get("type")
	if raw == "" {
		return api.MediaTypeEpisode, true
	}
	mt := api.MediaType(raw)
	if !mt.Valid() {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid type parameter")
		return "", false
	}
	return mt, true
}
