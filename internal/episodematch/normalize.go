package episodematch

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// romans and articles are the measured normaliser's tables
// (titlematch/tm.py norm_tokens), which TestAssignmentCorpus holds to.
var (
	romans   = map[string]string{"i": "1", "ii": "2", "iii": "3", "iv": "4", "v": "5", "vi": "6", "vii": "7", "viii": "8", "ix": "9", "x": "10"}
	articles = map[string]bool{"the": true, "a": true, "an": true, "le": true, "la": true, "les": true, "l": true, "der": true, "die": true, "das": true, "el": true, "los": true, "las": true, "un": true, "une": true}
)

var (
	partWordRe    = regexp.MustCompile(`(^|[^\p{L}\p{N}_])part` + pyspace + `+`)
	partParenRe   = regexp.MustCompile(`\(([\p{L}\p{N}_]{1,4})\)`)
	partTrailerRe = regexp.MustCompile(`([a-z]) (\p{Nd}|i{1,3})$`)
)

// pyspace is Python's str.isspace set, which the measured extractor's \s
// matched; Go's \s is ASCII only.
const pyspace = `[\t-\r\x1c-\x1f \x85\p{Z}]`

// normTitle is one title as the similarity reads it: its tokens less their
// part numbers, joined by single spaces, and the part numbers apart.
type normTitle struct {
	text  string
	runes []rune
	words []string // the distinct tokens of text, sorted
	parts []string // sorted, distinct
}

func normalize(s string) normTitle {
	var n normTitle
	toks := normTokens(s)
	kept := toks[:0:0]
	for _, t := range toks {
		if isPart(t) {
			n.parts = append(n.parts, t)
		} else {
			kept = append(kept, t)
		}
	}
	slices.Sort(n.parts)
	n.parts = slices.Compact(n.parts)
	n.text = strings.Join(kept, " ")
	n.runes = []rune(n.text)
	n.words = slices.Compact(slices.Sorted(slices.Values(kept)))
	return n
}

// normTokens folds case, accents and punctuation, reads "(1)", "Part 1", a
// trailing " 1" or " I" as one part token, and drops articles.
func normTokens(s string) []string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(s) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	s = strings.ToLower(b.String())
	s = strings.NewReplacer("&", " and ", "%", " percent ").Replace(s)
	s = partWordRe.ReplaceAllString(s, "${1}part")
	s = partParenRe.ReplaceAllString(s, " part$1 ")
	s = partTrailerRe.ReplaceAllString(strings.TrimFunc(s, unicode.IsSpace), "$1 part$2")
	fields := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	out := fields[:0]
	for _, t := range fields {
		if rest, ok := strings.CutPrefix(t, "part"); ok {
			if n, roman := romans[rest]; roman {
				t = "part" + n
			}
		}
		if !articles[t] {
			out = append(out, t)
		}
	}
	return out
}

func isPart(t string) bool {
	rest, ok := strings.CutPrefix(t, "part")
	return ok && rest != "" && !strings.ContainsFunc(rest, func(r rune) bool { return !unicode.Is(unicode.Nd, r) })
}

// similarity is the measured deterministic similarity (tm.py det_sim): 0
// when either title is empty or their part numbers differ, else the best of
// the Indel ratio, 0.96 of the token-set ratio and 0.95 of Jaro-Winkler.
func similarity(a, b *normTitle) float64 {
	if a.text == "" || b.text == "" {
		return 0
	}
	if len(a.parts) > 0 && len(b.parts) > 0 && !slices.Equal(a.parts, b.parts) {
		return 0
	}
	return max(indelRatio(a.runes, b.runes), 0.96*tokenSetRatio(a, b), 0.95*jaroWinkler(a.runes, b.runes))
}

// indelRatio is rapidfuzz's fuzz.ratio over 1: 1 - indel distance / total
// length, the indel distance being the length sum less twice the LCS.
func indelRatio(a, b []rune) float64 {
	if len(a)+len(b) == 0 {
		return 1
	}
	return 1 - float64(len(a)+len(b)-2*lcs(a, b))/float64(len(a)+len(b))
}

func lcs(a, b []rune) int {
	row := make([]int, len(b)+1)
	for _, ra := range a {
		diag := 0
		for j, rb := range b {
			up := row[j+1]
			switch {
			case ra == rb:
				row[j+1] = diag + 1
			case row[j] > row[j+1]:
				row[j+1] = row[j]
			}
			diag = up
		}
	}
	return row[len(b)]
}

// tokenSetRatio is rapidfuzz's fuzz.token_set_ratio over 1
// (https://github.com/rapidfuzz/RapidFuzz, fuzz_py.py): 1 when one token set
// holds the other, else the best ratio among the shared tokens and each
// side's shared-plus-own tokens.
func tokenSetRatio(a, b *normTitle) float64 {
	var sect, onlyA, onlyB []string
	for _, w := range a.words {
		if _, found := slices.BinarySearch(b.words, w); found {
			sect = append(sect, w)
		} else {
			onlyA = append(onlyA, w)
		}
	}
	for _, w := range b.words {
		if _, found := slices.BinarySearch(a.words, w); !found {
			onlyB = append(onlyB, w)
		}
	}
	if len(sect) > 0 && (len(onlyA) == 0 || len(onlyB) == 0) {
		return 1
	}
	ab, ba := []rune(strings.Join(onlyA, " ")), []rune(strings.Join(onlyB, " "))
	sectLen := len([]rune(strings.Join(sect, " ")))
	sep := 0
	if sectLen > 0 {
		sep = 1
	}
	sectAB, sectBA := sectLen+sep+len(ab), sectLen+sep+len(ba)
	dist := len(ab) + len(ba) - 2*lcs(ab, ba)
	result := 1 - float64(dist)/float64(sectAB+sectBA)
	if sectLen == 0 {
		return result
	}
	return max(result,
		1-float64(sep+len(ab))/float64(sectLen+sectAB),
		1-float64(sep+len(ba))/float64(sectLen+sectBA))
}

// jaroWinkler is rapidfuzz's JaroWinkler.similarity with prefix weight 0.1,
// whose prefix bonus applies only above a Jaro similarity of 0.7.
func jaroWinkler(a, b []rune) float64 {
	sim := jaro(a, b)
	if sim <= 0.7 {
		return sim
	}
	prefix := 0
	for prefix < min(4, len(a), len(b)) && a[prefix] == b[prefix] {
		prefix++
	}
	return sim + float64(prefix)*0.1*(1-sim)
}

func jaro(a, b []rune) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	bound := max(max(len(a), len(b))/2-1, 0)
	matchedA, matchedB := make([]bool, len(a)), make([]bool, len(b))
	common := 0
	for i, r := range a {
		for j := max(0, i-bound); j <= min(i+bound, len(b)-1); j++ {
			if !matchedB[j] && b[j] == r {
				matchedA[i], matchedB[j] = true, true
				common++
				break
			}
		}
	}
	if common == 0 {
		return 0
	}
	m := float64(common)
	return (m/float64(len(a)) + m/float64(len(b)) + (m-float64(transpositions(a, b, matchedA, matchedB)/2))/m) / 3
}

// transpositions counts the matched runes of a and b, taken in order, that
// differ.
func transpositions(a, b []rune, matchedA, matchedB []bool) int {
	n, k := 0, 0
	for i, r := range a {
		if !matchedA[i] {
			continue
		}
		for !matchedB[k] {
			k++
		}
		if r != b[k] {
			n++
		}
		k++
	}
	return n
}
