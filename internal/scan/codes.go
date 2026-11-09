package scan

import (
	"slices"

	"github.com/cplieger/subflux/internal/status"
)

// Codes is every sentinel Start returns, status's passed on, with the code
// api answers for it. Start's *mediasource.SourceError is typed, not a
// sentinel, so api lists its codes through Owner.Typed. The element type is
// unnamed, so scan never imports api's wirespec.
var Codes = slices.Concat([]struct {
	Err  error
	Code string
}{
	{ErrScanCapacity, "scan_capacity"},
}, status.StartCodes)
