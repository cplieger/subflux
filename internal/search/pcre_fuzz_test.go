package search

import (
	"testing"

	"subflux/internal/search/release"
)

func FuzzPcreTranslate(f *testing.F) {
	seeds := []string{
		"", "hello", "(?=foo)bar", "(?!baz)qux",
		"(?<=abc)def", "(?<!xyz)ghi", "foo|bar",
		"[a-z]+", "(", ")", "(?=", "[",
		"a{2,5}", "\\d+\\.\\d+", "(?i)test",
		"(?:group)", "(?<=a(?=b))c",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		p, err := release.CompilePCRE(input)
		if err != nil {
			return
		}
		p.MatchString("")
		p.MatchString("test string")
		p.FindStringSubmatch("test")
	})
}

func FuzzCompilePCRE(f *testing.F) {
	// Seeds: valid PCRE with lookahead, lookbehind, alternation, character class,
	// empty string, unbalanced parens, nested groups, PCRE-only flags.
	seeds := []string{
		"",
		"hello",
		"(?=foo)bar",
		"(?!baz)qux",
		"(?<=abc)def",
		"(?<!xyz)ghi",
		"foo|bar|baz",
		"(?=a)b|(?!c)d",
		"[a-z]+",
		"[^0-9]",
		"(foo)(bar)",
		"((nested))",
		"(?i)test",
		"(?:group)",
		"a{2,5}",
		"\\d+\\.\\d+",
		"(",
		")",
		"(?=",
		"[",
		"(?<=a(?=b))c",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		p, err := release.CompilePCRE(input)
		if err != nil {
			return
		}
		// When compilation succeeds, exercise the compiled pattern.
		p.MatchString("")
		p.MatchString("test")
		p.FindStringSubmatch("")
		p.FindStringSubmatch("test")
	})
}
