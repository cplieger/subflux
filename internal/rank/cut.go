package rank

import "github.com/cplieger/subflux/internal/release"

const (
	primaryCuts = release.CutTheatrical | release.CutExtended | release.CutDirectors | release.CutUltimate |
		release.CutFinal | release.CutAssembly | release.CutRecut | release.CutDespecialized |
		release.CutSpecial | release.CutFanEdit
	qualifierCuts = release.CutUnrated | release.CutUncut | release.CutUncensored
)

// cutFact is the first matching row: an unlabelled side is Unknown; two
// sides with primary cuts compare those; a primary cut on one side only
// differs; two qualifier-only sides compare their qualifiers. So a shared
// qualifier never makes two different cuts the same.
func cutFact(a, b release.Cut) Tri {
	pa, pb := a&primaryCuts, b&primaryCuts
	switch {
	case a == 0 || b == 0:
		return Unknown
	case pa != 0 && pb != 0:
		return same(pa&pb != 0)
	case pa != 0 || pb != 0:
		return Differs
	}
	return same(a&b&qualifierCuts != 0)
}
