// Package a imports b and builds raw HTTP clients, for the checker tests.
package a

import (
	"net/http"

	"example.com/confinement/b"
)

var client = &http.Client{}
var transport = http.DefaultTransport

func Fetch(url string) (*http.Response, error) {
	_, _ = client, transport
	return http.Get(url + b.Suffix)
}
