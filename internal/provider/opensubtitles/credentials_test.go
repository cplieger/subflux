package opensubtitles

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

// credProvider builds a provider whose transport answers one canned response.
// The rate-limit token is pre-filled the way the factory fills it, so the login
// the check performs is not waiting on a bucket the test never refills.
func credProvider(status int, body string, transportErr error) *Provider {
	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{}
	return &Provider{
		username: "user",
		password: "pass",
		apiKey:   "key",
		rateCh:   rateCh,
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if transportErr != nil {
				return nil, transportErr
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})},
	}
}

// TestCheckCredentials pins the three outcomes the connection test renders
// differently. The two refusal codes are the two halves of the credential set:
// the API gateway answers 403 when the consumer's key may not use the service,
// and the endpoint answers 401 when the username and password are wrong, which
// is why one /login call is what validates all three fields.
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
			name:   "accepted credentials",
			status: http.StatusOK, body: `{"token":"tok","user":{"vip":false}}`,
		},
		{
			name:   "refused username or password",
			status: http.StatusUnauthorized, body: `{"message":"You must be logged in"}`,
			wantRefused: true, wantErr: true,
		},
		{
			name:   "refused api key",
			status: http.StatusForbidden, body: `{"message":"You cannot consume this service"}`,
			wantRefused: true, wantErr: true,
		},
		{
			name:         "unreachable service",
			transportErr: errors.New("dial tcp: connect: connection refused"),
			wantErr:      true,
		},
		{
			name:   "a server failure is not a verdict about the credentials",
			status: http.StatusBadGateway, body: "",
			wantErr: true,
		},
		{
			name:   "a token-less success is not an acceptance",
			status: http.StatusOK, body: `{"token":""}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := credProvider(tt.status, tt.body, tt.transportErr).CheckCredentials(t.Context())

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

// The check is one POST to /login and nothing else: a search would spend the
// account's request rate and could answer "no results" for credentials that are
// perfectly correct.
func TestCheckCredentials_posts_to_login_only(t *testing.T) {
	t.Parallel()
	var requests []string
	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{}
	p := &Provider{
		username: "user", password: "pass", apiKey: "key", rateCh: rateCh,
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r.Method+" "+r.URL.String())
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"token":"tok"}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	if err := p.CheckCredentials(t.Context()); err != nil {
		t.Fatalf("CheckCredentials() = %v, want nil", err)
	}
	want := []string{http.MethodPost + " " + baseURL + "/login"}
	if len(requests) != 1 || requests[0] != want[0] {
		t.Errorf("CheckCredentials() issued %v, want %v", requests, want)
	}
}
