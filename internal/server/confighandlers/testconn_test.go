package confighandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/auth/v6"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
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

// testRegistry stands in for the provider registry: one provider that offers a
// credential check, recording the settings the check was handed so the tests can
// assert what the endpoint resolved before probing.
type testRegistry struct {
	got    *map[string]any
	calls  *int
	err    error
	name   subflux.ProviderID
	fields []subflux.ProviderSchemaField
}

func (r testRegistry) ProviderNames() []subflux.ProviderID { return []subflux.ProviderID{r.name} }

func (r testRegistry) Schema(name subflux.ProviderID) (string, []subflux.ProviderSchemaField) {
	if name != r.name {
		return "", nil
	}
	return string(r.name), r.fields
}

func (r testRegistry) CredentialCheck(name subflux.ProviderID) bool { return name == r.name }

func (r testRegistry) Normalize(name subflux.ProviderID, raw map[string]any) map[string]any {
	if name != r.name {
		return raw
	}
	return provider.NormalizeSettings(r.fields, raw)
}

func (r testRegistry) CheckCredentials(_ context.Context, name subflux.ProviderID, settings map[string]any) error {
	if r.calls != nil {
		*r.calls++
	}
	if r.got != nil {
		*r.got = settings
	}
	if name != r.name {
		return errors.New("wrong provider")
	}
	return r.err
}

// opensubsRegistry is the provider fixture every provider-arm case shares: two
// secrets and one plain field, the shape a real provider card renders.
func opensubsRegistry(got *map[string]any, calls *int, err error) testRegistry {
	return testRegistry{
		name:  "opensubtitles",
		got:   got,
		calls: calls,
		err:   err,
		fields: []subflux.ProviderSchemaField{
			{Key: "username"},
			{Key: "password", Secret: true},
			{Key: "api_key", Secret: true},
		},
	}
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
			body:       `{"kind":"sonarr","settings":{"url":"http://sonarr:8989","api_key":"k1"}}`,
			wantStatus: http.StatusOK,
			wantValid:  true,
			wantSonarr: []string{"http://sonarr:8989|k1"},
			wantPings:  1,
		},
		{
			name:       "reachable radarr builds the radarr client",
			body:       `{"kind":"radarr","settings":{"url":"http://radarr:7878","api_key":"k2"}}`,
			wantStatus: http.StatusOK,
			wantValid:  true,
			wantRadarr: []string{"http://radarr:7878|k2"},
			wantPings:  1,
		},
		{
			name:       "unreachable arr is a 200 verdict carrying the reason",
			body:       `{"kind":"sonarr","settings":{"url":"http://sonarr:8989","api_key":"k1"}}`,
			pingErr:    &arrapi.StatusError{Code: http.StatusUnauthorized},
			wantStatus: http.StatusOK,
			wantErrIs:  "the API key was rejected",
			wantSonarr: []string{"http://sonarr:8989|k1"},
			wantPings:  1,
		},
		{
			name:       "malformed URL is a verdict, not a bad request",
			body:       `{"kind":"sonarr","settings":{"url":"sonarr:8989","api_key":"k1"}}`,
			newErr:     errors.New("baseURL must be an absolute http(s) URL"),
			wantStatus: http.StatusOK,
			wantErrIs:  "absolute http(s) URL",
			wantSonarr: []string{"sonarr:8989|k1"},
			wantPings:  0,
		},
		{
			name:       "omitted key falls back to the one on disk",
			body:       `{"kind":"radarr","settings":{"url":"http://radarr:7878","api_key":""}}`,
			existing:   onDisk,
			wantStatus: http.StatusOK,
			wantValid:  true,
			wantRadarr: []string{"http://radarr:7878|k-stored"},
			wantPings:  1,
		},
		{
			name:       "unchanged credentials still ping",
			body:       `{"kind":"sonarr","settings":{"url":"http://live:8989","api_key":"k-live"}}`,
			existing:   onDisk,
			wantStatus: http.StatusOK,
			wantValid:  true,
			wantSonarr: []string{"http://live:8989|k-live"},
			wantPings:  1,
		},
		{
			name:       "empty URL never builds a client",
			body:       `{"kind":"sonarr","settings":{"url":"  ","api_key":"k1"}}`,
			wantStatus: http.StatusOK,
			wantErrIs:  "URL is required",
			wantPings:  0,
		},
		{
			name:       "omitted key with nothing on disk never builds a client",
			body:       `{"kind":"sonarr","settings":{"url":"http://sonarr:8989","api_key":""}}`,
			wantStatus: http.StatusOK,
			wantErrIs:  "API key is required",
			wantPings:  0,
		},
		{
			name:       "unknown kind is the one bad request",
			body:       `{"kind":"lidarr","settings":{"url":"http://lidarr:8686","api_key":"k1"}}`,
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
				Registry:   opensubsRegistry(nil, nil, nil),
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

// TestStoredSecret covers the fallback's read failures, each of which must
// answer "" so the handler reports the missing value as the operator-facing
// answer it is rather than a 500 about the server's own file. The nested path
// case is the provider arm's: a provider secret sits four keys deep, so a
// resolver that only ever walked two would answer "" for every provider and
// silently fail the test on exactly the configs that work.
func TestStoredSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		existing string // "" writes no file at all
		path     secretPath
		want     string
	}{
		{
			name: "reads the section's key", path: secretPath{"sonarr", "api_key"}, want: "k1",
			existing: "sonarr:\n  api_key: \"k1\"\n",
		},
		{name: "missing file", path: secretPath{"sonarr", "api_key"}, want: ""},
		{name: "unparseable file", path: secretPath{"sonarr", "api_key"}, want: "", existing: "\tnot: yaml\n"},
		{name: "scalar document", path: secretPath{"sonarr", "api_key"}, want: "", existing: "just-a-string\n"},
		{
			name: "section absent", path: secretPath{"radarr", "api_key"}, want: "",
			existing: "sonarr:\n  api_key: \"k1\"\n",
		},
		{
			name: "key absent", path: secretPath{"sonarr", "api_key"}, want: "",
			existing: "sonarr:\n  url: \"http://sonarr:8989\"\n",
		},
		{
			name: "key is a mapping, not a scalar", path: secretPath{"sonarr", "api_key"}, want: "",
			existing: "sonarr:\n  api_key:\n    nested: no\n",
		},
		{
			name: "surrounding whitespace is trimmed", path: secretPath{"sonarr", "api_key"}, want: "k1",
			existing: "sonarr:\n  api_key: \"  k1  \"\n",
		},
		{
			name: "reads a provider secret four keys deep",
			path: secretPath{"providers", "opensubtitles", "settings", "api_key"}, want: "k-prov",
			existing: "providers:\n  opensubtitles:\n    settings:\n      api_key: \"k-prov\"\n",
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
			if got := h.storedSecret(t.Context(), tt.path); got != tt.want {
				t.Errorf("storedSecret(%v) = %q, want %q", tt.path, got, tt.want)
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
			want: "the server at this URL answered HTTP 404 but has no arr API. Check for a missing or extra base path",
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
		Registry: opensubsRegistry(nil, nil, nil),
		NewSonarr: func(string, string) (ArrPinger, error) {
			return recordingPinger{pings: new(int), err: errors.New(hostile)}, nil
		},
		ConfigPath: func() string { return filepath.Join(t.TempDir(), "config.yaml") },
	})

	rec := doArrTest(t, h, `{"kind":"sonarr","settings":{"url":"http://sonarr:8989","api_key":"k1"}}`)
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
				Registry:   opensubsRegistry(nil, nil, nil),
				NewSonarr:  newPinger,
				NewRadarr:  newPinger,
				ConfigPath: func() string { return filepath.Join(t.TempDir(), "config.yaml") },
			})

			doArrTest(t, h, `{"kind":"sonarr","settings":{"url":"http://sonarr:8989","api_key":"k1"}}`)

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

