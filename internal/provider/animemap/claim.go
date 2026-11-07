package animemap

import (
	"slices"
	"strings"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/episodematch"
	"github.com/cplieger/subflux/internal/epmarker"
)

// Step is the claim reader's step that gave a claim.
type Step uint8

// The steps, in the order the reader tries them.
const (
	NoClaim    Step = iota
	ByMarker        // epmarker's claims through episodematch, the bare leading number included
	ByAbsolute      // epmarker.Absolute's one-number form
	ByLabel         // a credit or promotion label
)

// Claim is what one file name of an anime release claims. A ByMarker or
// ByAbsolute claim is of kind Regular or Special with its Episodes; a
// ByLabel claim is of kind Credit or Promotion with none, since AniDB numbers
// each of those kinds as one sequence a name's number cannot be read
// against.
type Claim struct {
	Kind mapread.Kind
	// Markers are a ByMarker claim's seasons and episodes, nil for a bare
	// leading number, which states no season.
	Markers []epmarker.Marker
	// Episodes are the claimed numbers of Kind, ascending.
	Episodes []int
	Step     Step
}

// ReadEntry reads every file name of one release entry, each within the
// entry as episodematch reads a pack's members. bare reads a name's bare
// leading number as an episode of the entry's own numbering, which only the
// id path knows: a title search passes false.
func ReadEntry(names []string, bare bool) []Claim {
	season := -1
	if bare {
		season = 1
	}
	read := episodematch.ReadPack(names, season)
	out := make([]Claim, len(names))
	for i, n := range names {
		out[i] = readClaim(baseName(n), &read[i])
	}
	return out
}

func readClaim(base string, n *episodematch.Name) Claim {
	kind := mapread.Regular
	if n.Special {
		kind = mapread.Special
	}
	if len(n.Claims) > 0 {
		c := Claim{Step: ByMarker, Kind: kind}
		for _, m := range n.Claims {
			c.Episodes = append(c.Episodes, m.Episode)
		}
		slices.Sort(c.Episodes)
		c.Episodes = slices.Compact(c.Episodes)
		if !n.Bare {
			c.Markers = n.Claims
		}
		return c
	}
	if epmarker.Present(base) {
		return Claim{}
	}
	toks := tokens(base)
	if ep, ok := epmarker.Absolute(maskLabels(base, toks)); ok {
		if ep == 0 {
			kind = mapread.Special
		}
		return Claim{Step: ByAbsolute, Kind: kind, Episodes: []int{ep}}
	}
	for _, t := range toks {
		if k, ok := labelKind(t.text); ok {
			return Claim{Step: ByLabel, Kind: k}
		}
	}
	return Claim{}
}

// baseName is the last element of a path in either separator: AnimeTosho
// names an entry's files with the folders they were packed under.
func baseName(name string) string {
	return name[strings.LastIndexAny(name, `/\`)+1:]
}

// token is one run of ASCII letters and digits, as epmarker reads names.
type token struct {
	text string
	at   int
}

func tokens(name string) []token {
	var out []token
	for i := 0; i < len(name); {
		if !alnum(name[i]) {
			i++
			continue
		}
		end := i + 1
		for end < len(name) && alnum(name[end]) {
			end++
		}
		out = append(out, token{text: name[i:end], at: i})
		i = end
	}
	return out
}

// maskLabels blanks every label token and the number it owns with NUL bytes,
// which epmarker reads as a separator that is neither a space nor a joiner, so
// a label's number is never Absolute's and no other number changes form.
func maskLabels(name string, toks []token) string {
	b := []byte(name)
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if _, ok := labelKind(t.text); !ok {
			continue
		}
		end := t.at + len(t.text)
		if i+1 < len(toks) && ownsNumber(name, t, toks[i+1]) {
			i++
			end = toks[i].at + len(toks[i].text)
		}
		for j := t.at; j < end; j++ {
			b[j] = 0
		}
	}
	return string(b)
}

// ownsNumber reports whether next is a label's number written after one
// separator ("OP 2", "NCOP_01", "ED.2", "PV-1"); a joined one ("NCED1") is
// part of the label's token.
func ownsNumber(name string, label, next token) bool {
	sep := name[label.at+len(label.text) : next.at]
	return len(sep) == 1 && strings.IndexByte(" _.-", sep[0]) >= 0 && digits(next.text)
}

// labelKind reads a credit or promotion label as AnimeTosho's names write it:
// NCOP, NCED (a special's SPNCOP and SPNCED too), OP and ED a credit, PV and
// CM a promotion, each in upper case, and Trailer, in any case, a promotion;
// a number joined after it may carry one lower-case variant letter or a
// version ("NCED01a", "OP1v2"). Any longer word (EDGE, OPUS, CMYK) is none.
func labelKind(t string) (mapread.Kind, bool) {
	if rest, ok := strings.CutPrefix(t, "SPNC"); ok && len(rest) >= 2 && (rest[:2] == "OP" || rest[:2] == "ED") && labelNumber(rest[2:]) {
		return mapread.Credit, true
	}
	for _, l := range []struct {
		word string
		kind mapread.Kind
	}{{"NCOP", mapread.Credit}, {"NCED", mapread.Credit}, {"OP", mapread.Credit}, {"ED", mapread.Credit}, {"PV", mapread.Promotion}, {"CM", mapread.Promotion}} {
		if rest, ok := strings.CutPrefix(t, l.word); ok && labelNumber(rest) {
			return l.kind, true
		}
	}
	if len(t) >= len("trailer") && strings.EqualFold(t[:len("trailer")], "trailer") && labelNumber(t[len("trailer"):]) {
		return mapread.Promotion, true
	}
	return "", false
}

// labelNumber reports whether s, what follows a label in its token, is empty
// or digits with an optional lower-case letter or vN after them.
func labelNumber(s string) bool {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	switch rest := s[end:]; {
	case rest == "":
		return true
	case end == 0:
		return false
	case len(rest) == 1:
		return rest[0] >= 'a' && rest[0] <= 'z'
	default:
		return rest[0] == 'v' && digits(rest[1:])
	}
}

func digits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

func alnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
