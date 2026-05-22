// Package crosslang implements cross-language subtitle alignment using
// anchor-based matching and dynamic programming.
package crosslang

import "time"

// Cue represents a single subtitle cue with timing and text.
// Structurally identical to subsync.Cue to allow zero-copy conversion.
type Cue struct {
	Text  string
	Start time.Duration
	End   time.Duration
}

// Result holds the output of cross-language alignment.
type Result struct {
	Cues       []Cue
	Offset     int64   // milliseconds
	Rate       float64 // always 1.0 for crosslang
	Confidence float64
}
