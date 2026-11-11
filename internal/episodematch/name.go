package episodematch

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/cplieger/subflux/internal/epmarker"
)

// Name is what one member or release name says about its episode.
type Name struct {
	// Title is the episode title the name carries, "" when none.
	Title string
	// Claims are the episodes the name states, ranges expanded; nil when
	// none.
	Claims []epmarker.Marker
	// Special is set for a season 0 claim, an episode 0 claim (x00, E00) or
	// the word "Special".
	Special bool
	// Bare marks Claims read from a bare leading number under the pack's
	// season, which the name itself does not state.
	Bare bool
}

// ReadPack reads every member name of one pack by its base name. season is
// the pack's season, the one its search asked for: a name with no claim
// states its bare leading number under it, marked Bare, but only when no
// member claims a marker (epmarker.BareEpisode's precondition)
// and season is 0 or more.
func ReadPack(names []string, season int) []Name {
	out := make([]Name, len(names))
	marked := false
	for i, n := range names {
		out[i] = ReadName(n)
		marked = marked || len(out[i].Claims) > 0
	}
	if marked || season < 0 {
		return out
	}
	for i, n := range names {
		if ep, ok := epmarker.BareEpisode(baseName(n)); ok {
			out[i].Claims = []epmarker.Marker{{Season: season, Episode: ep}}
			out[i].Bare = true
			out[i].Special = out[i].Special || season == 0 || ep == 0
		}
	}
	return out
}

// ReadName reads one name by its base name, with no bare-number reading.
func ReadName(name string) Name {
	base := baseName(name)
	n := Name{Claims: epmarker.Claims(base), Title: extractTitle(base)}
	for _, c := range n.Claims {
		n.Special = n.Special || c.Season == 0 || c.Episode == 0
	}
	n.Special = n.Special || specialRe.MatchString(base)
	return n
}

