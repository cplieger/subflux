package anidb

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

// roundTripFunc adapts a function to http.RoundTripper so a test can drive the
// mapper's HTTP client without a real network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// gzipped is what AniDB actually puts on the wire: the live API answers the
// httpapi endpoint gzip-compressed even for a 75-byte error envelope, so the
// check has to inflate before it can read a code.
func gzipped(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.String()
}

// credMapper builds a mapper whose transport answers one canned response.
func credMapper(clientKey string, status int, body string, transportErr error) *Mapper {
	m := NewMapper(clientKey)
	m.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		if transportErr != nil {
			return nil, transportErr
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	return m
}

// TestCheckClientKey pins the three outcomes the connection test renders
// differently. AniDB answers HTTP 200 for every one of them and puts the verdict
// in an <error> element, so the arm that matters is the code: 302 is the client
// key, and a ban is a wait rather than a field to fix.
func TestCheckClientKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		clientKey    string
		body         string
		transportErr error
		status       int
		wantRefused  bool
		wantErr      bool
	}{
		{
			name: "accepted client key", clientKey: "subfluxclient",
			status: http.StatusOK, body: `<main><hotanime></hotanime></main>`,
		},
		{
			name: "refused client key", clientKey: "subfluxclient",
			status:      http.StatusOK,
			body:        `<error code="302">client version missing or invalid</error>`,
			wantRefused: true, wantErr: true,
		},
		{
			name: "a ban is not a verdict about the client key", clientKey: "subfluxclient",
			status: http.StatusOK, body: `<error>Banned</error>`,
			wantErr: true,
		},
		{
			name: "an absent key has nothing to validate", clientKey: "",
			wantRefused: true, wantErr: true,
		},
		{
			name: "unreachable service", clientKey: "subfluxclient",
			transportErr: errors.New("dial tcp: connect: connection refused"),
			wantErr:      true,
		},
		{
			name: "a server failure is not a verdict about the client key", clientKey: "subfluxclient",
			status: http.StatusBadGateway, body: "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := credMapper(tt.clientKey, tt.status, tt.body, tt.transportErr)

			err := m.CheckClientKey(t.Context())

			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckClientKey() error = %v, want an error: %t", err, tt.wantErr)
			}
			_, refused := errors.AsType[*subflux.AuthError](err)
			if refused != tt.wantRefused {
				t.Errorf("CheckClientKey() error %v is *subflux.AuthError = %t, want %t",
					err, refused, tt.wantRefused)
			}
		})
	}
}

// AniDB serves the httpapi gzip-compressed, so a check that read the raw bytes
// would find no <error> element and report every refused key as accepted.
func TestCheckClientKey_reads_a_gzipped_refusal(t *testing.T) {
	t.Parallel()
	m := credMapper("subfluxclient", http.StatusOK,
		gzipped(t, `<error code="302">client version missing or invalid</error>`), nil)

	err := m.CheckClientKey(t.Context())

	if _, refused := errors.AsType[*subflux.AuthError](err); !refused {
		t.Errorf("CheckClientKey() error = %v, want a *subflux.AuthError", err)
	}
}

// The check asks the parameterless `main` request. `request=anime` would need an
// anime id and would download that anime's whole episode list to answer a
// question about a key, against an API that bans for volume.
func TestCheckClientKey_asks_the_parameterless_request(t *testing.T) {
	t.Parallel()
	var got *http.Request
	m := NewMapper("subfluxclient")
	m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`<main></main>`)),
			Header:     make(http.Header),
		}, nil
	})}

	if err := m.CheckClientKey(t.Context()); err != nil {
		t.Fatalf("CheckClientKey() = %v, want nil", err)
	}
	if got == nil {
		// Establishes the value every check below reads.
		t.Fatal("CheckClientKey() issued no request")
	}
	q := got.URL.Query()
	if q.Get("request") != "main" || q.Get("aid") != "" {
		t.Errorf("CheckClientKey() query = %v, want request=main and no aid", q)
	}
	if q.Get("client") != "subfluxclient" {
		t.Errorf("CheckClientKey() sent client %q, want %q", q.Get("client"), "subfluxclient")
	}
}
