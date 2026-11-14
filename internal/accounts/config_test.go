package accounts

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/access"
	"github.com/cplieger/access/oidc"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/statedb"
	"go.yaml.in/yaml/v4"
)

// section builds an auth section node from key, value pairs; a []string
// value is a list.
func section(kv map[string]any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k})
		switch v := kv[k].(type) {
		case string:
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
		case []string:
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, e := range v {
				seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e})
			}
			n.Content = append(n.Content, seq)
		default:
			panic("section: unsupported value")
		}
	}
	return n
}

// withOIDC is a valid enabled OIDC block with over's keys replaced.
func withOIDC(over map[string]any) map[string]any {
	kv := map[string]any{
		keyOIDCEnabled:  "true",
		keyOIDCIssuer:   "https://idp.example/realm",
		keyOIDCClientID: "subflux",
		keyOIDCSecret:   "s3cret",
		keyOIDCRedirect: "https://nas.example" + callbackPath,
	}
	maps.Copy(kv, over)
	return kv
}

func only(t *testing.T, errs []formschema.FieldError, path, msg string) {
	t.Helper()
	if len(errs) != 1 || errs[0].Path != path || (msg != "" && errs[0].Message != msg) {
		t.Errorf("errors %v, want one at %s reading %q", errs, path, msg)
	}
}

func TestParseConfigFieldErrors(t *testing.T) {
	label := strings.Repeat("é", maxLabelRunes)
	cases := []struct {
		name string
		kv   map[string]any
		path string
	}{
		{"OIDC with an empty issuer", withOIDC(map[string]any{keyOIDCIssuer: ""}), "auth.oidc_issuer"},
		{"OIDC with an empty client id", withOIDC(map[string]any{keyOIDCClientID: " "}), "auth.oidc_client_id"},
		{"OIDC with an empty secret", withOIDC(map[string]any{keyOIDCSecret: ""}), "auth.oidc_client_secret"},
		{"OIDC with an empty redirect", withOIDC(map[string]any{keyOIDCRedirect: ""}), "auth.oidc_redirect_url"},
		{"an issuer over plain http", withOIDC(map[string]any{keyOIDCIssuer: "http://idp.example"}), "auth.oidc_issuer"},
		{"a redirect over plain http", withOIDC(map[string]any{keyOIDCRedirect: "http://nas.example" + callbackPath}), "auth.oidc_redirect_url"},
		{"a relative issuer", withOIDC(map[string]any{keyOIDCIssuer: "idp.example"}), "auth.oidc_issuer"},
		{"an issuer with a query", withOIDC(map[string]any{keyOIDCIssuer: "https://idp.example/?tenant=a"}), "auth.oidc_issuer"},
		{"an issuer with a user name", withOIDC(map[string]any{keyOIDCIssuer: "https://admin@idp.example"}), "auth.oidc_issuer"},
		{"a redirect with a fragment", withOIDC(map[string]any{keyOIDCRedirect: "https://nas.example" + callbackPath + "#x"}), "auth.oidc_redirect_url"},
		{"a redirect to another path", withOIDC(map[string]any{keyOIDCRedirect: "https://nas.example/callback"}), "auth.oidc_redirect_url"},
		{"a 1 s idle timeout", map[string]any{keyIdleTimeout: "1s"}, "auth.session_idle_timeout"},
		{"a negative idle timeout", map[string]any{keyIdleTimeout: "-1h"}, "auth.session_idle_timeout"},
		{"an idle timeout over 30 days", map[string]any{keyIdleTimeout: "721h"}, "auth.session_idle_timeout"},
		{"an absolute timeout below the idle one", map[string]any{keyIdleTimeout: "2h", keyAbsoluteTimeout: "1h"}, "auth.session_absolute_timeout"},
		{"an absolute timeout over 90 days", map[string]any{keyAbsoluteTimeout: "2161h"}, "auth.session_absolute_timeout"},
		{"auto-provisioning without groups", map[string]any{keyAutoProvision: "true"}, "auth.oidc_auto_provision"},
		{"a 65-rune label", map[string]any{keyOIDCLabel: label + "x"}, "auth.oidc_label"},
		{"an unknown key", map[string]any{"bogus": "1"}, "auth.bogus"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, errs := ParseConfig(section(c.kv))
			only(t, errs, c.path, "")
			if MethodRecord(got) != MethodRecord(Config{}) {
				t.Errorf("a failed section answers %+v, want the defaults", MethodRecord(got))
			}
		})
	}
}

