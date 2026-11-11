package rank

import (
	"fmt"
	"strings"

	"github.com/cplieger/subflux/internal/release"
)

// Tri is one fact's answer; Same and Differs need both sides known.
type Tri uint8

// The answers; Unknown is the zero value and valid.
const (
	Unknown Tri = iota
	Same
	Differs
)

// MatchedBy is how the provider matched the query; diagnostic, except that
// only Hash can make a hash fact.
type MatchedBy uint8

// The matches; the zero value is not one.
const (
	Title MatchedBy = iota + 1
	ID
	Hash
)

// Facts compares one subtitle's release with the video's.
type Facts struct {
	// Hash is a validated hash claim: void when identity was not confirmed
	// or Source or Cut is Differs.
	Hash                              bool
	Source, Group, Service, Cut, Rate Tri
	// PreRelease is true when exactly one side is a cam, telesync,
	// telecine, screener, regional or workprint release.
	PreRelease bool
	// SeasonPack is true when an episode's subtitle name states a season
	// and no episode; a tie-break only.
	SeasonPack bool
	// EpisodeByNumber marks a pack member chosen by its number alone: its
	// episode is unconfirmed, so its timing is never skipped. The engine
	// sets it, never Compare, and FitOf never reads it.
	EpisodeByNumber bool
}

// Fit grades Facts, ascending; the zero value is not a grade.
type Fit uint8

// The grades.
const (
	Conflict Fit = iota + 1
	Other
	SameSource
	SameRelease
	Exact
)

// Compare builds the facts. Same and Differs need both sides known; Service
// compares only when both sources are web; Cut is Unknown when one side is
// unlabelled, compares primary cut tokens when both sides carry one (Same
// when they share one), is Differs when only one side does, and compares the
// Unrated, Uncut and Uncensored qualifiers only when neither side carries a
// primary cut; two pre-release sources compare Unknown. Hash holds only when m
// is Hash, identityOK is true and neither Source nor Cut is Differs.
func Compare(video release.Parsed, videoFPS float64, sub release.Parsed, subFPS float64, m MatchedBy, identityOK bool) Facts {
	vf, sf := video.Source().Family(), sub.Source().Family()
	f := Facts{
		Source:     sourceFact(vf, sf),
		Group:      textFact(video.Group(), sub.Group()),
		Cut:        cutFact(video.Cut(), sub.Cut()),
		Rate:       rateFact(rateFamilyOf(videoFPS), rateFamilyOf(subFPS)),
		PreRelease: (vf == release.PreRelease) != (sf == release.PreRelease),
		SeasonPack: sub.SeasonPack(),
	}
	if vf == release.Web && sf == release.Web {
		f.Service = textFact(video.Service(), sub.Service())
	}
	f.Hash = m == Hash && identityOK && f.Source != Differs && f.Cut != Differs
	return f
}

func sourceFact(a, b release.Family) Tri {
	switch {
	case a == release.FamilyUnknown || b == release.FamilyUnknown:
		return Unknown
	case a == release.PreRelease && b == release.PreRelease:
		return Unknown
	}
	return same(a == b)
}

// textFact compares two labels case-insensitively; "" is unknown.
func textFact(a, b string) Tri {
	if a == "" || b == "" {
		return Unknown
	}
	return same(strings.EqualFold(a, b))
}

func rateFact(a, b rateFamily) Tri {
	if a == rateUnknown || b == rateUnknown {
		return Unknown
	}
	return same(a == b)
}

func same(ok bool) Tri {
	if ok {
		return Same
	}
	return Differs
}

// FitOf grades f: a hash is Exact; a differing cut or a one-sided
// pre-release is a Conflict; the same group with no differing source,
// service or rate is SameRelease; the same source with no differing rate is
// SameSource; anything else is Other.
func FitOf(f Facts) Fit {
	switch {
	case f.Hash:
		return Exact
	case f.Cut == Differs || f.PreRelease:
		return Conflict
	case f.Group == Same && f.Source != Differs && f.Service != Differs && f.Rate != Differs:
		return SameRelease
	case f.Source == Same && f.Rate != Differs:
		return SameSource
	}
	return Other
}

var (
	triNames   = []string{Unknown: "unknown", Same: "same", Differs: "differs"}
	matchNames = []string{Title: "title", ID: "id", Hash: "hash"}
	fitNames   = []string{Conflict: "conflict", Other: "other", SameSource: "same_source", SameRelease: "same_release", Exact: "exact"}
)

// marshalName is names[v], refusing a value without a name.
func marshalName[T ~uint8](kind string, names []string, v T) ([]byte, error) {
	if int(v) < len(names) && names[v] != "" {
		return []byte(names[v]), nil
	}
	return nil, fmt.Errorf("rank: %s %d has no name", kind, v)
}

// unmarshalName reads one of names exactly.
func unmarshalName[T ~uint8](kind string, names []string, text []byte, v *T) error {
	for i, n := range names {
		if n != "" && n == string(text) {
			*v = T(i)
			return nil
		}
	}
	return fmt.Errorf("rank: %q is not a %s", text, kind)
}

// MarshalText is unknown, same or differs.
func (t Tri) MarshalText() ([]byte, error) { return marshalName("tri", triNames, t) }

// UnmarshalText reads unknown, same or differs and refuses every other spelling.
func (t *Tri) UnmarshalText(b []byte) error { return unmarshalName("tri", triNames, b, t) }

// MarshalText is title, id or hash; the zero value has no name.
func (m MatchedBy) MarshalText() ([]byte, error) { return marshalName("match", matchNames, m) }

// UnmarshalText reads title, id or hash and refuses every other spelling.
func (m *MatchedBy) UnmarshalText(b []byte) error { return unmarshalName("match", matchNames, b, m) }

// MarshalText is conflict, other, same_source, same_release or exact; the
// zero value has no name.
func (f Fit) MarshalText() ([]byte, error) { return marshalName("fit", fitNames, f) }

// UnmarshalText reads a grade's name and refuses every other spelling.
func (f *Fit) UnmarshalText(b []byte) error { return unmarshalName("fit", fitNames, b, f) }
