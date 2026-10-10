package subsource

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

// roundTripFunc adapts a function to http.RoundTripper so a test can drive the
// provider's HTTP client without a real network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const credAPIKey = "ss-test-key"

// credProvider builds a provider whose transport answers one canned response.
func credProvider(status int, transportErr error) *source {
	return &source{
		apiKey: credAPIKey,
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if transportErr != nil {
				return nil, transportErr
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}
}

// TestCheckCredentials pins the three outcomes the connection test renders
// differently. SubSource checks the key ahead of routing, so the verdict is the
// status: 401 and 403 are the gate refusing it, a 4xx from the router means the
// gate already let it through, and only a 5xx leaves the question unanswered.
func TestCheckCredentials(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		transportErr error
		status       int
		wantRefused  bool
		wantErr      bool
	}{
		{name: "accepted key", status: http.StatusOK},
		{
			name: "refused key", status: http.StatusUnauthorized,
			wantRefused: true, wantErr: true,
		},
		{
			name: "forbidden key", status: http.StatusForbidden,
			wantRefused: true, wantErr: true,
		},
		{
			name: "a router refusal means the key was accepted",
			// The gate answers 401 for an unknown path too, so any other 4xx
			// is the router's verdict on the request rather than the gate's on
			// the key.
			status: http.StatusBadRequest,
		},
		{
			name: "a server failure answers neither way", status: http.StatusBadGateway,
			wantErr: true,
		},
		{
			name:         "unreachable service",
			transportErr: errors.New("dial tcp: connect: connection refused"),
			wantErr:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := credProvider(tt.status, tt.transportErr).CheckCredentials(t.Context())

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

// The probe is an indexed miss on the title lookup, not a subtitle search: the
// id matches nothing, so SubSource answers without assembling a result set.
func TestCheckCredentials_asks_for_an_id_that_matches_nothing(t *testing.T) {
	t.Parallel()
	var got *http.Request
	p := &source{
		apiKey: credAPIKey,
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			got = r
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	if err := p.CheckCredentials(t.Context()); err != nil {
		t.Fatalf("CheckCredentials() = %v, want nil", err)
	}
	if got == nil {
		// Establishes the value every check below reads.
		t.Fatal("CheckCredentials() issued no request")
	}
	if got.URL.Path != "/api/v1/movies/search" {
		t.Errorf("CheckCredentials() requested path %q, want %q", got.URL.Path, "/api/v1/movies/search")
	}
	q := got.URL.Query()
	if q.Get("imdb") != unmatchedIMDB || q.Get("searchType") != string(matchedByIMDB) {
		t.Errorf("CheckCredentials() query = %v, want searchType=%s and imdb=%s",
			q, matchedByIMDB, unmatchedIMDB)
	}
	if q.Get(paramAPIKey) != credAPIKey {
		t.Errorf("CheckCredentials() sent api_key %q, want %q", q.Get(paramAPIKey), credAPIKey)
	}
}

// The api_key rides in the query string, so a transport failure wraps it inside
// a *url.Error. Every other subsource path redacts it for that reason.
func TestCheckCredentials_redacts_the_key_from_a_transport_error(t *testing.T) {
	t.Parallel()
	err := credProvider(0, errors.New("i/o timeout")).CheckCredentials(t.Context())
	if err == nil {
		t.Fatal("CheckCredentials() with a failing transport = nil, want an error")
	}
	if strings.Contains(err.Error(), credAPIKey) {
		t.Errorf("CheckCredentials() leaked the api_key: %q", err.Error())
	}
}