func TestParseConfigAccepts(t *testing.T) {
	cases := []struct {
		name string
		kv   map[string]any
	}{
		{"no section", nil},
		{"https OIDC", withOIDC(nil)},
		{"loopback http OIDC", withOIDC(map[string]any{keyOIDCIssuer: "http://127.0.0.1:8080/realm", keyOIDCRedirect: "http://localhost:8374" + callbackPath})},
		{"a 64-rune label", map[string]any{keyOIDCLabel: strings.Repeat("é", maxLabelRunes)}},
		{"the timeout bounds", map[string]any{keyIdleTimeout: "5m", keyAbsoluteTimeout: "2160h"}},
		{"equal timeouts", map[string]any{keyIdleTimeout: "720h", keyAbsoluteTimeout: "720h"}},
		{"auto-provisioning with groups", withOIDC(map[string]any{keyAutoProvision: "true", keyAllowedGroups: []string{"media"}})},
		{"OIDC off with a half-written block", map[string]any{keyOIDCIssuer: "http://idp.example", keyOIDCRedirect: "https://nas.example/x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var n *yaml.Node
			if c.kv != nil {
				n = section(c.kv)
			}
			got, errs := ParseConfig(n)
			if errs != nil {
				t.Fatalf("errors %v, want none", errs)
			}
			g := got.get()
			if !g.oidcEnabled {
				return
			}
			cfg := oidc.Config{IssuerURL: g.issuer, ClientID: g.clientID, RedirectURL: g.redirect, ClientSecret: access.NewSecret(g.secret)}
			if err := cfg.Validate(); err != nil {
				t.Errorf("an accepted section fails the provider's own rule: %v", err)
			}
		})
	}
	t.Run("the defaults", func(t *testing.T) {
		d := Config{}.get()
		if !d.passwordLogin || d.oidcEnabled || d.rpID != "" || d.disableAuth || d.breach ||
			d.idle.Hours() != 24 || d.absolute.Hours() != 168 || d.label != "Single sign-on" {
			t.Errorf("Defaults = %+v, want password on, 24h idle, 168h absolute, no OIDC, no passkeys, no bypass", d)
		}
	})
}

func TestBypassEnabledReadsConfig(t *testing.T) {
	on, errs := ParseConfig(section(map[string]any{keyDisableAuth: "true"}))
	if errs != nil || !BypassEnabled(on) {
		t.Errorf("disable_auth: true = %v, %v; want the bypass on", BypassEnabled(on), errs)
	}
	off, errs := ParseConfig(section(map[string]any{keyDisableAuth: "false"}))
	if errs != nil || BypassEnabled(off) {
		t.Errorf("disable_auth: false = %v, %v; want the bypass off", BypassEnabled(off), errs)
	}
	if BypassEnabled(Config{}) {
		t.Error("the defaults turn the bypass on")
	}
}