var specialRe = regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}])special([^\p{L}\p{N}]|$)`)

// baseName is the last element of a path in either separator: archive member
// names carry the folders they were packed under.
func baseName(name string) string {
	return name[strings.LastIndexAny(name, `/\`)+1:]
}

// The measured extractor's patterns (titlematch/tm.py extract_title), held
// to by TestAssignmentCorpus. Its lookarounds have no RE2 form: tagRe
// consumes the character its lookahead tested, which keeps the match's start
// exact, the only position read; the marker and number forms are scanned by
// hand.
var (
	extRe       = regexp.MustCompile(`(?i)(\.(srt|ass|ssa|sub|vtt|mkv|mp4|avi|txt))+$`)
	langSuffRe  = regexp.MustCompile(`(?i)(\.(eng|en|english|hi|sdh|cc|forced|fre|fr|ger|de|spa|es))+$`)
	bracketRe   = regexp.MustCompile(`\[[^\]]*\]`)
	tagRe       = regexp.MustCompile(`(?i)[ ._\-\[\(](?:\d{3,4}p|2160p|4k|hdtv|web[ ._-]?dl|webrip|web|bluray|blu-ray|bdrip|brrip|dvdrip|dsnp|amzn|nf|hulu|remux|x26[45]|h[ ._]?26[45]|hevc|av1|xvid|ddp?\d?[ ._]?\d?|eac3|aac|dts|opus|flac|10bit|dual[ ._]audio|multisubs|multi[ ._-]subs|proper|repack|internal)(?:[^a-z]|$)`)
	groupTailRe = regexp.MustCompile(`\.[\p{L}\p{N}_]+-[\p{L}\p{N}_]+$`)
	groupRe     = regexp.MustCompile(`-[A-Za-z0-9]+$`)
	parenTagRe  = regexp.MustCompile(`(?i)\((?:[^)]*(?:\d{3,4}p|web|bluray|hevc|x26)[^)]*)\)`)
	dashGroupRe = regexp.MustCompile(pyspace + `+-` + pyspace + `*[A-Za-z0-9]+` + pyspace + `*$`)
	leadRe      = regexp.MustCompile(`^[\t-\r\x1c-\x1f \x85\p{Z}\])._~-]*(?:\(\p{Nd}+\))?[\t-\r\x1c-\x1f \x85\p{Z}._~-]*`)
	leadNumRe   = regexp.MustCompile(`^\p{Nd}{1,4}` + pyspace + `+-` + pyspace + `+`)
)

// extractTitle is the episode title a base name carries after its episode
// marker, else after its last " - NN", else after its first bare number, or
// "" when the rest holds no letter.
func extractTitle(base string) string {
	base = extRe.ReplaceAllString(base, "")
	base = langSuffRe.ReplaceAllString(base, "")
	end, ok := markerEnd(base)
	if !ok {
		end, ok = lastDashNumberEnd(base)
	}
	if !ok {
		end, ok = bareNumberEnd(base)
	}
	if !ok {
		return ""
	}
	rest := base[end:]
	tagged := strings.Contains(rest, "[")
	rest = bracketRe.ReplaceAllString(rest, " ")
	if loc := tagRe.FindStringIndex(rest); loc != nil {
		rest, tagged = rest[:loc[0]], true
	}
	if groupTailRe.MatchString(rest) {
		rest = groupRe.ReplaceAllString(rest, "")
	}
	if strings.Count(rest, ".") >= 2 && !strings.Contains(strings.TrimFunc(rest, unicode.IsSpace), " ") {
		rest = strings.ReplaceAll(rest, ".", " ")
	}
	rest = parenTagRe.ReplaceAllString(rest, " ")
	if tagged {
		rest = dashGroupRe.ReplaceAllString(rest, "")
	}
	rest = leadRe.ReplaceAllString(rest, "")
	rest = leadNumRe.ReplaceAllString(rest, "")
	rest = strings.Trim(rest, " ._-~")
	if !strings.ContainsFunc(rest, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsNumber(r) && !unicode.Is(unicode.Nd, r)
	}) {
		return ""
	}
	return rest
}

// markerEnd ends the first S##E## marker (an optional "-E##", ".E#" or "E#"
// second episode included) or ##x## token, as the extractor's MARKER reads
// them.
func markerEnd(s string) (int, bool) {
	for i := range len(s) {
		if end, ok := seasonEpisodeAt(s, i); ok {
			return end, true
		}
		if end, ok := crossAt(s, i); ok {
			return end, true
		}
	}
	return 0, false
}

func seasonEpisodeAt(s string, i int) (int, bool) {
	if s[i] != 'S' && s[i] != 's' {
		return 0, false
	}
	j := digitsEnd(s, i+1)
	if n := j - i - 1; n < 1 || n > 4 {
		return 0, false
	}
	k := j
	for k < len(s) && strings.IndexByte(" ._-", s[k]) >= 0 {
		k++
	}
	if k-j > 3 || k >= len(s) || (s[k] != 'E' && s[k] != 'e') {
		return 0, false
	}
	p := digitsEnd(s, k+1)
	if n := p - k - 1; n < 1 || n > 4 {
		return 0, false
	}
	return secondEpisodeEnd(s, p), true
}

// secondEpisodeEnd ends an optional second episode after a marker ending at
// p: "-E##" or "-##", ".E#" or "E#".
func secondEpisodeEnd(s string, p int) int {
	switch {
	case p >= len(s):
	case s[p] == '-':
		q := p + 1
		if q < len(s) && isE(s[q]) {
			q++
		}
		if n := digitsEnd(s, q) - q; n == 2 || n == 3 {
			return q + n
		}
	case s[p] == '.' && p+1 < len(s) && isE(s[p+1]):
		return episodeDigitsEnd(s, p+2, p)
	case isE(s[p]):
		return episodeDigitsEnd(s, p+1, p)
	}
	return p
}

// episodeDigitsEnd ends one to three digits at i, else returns fallback.
func episodeDigitsEnd(s string, i, fallback int) int {
	if n := digitsEnd(s, i) - i; n >= 1 && n <= 3 {
		return i + n
	}
	return fallback
}

func isE(b byte) bool { return b == 'E' || b == 'e' }

func crossAt(s string, i int) (int, bool) {
	if i > 0 && isASCIIAlnum(s[i-1]) {
		return 0, false
	}
	j := digitsEnd(s, i)
	if n := j - i; n < 1 || n > 2 || j >= len(s) || (s[j] != 'x' && s[j] != 'X') {
		return 0, false
	}
	if n := digitsEnd(s, j+1) - j - 1; n == 2 || n == 3 {
		return j + 1 + n, true
	}
	return 0, false
}

// lastDashNumberEnd ends the last " - NN" (an optional vN after it) followed
// by one of " ._[(" or the end.
func lastDashNumberEnd(s string) (int, bool) {
	end, found := 0, false
	for i := 0; i+3 <= len(s); {
		if s[i:i+3] == " - " {
			if e, ok := numberAt(s, i+3, " ._[("); ok {
				end, found, i = e, true, e
				continue
			}
		}
		i++
	}
	return end, found
}

// bareNumberEnd ends the first number at the start or after one of " ._-",
// followed by one of " ._-[(" or the end.
func bareNumberEnd(s string) (int, bool) {
	for i := range len(s) {
		if i == 0 {
			if e, ok := numberAt(s, 0, " ._-[("); ok {
				return e, true
			}
		}
		if strings.IndexByte(" ._-", s[i]) >= 0 {
			if e, ok := numberAt(s, i+1, " ._-[("); ok {
				return e, true
			}
		}
	}
	return 0, false
}

// numberAt reads one to four digits at i, an optional "v" and digit, and
// requires one of follow, or the end, after them.
func numberAt(s string, i int, follow string) (int, bool) {
	p := digitsEnd(s, i)
	if n := p - i; n < 1 || n > 4 {
		return 0, false
	}
	if p+1 < len(s) && s[p] == 'v' && isDigit(s[p+1]) {
		p += 2
	}
	if p < len(s) && strings.IndexByte(follow, s[p]) < 0 {
		return 0, false
	}
	return p, true
}

func digitsEnd(s string, i int) int {
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isASCIIAlnum(b byte) bool {
	return isDigit(b) || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