// TestHandleTestConnection_provider_arm pins the second arm of the endpoint: a
// provider's own credential check, with every empty secret resolved from the
// config file the way the arr arm resolves its key.
//
// The three verdicts are three different sentences on purpose. A refused
// credential is a field to go fix and an unreachable service is not, and the
// banner is the only place the operator learns which; a check that reported both
// the same way would send them to the wrong remedy half the time.
func TestHandleTestConnection_provider_arm(t *testing.T) {
	t.Parallel()
	const onDisk = "providers:\n  opensubtitles:\n    settings:\n" +
		"      password: \"p-stored\"\n      api_key: \"k-stored\"\n"

	tests := []struct {
		checkErr     error
		wantSettings map[string]any
		name         string
		body         string
		existing     string
		wantErrIs    string
		wantStatus   int
		wantChecks   int
		wantValid    bool
	}{
		{
			name:       "accepted credentials answer valid",
			body:       `{"kind":"opensubtitles","settings":{"username":"u","password":"p","api_key":"k"}}`,
			wantStatus: http.StatusOK, wantValid: true, wantChecks: 1,
			wantSettings: map[string]any{"username": "u", "password": "p", "api_key": "k"},
		},
		{
			name:       "refused credentials name the credentials",
			body:       `{"kind":"opensubtitles","settings":{"username":"u","password":"p","api_key":"k"}}`,
			checkErr:   &subflux.AuthError{Msg: "HTTP 401"},
			wantStatus: http.StatusOK, wantErrIs: "the credentials were rejected: HTTP 401",
			wantChecks: 1,
		},
		{
			name:       "an unreachable service does not blame the credentials",
			body:       `{"kind":"opensubtitles","settings":{"username":"u","password":"p","api_key":"k"}}`,
			checkErr:   errors.New("dial tcp: connection refused"),
			wantStatus: http.StatusOK, wantErrIs: "could not reach the service: dial tcp: connection refused",
			wantChecks: 1,
		},
		{
			name:       "empty secrets fall back to the ones on disk",
			body:       `{"kind":"opensubtitles","settings":{"username":"u","password":"","api_key":""}}`,
			existing:   onDisk,
			wantStatus: http.StatusOK, wantValid: true, wantChecks: 1,
			wantSettings: map[string]any{"username": "u", "password": "p-stored", "api_key": "k-stored"},
		},
		{
			name:       "a typed secret is never replaced by the stored one",
			body:       `{"kind":"opensubtitles","settings":{"username":"u","password":"p-typed","api_key":""}}`,
			existing:   onDisk,
			wantStatus: http.StatusOK, wantValid: true, wantChecks: 1,
			wantSettings: map[string]any{"username": "u", "password": "p-typed", "api_key": "k-stored"},
		},
		{
			name:       "a plain field is never resolved from disk",
			body:       `{"kind":"opensubtitles","settings":{"username":"","password":"p","api_key":"k"}}`,
			existing:   "providers:\n  opensubtitles:\n    settings:\n      username: \"u-stored\"\n",
			wantStatus: http.StatusOK, wantValid: true, wantChecks: 1,
			wantSettings: map[string]any{"username": "", "password": "p", "api_key": "k"},
		},
		{
			name:       "a provider offering no check is the one bad request",
			body:       `{"kind":"gestdown","settings":{}}`,
			wantStatus: http.StatusBadRequest,
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
			var gotSettings map[string]any
			checks := 0
			h := New(&Deps{
				Registry:     opensubsRegistry(&gotSettings, &checks, tt.checkErr),
				ProviderAuth: openGate(t, nil),
				ConfigPath:   func() string { return configPath },
			})

			rec := doArrTest(t, h, tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("HandleTestConnection(%s) status = %d, want %d; body %s",
					tt.body, rec.Code, tt.wantStatus, rec.Body.String())
			}
			if checks != tt.wantChecks {
				t.Errorf("HandleTestConnection(%s) ran %d checks, want %d", tt.body, checks, tt.wantChecks)
			}
			if tt.wantSettings != nil && !maps.Equal(gotSettings, tt.wantSettings) {
				t.Errorf("HandleTestConnection(%s) checked settings %v, want %v",
					tt.body, gotSettings, tt.wantSettings)
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
			if got.Error != tt.wantErrIs {
				t.Errorf("HandleTestConnection(%s) error = %q, want %q", tt.body, got.Error, tt.wantErrIs)
			}
		})
	}
}

