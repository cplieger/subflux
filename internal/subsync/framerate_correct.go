package subsync

import "time"

// scaleCues applies a framerate ratio to all cue timings.
// ratio > 1 compresses (subtitle was for a slower framerate than the video),
// ratio < 1 stretches (subtitle was for a faster framerate than the video).
func scaleCues(cues []Cue, ratio float64) []Cue {
	if ratio <= 0 {
		return cues
	}
	scaled := make([]Cue, len(cues))
	for i, c := range cues {
		scaled[i] = Cue{
			Start: time.Duration(float64(c.Start) / ratio),
			End:   time.Duration(float64(c.End) / ratio),
			Text:  c.Text,
		}
	}
	return scaled
}
