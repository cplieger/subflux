package timing

import (
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/rank"
)

// Plan decides one subtitle's timing pass from its release facts, the
// variant it serves and the timing section, in order: sync off is
// Skip/Disabled, a validated hash Skip/Hash, a forced variant Skip/Forced,
// the same release Skip/SameRelease; a flagged different cut is
// RigidReference, whatever AudioFallback holds; otherwise ReferenceThenAudio
// with AudioFallback on, else ActionReference.
func Plan(f rank.Facts, v language.Variant, cfg Config) Decision {
	switch {
	case !cfg.Sync:
		return Decision{Action: Skip, Skip: Disabled}
	case f.Hash:
		return Decision{Action: Skip, Skip: Hash}
	case v == language.Forced:
		return Decision{Action: Skip, Skip: Forced}
	case sameRelease(f):
		return Decision{Action: Skip, Skip: SameRelease}
	case f.Cut == rank.Differs:
		return Decision{Action: RigidReference}
	case cfg.AudioFallback:
		return Decision{Action: ReferenceThenAudio}
	}
	return Decision{Action: ActionReference}
}

// sameRelease is the one skip a release match earns. A different group never
// earns it, and a pick chosen by its episode number alone never does, since
// its release can match the video's while it is another episode.
func sameRelease(f rank.Facts) bool {
	return f.Source == rank.Same && f.Group == rank.Same && f.Service != rank.Differs &&
		f.Rate != rank.Differs && f.Cut != rank.Differs && !f.PreRelease && !f.EpisodeByNumber
}
