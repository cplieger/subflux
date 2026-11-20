package timing

import (
	"errors"

	"github.com/cplieger/go/httpwire"
)

var (
	// ErrTimingInvalid is a Timing outside Validate's rules, or timing bytes
	// DecodeTiming cannot read.
	ErrTimingInvalid = errors.New("invalid timing")
	// ErrFormatUnsupported is a subtitle document no parser here reads.
	ErrFormatUnsupported = errors.New("unsupported subtitle format")
)

// Owner maps every sentinel timing's exported calls return to the code the
// API answers for it.
var Owner = &httpwire.Owner{
	Name: "timing",
	Sentinels: map[string]error{
		"ErrTimingInvalid":     ErrTimingInvalid,
		"ErrFormatUnsupported": ErrFormatUnsupported,
	},
	Codes: []httpwire.OwnerCode{
		{Err: ErrTimingInvalid, Code: "timing_invalid"},
		{Err: ErrFormatUnsupported, Code: "format_unsupported"},
	},
}
