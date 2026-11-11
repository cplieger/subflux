package search

import (
	"slices"

	"github.com/cplieger/subflux/internal/status"
)

// Codes pairs every sentinel Candidates, Download and SearchNow return with
// the code api answers for it: search's own, then status's StartCodes, the
// only status errors Keyed and Begin return.
var Codes = slices.Concat([]struct {
	Err  error
	Code string
}{
	{ErrVariantUnserved, "variant_unserved"},
	{ErrCandidateUnknown, "candidate_unknown"},
	{ErrSearchCapacity, "search_capacity"},
	{ErrInstanceUnknown, "instance_unknown"},
	{ErrTargetUnconfigured, "target_unconfigured"},
}, status.StartCodes)
