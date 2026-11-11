package search

import (
	"fmt"
)

// WhyNot is why a candidate is not taken automatically, or why a pair was
// skipped; None is a candidate automation would take.
type WhyNot uint8

// The reasons.
const (
	None WhyNot = iota
	NoCandidate
	BelowMinFit
	DifferentCut
	PreRelease
	LanguageManualOnly
	PartialCoverage
	FormatUnsupported
	// IdentityUnconfirmed: a candidate its adapter could not place on the
	// item, or a title-matched movie whose year is unknown on either side.
	IdentityUnconfirmed
	// DifferentEpisode, EpisodeCount and OrderUndetermined: an automatic
	// run within attemptMemory refused the candidate's pack member chosen
	// by number, the pack's count, or its undetermined order (8.11).
	DifferentEpisode
	EpisodeCount
	OrderUndetermined
)

var whyNotText = [...]string{
	None: "none", NoCandidate: "no_candidate", BelowMinFit: "below_min_fit", DifferentCut: RefusalDifferentCut,
	PreRelease: "pre_release", LanguageManualOnly: "language_manual_only", PartialCoverage: "partial_coverage",
	FormatUnsupported: RefusalFormatUnsupported, IdentityUnconfirmed: "identity_unconfirmed",
	DifferentEpisode: RefusalDifferentEpisode, EpisodeCount: RefusalEpisodeCount, OrderUndetermined: RefusalOrderUndetermined,
}

// MarshalText is the reason's wire spelling, refusing any other value.
func (w WhyNot) MarshalText() ([]byte, error) {
	if int(w) >= len(whyNotText) {
		return nil, fmt.Errorf("search: no WhyNot %d", w)
	}
	return []byte(whyNotText[w]), nil
}

// UnmarshalText reads a reason's wire spelling and refuses every other one.
func (w *WhyNot) UnmarshalText(b []byte) error {
	for v := range WhyNot(len(whyNotText)) {
		if whyNotText[v] == string(b) {
			*w = v
			return nil
		}
	}
	return fmt.Errorf("search: no WhyNot %q", b)
}
