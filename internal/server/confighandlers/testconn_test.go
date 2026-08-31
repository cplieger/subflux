package confighandlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/subflux"
)

// testArrSchema is the minimum HandleTestConnection needs: both arr sections with a
// secret api_key, so secretPaths can address the stored key on disk.
func testArrSchema() []subflux.SchemaSection {
	arr := func(key string) subflux.SchemaSection {
		return subflux.SchemaSection{
			Key: key, Type: "fields", ConnTest: true,
			Fields: []subflux.SchemaField{
				{Key: "url"},
				{Key: "api_key", Secret: true},
			},
		}
	}
	return []subflux.SchemaSection{arr("sonarr"), arr("radarr")}
}

// TestHandleTestConnection pins the whole probe: which instance is built and with
// which credentials, that a failed test is a 200 verdict rather than an HTTP
// error, that an omitted key falls back to the one on disk, and that the ping
// is unconditional where the save path's is not.
//
// The constructor arguments are asserted rather than counted for the same
// reason the save-path gate asserts them: a probe that built the radarr client
// for a sonarr request would validate the operator's new sonarr credentials
// against the wrong instance and report a confident green.
func TestHandleTestConnection(t *testing.T) {
	t.Parallel()
	// A stored radarr key the request can omit, and a live config identical to
	// what one case submits (the save path would skip that ping).
	const onDisk = "sonarr:\n  url: \"http://live:8989\"\n  api_key: \"k-live\"\n" +
		"radarr:\n  url: \"http://radarr:7878\"\n  api_key: \"k-stored\"\n"

	tests := []struct {
		name       string
		body       string
		existing   string
		pingErr    error
		newErr     error
		wantStatus int
		wantValid  bool
		wantErrIs  string // substring of the response's error field
		wantSonarr []string
		wantRadarr []string
		wantPings  int
	}{
		{
			name:       "reachable sonarr answers valid",
			body:       `{"kind":"sonarr","url":"http://sonarr:8989","api_key":"k1"}`,
			wantStatus: http.StatusOK,
			wantValid:  true,
			wantSonarr: []string{"http://sonarr:8989|k1"},
			wantPings:  1,
		},
		{
			name:       "reachable radarr builds the radarr client",
			body:       `{"kind":"radarr","url":"http://radarr:7878","api_key":"k2"}`,
			wantStatus: http.StatusOK,
			wantValid:  true,
			wantRadarr: []string{"http://radarr:7878|k2"},
			wantPings:  1,
		},
		{
			name:       "unreachable arr is a 200 verdict carrying the reason",
			body:       `{"kind":"sonarr","url":"http://sonarr:8989","api_key":"k1"}`,
			pingErr:    &arrapi.StatusError{Code: http.StatusUnauthorized},
			wantStatus: http.StatusOK,
			wantErrIs:  "the API key was rejected",
			wantSonarr: []string{"http://sonarr:8989|k1"},
			wantPings:  1,
		},
		{
			name:       "malformed URL is a verdict, not a bad request",
			body:       `{"kind":"sonarr","url":"sonarr:8989","api_key":"k1"}`,
			newErr:     errors.New("baseURL must be an absolute http(s) URL"),
			wantStatus: http.StatusOK,
			wantErrIs:  "absolute http(s) URL",
			wantSonarr: []string{"sonarr:8989|k1"},
			wantPings:  0,
		},
		{
			name:       "omitted key falls back to the one on disk",
			body:       `{"kind":"radarr","url":"http://radarr:7878","api_key":""}`,
			existing:   onDisk,
			wantStatus: http.StatusOK,
			wantValid:  true,
			wantRadarr: []string{"http://radarr:7878|k-stored"},
			wantPings:  1,
		},
		{
			name:       "unchanged credentials still ping",
			body:       `{"kind":"sonarr","url":"http://live:8989","api_key":"k-live"}`,
			existing:   onDisk,
			wantStatus: http.StatusOK,
			wantValid:  true,
			wantSonarr: []string{"http://live:8989|k-live"},
			wantPings:  1,
		},
		{
			name:       "empty URL never builds a client",
			body:       `{"kind":"sonarr","url":"  ","api_key":"k1"}`,
			wantStatus: http.StatusOK,
			wantErrIs:  "URL is required",
			wantPings:  0,
		},
		{
			name:       "omitted key with nothing on disk never builds a client",
			body:       `{"kind":"sonarr","url":"http://sonarr:8989","api_key":""}`,
			wantStatus: http.StatusOK,
			wantErrIs:  "API key is required",
			wantPings:  0,
		},
		{
			name:       "unknown kind is the one bad request",
			body:       `{"kind":"lidarr","url":"http://lidarr:8686","api_key":"k1"}`,
			wantStatus: http.StatusBadRequest,
			wantPings:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			if tt.existing != "" {
				if err := os.WriteFile(configPath, []byte(tt.existing), 0o600); err != nil {
					t.Fatalf("write existing config: %v", err)
				}
			}
			pings := 0
			var gotSonarr, gotRadarr []string
			record := func(dst *[]string) func(string, string) (ArrPinger, error) {
				return func(baseURL, apiKey string) (ArrPinger, error) {
					*dst = append(*dst, baseURL+"|"+apiKey)
					if tt.newErr != nil {
						return nil, tt.newErr
					}
					return recordingPinger{pings: &pings, err: tt.pingErr}, nil
				}
			}
			h := New(&Deps{
				SchemaFunc: func(_ []subflux.ProviderSchema) []subflux.SchemaSection {
					return testArrSchema()
				},
				NewSonarr:  record(&gotSonarr),
				NewRadarr:  record(&gotRadarr),
				ConfigPath: func() string { return configPath },
			})

			rec := doArrTest(t, h, tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("HandleTestConnection(%s) status = %d, want %d; body %s",
					tt.body, rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !slices.Equal(gotSonarr, tt.wantSonarr) {
				t.Errorf("HandleTestConnection(%s) built sonarr client with %v, want %v",
					tt.body, gotSonarr, tt.wantSonarr)
			}
			if !slices.Equal(gotRadarr, tt.wantRadarr) {
				t.Errorf("HandleTestConnection(%s) built radarr client with %v, want %v",
					tt.body, gotRadarr, tt.wantRadarr)
			}
			if pings != tt.wantPings {
				t.Errorf("HandleTestConnection(%s) pinged %d times, want %d", tt.body, pings, tt.wantPings)
			}
			if tt.wantStatus != http.StatusOK {
				return
			}

			var got ConnTestResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				// Establishes the value every check below reads.
				t.Fatalf("HandleTestConnection(%s) response %s: %v", tt.body, rec.Body.String(), err)
			}
			if got.Valid != tt.wantValid {
				t.Errorf("HandleTestConnection(%s) valid = %t, want %t (error %q)",
					tt.body, got.Valid, tt.wantValid, got.Error)
			}
			if tt.wantErrIs == "" {
				if got.Error != "" {
					t.Errorf("HandleTestConnection(%s) error = %q, want empty", tt.body, got.Error)
				}
			} else if !strings.Contains(got.Error, tt.wantErrIs) {
				t.Errorf("HandleTestConnection(%s) error = %q, want it to contain %q",
					tt.body, got.Error, tt.wantErrIs)
			}
		})
	}
}

