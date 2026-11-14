package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/provider/providers"
	"github.com/cplieger/subflux/internal/settings"
	"go.yaml.in/yaml/v4"
)

// loginService loads file (absent when nil) into a settings service with no
// store activation and activateAuth as its auth half, booted.
func loginService(t *testing.T, file *string, activateAuth func(context.Context, accounts.Config) error) (passwordLogin, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if file != nil {
		if err := os.WriteFile(path, []byte(*file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reg := registries(providers.New(), filepath.Join(dir, "subflux.bolt"))
	svc := settings.NewService(path, reg, settings.Load(path, reg), nil, activateAuth, nil)
	if err := svc.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	return passwordLogin{svc: svc}, path
}

// fileValue is the scalar at section.key in the file at path, "" when absent.
func fileValue(t *testing.T, path, section, key string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	v, ok := m[section][key]
	if !ok {
		return ""
	}
	out, _ := yaml.Marshal(v)
	return string(out)
}

func TestEnablePasswordLoginModes(t *testing.T) {
	t.Run("an Active file commits and keeps its other values", func(t *testing.T) {
		p, path := loginService(t, new("auth:\n  password_login: false\n"+validLanguages), nil)
		out, err := p.EnablePasswordLogin(t.Context())
		if err != nil || len(out.Fields) != 0 || out.AlreadyOn {
			t.Fatalf("EnablePasswordLogin = %+v, %v", out, err)
		}
		if got := fileValue(t, path, "auth", "password_login"); got != "true\n" {
			t.Errorf("password_login = %q, want true", got)
		}
		if got := fileValue(t, path, "languages", "default"); got != "- tag: en\n" {
			t.Errorf("languages.default = %q, want the file's en", got)
		}
		if _, ok := p.svc.Snapshot().Active(); !ok {
			t.Error("the saved file is not Active")
		}
	})
	t.Run("a Failed file the change repairs commits", func(t *testing.T) {
		p, path := loginService(t, new("auth:\n  password_login: nope\n"+validLanguages), nil)
		if p.svc.Snapshot().Mode() != settings.Failed {
			t.Fatal("the fixture did not load Failed")
		}
		out, err := p.EnablePasswordLogin(t.Context())
		if err != nil || len(out.Fields) != 0 {
			t.Fatalf("EnablePasswordLogin = %+v, %v", out, err)
		}
		if got := fileValue(t, path, "auth", "password_login"); got != "true\n" {
			t.Errorf("password_login = %q, want true", got)
		}
		if p.svc.Snapshot().Mode() != settings.ModeActive {
			t.Error("the repaired file is not Active")
		}
	})
	for _, c := range []struct{ name, file, path string }{
		{"a Failed file the change does not repair writes nothing", "auth:\n  password_login: false\nlanguages: 7\n", "languages"},
		// The root field error: the file is not one mapping.
		{"a file that is a sequence writes nothing", "- 1\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, path := loginService(t, new(c.file), nil)
			out, err := p.EnablePasswordLogin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(out.Fields, func(f formschema.FieldError) bool { return strings.HasPrefix(f.Path, c.path) }) {
				t.Errorf("fields = %v, want one under %q", out.Fields, c.path)
			}
			if b, _ := os.ReadFile(path); string(b) != c.file {
				t.Errorf("the file changed to %q", b)
			}
		})
	}
	t.Run("unconfigured writes nothing and says password sign-in is on", func(t *testing.T) {
		p, path := loginService(t, nil, nil)
		out, err := p.EnablePasswordLogin(t.Context())
		if err != nil || !out.AlreadyOn || len(out.Fields) != 0 {
			t.Fatalf("EnablePasswordLogin = %+v, %v", out, err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a file was written: %v", err)
		}
	})
	t.Run("a refused unconfigured boot writes nothing and names its field", func(t *testing.T) {
		stranded := formschema.FieldError{Path: "auth.oidc_enabled", Message: "would leave 1 user without a sign-in method"}
		refuse := func(context.Context, accounts.Config) error {
			return &formschema.RefusedError{Fields: []formschema.FieldError{stranded}}
		}
		p, path := loginService(t, nil, refuse)
		out, err := p.EnablePasswordLogin(t.Context())
		if err != nil || out.AlreadyOn {
			t.Fatalf("EnablePasswordLogin = %+v, %v", out, err)
		}
		if !slices.Equal(out.Fields, []formschema.FieldError{stranded}) {
			t.Errorf("fields = %v, want %v", out.Fields, stranded)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a file was written: %v", err)
		}
	})
}
