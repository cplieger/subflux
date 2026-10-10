package subdl

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

const credAPIKey = "subdl-test-key"

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
				Body:       io.NopCloser(strings.NewReader(`{"status":true}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}
}

// TestCheckCredentials pins the three outcomes the connection test renders
// differently. /me is a documented account endpoint, so only 200 is an
// acceptance: a 404 would mean the endpoint moved, which says nothing about the
// key.
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
			name: "refused key", status: http.StatusForbidden,
			wantRefused: true, wantErr: true,
		},
		{
			name: "unauthorized key", status: http.StatusUnauthorized,
			wantRefused: true, wantErr: true,
		},
		{
			name: "a moved endpoint is not a verdict about the key", status: http.StatusNotFound,
			wantErr: true,
		},
		{
			name: "a server failure is not a verdict about the key", status: http.StatusBadGateway,
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

// The check asks the account endpoint, not the subtitle index: a search would
// spend a request from the operator's daily quota to answer a question about a
// key.
func TestCheckCredentials_asks_the_account_endpoint(t *testing.T) {
	t.Parallel()
	var got *http.Request
	p := &source{
		apiKey: credAPIKey,
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			got = r
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":true}`)),
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
	if got.URL.Path != "/api/v1/me" {
		t.Errorf("CheckCredentials() requested path %q, want %q", got.URL.Path, "/api/v1/me")
	}
	if key := got.URL.Query().Get("api_key"); key != credAPIKey {
		t.Errorf("CheckCredentials() sent api_key %q, want %q", key, credAPIKey)
	}
}

// The api_key rides in the query string, so a transport failure wraps it inside
// a *url.Error. Every other subdl path redacts it for that reason.
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