// TestDescribeCredentialFailure pins which failures accuse the credentials. The
// two sentences are the two remedies, and classification is on the published
// error TYPE, so a failure this package cannot recognize degrades to the weaker
// claim rather than telling the operator to go and retype a working key.
func TestDescribeCredentialFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		name string
		want string
	}{
		{
			name: "a refusal names the credentials",
			err:  &subflux.AuthError{Msg: "SubDL refused the API key (HTTP 403)"},
			want: "the credentials were rejected: SubDL refused the API key (HTTP 403)",
		},
		{
			name: "a wrapped refusal is still recognized",
			err:  fmt.Errorf("check: %w", &subflux.AuthError{Msg: "refused"}),
			want: "the credentials were rejected: refused",
		},
		{
			name: "a transport failure does not accuse the credentials",
			err:  errors.New("dial tcp 10.0.0.5:443: connect: connection refused"),
			want: "could not reach the service: dial tcp 10.0.0.5:443: connect: connection refused",
		},
		{
			name: "a rate limit is not a credential verdict either",
			err:  &subflux.RateLimitError{Msg: "rate limited (429)"},
			want: "could not reach the service: rate limited (429)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := describeCredentialFailure(tt.err); got != tt.want {
				t.Errorf("describeCredentialFailure(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// gateStore is an in-memory providergate.Store, so a second gate can load what
// a first one persisted.
type gateStore struct {
	recs map[subflux.ProviderID]subflux.ProviderAuthRecord
	mu   sync.Mutex
}

func (s *gateStore) ProviderAuthRecords(context.Context) ([]subflux.ProviderAuthRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Collect(maps.Values(s.recs)), nil
}

func (s *gateStore) PutProviderAuthRecord(_ context.Context, rec *subflux.ProviderAuthRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recs == nil {
		s.recs = map[subflux.ProviderID]subflux.ProviderAuthRecord{}
	}
	s.recs[rec.Provider] = *rec
	return nil
}

func (s *gateStore) DeleteProviderAuthRecord(_ context.Context, id subflux.ProviderID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recs, id)
	return nil
}

func (s *gateStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recs)
}

// openGate opens a credential gate over store (memory-only when nil) whose
// clock is now.
func openGate(t *testing.T, store *gateStore, now ...func() time.Time) *providergate.Gate {
	t.Helper()
	cfg := providergate.Config{Metrics: testsupport.NopGateMetrics{}}
	if store != nil {
		h, err := auth.NewHasher(auth.Argon2Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
		if err != nil {
			t.Fatalf("NewHasher: %v", err)
		}
		cfg.Store, cfg.Hasher = store, h
	}
	if len(now) > 0 {
		cfg.Now = now[0]
	}
	g, err := providergate.Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("providergate.Open: %v", err)
	}
	return g
}

// disabledGate returns a gate whose live binding has opensubtitles disabled
// for rejecting settings.
func disabledGate(t *testing.T, store *gateStore, settings map[string]any) *providergate.Gate {
	t.Helper()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	gate := openGate(t, store, func() time.Time { return at })
	b := gate.Bind(map[subflux.ProviderID]map[string]any{"opensubtitles": settings})
	gate.Activate(b)
	for range providergate.MaxAuthFailures {
		b.Observe(t.Context(), "opensubtitles", providergate.OpSearch, &subflux.AuthError{Msg: "HTTP 401"})
		at = at.Add(time.Hour)
	}
	if !gate.Status()["opensubtitles"].Disabled {
		t.Fatal("setup: opensubtitles is not disabled after the ladder")
	}
	return gate
}

// A passing check is the operator's proof that the credentials work, so it
// re-enables a provider disabled for exactly those settings, and says so. A
// pass with other settings cannot vouch for the recorded ones: the disable
// stays until those settings are saved. With no engine live, the record the
// gate loaded at start is cleared all the same.
func TestHandleTestConnection_provider_check_clears_a_matching_disable(t *testing.T) {
	t.Parallel()
	recorded := map[string]any{"username": "u", "password": "p", "api_key": "k"}
	const matching = `{"kind":"opensubtitles","settings":{"username":"u","password":"p","api_key":"k"}}`
	check := func(t *testing.T, gate providerAuthClearer, body string) ConnTestResponse {
		t.Helper()
		h := New(&Deps{
			Registry:     opensubsRegistry(nil, nil, nil),
			ProviderAuth: gate,
			ConfigPath:   func() string { return filepath.Join(t.TempDir(), "config.yaml") },
		})
		var got ConnTestResponse
		if err := json.Unmarshal(doArrTest(t, h, body).Body.Bytes(), &got); err != nil {
			t.Fatalf("HandleTestConnection(%s): %v", body, err)
		}
		return got
	}

	t.Run("recorded settings", func(t *testing.T) {
		t.Parallel()
		gate := disabledGate(t, nil, recorded)
		got := check(t, gate, matching)
		if !got.Valid || got.Message != "credentials accepted. Provider re-enabled" {
			t.Errorf("HandleTestConnection(recorded settings) = %+v, want valid and re-enabled", got)
		}
		if gate.Status()["opensubtitles"].Disabled {
			t.Error("opensubtitles still disabled after a passing check with its recorded settings")
		}
	})
	t.Run("other settings", func(t *testing.T) {
		t.Parallel()
		gate := disabledGate(t, nil, recorded)
		got := check(t, gate, `{"kind":"opensubtitles","settings":{"username":"u","password":"p2","api_key":"k"}}`)
		if !got.Valid || got.Message != "credentials accepted. Save to apply them and re-enable the provider" {
			t.Errorf("HandleTestConnection(other settings) = %+v, want valid and asked to save", got)
		}
		if !gate.Status()["opensubtitles"].Disabled {
			t.Error("a check with other settings re-enabled the provider")
		}
	})
	t.Run("no live binding", func(t *testing.T) {
		t.Parallel()
		store := &gateStore{}
		disabledGate(t, store, recorded)
		got := check(t, openGate(t, store), matching)
		if got.Message != "credentials accepted. Provider re-enabled" || store.count() != 0 {
			t.Errorf("HandleTestConnection(unconfigured) = %+v with %d stored record(s), want re-enabled and none",
				got, store.count())
		}
	})
	t.Run("nothing recorded", func(t *testing.T) {
		t.Parallel()
		if got := check(t, openGate(t, nil), matching); !got.Valid || got.Message != "" {
			t.Errorf("HandleTestConnection(no record) = %+v, want valid with no message", got)
		}
	})
}