// TestLoggedValueRule runs every loggedFields row's source through each
// check, with OIDC off and on, at the field's absolute path.
func TestLoggedValueRule(t *testing.T) {
	type sample struct {
		name, bad, msg string
	}
	samples := []sample{
		{"U+202E", "\u202e", msgUnsafeRune},
		{"U+2028", "\u2028", msgUnsafeRune},
		{"U+0001", "\x01", msgUnsafeRune},
		{"invalid UTF-8", "\xe9", msgNotUTF8},
	}
	// value places a sample inside a value of the key's shape; hostLen
	// builds a host of exactly n bytes.
	hostLen := func(n int) string {
		var labels []string
		for n > 0 {
			l := min(n, 63)
			if n-l == 1 {
				l--
			}
			labels = append(labels, strings.Repeat("a", l))
			n -= l + 1
		}
		return strings.Join(labels, ".")
	}
	oidcRows := []struct {
		key   string
		value func(bad string) string
		sized func(n int) string
	}{
		{keyOIDCIssuer, func(b string) string { return "https://idp.example/r" + b + "alm" }, func(n int) string { return "https://" + hostLen(n-len("https://")) }},
		{keyOIDCClientID, func(b string) string { return "a" + b }, func(n int) string { return strings.Repeat("c", n) }},
		{keyOIDCRedirect, func(b string) string { return "https://n" + b + "s.example" + callbackPath }, func(n int) string {
			return "https://" + hostLen(n-len("https://")-len(callbackPath)) + callbackPath
		}},
	}
	for _, enabled := range []string{"false", "true"} {
		for _, row := range oidcRows {
			for _, s := range samples {
				t.Run(row.key+" "+s.name+" oidc_enabled "+enabled, func(t *testing.T) {
					_, errs := ParseConfig(section(withOIDC(map[string]any{keyOIDCEnabled: enabled, row.key: row.value(s.bad)})))
					only(t, errs, "auth."+row.key, s.msg)
				})
			}
			t.Run(row.key+" over its bound oidc_enabled "+enabled, func(t *testing.T) {
				_, errs := ParseConfig(section(withOIDC(map[string]any{keyOIDCEnabled: enabled, row.key: row.sized(oidcBound + 1)})))
				only(t, errs, "auth."+row.key, "must be at most 255 bytes")
			})
			t.Run(row.key+" at its bound oidc_enabled "+enabled, func(t *testing.T) {
				v := row.sized(oidcBound)
				if len(v) != oidcBound {
					t.Fatalf("fixture is %d bytes, want %d", len(v), oidcBound)
				}
				if _, errs := ParseConfig(section(withOIDC(map[string]any{keyOIDCEnabled: enabled, row.key: v}))); errs != nil {
					t.Errorf("errors %v, want none", errs)
				}
			})
		}
		for _, s := range samples {
			t.Run("RPID "+s.name+" oidc_enabled "+enabled, func(t *testing.T) {
				_, errs := ParseConfig(section(withOIDC(map[string]any{keyOIDCEnabled: enabled, keyPasskeyOrigins: []string{"https://nas.example", "https://n" + s.bad + "s.nas.example"}})))
				msg := s.msg
				if s.bad == "\x01" {
					msg = msgOrigin
				}
				only(t, errs, "auth.passkey_origins[1]", msg)
			})
		}
	}
	t.Run("RPID over its bound", func(t *testing.T) {
		_, errs := ParseConfig(section(map[string]any{keyPasskeyOrigins: []string{"https://" + hostLen(hostBound+1)}}))
		only(t, errs, "auth.passkey_origins[0]", "must be at most 253 bytes")
	})
	t.Run("RPID at its bound", func(t *testing.T) {
		host := hostLen(hostBound)
		c, errs := ParseConfig(section(map[string]any{keyPasskeyOrigins: []string{"https://" + host}}))
		if errs != nil || c.get().rpID != host {
			t.Errorf("a %d-byte host: RP ID %q, errors %v; want it accepted", len(host), c.get().rpID, errs)
		}
	})
	t.Run("the ruling's OIDC-off cases", func(t *testing.T) {
		_, errs := ParseConfig(section(map[string]any{keyOIDCClientID: "a\xe9"}))
		only(t, errs, "auth.oidc_client_id", msgNotUTF8)
		redirect := "https://nas.example/" + strings.Repeat("r", 280)
		_, errs = ParseConfig(section(map[string]any{keyOIDCRedirect: redirect}))
		only(t, errs, "auth.oidc_redirect_url", "must be at most 255 bytes")
	})
}

// TestLoggedFieldsCoverRecord fails on a string field of the method record
// that the logged-value rule does not cover, and on a row naming no field.
func TestLoggedFieldsCoverRecord(t *testing.T) {
	var fields []string
	var walk func(t reflect.Type, prefix string)
	walk = func(typ reflect.Type, prefix string) {
		for f := range typ.Fields() {
			switch f.Type.Kind() {
			case reflect.String:
				fields = append(fields, prefix+f.Name)
			case reflect.Struct:
				walk(f.Type, prefix+f.Name+".")
			}
		}
	}
	walk(reflect.TypeFor[statedb.AuthMethodState](), "")
	rows := make([]string, 0, len(loggedFields))
	for _, f := range loggedFields {
		rows = append(rows, f.field)
	}
	slices.Sort(fields)
	slices.Sort(rows)
	if !slices.Equal(fields, rows) {
		t.Errorf("record string fields %v, loggedFields rows %v; want one row per field", fields, rows)
	}
}
