package accounts

import (
	"slices"
	"testing"
)

func originsConfig(t *testing.T, origins ...string) Config {
	t.Helper()
	c, errs := ParseConfig(section(map[string]any{keyPasskeyOrigins: origins}))
	if errs != nil {
		t.Fatalf("origins %v: %v", origins, errs)
	}
	return c
}

func TestCheckOrigins(t *testing.T) {
	refused := []struct {
		name    string
		origins []string
		path    string
	}{
		{"no common host", []string{"https://a.example", "https://b.example"}, "auth.passkey_origins"},
		{"siblings under an unlisted parent", []string{"https://app.nas.example", "https://tv.nas.example"}, "auth.passkey_origins"},
		{"an IPv4 host", []string{"https://192.168.1.5"}, "auth.passkey_origins[0]"},
		{"an IPv6 host", []string{"https://nas.example", "https://[fd00::5]:8443"}, "auth.passkey_origins[1]"},
		{"a path", []string{"https://nas.example/subflux"}, "auth.passkey_origins[0]"},
		{"a query", []string{"https://nas.example?x=1"}, "auth.passkey_origins[0]"},
		{"plain http off localhost", []string{"http://nas.example"}, "auth.passkey_origins[0]"},
		{"no scheme", []string{"nas.example"}, "auth.passkey_origins[0]"},
		{"a single-label host", []string{"https://nas"}, "auth.passkey_origins"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			_, errs := ParseConfig(section(map[string]any{keyPasskeyOrigins: c.origins}))
			only(t, errs, c.path, "")
		})
	}

	t.Run("one list in two orders gives one RP ID", func(t *testing.T) {
		a := originsConfig(t, "https://app.nas.example", "https://nas.example:8443", "https://NAS.example")
		b := originsConfig(t, "https://NAS.example", "https://nas.example:8443", "https://app.nas.example")
		if a.get().rpID != "nas.example" || b.get().rpID != "nas.example" {
			t.Errorf("RP IDs %q and %q, want nas.example for both", a.get().rpID, b.get().rpID)
		}
	})
	t.Run("localhost over plain http", func(t *testing.T) {
		if c := originsConfig(t, "http://localhost:8374").get(); c.rpID != "localhost" {
			t.Errorf("RP ID %q, want localhost", c.rpID)
		}
	})
	t.Run("an empty list turns passkeys off", func(t *testing.T) {
		if c := originsConfig(t).get(); c.rpID != "" || c.origins != nil {
			t.Errorf("empty list: RP ID %q, origins %v; want none", c.rpID, c.origins)
		}
	})

	c := originsConfig(t, "https://nas.example", "https://app.nas.example:8443")
	t.Run("an origin outside allowed_hosts", func(t *testing.T) {
		only(t, CheckOrigins(c, []string{"nas.example"}), "auth.passkey_origins[1]", "")
	})
	t.Run("every origin listed", func(t *testing.T) {
		if errs := CheckOrigins(c, []string{"app.nas.example", "nas.example"}); errs != nil {
			t.Errorf("errors %v, want none", errs)
		}
	})
	t.Run("an empty allowed_hosts lists every host", func(t *testing.T) {
		if errs := CheckOrigins(c, nil); errs != nil {
			t.Errorf("errors %v, want none", errs)
		}
	})
	t.Run("each origin's host is compared in canonical form", func(t *testing.T) {
		upper := originsConfig(t, "https://NAS.Example")
		if errs := CheckOrigins(upper, []string{"nas.example"}); errs != nil {
			t.Errorf("errors %v, want none", errs)
		}
		if got := upper.get().origins; !slices.Equal(got, []string{"https://nas.example"}) {
			t.Errorf("origins %v, want the lowercase origin", got)
		}
	})
}
