package rank

import "math"

// RateFamily groups frame rates that need no linear correction.
type RateFamily uint8

// The families; RateUnknown is the zero value.
const (
	RateUnknown RateFamily = iota
	PAL                    // 25, 50
	Film                   // 23.976, 24, 29.97, 30, 47.95, 48, 59.94, 60
)

// RateTolerance is the relative distance within which an fps value is a
// family member (subliminal's fps_matches).
const RateTolerance = 0.0011

var familyRates = []struct {
	fps    float64
	family RateFamily
}{
	{25, PAL},
	{50, PAL},
	{23.976, Film},
	{24, Film},
	{29.97, Film},
	{30, Film},
	{47.95, Film},
	{48, Film},
	{59.94, Film},
	{60, Film},
}

// RateFamilyOf answers RateUnknown for 0 and for a rate in no family.
func RateFamilyOf(fps float64) RateFamily {
	for _, r := range familyRates {
		if math.Abs(fps-r.fps) <= r.fps*RateTolerance {
			return r.family
		}
	}
	return RateUnknown
}
