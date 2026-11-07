// Package epmarker is the one authority for what a release or archive member
// name claims about its season and episode. A name is read once into tokens,
// runs of ASCII letters and digits, and every view classifies those tokens;
// every notation is case-insensitive.
//
//   - Find: S##E## markers (S## E## too, across up to three of " ._-") and
//     ##x## tokens. Claims: the episodes stated under a season, ranges expanded.
//   - FirstIndex, Present: where a marker-shaped token starts, unreadable
//     digits included, so such a name still reaches no fallback.
//   - Season: the first S##E## or S## token, else "Season N", else ##x##.
//   - StatesEpisode: a marker, E##, EP##, Episode N, " - NN" after the season
//     or a range right after it or opening a bracket; a season pack states a
//     Season and none.
//   - Absolute: an episode number with no season attached (the caller supplies
//     it); BareEpisode: a leading number in a name with no readable marker.
package epmarker

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// markerRe reads S##E## inside one token, so a marker glued to its title
// ("TheOfficeS01E05") still reads: the digits after both letters delimit it,
// and an audio tag ("DTS5.1") or a word ("Edition") lacks one of them.
var markerRe = regexp.MustCompile(`(?i)s(\d+)e(\d+)`)

// maxRangeEpisode ends a range, so a year is none: "S01E01-1923" would span 1
// to 1923. maxRangeSpan bounds only how far Claims expands a range.
const (
	maxRangeSpan    = 50
	maxRangeEpisode = 999
)

// A file suffix after a spaced number is at most three dotted tokens of up to
// six bytes, as in ".eng.forced.srt": a longer dotted tail is a title's words.
const (
	maxSuffixes  = 3
	maxSuffixLen = 6
)

// Marker is the season/episode pair one token claims. Both numbers are
// non-negative: they are read from digit runs, so "S00E00" is a legitimate
// Marker{0, 0} rather than a miss.
type Marker struct {
	Season  int
	Episode int
}

// String renders a marker in the canonical S##E## form, which is what a
// diagnostic naming the episode a caller looked for should say.
func (m Marker) String() string {
	return "S" + pad2(m.Season) + "E" + pad2(m.Episode)
}

