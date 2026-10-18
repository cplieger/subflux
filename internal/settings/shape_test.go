package settings

import (
	"slices"
	"testing"
)

func TestLoadRefusesShapes(t *testing.T) {
	cases := []struct {
		name, text string
		paths      []string
	}{
		{"tagged reference", "t: {a: !!str '${SUBFLUX_X}'}\n", []string{"t.a", "t.a"}},
		{"forged plain mark", "t: {s: !plain s3cret}\n", []string{"t.s"}},
		{"any explicit tag", "t: {n: !!int 5}\n", []string{"t.n"}},
		{"tagged key", "t: {!!str a: x}\n", []string{"t.a"}},
		{"key held twice", "t: {a: x, a: y}\n", []string{"t.a"}},
		{"section held twice", "t: {a: x}\nt: {b: y}\n", []string{"t"}},
		{"anchor and alias across entries", "t: {a: &k x, b: *k}\n", []string{"t.a", "t.b"}},
		{"merge key", "t: {a: &base x}\nserver:\n  <<: {log_level: debug}\n", []string{"t.a", "server.<<"}},
		{"alias into an enum field", "t: {a: &l debug}\nserver: {log_level: *l}\n", []string{"t.a", "server.log_level"}},
		{"alias of a secret into a string", "t: {s: &k s3cret, a: *k}\n", []string{"t.s", "t.a"}},
		{"anchored section", "t: &sec {a: x}\n", []string{"t"}},
		{"alias in a list", "t: {a: &h x}\nserver: {allowed_hosts: [*h]}\n", []string{"t.a", "server.allowed_hosts[0]"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, calls := testSection(t)
			s := loadText(t, c.text)
			if got := failurePaths(t, s); !slices.Equal(got, c.paths) {
				t.Errorf("paths %v, want %v", got, c.paths)
			}
			if *calls != 0 {
				t.Errorf("section t parsed %d times over a refused shape", *calls)
			}
		})
	}
	t.Run("top-level merge key fails every section", func(t *testing.T) {
		s := loadText(t, "<<: {server: {allowed_hosts: [nas.lan]}}\n")
		if got := failurePaths(t, s); !slices.Equal(got, []string{"<<"}) {
			t.Errorf("paths %v, want [<<]", got)
		}
		if srv, ok := s.Server(); ok || admits(srv, "nas.lan") || !srv.Hosts().Active() {
			t.Errorf("Server() = %v; want false, denying every Host", ok)
		}
	})
	t.Run("a refused shape leaves other sections parsing", func(t *testing.T) {
		got, calls := testSection(t)
		s := loadText(t, "t: {a: kept}\nx: &a 1\n")
		if paths := failurePaths(t, s); !slices.Equal(paths, []string{"x", "x"}) {
			t.Errorf("paths %v, want [x x] (shape, unknown section)", paths)
		}
		if a, _ := got.String("a"); *calls != 1 || a != "kept" {
			t.Errorf("section t: %d parses, a = %q", *calls, a)
		}
	})
}

func TestLoadLeavesStoredDocumentUnexpanded(t *testing.T) {
	testSection(t)
	t.Setenv("SUBFLUX_KEY", "secret")
	root, msg := decodeDocument([]byte("t: {s: '${SUBFLUX_KEY}', a: &k x}\n"))
	if msg != "" {
		t.Fatal(msg)
	}
	before := deepCopy(root)
	parse(root, Registries{}, fileState{})
	if root.Content[1].Content[1].Value != "${SUBFLUX_KEY}" || root.Content[1].Content[1].Tag != before.Content[1].Content[1].Tag {
		t.Errorf("parse changed the stored document: %+v", root.Content[1].Content[1])
	}
}

func TestDeepCopySharesNothing(t *testing.T) {
	root, msg := decodeDocument([]byte("a: &k {b: c}\nd: *k\n"))
	if msg != "" {
		t.Fatal(msg)
	}
	c := deepCopy(root)
	c.Content[1].Content[1].Value = "changed"
	if root.Content[1].Content[1].Value != "c" {
		t.Error("the copy shares a node with the original")
	}
	if c.Content[3].Alias != c.Content[1] {
		t.Error("the copy's alias does not point at the copy of its anchor")
	}
}
