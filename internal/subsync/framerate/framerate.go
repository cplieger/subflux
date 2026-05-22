// Package framerate provides framerate drift detection for subtitle alignment.
package framerate

// MinLinearR2 is the minimum R² (coefficient of determination) for drift
// to be considered linear (indicating a framerate mismatch rather than
// random or constant offset). Below this threshold, drift is non-linear.
const MinLinearR2 = 0.8

// DriftPoint represents a measured drift at a timeline position.
type DriftPoint struct {
	TimeMs  float64
	DriftMs float64
}

// LinearRegression fits y = slope*x + intercept to the drift points.
func LinearRegression(points []DriftPoint) (slope, intercept, r2 float64) {
	n := float64(len(points))
	if n < 2 {
		return 0, 0, 0
	}

	var sumX, sumY, sumXY, sumX2 float64
	for _, p := range points {
		sumX += p.TimeMs
		sumY += p.DriftMs
		sumXY += p.TimeMs * p.DriftMs
		sumX2 += p.TimeMs * p.TimeMs
	}

	denom := n*sumX2 - sumX*sumX
	if denom == 0 {
		return 0, 0, 0
	}

	slope = (n*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / n

	meanY := sumY / n
	var ssTot, ssRes float64
	for _, p := range points {
		predicted := slope*p.TimeMs + intercept
		ssRes += (p.DriftMs - predicted) * (p.DriftMs - predicted)
		ssTot += (p.DriftMs - meanY) * (p.DriftMs - meanY)
	}
	if ssTot == 0 {
		r2 = 1.0
	} else {
		r2 = 1.0 - ssRes/ssTot
	}
	return slope, intercept, r2
}

// KnownFramerates contains common video framerates and their NTSC/PAL conversions.
var KnownFramerates = []float64{
	23.976, // NTSC film (24000/1001)
	24.0,   // Cinema
	25.0,   // PAL
	29.97,  // NTSC video (30000/1001)
	30.0,   // NTSC round
	48.0,   // HFR cinema
	50.0,   // PAL interlaced
	59.94,  // NTSC interlaced (60000/1001)
	60.0,   // NTSC round interlaced
}

// RatioPair represents a source→target framerate conversion.
type RatioPair struct {
	From, To float64
	Ratio    float64 // To / From
}

// KnownRatios contains all common framerate conversion pairs.
var KnownRatios = buildKnownRatios()

func buildKnownRatios() []RatioPair {
	var ratios []RatioPair
	for _, from := range KnownFramerates {
		for _, to := range KnownFramerates {
			if from == to {
				continue
			}
			ratios = append(ratios, RatioPair{
				From:  from,
				To:    to,
				Ratio: to / from,
			})
		}
	}
	return ratios
}
