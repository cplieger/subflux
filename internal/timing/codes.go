package timing

import "errors"

var (
	// ErrTimingInvalid is a Timing outside Validate's rules, or timing bytes
	// DecodeTiming cannot read.
	ErrTimingInvalid = errors.New("invalid timing")
	// ErrFormatUnsupported is a subtitle document no parser here reads.
	ErrFormatUnsupported = errors.New("unsupported subtitle format")
)

// Codes is every sentinel timing's exported calls return, with the code api
// answers for it. The element type is unnamed, so timing never imports api's
// wirespec.
var Codes = []struct {
	Err  error
	Code string
}{
	{ErrTimingInvalid, "timing_invalid"},
	{ErrFormatUnsupported, "format_unsupported"},
}
