package betaseries

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

// bodyRoundTripper answers every request with one canned status and body, or
// fails the dial. statusRoundTripper next door answers a status only, and the
// credential arms turn on the error CODE inside a 400 body.
type bodyRoundTripper struct {
	err    error
	body   string
	status int
}

func (rt bodyRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	if rt.err != nil {
		return nil, rt.err
	}
	return &http.Response{
		StatusCode: rt.status,
		Body:       io.NopCloser(strings.NewReader(rt.body)),
		Header:     make(http.Header),
	}, nil
}

// TestCheckCredentials pins the three outcomes the connection test renders
// differently. BetaSeries answers HTTP 400 for both a refused key and a
// not-found lookup, so the arm that matters is the error code: 1001 is the key,
// anything else is not a verdict about it.
func TestCheckCredentials(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		body         string
		transportErr error
		status       int
		wantRefused  bool
		wantErr      bool
	}{
		{
			name:   "accepted token",
			status: http.StatusOK, body: `{"errors":[]}`,
		},
		{
			name:   "refused token",
			status: http.StatusBadRequest, body: `{"errors":[{"code":1001,"text":"Please set an API key."}]}`,
			wantRefused: true, wantErr: true,
		},
		{
			name:   "another error code is not a verdict about the token",
			status: http.StatusBadRequest, body: `{"errors":[{"code":2001,"text":"nope"}]}`,
			wantErr: true,
		},
		{
			name:         "unreachable service",
			transportErr: errors.New("dial tcp: connect: connection refused"),
			wantErr:      true,
		},
		{
			name:   "a server failure is not a verdict about the token",
			status: http.StatusBadGateway, body: "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Provider{
				token: "tok",
				client: &http.Client{Transport: bodyRoundTripper{
					status: tt.status, body: tt.body, err: tt.transportErr,
				}},
			}

			err := p.CheckCredentials(t.Context())

			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckCredentials() error = %v, want an error: %t", err, tt.wantErr)
			}
			_, refused := errors.AsType[*subflux.AuthError](err)
			if refused != tt.wantRefused {
				t.Errorf("CheckCredentials() error %v is *subflux.AuthError = %t, want %t",
					err, refused, tt.wantRefused)
			}
		})
	}
}

// The check asks the parameterless status route, carrying the token in the
// header the rest of the provider uses. Anything under /shows or /subtitles
// would make BetaSeries look something up to answer a question about a key.
func TestCheckCredentials_asks_the_status_route(t *testing.T) {
	t.Parallel()
	var gotURL, gotKey string
	p := &Provider{
		token: "tok",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotURL = r.URL.String()
			gotKey = r.Header.Get("X-BetaSeries-Key")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	if err := p.CheckCredentials(t.Context()); err != nil {
		t.Fatalf("CheckCredentials() = %v, want nil", err)
	}
	// Hardcoded rather than compared against statusURL: reading the constant on
	// both sides of the check makes it satisfied by any value the constant takes.
	const want = "https://api.betaseries.com/status"
	if gotURL != want {
		t.Errorf("CheckCredentials() requested %q, want %q", gotURL, want)
	}
	if gotKey != "tok" {
		t.Errorf("CheckCredentials() sent X-BetaSeries-Key %q, want %q", gotKey, "tok")
	}
}

// roundTripFunc adapts a function to http.RoundTripper so a test can answer,
// or read, a request without a network dial.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
