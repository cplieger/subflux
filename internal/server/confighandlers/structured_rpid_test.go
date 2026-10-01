package confighandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/config"
)

// rpidPayload builds a structured save whose auth section is exactly the
// given JSON (or absent when "" is passed), beside the minimum the loader
// needs. No secret is empty, so the secret merge never reaches the baseline
// and only the RP-ID fill and gate decide the outcome.
func rpidPayload(authSection string) string {
	sections := []string{
		`"sonarr": {"url": "http://s:8989", "api_key": "k"}`,
		`"languages": {"default": [{"code": "en"}]}`,
	}
	if authSection != "" {
		sections = append(sections, `"auth": `+authSection)
	}
	return `{"sections": {` + strings.Join(sections, ",") + `}}`
}

// storedRPIDYAML is a loadable config file holding the given RP ID.
func storedRPIDYAML(rpID string) string {
	return strings.Join([]string{
		"sonarr:",
		"  url: http://s:8989",
		"  api_key: k",
		"languages:",
		"  default:",
		"    - code: en",
		"auth:",
		"  webauthn_rp_id: " + rpID,
		"",
	}, "\n")
}

func doStructuredSaveFrom(t *testing.T, h *Handler, origin, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPut, "/api/config/structured", strings.NewReader(payload))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.HandleSaveConfigStructured(rec, req)
	return rec
}

func decodeStructuredJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func storedRPID(t *testing.T, cfgPath string) string {
	t.Helper()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	cfg, err := config.LoadFromBytes(t.Context(), data)
	if err != nil {
		t.Fatalf("saved config does not load: %v\n%s", err, data)
	}
	return cfg.WebAuthnRPID()
}

func TestStructuredSave_rpid_fillWithNothingStored(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		auth   string
		origin string
		want   string
	}{
		{name: "absent_auth_section_derives", auth: "", origin: "https://subflux.example.com", want: "example.com"},
		{name: "auth_without_the_key_derives", auth: `{"check_breached_passwords": false}`, origin: "https://subflux.example.com", want: "example.com"},
		{name: "empty_string_derives", auth: `{"webauthn_rp_id": ""}`, origin: "https://subflux.example.com", want: "example.com"},
		{name: "submitted_value_kept_verbatim", auth: `{"webauthn_rp_id": "sub.example.com"}`, origin: "https://a.sub.example.com", want: "sub.example.com"},
		{name: "no_origin_fills_nothing", auth: "", origin: "", want: ""},
		{name: "underivable_host_fills_nothing", auth: "", origin: "http://10.0.0.5:8374", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, cfgPath := newStructuredHandler(t, "")
			rec := doStructuredSaveFrom(t, h, tt.origin, rpidPayload(tt.auth))
			if rec.Code != http.StatusOK {
				t.Fatalf("save status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			if got := storedRPID(t, cfgPath); got != tt.want {
				t.Errorf("stored auth.webauthn_rp_id after save from %q = %q, want %q", tt.origin, got, tt.want)
			}
		})
	}
}

func TestStructuredSave_rpid_preservesTheStoredValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stored string
		origin string
	}{
		// Deriving here would widen a deliberately narrowed value and strand
		// every credential registered against it.
		{name: "narrowed_value_survives_a_no_value_save", stored: "sub.example.com", origin: "https://sub.example.com"},
		// Deriving here fails, and a fill with no stored-value arm would drop
		// the key from the regenerated file.
		{name: "value_survives_a_save_from_an_underivable_host", stored: "example.com", origin: "http://10.0.0.5:8374"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, cfgPath := newStructuredHandler(t, storedRPIDYAML(tt.stored))
			rec := doStructuredSaveFrom(t, h, tt.origin, rpidPayload(`{"check_breached_passwords": false}`))
			if rec.Code != http.StatusOK {
				t.Fatalf("save status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			if got := storedRPID(t, cfgPath); got != tt.stored {
				t.Errorf("stored auth.webauthn_rp_id after a no-value save from %q = %q, want %q", tt.origin, got, tt.stored)
			}
		})
	}
}

