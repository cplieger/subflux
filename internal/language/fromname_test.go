package language

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/langtag/v3/names"
)

type arrName struct{ name, want string }

func arrNames(t testing.TB) []arrName {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "arr-languages.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []arrName
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, want, ok := strings.Cut(line, "\t")
		if !ok || name == "" || want == "" {
			t.Fatalf("arr-languages.txt line %q is not name<TAB>tag", line)
		}
		out = append(out, arrName{name, want})
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFromNameArrNames(t *testing.T) {
	rows := arrNames(t)
	if len(rows) != 64 {
		t.Fatalf("arr-languages.txt holds %d names, want the recorded 64", len(rows))
	}
	for i, r := range rows {
		if i > 0 && rows[i-1].name >= r.name {
			t.Errorf("arr-languages.txt: %q follows %q; want sorted, unique names", r.name, rows[i-1].name)
		}
		got, ok := FromName(r.name)
		switch {
		case r.want == "-" && (ok || got != Tag{}):
			t.Errorf("FromName(%q) = %q, %v; want false", r.name, got, ok)
		case r.want != "-" && (!ok || got.String() != r.want):
			t.Errorf("FromName(%q) = %q, %v; want %s", r.name, got, ok, r.want)
		}
	}
}

func TestFromName(t *testing.T) {
	for name, want := range map[string]string{
		"Tagalog":                 "fil",
		"Spanish (Latino)":        "es-419",
		"Flemish":                 "nl-BE",
		"Bengali":                 "bn",
		"Bangla":                  "bn",
		"Portuguese (Brazil)":     "pt-BR",
		"portuguese (brazil)":     "pt-BR",
		"Chinese (Traditional)":   "zh-Hant",
		"Norwegian":               "no",
		"Norwegian Bokmål":        "nb",
		"Serbo-Croatian":          "sr-Latn",
		"  French  ":              "fr",
		"FRENCH":                  "fr",
		"fre":                     "fr",
		"ger":                     "de",
		"eng":                     "en",
		"pt-BR":                   "pt-BR",
		"Chinese":                 "zh",
		"Serbian":                 "sr",
		"Spanish (Latin America)": "es-419",
	} {
		if got, ok := FromName(name); !ok || got.String() != want {
			t.Errorf("FromName(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"", "   ", "Unknown", "any", "ORIGINAL", "und", "lang=a:b", "Klingonese", strings.Repeat("a", 65), " " + strings.Repeat("x", 64) + " "} {
		if got, ok := FromName(name); ok || got != (Tag{}) {
			t.Errorf("FromName(%q) = %q, %v; want false", name, got, ok)
		}
	}
	overLong := "en-x-aaaaaaaa-bbbbbbbb-cccccccc-dddddddd-eeeeeeee-ffffffff-gggggggg"
	if _, ok := Parse(overLong); !ok || len(overLong) <= 64 {
		t.Fatalf("the bound fixture %q (%d bytes) must parse and exceed 64 bytes", overLong, len(overLong))
	}
	if got, ok := FromName(overLong); ok {
		t.Errorf("FromName of a %d-byte tag = %q, true; want false past the 64-byte bound", len(overLong), got)
	}
	if got, ok := FromName(" en" + strings.Repeat(" ", 70)); !ok || got.String() != "en" {
		t.Errorf("FromName of en with trailing spaces = %q, %v; want en: the bound applies after trimming", got, ok)
	}
}

func TestLanguageNameIndexSharedName(t *testing.T) {
	for _, c := range []struct {
		name  string
		langs []names.Language
		want  string
	}{
		{"three-letter code listed first", []names.Language{{Code: "deu", Name: "Shared"}, {Code: "fr", Name: "Shared"}}, "fr"},
		{"two-letter code listed first", []names.Language{{Code: "fr", Name: "Shared"}, {Code: "deu", Name: "Shared"}}, "fr"},
		{"two three-letter codes", []names.Language{{Code: "deu", Name: "Shared"}, {Code: "fra", Name: "Shared"}}, "de"},
	} {
		if got := languageNameIndex(c.langs)["shared"]; got.String() != c.want {
			t.Errorf("%s: the shared name maps to %q, want %q", c.name, got, c.want)
		}
	}
	if _, ok := languageNameIndex([]names.Language{{Code: "pb", Name: "Refused"}})["refused"]; ok {
		t.Error("a code Parse refuses was indexed")
	}
}
