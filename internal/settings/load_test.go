package settings

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

// loadText writes text as the file in a fresh directory and loads it.
func loadText(t testing.TB, text string) Snapshot {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, Registries{})
}

// testSection registers a section "t" for one test, holding Strings a and b,
// a Secret s and an Int n; it answers the values of its last parse and how
// many parses ran.
func testSection(t *testing.T) (*formschema.Values, *int) {
	t.Helper()
	schema := formschema.Schema{Fields: []formschema.Field{
		{Key: "a", Kind: formschema.String},
		{Key: "b", Kind: formschema.String},
		{Key: "s", Kind: formschema.Secret},
		{Key: "n", Kind: formschema.Int},
	}}
	got := formschema.Values{}
	calls := 0
	prev := sections
	sections = append(slices.Clone(sections), section{key: "t", parse: func(n *yaml.Node, _ *Active) []formschema.FieldError {
		calls++
		vals, errs := schema.Decode(n)
		got = vals
		return formschema.Prefix("t", errs)
	}})
	t.Cleanup(func() { sections = prev })
	return &got, &calls
}

func failurePaths(t *testing.T, s Snapshot) []string {
	t.Helper()
	errs, ok := s.Failure()
	if !ok {
		t.Fatalf("mode %v, want Failed", s.Mode())
	}
	paths := make([]string, 0, len(errs))
	for _, e := range errs {
		paths = append(paths, e.Path)
	}
	return paths
}

// admits reports whether s's host policy admits a request naming host from a
// LAN peer.
func admits(s Server, host string) bool {
	return s.Hosts().Allows(httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil))
}

func TestLoadModes(t *testing.T) {
	t.Run("absent file is Unconfigured and writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		s := Load(filepath.Join(dir, "config.yaml"), Registries{})
		if s.Mode() != Unconfigured {
			t.Errorf("mode %v, want Unconfigured", s.Mode())
		}
		if srv, ok := s.Server(); !ok || srv.Hosts().Active() || srv.LogFormat() != "json" {
			t.Errorf("Server() = %+v, %v; want defaults", srv, ok)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Errorf("directory holds %v (%v), want nothing", entries, err)
		}
	})
	for _, text := range []string{"", "# only a comment\n", "\n\n"} {
		t.Run("empty "+strings.TrimSpace(text), func(t *testing.T) {
			s := loadText(t, text)
			if _, ok := s.Active(); !ok || s.Mode() != ModeActive {
				t.Errorf("mode %v, want ModeActive", s.Mode())
			}
		})
	}
	dir := t.TempDir()
	roots := map[string]func(t *testing.T) Snapshot{
		"two documents":      func(t *testing.T) Snapshot { return loadText(t, "server: {}\n---\nserver: {}\n") },
		"trailing separator": func(t *testing.T) Snapshot { return loadText(t, "server: {}\n---\n") },
		"over 1 MiB": func(t *testing.T) Snapshot {
			return loadText(t, "# "+strings.Repeat("x", maxFileBytes)+"\n")
		},
		"non-mapping root": func(t *testing.T) Snapshot { return loadText(t, "- server\n") },
		"null root":        func(t *testing.T) Snapshot { return loadText(t, "~\n") },
		"invalid YAML":     func(t *testing.T) Snapshot { return loadText(t, "server: [\n") },
		"directory":        func(*testing.T) Snapshot { return Load(dir, Registries{}) },
	}
	for name, load := range roots {
		t.Run(name, func(t *testing.T) {
			s := load(t)
			errs, ok := s.Failure()
			if !ok || len(errs) != 1 || errs[0].Path != "" || errs[0].Message == "" {
				t.Errorf("Failure() = %v, %v; want one root error", errs, ok)
			}
		})
	}
}

