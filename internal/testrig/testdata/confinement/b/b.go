// Package b is the confined package.
package b

import "net/http"

const Suffix = "/x"

// NewRequest builds a request, which the HTTP check allows.
func NewRequest(url string) (*http.Request, error) {
	return http.NewRequest(http.MethodGet, url+Suffix, nil)
}
