package rank

import "math"

// rateFamily groups frame rates that need no linear correction.
type rateFamily uint8

// The families; rateUnknown is the zero value.
const (
	rateUnknown rateFamily = iota
	pal                    // 25, 50
	film                   // 23.976, 24, 29.97, 30, 47.95, 48, 59.94, 60
)

// rateTolerance is the relative distance within which an fps value is a
// family member (subliminal's fps_matches).
const rateTolerance = 0.0011

var familyRates = []struct {
	fps    float64
	family rateFamily
}{
	{25, pal},
	{50, pal},
	{23.976, film},
	{24, film},
	{29.97, film},
	{30, film},
	{47.95, film},
	{48, film},
	{59.94, film},
	{60, film},
}

// rateFamilyOf answers rateUnknown for 0 and for a rate in no family.
func rateFamilyOf(fps float64) rateFamily {
	for _, r := range familyRates {
		if math.Abs(fps-r.fps) <= r.fps*rateTolerance {
			return r.family
		}
	}
	return rateUnknown
}
