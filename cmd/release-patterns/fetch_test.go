package main

import (
	"errors"
	"slices"
	"testing"
)

func TestExtractSymbol(t *testing.T) {
	const src = `
// Regex Commented = new(@"no");
/* HashSet<string> Block = new() { "no" }; */
private static readonly Regex Quoted = new(@"a""b\d", RegexOptions.IgnoreCase | RegexOptions.Compiled);
private static readonly Regex Escaped = new Regex("c\\d\u00e9", RegexOptions.None);
internal static readonly RegexReplace Clean = new(@"-x$", string.Empty, RegexOptions.IgnoreCase);
internal static readonly RegexReplace Literal = new(@"y", "z", RegexOptions.IgnoreCase);
private static readonly Regex Joined = new(@"^.+?" + Other, RegexOptions.IgnoreCase);
private static readonly HashSet<string> Set = new HashSet<string>() { ".par2", ".nzb" };
private static readonly Dictionary<string, Quality> Keys = new(StringComparer.OrdinalIgnoreCase)
{
    // Unknown
    { ".webm", Quality.Unknown },
    { ".m2ts", Quality.Bluray720p },
};
public enum Source { UNKNOWN = 0, CAM, DVD = 5, TV }
`
	t.Run("verbatim string and options", func(t *testing.T) {
		s, err := extractSymbol([]byte(src), "Quoted", "regex")
		if err != nil || s.pattern != `a"b\d` || !slices.Equal(s.options, []string{"IgnoreCase", "Compiled"}) {
			t.Errorf("got %+v, %v", s, err)
		}
	})
	t.Run("regular string escapes", func(t *testing.T) {
		s, err := extractSymbol([]byte(src), "Escaped", "regex")
		if err != nil || s.pattern != `c\dé` {
			t.Errorf("got %+v, %v", s, err)
		}
	})
	t.Run("replace with string.Empty", func(t *testing.T) {
		s, err := extractSymbol([]byte(src), "Clean", "replace")
		if err != nil || s.pattern != `-x$` || s.replacement != "" {
			t.Errorf("got %+v, %v", s, err)
		}
	})
	t.Run("replace with a literal", func(t *testing.T) {
		s, err := extractSymbol([]byte(src), "Literal", "replace")
		if err != nil || s.replacement != "z" {
			t.Errorf("got %+v, %v", s, err)
		}
	})
	t.Run("a concatenated pattern is refused", func(t *testing.T) {
		if _, err := extractSymbol([]byte(src), "Joined", "regex"); err == nil {
			t.Errorf("no error")
		}
	})
	t.Run("hash set", func(t *testing.T) {
		s, err := extractSymbol([]byte(src), "Set", "list")
		if err != nil || !slices.Equal(s.values, []string{".par2", ".nzb"}) {
			t.Errorf("got %+v, %v", s, err)
		}
	})
	t.Run("dictionary keys", func(t *testing.T) {
		s, err := extractSymbol([]byte(src), "Keys", "list")
		if err != nil || !slices.Equal(s.values, []string{".webm", ".m2ts"}) {
			t.Errorf("got %+v, %v", s, err)
		}
	})
	t.Run("enum values count on", func(t *testing.T) {
		s, err := extractSymbol([]byte(src), "Source", "enum")
		want := []enumMember{{"UNKNOWN", 0}, {"CAM", 1}, {"DVD", 5}, {"TV", 6}}
		if err != nil || !slices.Equal(s.members, want) {
			t.Errorf("got %+v, %v", s, err)
		}
	})
	for _, name := range []string{"Commented", "Block"} {
		t.Run("a commented declaration is absent: "+name, func(t *testing.T) {
			if _, err := extractSymbol([]byte(src), name, "regex"); !errors.Is(err, errRenamed) {
				t.Errorf("got %v, want renamed or removed", err)
			}
		})
	}
	t.Run("a declaration of another type is absent", func(t *testing.T) {
		if _, err := extractSymbol([]byte(src), "Set", "regex"); !errors.Is(err, errRenamed) {
			t.Errorf("got %v, want renamed or removed", err)
		}
	})
	t.Run("raw string literals are refused", func(t *testing.T) {
		if _, err := extractSymbol([]byte(`Regex R = new("""x""");`), "R", "regex"); err == nil {
			t.Errorf("no error")
		}
	})
}