func pad2(n int) string {
	if n >= 0 && n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// Target is what a lookup is looking for. The zero Target matches any name,
// which is what a movie download and a provider that reports no numbering both
// arrive with, so a caller selecting from candidates needs one loop rather than
// one per case.
type Target struct {
	want  Marker
	fixed bool
}

// Any returns the Target that matches every name.
func Any() Target { return Target{} }

// For returns the Target that matches only m, or Any when either half of m is
// absent. A zero season or episode is how the wire says "this download has no
// episode to disambiguate", so a caller can hand over a subtitle's fields
// without deciding what that means.
func For(m Marker) Target {
	if m.Season <= 0 || m.Episode <= 0 {
		return Any()
	}
	return Target{want: m, fixed: true}
}

// Matches reports whether name claims the episode t is looking for. An Any
// target matches every name.
func (t Target) Matches(name string) bool {
	if !t.fixed {
		return true
	}
	return slices.Contains(Claims(name), t.want)
}

// MatchesBareEpisode reports whether name states t's episode as a bare leading
// number, with no season anywhere in the name. The name does not carry the
// season, so a caller uses it only when both conditions hold, neither checkable
// here: t's season is known reliably from outside the name (a provider result's
// season is stamped from the seasonNumber its search sent), and no member of the
// same archive claims a readable marker, so this reading never outvotes a
// notation that parses.
func (t Target) MatchesBareEpisode(name string) bool {
	if !t.fixed {
		return false
	}
	ep, ok := BareEpisode(name)
	return ok && ep == t.want.Episode
}

// String renders t for a diagnostic: the episode in canonical form, or "any"
// when nothing narrows the lookup.
func (t Target) String() string {
	if !t.fixed {
		return "any episode"
	}
	return t.want.String()
}

// Find returns every well-formed single marker in name, in order of appearance,
// across both notations. Callers that care about precedence pick from the
// returned order themselves. A marker-shaped token whose season or episode does
// not fit an int is skipped here but still reported by Present.
func Find(name string) []Marker {
	r := read(name)
	return r.markers()
}

// Claims returns every episode name claims, ordered, deduplicated, nil when it
// claims none: its markers, and each episode StatesEpisode reads under its
// marker's season, else the latest season statement before it; a range longer
// than maxRangeSpan claims its first. It selects archive members, so a number
// with no season before it ("E01-E02") claims nothing: guessing one risks the
// wrong episode's subtitle written under the right episode's name.
func Claims(name string) []Marker {
	r := read(name)
	claims := r.markers()
	for _, u := range r.runs {
		// Beside a marker, which numbers the name, a lone number is a tag or a
		// group's ("x264-E02") and bare digits a title's ("Part.1-2"), so only
		// a lettered run of numbers adds episodes.
		if len(r.shapes) > 0 && (len(u.nums) == 1 || !u.form.lettered()) || !u.states() {
			continue
		}
		season, seasoned := r.seasonOf(&u)
		first, last, ok := u.episodes()
		if !seasoned || !ok {
			continue
		}
		for ep := first; ep <= last; ep++ {
			claims = append(claims, Marker{Season: season, Episode: ep})
		}
	}
	slices.SortFunc(claims, func(a, b Marker) int {
		if a.Season != b.Season {
			return a.Season - b.Season
		}
		return a.Episode - b.Episode
	})
	claims = slices.Compact(claims)
	if len(claims) == 0 {
		return nil
	}
	return claims
}

// FirstIndex returns the byte offset at which the first marker-shaped token
// starts, or -1 when name carries none. Used to split a release name into its
// title portion and its marker portion.
func FirstIndex(name string) int {
	if r := read(name); len(r.shapes) > 0 {
		return r.shapes[0].at
	}
	return -1
}

// Present reports whether name carries a marker-shaped token at all. This is
// the gate for "the name numbers its episodes some other way, fall back":
// a name containing an unparseable marker is still explicitly numbered, so it
// must not reach an absolute-number or season-pack fallback.
func Present(name string) bool { return FirstIndex(name) >= 0 }

// Season returns the season of name's first S## token, a marker's S## included,
// else of "Season N", else of the cross form, so Season never contradicts Find
// ("Black Sails - 01x08" is season 1). ok is false when name carries no season
// token or its digits do not fit an int.
func Season(name string) (season int, ok bool) {
	r := read(name)
	// The first form PRESENT decides, whether or not its digits are readable: a
	// token too wide for an int is unreadable rather than absent, so falling
	// through would answer with a different notation's season.
	if st, found := r.seasonStatement(); found {
		n, err := strconv.Atoi(st.digits)
		if err != nil {
			// Atoi clamps an overflowing run to MaxInt64 and returns it
			// alongside the error, so the value has to be dropped here.
			return 0, false
		}
		return n, true
	}
	for _, s := range r.shapes {
		if s.cross {
			n, _ := strconv.Atoi(s.season) // at most two digits
			return n, true
		}
	}
	return 0, false
}

// StatesEpisode reports whether name states an episode, as one number or a
// range: a marker-shaped token (Present), an E##, EP## or Episode N, or bare
// digits after a season statement, as " - 05" or as a range right after it or
// opening a bracket ("S2 01-12", "S2 [01 ~ 12]"). Other bare digits may be a
// title's, a volume's ("Vol.1-6"), a bit depth's ("10 bit"), a CRC's or a
// date's, so none of those states one. A season pack is a name with a Season
// that states none.
func StatesEpisode(name string) bool {
	r := read(name)
	if len(r.shapes) > 0 {
		return true
	}
	for _, u := range r.runs {
		if u.states() && (u.form.lettered() || r.afterSeason(&u)) {
			return true
		}
	}
	return false
}

// Absolute returns the episode a name with no marker shape states by one
// number, as batch entries name their files ("[Group] Show - 26"): the last
// " - NN", since a title's number comes first, else the first E##, EP## or
// Episode N, else the last spaced NN that is no bit depth and that a space,
// the name's end or a file suffix follows ("Mob Psycho 100 05.mkv" states 5,
// "Show 02 10 bits" 2). A range's number or a bracketed bare one is none. ok
// is false when no form is present, the number overflows an int, or Present
// reports a marker shape.
func Absolute(name string) (episode int, ok bool) {
	r := read(name)
	if len(r.shapes) > 0 {
		return 0, false
	}
	var dash, word, spaced string
	for _, u := range r.runs {
		if len(u.nums) != 1 || !u.readsAlone() {
			continue
		}
		switch u.form {
		case byDash:
			dash = u.nums[0]
		case byE, byWord:
			word = cmp.Or(word, u.nums[0])
		case bySpace:
			spaced = u.nums[0]
		}
	}
	digits := cmp.Or(dash, word, spaced)
	if digits == "" {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return n, true
}

// BareEpisode returns the episode a name states as a bare leading number:
// "6 - A Golden Crown..srt" states episode 6. The number is the name's first
// token, one to three digits, followed by one of " ._-", so a resolution
// ("1080p") or a year ("2011 - ") is none. A name with a readable marker
// returns false.
//
// A bare number names no season, so this reading is kept apart from Find and
// Claims; Target.MatchesBareEpisode states what a caller must know to use it.
func BareEpisode(name string) (episode int, ok bool) {
	end := digitEnd(name, 0)
	if end == 0 || end > 3 || end == len(name) || !strings.ContainsRune(" ._-", rune(name[end])) {
		return 0, false
	}
	if len(Claims(name)) > 0 {
		return 0, false
	}
	n, _ := strconv.Atoi(name[:end]) // at most three digits
	return n, true
}

// reading is what one name states, as read classifies it.
type reading struct {
	shapes []shape
	runs   []run
	stated []statement // every S## token, a marker's included, and "Season N", in order
}

// shape is one marker-shaped token: an S##E## marker or a cross token.
type shape struct {
	season, episode string
	at              int
	cross           bool
}

// seasonStatement returns the first S## token, else the first "Season N".
func (r *reading) seasonStatement() (statement, bool) {
	for _, word := range []bool{false, true} {
		for _, st := range r.stated {
			if st.word == word {
				return st, true
			}
		}
	}
	return statement{}, false
}

// afterSeason reports whether u stands after a season statement.
func (r *reading) afterSeason(u *run) bool {
	return len(r.stated) > 0 && r.stated[0].end <= u.at
}

// seasonOf returns the season u claims under: its marker's, else the latest
// season statement ending before it.
func (r *reading) seasonOf(u *run) (int, bool) {
	digits := u.season
	if digits == "" {
		for _, st := range r.stated {
			if st.end > u.at {
				break
			}
			digits = st.digits
		}
	}
	if digits == "" {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	return n, err == nil
}

func (r *reading) markers() []Marker {
	var out []Marker
	for _, s := range r.shapes {
		season, sErr := strconv.Atoi(s.season)
		episode, eErr := strconv.Atoi(s.episode)
		if sErr == nil && eErr == nil {
			out = append(out, Marker{Season: season, Episode: episode})
		}
	}
	return out
}

// statement is a season statement's digits and the offset just past them;
// word marks a "Season N".
type statement struct {
	digits string
	end    int
	word   bool
}

// form is how a run's first number states an episode.
type form uint8

const (
	byMarker form = iota + 1 // an S##E## or ##x## marker's episode
	byE                      // E05
	byWord                   // EP05, Ep 05, Episode 5
	byDash                   // " - 05"
	bySpace                  // " 05 "
	byRange                  // "S2 01-12", "[01 ~ 12]": bare digits opening a range
)

// lettered reports whether f writes a letter before its number, which then
// states an episode whatever follows it.
func (f form) lettered() bool { return f == byMarker || f == byE || f == byWord }

// run is one episode number, or several joined by a range joiner ("E01-E12",
// "01 ~ 12", "S01E01E02"). season holds a marker's season digits; any other
// run takes the latest season statement before it. A range states an episode
// set in every notation, so a name stating one is no season pack.
type run struct {
	season string
	nums   []string
	at     int
	form   form
}

// readsAlone reports whether u's first number is an episode number by its own
// digits: a bare-digit form or an E## needs two or more, as zero-padded
// notation writes them, so a group's "E1" or a "1-2" is none; a marker, EP##
// or Episode N reads any.
func (u *run) readsAlone() bool {
	return u.form == byMarker || u.form == byWord || len(u.nums[0]) >= 2
}

// states reports whether u states episodes: one number it reads alone but a
// spaced one ("10 bit"), a range whose first number it reads alone, or a
// lettered run's first number whatever follows it. Bare digits that are no
// range state none ("2014-06-02").
func (u *run) states() bool {
	switch {
	case len(u.nums) == 1:
		return u.form != bySpace && u.readsAlone()
	case u.form.lettered():
		return true
	}
	_, _, isRange := u.span()
	return isRange && u.readsAlone()
}

// span returns a range's first and last episode. Its numbers must not
// decrease and the last must not pass maxRangeEpisode, so a date
// ("2014-06-02") or a year is no range.
func (u *run) span() (first, last int, ok bool) {
	if len(u.nums) < 2 {
		return 0, 0, false
	}
	prev := -1
	for _, d := range u.nums {
		n, err := strconv.Atoi(d)
		if err != nil || n < prev {
			return 0, 0, false
		}
		prev = n
	}
	first, _ = strconv.Atoi(u.nums[0])
	return first, prev, prev <= maxRangeEpisode
}

// episodes returns the episodes a stating run claims: its range when at most
// maxRangeSpan long, else its first number.
func (u *run) episodes() (first, last int, ok bool) {
	if first, last, ok := u.span(); ok && last-first <= maxRangeSpan {
		return first, last, true
	}
	n, err := strconv.Atoi(u.nums[0])
	return n, n, err == nil
}

// token is one run of ASCII letters and digits. Every other byte, a non-ASCII
// one included, separates tokens; before and after are the separator runs
// around it. inside reports a token within [] or (), where CRCs, resolutions
// and bit depths stand: there eight hex digits are a CRC, bare digits read
// only as a range, and a spaced hyphen joins one ("[01 - 12]"). leads reports
// the first token after an opening bracket.
type token struct {
	text, before, after string
	at                  int
	inside, leads       bool
}

func tokenize(name string) []token {
	var toks []token
	depth, sep := 0, 0
	opened := false
	for i := 0; i < len(name); {
		if !isAlnum(name[i]) {
			switch name[i] {
			case '[', '(':
				depth++
				opened = true
			case ']', ')':
				depth = max(depth-1, 0)
				opened = false
			}
			i++
			continue
		}
		end := i + 1
		for end < len(name) && isAlnum(name[end]) {
			end++
		}
		if n := len(toks); n > 0 {
			toks[n-1].after = name[sep:i]
		}
		toks = append(toks, token{text: name[i:end], before: name[sep:i], at: i, inside: depth > 0, leads: opened && depth > 0})
		sep, i, opened = end, end, false
	}
	if n := len(toks); n > 0 {
		toks[n-1].after = name[sep:]
	}
	return toks
}

// read classifies name's tokens in order. The token after one that added a
// run extends that run when a range joiner separates them and it is a number
// in the run's notation. Only a run is extended, so a codec's or an audio
// tag's digits ("x264-E02", "H.264-E05", "5.1-E02") start no range and the E##
// after them is a number of its own.
func read(name string) reading {
	var r reading
	toks := tokenize(name)
	open := false // the previous token added or extended a run
	rangeAt := -1 // the token right after a season statement of its own
	for k := 0; k < len(toks); k++ {
		t := toks[k]
		if open && r.extend(t) {
			continue
		}
		runs := len(r.runs)
		lower := strings.ToLower(t.text)
		switch {
		case markerRe.MatchString(t.text):
			r.readMarkers(t)
		case cross(t.text) != "":
			x := cross(t.text)
			season := t.text[:len(t.text)-len(x)-1]
			r.shapes = append(r.shapes, shape{season: season, episode: x, at: t.at, cross: true})
			r.runs = append(r.runs, run{nums: []string{x}, season: season, at: t.at + len(season) + 1, form: byMarker})
		case lower[0] == 's' && len(lower) > 1 && digitEnd(lower, 1) == len(lower):
			k, rangeAt = r.readSeason(toks, k)
		case strings.HasPrefix(lower, "season"):
			k, rangeAt = r.readSeasonWord(toks, k)
		case t.inside && crc(t.text):
		default:
			k = r.readNumber(toks, k, lower, k == rangeAt || t.leads)
		}
		open = len(r.runs) > runs
	}
	return r
}

// readSeason records an S## token, with the E## token after it as a marker
// ("S01 E01"). It returns the last token it read and, for a lone S##, the
// token after it, where bare digits may open a range (-1 otherwise).
func (r *reading) readSeason(toks []token, k int) (last, anchor int) {
	t := toks[k]
	r.stated = append(r.stated, statement{digits: t.text[1:], end: t.at + len(t.text)})
	if k+1 == len(toks) || !joinsMarker(toks[k+1].before) {
		return k, k + 1
	}
	nums := eNumbers(toks[k+1].text)
	if nums == nil {
		return k, k + 1
	}
	r.shapes = append(r.shapes, shape{season: t.text[1:], episode: nums[0], at: t.at})
	r.runs = append(r.runs, run{nums: nums, season: t.text[1:], at: toks[k+1].at, form: byMarker})
	return k + 1, -1
}

// readSeasonWord records a "Season N" as readSeason does an S##.
func (r *reading) readSeasonWord(toks []token, k int) (last, anchor int) {
	digits, at, used := wordNumber(toks, k, len("season"))
	if digits == "" {
		return k, -1
	}
	r.stated = append(r.stated, statement{digits: digits, end: at + len(digits), word: true})
	return k + used, k + used + 1
}

// readMarkers records every S##E## marker in t and adds a run at the last
// one's episode, with the E## runs glued after it ("S01E01E02").
func (r *reading) readMarkers(t token) {
	ms := markerRe.FindAllStringSubmatchIndex(t.text, -1)
	for _, m := range ms {
		r.shapes = append(r.shapes, shape{season: t.text[m[2]:m[3]], episode: t.text[m[4]:m[5]], at: t.at + m[0]})
		r.stated = append(r.stated, statement{digits: t.text[m[2]:m[3]], end: t.at + m[3]})
	}
	last := ms[len(ms)-1]
	nums := append([]string{t.text[last[4]:last[5]]}, eNumbers(t.text[last[1]:])...)
	r.runs = append(r.runs, run{nums: nums, season: t.text[last[2]:last[3]], at: t.at + last[4], form: byMarker})
}

// readNumber reads an episode number, an E## token, an EP## or Episode N, or
// bare digits as readDigits does, and returns the last token it read.
func (r *reading) readNumber(toks []token, k int, lower string, anchored bool) int {
	t := toks[k]
	if nums := eNumbers(t.text); nums != nil {
		r.runs = append(r.runs, run{nums: nums, at: t.at, form: byE})
		return k
	}
	for _, word := range []string{"episode", "ep"} {
		if strings.HasPrefix(lower, word) {
			if digits, at, used := wordNumber(toks, k, len(word)); digits != "" {
				r.runs = append(r.runs, run{nums: []string{digits}, at: at, form: byWord})
				return k + used
			}
			return k
		}
	}
	return r.readDigits(toks, k, anchored)
}

// readDigits reads bare digits: outside brackets a number after " - " or
// spaced as spacedEnd says, and, where anchored (right after a season statement or
// opening a bracket), digits a range joiner parts from the next number.
// Unanchored, such digits are a volume's, a part's or a title's ("Vol.1-6")
// and it skips them whole. It returns the last token it read.
func (r *reading) readDigits(toks []token, k int, anchored bool) int {
	t := toks[k]
	digits, ok := number(t.text)
	switch {
	case !ok:
	case !t.inside && strings.HasSuffix(t.before, " - "):
		r.runs = append(r.runs, run{nums: []string{digits}, at: t.at, form: byDash})
	case k+1 < len(toks) && joinsNumber(toks[k+1]):
		if anchored {
			r.runs = append(r.runs, run{nums: []string{digits}, at: t.at, form: byRange})
			return k
		}
		for k+1 < len(toks) && joinsNumber(toks[k+1]) {
			k++
		}
	case !t.inside && strings.HasSuffix(t.before, " ") && spacedEnd(toks, k) && !bitDepth(toks, k):
		r.runs = append(r.runs, run{nums: []string{digits}, at: t.at, form: bySpace})
	}
	return k
}

// spacedEnd reports whether a spaced number ends at token k: a space follows
// it, the name ends there, or only a dotted file suffix does (".mkv",
// ".en.srt"). A suffix token starts with a letter, so a dotted year or
// resolution (".2010", ".1080p") is none.
func spacedEnd(toks []token, k int) bool {
	t := toks[k]
	if strings.HasPrefix(t.after, " ") || k+1 == len(toks) && t.after == "" {
		return true
	}
	tail := toks[k+1:]
	if t.after != "." || len(tail) == 0 || len(tail) > maxSuffixes || tail[len(tail)-1].after != "" {
		return false
	}
	for i, s := range tail {
		if i > 0 && s.before != "." || len(s.text) > maxSuffixLen || !isLetter(s.text[0]) {
			return false
		}
	}
	return true
}

// bitDepth reports whether the token after k is "bit" or "bits", as in
// "10 bits".
func bitDepth(toks []token, k int) bool {
	return k+1 < len(toks) && (strings.EqualFold(toks[k+1].text, "bit") || strings.EqualFold(toks[k+1].text, "bits"))
}

// extend joins t to the last run when a range joiner precedes it and t is a
// number in the run's notation: digits, an E## after an E## or a marker, an
// EP## after an EP##.
func (r *reading) extend(t token) bool {
	u := &r.runs[len(r.runs)-1]
	var nums []string
	e := false
	if digits, ok := number(t.text); ok {
		nums = []string{digits}
	} else if u.form == byMarker || u.form == byE {
		nums, e = eNumbers(t.text), true
	} else if u.form == byWord && strings.HasPrefix(strings.ToLower(t.text), "ep") {
		if digits, ok := number(t.text[2:]); ok {
			nums = []string{digits}
		}
	}
	if nums == nil || !joinsRange(t.before, e, t.inside) {
		return false
	}
	u.nums = append(u.nums, nums...)
	return true
}

// wordNumber reads the number a spelled-out word ending at prefix states,
// glued to it ("EP05") or after one or two of " ._" or a "-" ("Ep. 05",
// "Season 2"), and how many tokens past k it read. A " - " joins none, since
// it opens the dash form ("2nd Season - 05").
func wordNumber(toks []token, k, prefix int) (digits string, at, used int) {
	t := toks[k]
	if rest := t.text[prefix:]; rest != "" {
		if digits, ok := number(rest); ok {
			return digits, t.at + prefix, 0
		}
		return "", 0, 0
	}
	if k+1 < len(toks) {
		sep := toks[k+1].before
		if sep == "-" || len(sep) <= 2 && strings.Trim(sep, " ._") == "" {
			if digits, ok := number(toks[k+1].text); ok {
				return digits, toks[k+1].at, 1
			}
		}
	}
	return "", 0, 0
}

// joinsMarker reports whether sep may part an S## from its E## ("S01 E01",
// "S01.E05"); a wider gap ("S01 - - - E05") reaches across unrelated text.
func joinsMarker(sep string) bool {
	return len(sep) <= 3 && strings.Trim(sep, " ._-") == ""
}

// joinsRange reports whether sep joins two numbers into a range: a hyphen, a
// tilde with optional spaces, a dot before an E## ("E01.E02") or, inside
// brackets, a spaced hyphen. Outside them a " - " parts a title's number from
// an episode's ("Show 100 - 05").
func joinsRange(sep string, beforeE, inside bool) bool {
	j := strings.Trim(sep, " ")
	return sep == "-" || j == "~" || inside && j == "-" || beforeE && sep == "."
}

// joinsNumber reports whether t is a number a range joiner parts from the
// token before it.
func joinsNumber(t token) bool {
	_, ok := number(t.text)
	return ok && joinsRange(t.before, false, t.inside)
}

// crc reports whether s is eight hex digits, a CRC-32 as release names write
// one in brackets ("E5A2C3D1", "E0123456").
func crc(s string) bool {
	if len(s) != 8 {
		return false
	}
	for i := range len(s) {
		if !isDigit(s[i]) && (s[i]|0x20 < 'a' || s[i]|0x20 > 'f') {
			return false
		}
	}
	return true
}

// cross returns the episode digits of a whole ##x## token, or "". The episode
// half needs two or three digits, as real notation pads ("01x08") while an
// aspect ratio does not ("4x3", "16x9"); a resolution ("1920x1080") is too wide
// for either half.
func cross(s string) string {
	x := digitEnd(s, 0)
	if x == 0 || x > 2 || x == len(s) || s[x]|0x20 != 'x' {
		return ""
	}
	end := digitEnd(s, x+1)
	if end != len(s) || end-x-1 < 2 || end-x-1 > 3 {
		return ""
	}
	return s[x+1:]
}

// eNumbers reads a token of E## runs ("E05", "E01E02") with an optional
// version suffix ("E05v2"), or returns nil.
func eNumbers(s string) []string {
	var nums []string
	i := 0
	for i < len(s) && s[i]|0x20 == 'e' {
		end := digitEnd(s, i+1)
		if end == i+1 {
			return nil
		}
		nums = append(nums, s[i+1:end])
		i = end
	}
	if i < len(s) && !version(s[i:]) {
		return nil
	}
	return nums
}

// number reads a token of digits with an optional version suffix ("05v2").
func number(s string) (digits string, ok bool) {
	end := digitEnd(s, 0)
	if end == 0 || end < len(s) && !version(s[end:]) {
		return "", false
	}
	return s[:end], true
}

func version(s string) bool {
	return len(s) > 1 && s[0]|0x20 == 'v' && digitEnd(s, 1) == len(s)
}

func digitEnd(s string, i int) int {
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}

func isAlnum(b byte) bool {
	return isDigit(b) || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isLetter(b byte) bool { return isAlnum(b) && !isDigit(b) }

// Compile-time proof that both diagnostic renderings satisfy fmt.Stringer, per
// the shared rule that a well-known interface method is paired with an assertion
// at the implementation site. Both have production call sites in the archive
// extractor's refusal messages.
var (
	_ fmt.Stringer = Marker{}
	_ fmt.Stringer = Target{}
)
