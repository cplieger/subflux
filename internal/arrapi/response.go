package arrapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cplieger/subflux/internal/httputil"
)

// readErrorBody reads and returns the error body from a non-OK response,
// capped at httputil.MaxErrorBodyBytes. The response body is closed after reading.
func readErrorBody(resp *http.Response) string {
	body, err := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxErrorBodyBytes))
	resp.Body.Close()
	if err != nil {
		return ""
	}
	return string(body)
}

// classifyResponse checks the HTTP status code and returns an error for
// non-OK responses. On success (200), it returns nil and the caller is
// responsible for reading and closing the body.
func classifyResponse(resp *http.Response, path string) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body := readErrorBody(resp)
	return newStatusError(resp.StatusCode, path, body)
}

// decodeJSON reads and decodes a JSON response body into dst, capped at
// maxBytes. This is a pure function that operates on an already-validated
// (status 200) response.
func decodeJSON(body io.ReadCloser, maxBytes int64, dst any, label string) error {
	defer body.Close()
	if err := json.NewDecoder(io.LimitReader(body, maxBytes)).Decode(dst); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	return nil
}

// decodeJSONSlice reads and decodes a JSON array response body into a slice,
// using Content-Length as a pre-allocation hint.
func decodeJSONSlice[T any](resp *http.Response, maxBytes int64, label string) ([]T, error) {
	defer resp.Body.Close()
	var items []T
	if cl := resp.ContentLength; cl > 0 {
		hint := int(cl) / fetchAllAvgItemSize
		if hint > 0 {
			items = make([]T, 0, hint)
		}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBytes)).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	return items, nil
}