// A no-value payload relies on the stored value; when the file cannot be read
// the save fails closed rather than deriving over (widening) or dropping
// (deleting) it. The payload carries no empty secret, so it is this fill and
// not the secret merge that stops the save.
func TestStructuredSave_rpid_unreadableBaselineFailsClosed(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	corrupt := "auth: [\n"
	if err := os.WriteFile(cfgPath, []byte(corrupt), 0o600); err != nil {
		t.Fatalf("write corrupt config: %v", err)
	}
	reloads := 0
	h := newStructuredHandlerAt(t, cfgPath,
		func(context.Context, *config.Config) error { reloads++; return nil })

	rec := doStructuredSaveFrom(t, h, "https://sub.example.com", rpidPayload(`{"check_breached_passwords": false}`))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("no-value save over an unparseable file status = %d, want 500; body %s", rec.Code, rec.Body.String())
	}
	if reloads != 0 {
		t.Errorf("hot reload calls = %d, want 0", reloads)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after failed save: %v", err)
	}
	if string(after) != corrupt {
		t.Errorf("failed save modified the config file:\nbefore: %q\nafter:  %q", corrupt, after)
	}
}

func TestStructuredSave_rpid_gate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		origin   string
		submit   string
		wantCode int
		want     string // stored value afterwards
	}{
		{name: "unchanged_value_from_an_outside_host_is_accepted", origin: "https://subflux-admin.example.net", submit: "example.com", wantCode: http.StatusOK, want: "example.com"},
		{name: "change_to_a_domain_covering_the_host_is_accepted", origin: "https://subflux-admin.example.net", submit: "example.net", wantCode: http.StatusOK, want: "example.net"},
		{name: "change_to_a_domain_not_covering_the_host_is_refused", origin: "https://subflux-admin.example.net", submit: "example.org", wantCode: http.StatusBadRequest, want: "example.com"},
		{name: "illegal_value_is_refused_from_anywhere", origin: "https://subflux.example.com", submit: "http://example.com", wantCode: http.StatusBadRequest, want: "example.com"},
		{name: "non_canonical_value_is_refused", origin: "https://subflux.example.com", submit: "Example.COM", wantCode: http.StatusBadRequest, want: "example.com"},
		{name: "no_origin_skips_the_served_half", origin: "", submit: "example.org", wantCode: http.StatusOK, want: "example.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			existing := storedRPIDYAML("example.com")
			h, cfgPath := newStructuredHandler(t, existing)
			rec := doStructuredSaveFrom(t, h, tt.origin, rpidPayload(`{"webauthn_rp_id": "`+tt.submit+`"}`))
			if rec.Code != tt.wantCode {
				t.Fatalf("save of %q from %q status = %d, want %d; body %s", tt.submit, tt.origin, rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantCode == http.StatusBadRequest {
				var resp map[string]string
				decodeStructuredJSON(t, rec, &resp)
				if resp["code"] != "config_invalid" {
					t.Errorf("code = %q, want config_invalid", resp["code"])
				}
				if !strings.HasPrefix(resp["error"], "auth.webauthn_rp_id: ") {
					t.Errorf("error = %q, want it to name auth.webauthn_rp_id", resp["error"])
				}
				after, err := os.ReadFile(cfgPath)
				if err != nil {
					t.Fatalf("read config after refused save: %v", err)
				}
				if string(after) != existing {
					t.Errorf("refused save modified the config file:\nbefore: %q\nafter:  %q", existing, after)
				}
			}
			if got := storedRPID(t, cfgPath); got != tt.want {
				t.Errorf("stored auth.webauthn_rp_id = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStructuredSave_rpid_refusalNamesTheRemedy(t *testing.T) {
	t.Parallel()
	h, _ := newStructuredHandler(t, storedRPIDYAML("example.com"))
	rec := doStructuredSaveFrom(t, h, "https://subflux-admin.example.net", rpidPayload(`{"webauthn_rp_id": "example.org"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	decodeStructuredJSON(t, rec, &resp)
	want := `auth.webauthn_rp_id: "example.org" is not usable as a WebAuthn relying-party ID: the host this save comes from is not inside it; save it from a browser at a host inside "example.org", or use "example.net" for the host you are on`
	if resp["error"] != want {
		t.Errorf("error = %q\nwant    %q", resp["error"], want)
	}
}

// The legality half is unconditional: a stored-but-illegal value cannot be
// laundered by re-saving it unchanged from a host that happens to cover it.
func TestStructuredSave_rpid_storedIllegalValueIsRefusedEvenUnchanged(t *testing.T) {
	t.Parallel()
	existing := storedRPIDYAML("http://example.com")
	h, cfgPath := newStructuredHandler(t, existing)
	rec := doStructuredSaveFrom(t, h, "https://subflux.example.com", rpidPayload(`{"webauthn_rp_id": "http://example.com"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("re-saving a stored illegal value status = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after refused save: %v", err)
	}
	if string(after) != existing {
		t.Errorf("refused save modified the config file:\nbefore: %q\nafter:  %q", existing, after)
	}
}
