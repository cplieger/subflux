package crosslang

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ellipsis = "..."

// Anchor represents language-independent features extracted from a subtitle cue.
type Anchor struct {
	Punctuation string
	Numbers     []string
	ProperNouns []string
	Cognates    []string
	WordCount   int
	CharLen     int
}

// internal alias for use within the package
type anchor = Anchor

var numberRe = regexp.MustCompile(`\d[\d.,]*\d|\d`)
var subTagRe = regexp.MustCompile(`</?[a-zA-Z][^>]*>|\{\\[^}]*\}`)

func stripSubTags(text string) string {
	return subTagRe.ReplaceAllString(text, "")
}

// ExtractAnchors extracts language-independent features from cue text.
func ExtractAnchors(text string) Anchor {
	return extractAnchors(text)
}

func extractAnchors(text string) anchor {
	cleaned := stripSubTags(text)
	trimmed := strings.TrimSpace(cleaned)
	var a anchor
	a.CharLen = utf8.RuneCountInString(trimmed)
	a.Numbers = extractNumbers(cleaned)
	extractWords(&a, cleaned)
	a.Punctuation = terminalPunctuation(trimmed)
	return a
}

func extractNumbers(text string) []string {
	var nums []string
	for _, m := range numberRe.FindAllString(text, -1) {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, m)
		if digits != "" {
			nums = append(nums, digits)
		}
	}
	return nums
}

func extractWords(a *anchor, cleaned string) {
	for line := range strings.SplitSeq(cleaned, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimSpace(line)
		words := strings.Fields(line)
		a.WordCount += len(words)
		for i, w := range words {
			atStart := i == 0 || (i > 0 && endsWithSentence(words[i-1]))
			classifyWord(a, w, atStart)
		}
	}
}

func classifyWord(a *anchor, w string, atSentenceStart bool) {
	clean := strings.TrimRightFunc(w, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(clean) < 2 {
		return
	}
	runes := []rune(clean)
	if unicode.IsUpper(runes[0]) && unicode.IsLetter(runes[0]) && !atSentenceStart {
		a.ProperNouns = append(a.ProperNouns, clean)
	}
	cogWord := stripWordPunct(clean)
	if utf8.RuneCountInString(cogWord) >= 4 && isLatinWord(cogWord) {
		a.Cognates = append(a.Cognates, strings.ToLower(cogWord))
	}
}

func terminalPunctuation(trimmed string) string {
	if trimmed == "" {
		return ""
	}
	if strings.HasSuffix(trimmed, "…") {
		return ellipsis
	}
	last := trimmed[len(trimmed)-1]
	switch last {
	case '?':
		return "?"
	case '!':
		return "!"
	case '.':
		if strings.HasSuffix(trimmed, ellipsis) {
			return ellipsis
		}
		return "."
	}
	return ""
}

func endsWithSentence(w string) bool {
	if w == "" {
		return false
	}
	last := w[len(w)-1]
	return last == '.' || last == '?' || last == '!'
}

func isLatinWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
		if r > 0x024F && !unicode.Is(unicode.Latin, r) {
			return false
		}
	}
	return true
}

func stripWordPunct(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '\'' || r == '\u2019' || r == '\u2018' {
			return -1
		}
		return r
	}, s)
}

// HasAnyAnchor returns true if a cue has at least one distinctive feature.
func HasAnyAnchor(a *anchor) bool {
	return hasAnyAnchor(a)
}

func hasAnyAnchor(a *anchor) bool {
	return len(a.Numbers) > 0 || len(a.ProperNouns) > 0 || len(a.Cognates) > 0
}

type anchorScoreConfig struct {
	NumberWeight      float64
	NumberPenalty     float64
	ProperNounWeight  float64
	ProperNounPenalty float64
	CognateWeight     float64
	PunctWeight       float64
	MinScore          float64
	PositionBlend     float64
}