// TestHandleTestConnection_rejects_non_post pins the method gate: the probe reads the
// config file and dials an operator-supplied host, so it must not be reachable
// by a GET a browser or crawler could trigger from a URL alone.
func TestHandleTestConnection_rejects_non_post(t *testing.T) {
	t.Parallel()
	h := New(&Deps{ConfigPath: func() string { return filepath.Join(t.TempDir(), "config.yaml") }})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config/test-connection", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleTestConnection(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleTestConnection(GET) status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

// TestStoredArrAPIKey covers the fallback's read failures, each of which must
// answer "" so the handler reports the missing key as the operator-facing
// answer it is rather than a 500 about the server's own file.
func TestStoredArrAPIKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		existing string // "" writes no file at all
		kind     string
		want     string
	}{
		{
			name: "reads the section's key", kind: "sonarr", want: "k1",
			existing: "sonarr:\n  api_key: \"k1\"\n",
		},
		{name: "missing file", kind: "sonarr", want: ""},
		{name: "unparseable file", kind: "sonarr", want: "", existing: "\tnot: yaml\n"},
		{name: "scalar document", kind: "sonarr", want: "", existing: "just-a-string\n"},
		{
			name: "section absent", kind: "radarr", want: "",
			existing: "sonarr:\n  api_key: \"k1\"\n",
		},
		{
			name: "key absent", kind: "sonarr", want: "",
			existing: "sonarr:\n  url: \"http://sonarr:8989\"\n",
		},
		{
			name: "key is a mapping, not a scalar", kind: "sonarr", want: "",
			existing: "sonarr:\n  api_key:\n    nested: no\n",
		},
		{
			name: "surrounding whitespace is trimmed", kind: "sonarr", want: "k1",
			existing: "sonarr:\n  api_key: \"  k1  \"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			if tt.existing != "" {
				if err := os.WriteFile(configPath, []byte(tt.existing), 0o600); err != nil {
					t.Fatalf("write existing config: %v", err)
				}
			}
			h := New(&Deps{ConfigPath: func() string { return configPath }})
			if got := h.storedArrAPIKey(t.Context(), tt.kind); got != tt.want {
				t.Errorf("storedArrAPIKey(%q) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

// TestDescribeArrFailure pins which failures get named and which keep the
// client's own words. The three named arms are three different fixes — the key,
// the URL's base path, and neither — and the unnamed ones are the failures whose
// raw text already IS the diagnosis.
func TestDescribeArrFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "401 names the credential",
			err:  &arrapi.StatusError{Code: http.StatusUnauthorized, Path: "/api/v3/system/status"},
			want: "HTTP 401: the API key was rejected",
		},
		{
			name: "403 names the credential too",
			err:  &arrapi.StatusError{Code: http.StatusForbidden},
			want: "HTTP 403: the API key was rejected",
		},
		{
			name: "404 names the base path",
			err:  &arrapi.StatusError{Code: http.StatusNotFound},
			want: "HTTP 404: no arr API at this URL — check for a missing or extra base path",
		},
		{
			name: "any other status is reported without a claim about the cause",
			err:  &arrapi.StatusError{Code: http.StatusBadGateway},
			want: "the server at this URL answered HTTP 502",
		},
		{
			name: "a wrapped status error is still recognized",
			err:  fmt.Errorf("ping: %w", &arrapi.StatusError{Code: http.StatusUnauthorized}),
			want: "HTTP 401: the API key was rejected",
		},
		{
			name: "a transport failure keeps its own text, which is already the diagnosis",
			err:  errors.New("dial tcp 10.0.0.5:8989: connect: connection refused"),
			want: "dial tcp 10.0.0.5:8989: connect: connection refused",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := describeArrFailure(tt.err); got != tt.want {
				t.Errorf("describeArrFailure(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// TestHandleTestConnection_sanitizes_the_upstream_error pins the display bound on the
// one field that carries text subflux did not write. A body arrapi captured is
// bounded at 64 KiB and reaches this endpoint through the unnamed arm of
// describeArrFailure, so an arr answering with a newline-bearing wall of text
// would otherwise put it verbatim into a JSON field the browser renders.
func TestHandleTestConnection_sanitizes_the_upstream_error(t *testing.T) {
	t.Parallel()
	hostile := "HTTP 500: " + strings.Repeat("a", 4096) + "\nSecond-Line: injected"
	h := New(&Deps{
		NewSonarr: func(string, string) (ArrPinger, error) {
			return recordingPinger{pings: new(int), err: errors.New(hostile)}, nil
		},
		ConfigPath: func() string { return filepath.Join(t.TempDir(), "config.yaml") },
	})

	rec := doArrTest(t, h, `{"kind":"sonarr","url":"http://sonarr:8989","api_key":"k1"}`)
	var got ConnTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("HandleTestConnection() response %s: %v", rec.Body.String(), err)
	}
	if strings.ContainsAny(got.Error, "\n\r") {
		t.Errorf("HandleTestConnection() error = %q, want no line breaks", got.Error)
	}
	if len(got.Error) > 512 {
		t.Errorf("HandleTestConnection() error is %d bytes, want it capped near 256", len(got.Error))
	}
}

func doArrTest(t *testing.T, h *Handler, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/config/test-connection", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.HandleTestConnection(rec, req)
	return rec
}

// The on-demand probe builds a client per press of the Test button, so it must
// close it whatever the ping answered. Left open, a settings dialog the
// operator retries a few times strands one client's transports per press.
func TestHandleTestConnection_closes_the_client_it_builds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pingErr error
	}{
		{name: "reachable"},
		{name: "unreachable", pingErr: errors.New("connection refused")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var built, pings, closes int
			newPinger := func(string, string) (ArrPinger, error) {
				built++
				return closingPinger{pings: &pings, closes: &closes, err: tt.pingErr}, nil
			}
			h := New(&Deps{
				SchemaFunc: func(_ []subflux.ProviderSchema) []subflux.SchemaSection {
					return testArrSchema()
				},
				NewSonarr:  newPinger,
				NewRadarr:  newPinger,
				ConfigPath: func() string { return filepath.Join(t.TempDir(), "config.yaml") },
			})

			doArrTest(t, h, `{"kind":"sonarr","url":"http://sonarr:8989","api_key":"k1"}`)

			if built != 1 || pings != 1 {
				// Establishes what the close count below is measured against.
				t.Fatalf("HandleTestConnection() built %d clients and pinged %d times, want 1 of each",
					built, pings)
			}
			if closes != 1 {
				t.Errorf("HandleTestConnection() closed the client %d times, want 1", closes)
			}
		})
	}
}