func TestModeText(t *testing.T) {
	for m, want := range map[Mode]string{Unconfigured: "unconfigured", Failed: "failed", ModeActive: "active"} {
		b, err := m.MarshalText()
		if err != nil || string(b) != want {
			t.Errorf("MarshalText(%d) = %q, %v; want %q", m, b, err, want)
		}
		var back Mode
		if err := back.UnmarshalText([]byte(want)); err != nil || back != m {
			t.Errorf("UnmarshalText(%q) = %d, %v; want %d", want, back, err, m)
		}
	}
	if b, err := Mode(0).MarshalText(); err == nil {
		t.Errorf("zero Mode marshals as %q", b)
	}
	if b, err := Mode(4).MarshalText(); err == nil {
		t.Errorf("Mode(4) marshals as %q", b)
	}
	for _, text := range []string{"", "Active", "ACTIVE", "modeactive", "unknown"} {
		var m Mode
		if err := m.UnmarshalText([]byte(text)); err == nil {
			t.Errorf("UnmarshalText(%q) = %d, want an error", text, m)
		}
	}
}

func TestUnknownSectionFails(t *testing.T) {
	s := loadText(t, "server: {allowed_hosts: [nas.lan]}\nbogus: 1\n")
	if got := failurePaths(t, s); !slices.Equal(got, []string{"bogus"}) {
		t.Errorf("paths %v, want [bogus]", got)
	}
	srv, ok := s.Server()
	if !ok || !admits(srv, "nas.lan") || admits(srv, "evil.example") {
		t.Errorf("server section %v lost beside an unknown section", ok)
	}
}

func TestExpansionKeepsDollarBraceInResolvedValue(t *testing.T) {
	got, _ := testSection(t)
	t.Setenv("SUBFLUX_PASS", "a${b")
	t.Setenv("SUBFLUX_NESTED", "${SUBFLUX_PASS}")
	t.Run("resolved value holding ${", func(t *testing.T) {
		s := loadText(t, "t: {a: '${SUBFLUX_PASS}', b: 'x-${SUBFLUX_NESTED}'}\n")
		if s.Mode() != ModeActive {
			t.Fatalf("mode %v, failure %v", s.Mode(), failurePaths(t, s))
		}
		a, _ := got.String("a")
		b, _ := got.String("b")
		if a != "a${b" || b != "x-${SUBFLUX_PASS}" {
			t.Errorf("a = %q, b = %q; want a${b and x-${SUBFLUX_PASS}", a, b)
		}
	})
	t.Run("reference outside SUBFLUX_ and unresolved name", func(t *testing.T) {
		t.Setenv("HOME", "/home/x")
		s := loadText(t, "t: {a: '${HOME}', b: '${SUBFLUX_MISSING}'}\n")
		errs, _ := s.Failure()
		if got := failurePaths(t, s); !slices.Equal(got, []string{"t.a", "t.b"}) {
			t.Fatalf("paths %v, want [t.a t.b]", got)
		}
		if !strings.Contains(errs[0].Message, "${HOME}") || !strings.Contains(errs[1].Message, "not set") {
			t.Errorf("messages %q", errs)
		}
	})
	t.Run("lowercase subflux name is outside the space", func(t *testing.T) {
		t.Setenv("SUBFLUX_lower", "x")
		if got := failurePaths(t, loadText(t, "t: {a: '${SUBFLUX_lower}'}\n")); !slices.Equal(got, []string{"t.a"}) {
			t.Errorf("paths %v, want [t.a]", got)
		}
	})
}

func TestExpandedDigitsStayStrings(t *testing.T) {
	got, _ := testSection(t)
	t.Setenv("SUBFLUX_KEY", "0123")
	t.Setenv("SUBFLUX_N", "42")
	s := loadText(t, "t:\n  a: '${SUBFLUX_KEY}'\n  s: ${SUBFLUX_KEY}\n  n: ${SUBFLUX_N}\n")
	if s.Mode() != ModeActive {
		t.Fatalf("mode %v, failure %v", s.Mode(), failurePaths(t, s))
	}
	a, _ := got.String("a")
	sec, _ := got.Secret("s")
	n, ok := got.Int("n")
	if a != "0123" || sec != "0123" || !ok || n != 42 {
		t.Errorf("a = %q, s = %q, n = %d %v; want 0123, 0123, 42", a, sec, n, ok)
	}
}