var defaultAnchorScoreConfig = anchorScoreConfig{
	NumberWeight:      0.40,
	NumberPenalty:     0.20,
	ProperNounWeight:  0.35,
	ProperNounPenalty: 0.15,
	CognateWeight:     0.20,
	PunctWeight:       0.05,
	MinScore:          0.05,
	PositionBlend:     0.1,
}

// AnchorMatchScore computes similarity between two anchors.
func AnchorMatchScore(a, b *anchor) float64 {
	return anchorMatchScore(a, b)
}

func anchorMatchScore(a, b *anchor) float64 {
	cfg := defaultAnchorScoreConfig
	var score, maxScore float64

	s, m := featureScore(a.Numbers, b.Numbers, cfg.NumberWeight, cfg.NumberPenalty, countShared)
	score += s
	maxScore += m

	s, m = featureScore(a.ProperNouns, b.ProperNouns, cfg.ProperNounWeight, cfg.ProperNounPenalty, countSharedFold)
	score += s
	maxScore += m

	s, m = featureScore(a.Cognates, b.Cognates, cfg.CognateWeight, 0, countCognates)
	score += s
	maxScore += m

	if (a.Punctuation == "?" || a.Punctuation == "!") &&
		a.Punctuation == b.Punctuation {
		score += cfg.PunctWeight
	}
	maxScore += cfg.PunctWeight

	if maxScore == 0 {
		return 0
	}
	return score / maxScore
}

func featureScore(a, b []string, matchWeight, penaltyWeight float64, matcher func([]string, []string) int) (score, maxScore float64) {
	if len(a) > 0 && len(b) > 0 {
		shared := matcher(a, b)
		total := max(len(a), len(b))
		return matchWeight * float64(shared) / float64(total), matchWeight
	}
	if len(a) > 0 || len(b) > 0 {
		return 0, penaltyWeight
	}
	return 0, 0
}

// IsCognate returns true if two words are likely cognates.
func IsCognate(a, b string) bool { return isCognate(a, b) }

// EditDistance computes the Levenshtein distance between two strings.
func EditDistance(a, b string) int { return editDistance(a, b) }

// CountShared counts exact string matches between two slices.
func CountShared(a, b []string) int { return countShared(a, b) }

// CountCognates counts cognate pairs between two word lists.
func CountCognates(a, b []string) int { return countCognates(a, b) }

func editDistance(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	la := len(ra)
	lb := len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func isCognate(a, b string) bool {
	la := utf8.RuneCountInString(a)
	lb := utf8.RuneCountInString(b)
	maxLen := max(la, lb)
	if maxLen < 4 {
		return false
	}
	if float64(min(la, lb))/float64(maxLen) < 0.5 {
		return false
	}
	dist := editDistance(a, b)
	threshold := max(maxLen*3/10, 1)
	return dist <= threshold
}

func countCognates(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	used := make([]bool, len(b))
	count := 0
	for _, wa := range a {
		for j, wb := range b {
			if used[j] {
				continue
			}
			if wa == wb || isCognate(wa, wb) {
				used[j] = true
				count++
				break
			}
		}
	}
	return count
}

func countShared(a, b []string) int {
	freq := make(map[string]int, len(b))
	for _, s := range b {
		freq[s]++
	}
	count := 0
	for _, s := range a {
		if freq[s] > 0 {
			freq[s]--
			count++
		}
	}
	return count
}

// CountSharedFold counts case-insensitive matches between two slices.
func CountSharedFold(a, b []string) int { return countSharedFold(a, b) }

// IsLatinWord returns true if the string contains only Latin letters.
func IsLatinWord(s string) bool { return isLatinWord(s) }

func countSharedFold(a, b []string) int {
	freq := make(map[string]int, len(b))
	for _, s := range b {
		freq[strings.ToLower(s)]++
	}
	count := 0
	for _, s := range a {
		key := strings.ToLower(s)
		if freq[key] > 0 {
			freq[key]--
			count++
		}
	}
	return count
}
