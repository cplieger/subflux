package search

import (
	"errors"
	"testing"

	"github.com/cplieger/subflux/internal/search/release"
)

// FuzzCompilePCRE pins the compile boundary of the release PCRE layer
// (spec subflux-release-parse-fidelity R4.3): CompilePCRE never panics,
// every failure is a typed *release.CompileError (typed-error stability),
// and a successfully compiled pattern's match paths never panic. Seeds
// merge the former FuzzPcreTranslate corpus and the study's adversarial
// shapes (including the BluRay class-(h) witness).
func FuzzCompilePCRE(f *testing.F) {
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
		// Adversarial corpus shapes (study appendix).
		`\b(?:BluRay|Blu-Ray|HD-?DVD|BDMux|BD(?!$))\b`,
		`-([a-z0-9]+(-[a-z0-9]+)?(?!.+?(?:480p|576p|720p|1080p|2160p)))(?:\b|[-._ ]|$)|[-._ ]\[([a-z0-9]+)\]$`,
		`\b(HDR10(?=[+]|P(lus)?))`,
		`(?:a(?=b))*`,
		`(a)\1`,
		`(?>atomic)`,
		`a*+`,
		`(?=x)?`,
		`x{0,2}(?!y)`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		p, err := release.CompilePCRE(input)
		if err != nil {
			var ce *release.CompileError
			if !errors.As(err, &ce) {
				t.Fatalf("CompilePCRE(%q) error %v (%T) is not a *release.CompileError", input, err, err)
			}
			if ce.Error() == "" {
				t.Fatalf("CompilePCRE(%q) CompileError has empty message", input)
			}
			return
		}
		// When compilation succeeds, exercise the compiled pattern.
		p.MatchString("")
		p.MatchString("test string")
		p.FindStringSubmatch("")
		p.FindStringSubmatch("test")
	})
}
