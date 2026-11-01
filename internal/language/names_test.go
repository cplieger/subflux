package language

import (
	"bytes"
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func nameTags(ns []Name) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Tag.String()
	}
	return out
}

func TestNamesHoldsNorwegian(t *testing.T) {
	got := nameTags(Names())
	want := []string{
		"nb", "nn", "no", "fil", "yue", "cnr",
		"pt-BR", "pt-PT", "es-419", "es-ES", "fr-CA", "fr-FR", "zh-Hans", "zh-Hant", "zh-Hant-HK", "sr-Latn", "sr-Cyrl",
	}
	for _, tag := range want {
		if !slices.Contains(got, tag) {
			t.Errorf("Names() lacks %s", tag)
		}
	}
	for _, tag := range []string{"zh", "bh", "sh", "tl"} {
		if slices.Contains(got, tag) {
			t.Errorf("Names() offers %s", tag)
		}
	}
}

func closeOf(t *testing.T, tag string) []string {
	t.Helper()
	for _, n := range Names() {
		if n.Tag.String() == tag {
			out := make([]string, len(n.Close))
			for i, c := range n.Close {
				out[i] = fmt.Sprintf("%s %s", c.Tag, classNames[c.Class-1])
			}
			return out
		}
	}
	t.Fatalf("Names() lacks %s", tag)
	return nil
}

func TestNamesSortedAndClose(t *testing.T) {
	ns := Names()
	if !slices.IsSortedFunc(ns, func(a, b Name) int { return cmp.Compare(a.English, b.English) }) {
		t.Error("Names() is not sorted by English name")
	}
	for _, n := range ns {
		if !slices.IsSortedFunc(n.Close, func(a, b CloseName) int {
			return cmp.Or(cmp.Compare(a.Class, b.Class), cmp.Compare(a.Closeness, b.Closeness), cmp.Compare(a.English, b.English))
		}) {
			t.Errorf("%s's close list is not sorted by class, closeness and name", n.Tag)
		}
		for _, c := range n.Close {
			if c.Class == ClassUnrelated || c.Tag == n.Tag {
				t.Errorf("%s's close list holds %s as %d", n.Tag, c.Tag, c.Class)
			}
		}
	}
	for tag, want := range map[string][]string{
		"pt-BR":   {"pt substitute", "pt-PT substitute"},
		"zh-Hant": {"zh-Hant-HK exact", "zh-Hans manual_only"},
	} {
		if got := closeOf(t, tag); !slices.Equal(got, want) {
			t.Errorf("%s close = %v, want %v", tag, got, want)
		}
	}
}

func TestNamesDeepCopy(t *testing.T) {
	first := Names()
	i := slices.IndexFunc(first, func(n Name) bool { return n.Tag.String() == "pt-BR" })
	first[i].Close[0].English = "changed"
	first[i].Close = nil
	first[0].English = "changed"
	again := Names()
	if again[0].English == "changed" || len(again[i].Close) == 0 || again[i].Close[0].English == "changed" {
		t.Error("an edit to one Names() answer reached the next")
	}
}

// TestNamesGolden pins every pickable entry and its close list; regenerate
// with UPDATE_GOLDEN=1 go test -run TestNamesGolden ./internal/language/.
func TestNamesGolden(t *testing.T) {
	var b bytes.Buffer
	ns := Names()
	for _, n := range ns {
		fmt.Fprintf(&b, "%s\t%s\n", n.Tag, n.English)
		for _, c := range n.Close {
			fmt.Fprintf(&b, "\t%s\t%s\t%s\n", c.Tag, classNames[c.Class-1], closenessNames[c.Closeness-1])
		}
	}
	if len(ns) != 195 {
		t.Errorf("Names() has %d entries, want 195 at CLDR 48", len(ns))
	}
	path := filepath.Join("testdata", "names.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("Names() differs from %s; if the change is intended, regenerate with UPDATE_GOLDEN=1 go test -run TestNamesGolden ./internal/language/", path)
	}
}

func TestEnglishName(t *testing.T) {
	for raw, want := range map[string]string{
		"pt-AO":      "Portuguese (Angola)",
		"zh-Hant-MO": "Chinese (Traditional, Macao SAR China)",
		"sr-Latn":    "Serbian (Latin)",
		"":           "",
	} {
		if got := EnglishName(mustParse(t, raw)); got != want {
			t.Errorf("EnglishName(%q) = %q, want %q", raw, got, want)
		}
	}
	for _, n := range Names() {
		if n.English == n.Tag.String() || strings.TrimSpace(n.English) == "" {
			t.Errorf("pickable %s has no English name (%q)", n.Tag, n.English)
		}
	}
}
