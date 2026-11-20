package search

import "github.com/cplieger/go/httpwire"

// Owner maps every sentinel Candidates, Download and SearchNow return to the
// code the API answers for it.
var Owner = &httpwire.Owner{
	Name: "search",
	Sentinels: map[string]error{
		"ErrVariantUnserved":    ErrVariantUnserved,
		"ErrCandidateUnknown":   ErrCandidateUnknown,
		"ErrSearchCapacity":     ErrSearchCapacity,
		"ErrInstanceUnknown":    ErrInstanceUnknown,
		"ErrTargetUnconfigured": ErrTargetUnconfigured,
	},
	Codes: []httpwire.OwnerCode{
		{Err: ErrVariantUnserved, Code: "variant_unserved"},
		{Err: ErrCandidateUnknown, Code: "candidate_unknown"},
		{Err: ErrSearchCapacity, Code: "search_capacity"},
		{Err: ErrInstanceUnknown, Code: "instance_unknown"},
		{Err: ErrTargetUnconfigured, Code: "target_unconfigured"},
	},
}
