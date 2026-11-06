package timing

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
)

const maxFuzzText = 256

// FuzzCleanMatchesOracle: the one-pass scan answers what applying each rule
// to a fixed point answers, for every combination of the text options, and
// a second pass changes nothing.
func FuzzCleanMatchesOracle(f *testing.F) {
	f.Add("Hello world", "[gunshot]", "♪ music ♪")
	f.Add("<i>styled</i>", "JOHN: Hi", "  spaces  ")
	f.Add("", "- dash", "normal text")
	f.Add("(a[b)c]", "<[x]i>keep", "JO[x]HN: hi")
	f.Add("<z[>]i>", "JOHN: [x] MARY: hi", "♪(♪)♪")
	f.Add("[a[b]c]", "(x(y)z)", "<b x>y> <i>>")
	f.Add("Hi (sighs)", "a [x]", "x ♪y♪")
	f.Fuzz(func(t *testing.T, t1, t2, t3 string) {
		if len(t1)+len(t2)+len(t3) > maxFuzzText {
			return // the reference is quadratic
		}
		text := strings.ToValidUTF8(t1+"\n"+t2+"\n"+t3, "\uFFFD")
		for k := range 8 {
			rules := textRules{hi: k&1 != 0, tags: k&2 != 0, blanks: k&4 != 0}
			got := applyRules(text, rules)
			if want := referenceRules(text, rules); got != want {
				t.Fatalf("applyRules(%q, %+v) = %q, reference %q", text, rules, got, want)
			}
			if again := applyRules(got, rules); again != got {
				t.Fatalf("applyRules not idempotent under %+v: %q -> %q -> %q", rules, text, got, again)
			}
		}
	})
}

// FuzzCleanIdempotent: Clean of a cleaned SRT or ASS document under the
// same options and tag writes the same bytes, for every option set.
func FuzzCleanIdempotent(f *testing.F) {
	f.Add("000000000</<b0>b>")
	f.Add("JOHN: [door] <i>Hi</i>\r\n\r\n♪")
	f.Add("{\\an8}(sighs)\n-")
	f.Add("[[sigh]] ok")
	f.Add(`{()\}`)
	f.Add(`a \(x)N b`)
	f.Add(`JO{\b1}HN: x\N  - \N♪a♪`)
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > maxFuzzText {
			return
		}
		text = strings.ToValidUTF8(text, "")
		srt := "1\n00:00:01,000 --> 00:00:02,000\n" + text + "\n\n2\n00:00:03,000 --> 00:00:04,000\nkept\n"
		ass := "[Script Info]\nScriptType: v4.00+\n\n[Events]\n" +
			"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,," + assLine.Replace(text) + "\n" +
			"Dialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,kept\n"
		for _, doc := range []string{srt, ass} {
			c, err := ParseCues([]byte(doc))
			if err != nil {
				continue
			}
			for k := range 64 {
				opts := CleanOptions{
					StripHI: k&1 != 0, StripTags: k&2 != 0, CleanWhitespace: k&4 != 0,
					RemoveEmpty: k&8 != 0, NormalizeUTF8: k&16 != 0, NormalizeLineEndings: k&32 != 0,
				}
				once, err := Clean(c, opts, language.Tag{})
				if err != nil {
					t.Fatalf("Clean(%q, %+v) = %v", doc, opts, err)
				}
				twice, err := Clean(once, opts, language.Tag{})
				if err != nil {
					t.Fatalf("Clean(Clean(%q), %+v) = %v", doc, opts, err)
				}
				if !bytes.Equal(once.doc, twice.doc) {
					t.Fatalf("Clean not idempotent under %+v: %q -> %q -> %q", opts, doc, once.doc, twice.doc)
				}
			}
		}
	})
}

// assLine keeps fuzz text on one Dialogue line.
var assLine = strings.NewReplacer("\r\n", `\N`, "\n", `\N`, "\r", `\N`)
