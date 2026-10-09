package hdbits

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

// stubbedProvider builds a provider whose transport answers one canned
// response, so the check runs its real request-building and classification
// without a network dial.
func stubbedProvider(status int, body string, transportErr error) *source {
	return &source{
		username: "user",
		passkey:  "supersecret32hex",
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
// differently: accepted, refused, and a check that did not complete. HDBits
// answers HTTP 200 for all of them and puts the verdict in a `status` field, so
// every arm here is the same status code and a different body — which is exactly
// why reading the HTTP status alone would report a wrong passkey as working.
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
			status: http.StatusOK, body: `{"status":0,"data":[]}`,
		},
		{
			name:   "refused passkey",
			status: http.StatusOK, body: `{"status":5,"message":"Invalid authentication credentials"}`,
			wantRefused: true, wantErr: true,
		},
		{
			name:   "missing auth data is also a refusal",
			status: http.StatusOK, body: `{"status":4,"message":"auth data missing"}`,
			wantRefused: true, wantErr: true,
		},
		{
			name:   "another api status is not a verdict about the credentials",
			status: http.StatusOK, body: `{"status":7,"message":"invalid parameter"}`,
			wantErr: true,
		},
		{
			name:         "unreachable service",
			transportErr: errors.New("dial tcp 1.2.3.4:443: connect: connection refused"),
			wantErr:      true,
		},
		{
			name:   "an http failure is not a verdict about the credentials",
			status: http.StatusBadGateway, body: "",
			wantErr: true,
		},
		{
			name:   "an unreadable body is not a verdict about the credentials",
			status: http.StatusOK, body: "not json",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := stubbedProvider(tt.status, tt.body, tt.transportErr).CheckCredentials(t.Context())

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

// The check posts the pair to HDBits' own validation endpoint, and to nothing
// else: a probe that searched torrents instead would cost the operator a real
// query per press and would answer for the search parameters as much as for the
// credentials.
func TestCheckCredentials_posts_the_pair_to_the_test_endpoint(t *testing.T) {
	t.Parallel()
	var gotMethod, gotURL, gotBody string
	p := &source{
		username: "user",
		passkey:  "supersecret32hex",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotMethod, gotURL = r.Method, r.URL.String()
			raw, _ := io.ReadAll(r.Body)
			gotBody = string(raw)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	if err := p.CheckCredentials(t.Context()); err != nil {
		t.Fatalf("CheckCredentials() = %v, want nil", err)
	}
	if gotMethod != http.MethodPost || gotURL != testURL {
		t.Errorf("CheckCredentials() requested %s %s, want POST %s", gotMethod, gotURL, testURL)
	}
	for _, want := range []string{`"username":"user"`, `"passkey":"supersecret32hex"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("CheckCredentials() body = %s, want it to contain %s", gotBody, want)
		}
	}
}

// A refusal's message is upstream text answering a request that carried the
// passkey, so it is redacted, including a spelling the single-line
// normalization turns back into the passkey.
func TestCheckCredentials_an_echoed_passkey_reaches_no_error(t *testing.T) {
	t.Parallel()
	p := stubbedProvider(http.StatusOK, `{"status":5,"message":"bad pair placeholder pass, placeholder\npass"}`, nil)
	p.passkey = "placeholder pass"
	err := p.CheckCredentials(t.Context())
	if _, refused := errors.AsType[*subflux.AuthError](err); !refused {
		t.Fatalf("CheckCredentials() = %v, want *subflux.AuthError", err)
	}
	if strings.Contains(err.Error(), "placeholder") || strings.Contains(err.Error(), "\n") {
		t.Errorf("CheckCredentials() = %q, want one line with no passkey", err.Error())
	}
}

// A transport failure wraps the request URL, and every hdbits error path
// redacts the passkey for that reason. The check is the newest path to the same
// client, so it needs the same redaction.
func TestCheckCredentials_redacts_the_passkey_from_a_transport_error(t *testing.T) {
	t.Parallel()
	const passkey = "supersecret32hex"
	err := stubbedProvider(0, "", errors.New("dial tcp: i/o timeout to "+passkey)).
		CheckCredentials(t.Context())
	if err == nil {
		t.Fatal("CheckCredentials() with a failing transport = nil, want an error")
	}
	if strings.Contains(err.Error(), passkey) {
		t.Errorf("CheckCredentials() leaked the passkey: %q", err.Error())
	}
}
