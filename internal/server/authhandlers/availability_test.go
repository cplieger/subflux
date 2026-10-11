package authhandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	authwebauthn "github.com/cplieger/auth/v6/webauthn"
	"github.com/cplieger/subflux/internal/subflux"
)

type availabilityConfig struct{ rpID string }

func (availabilityConfig) BasicAuthEnabled() bool       { return true }
func (availabilityConfig) CheckBreachedPasswords() bool { return false }
func (availabilityConfig) OIDCEnabled() bool            { return false }
func (c availabilityConfig) WebAuthnRPID() string       { return c.rpID }

func availabilityHandler(t *testing.T, rp *authwebauthn.RelyingParty, cfg AuthConfig) *Handler {
	t.Helper()
	return &Handler{
		WebAuthnResolver: func() *authwebauthn.RelyingParty { return rp },
		Config:           func() AuthConfig { return cfg },
	}
}

func probe(t *testing.T, h *Handler, origin string, present bool) (int, subflux.WebAuthnAvailability) {
	t.Helper()
	target := "/api/auth/webauthn/availability"
	if present {
		target += "?origin=" + url.QueryEscape(origin)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleWebAuthnAvailability(rec, req)
	var out subflux.WebAuthnAvailability
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode availability body %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, out
}

func TestWebAuthnAvailability(t *testing.T) {
	t.Parallel()
	configured, err := authwebauthn.New(authwebauthn.RPConfig{ID: "example.com", DisplayName: "Test RP"})
	if err != nil {
		t.Fatalf("webauthn.New: %v", err)
	}
	allowlisted, err := authwebauthn.New(authwebauthn.RPConfig{
		ID: "example.com", DisplayName: "Test RP", Origins: []string{"https://subflux.example.com"},
	})
	if err != nil {
		t.Fatalf("webauthn.New(list): %v", err)
	}

	tests := []struct {
		name   string
		rp     *authwebauthn.RelyingParty
		cfg    AuthConfig
		origin string
		absent bool
		want   subflux.WebAuthnAvailability
	}{
		{
			name: "unconfigured_derives_a_suggestion", cfg: availabilityConfig{}, origin: "https://subflux.example.com",
			want: subflux.WebAuthnAvailability{Reason: "unconfigured", SuggestedRPID: "example.com"},
		},
		{
			name: "unconfigured_with_nil_config_is_the_fresh_install", cfg: nil, origin: "https://subflux.example.com",
			want: subflux.WebAuthnAvailability{Reason: "unconfigured", SuggestedRPID: "example.com"},
		},
		{
			name: "unconfigured_ip_host_is_unconfigurable", cfg: availabilityConfig{}, origin: "https://10.0.0.5",
			want: subflux.WebAuthnAvailability{Reason: "unconfigurable"},
		},
		{
			name: "stored_but_refused_is_init_failed", cfg: availabilityConfig{rpID: "Example.COM"}, origin: "https://subflux.example.com",
			want: subflux.WebAuthnAvailability{Reason: "init_failed", RPID: "Example.COM"},
		},
		{
			name: "configured_and_covered", rp: configured, cfg: availabilityConfig{rpID: "example.com"}, origin: "https://subflux.example.com",
			want: subflux.WebAuthnAvailability{Available: true},
		},
		{
			name: "configured_not_covered", rp: configured, cfg: availabilityConfig{rpID: "example.com"}, origin: "https://subflux.example.net",
			want: subflux.WebAuthnAvailability{Reason: "origin_not_accepted", RPID: "example.com", SuggestedRPID: "example.net"},
		},
		{
			name: "configured_plain_http_is_the_scheme_not_the_host", rp: configured, cfg: availabilityConfig{rpID: "example.com"}, origin: "http://subflux.example.com",
			want: subflux.WebAuthnAvailability{Reason: "insecure_scheme", RPID: "example.com"},
		},
		{
			name: "configured_ip_over_http_is_unconfigurable", rp: configured, cfg: availabilityConfig{rpID: "example.com"}, origin: "http://10.0.0.5:8374",
			want: subflux.WebAuthnAvailability{Reason: "unconfigurable", RPID: "example.com"},
		},
		{
			name: "trailing_dot_is_an_answer_not_a_400", rp: configured, cfg: availabilityConfig{rpID: "example.com"}, origin: "https://subflux.example.com.",
			want: subflux.WebAuthnAvailability{Reason: "address_unusable", RPID: "example.com"},
		},
		{
			name: "sibling_refused_by_the_list", rp: allowlisted, cfg: availabilityConfig{rpID: "example.com"}, origin: "https://evil.example.com",
			want: subflux.WebAuthnAvailability{Reason: "origin_not_allowlisted", RPID: "example.com"},
		},
		{
			name: "sibling_accepted_by_policy_alone", rp: configured, cfg: availabilityConfig{rpID: "example.com"}, origin: "https://evil.example.com",
			want: subflux.WebAuthnAvailability{Available: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := availabilityHandler(t, tt.rp, tt.cfg)
			code, got := probe(t, h, tt.origin, true)
			if code != http.StatusOK {
				t.Fatalf("probe(%q) status = %d, want 200", tt.origin, code)
			}
			if got != tt.want {
				t.Errorf("probe(%q) = %+v, want %+v", tt.origin, got, tt.want)
			}
		})
	}
}

func TestWebAuthnAvailability_missingOrEmptyOriginIs400(t *testing.T) {
	t.Parallel()
	h := availabilityHandler(t, nil, availabilityConfig{})
	if code, _ := probe(t, h, "", false); code != http.StatusBadRequest {
		t.Errorf("probe without ?origin= status = %d, want 400", code)
	}
	if code, _ := probe(t, h, "", true); code != http.StatusBadRequest {
		t.Errorf("probe with empty ?origin= status = %d, want 400", code)
	}
}
