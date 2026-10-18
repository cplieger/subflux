package mediasource

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cplieger/subflux/internal/media"
)

// Cause is why a source call failed.
type Cause uint8

// The causes; the zero Cause is unset.
const (
	Unreachable Cause = iota + 1
	Unauthorized
	NotFound
	Protocol
	Unsupported
	UnknownSource
	// Expired is a cursor the source no longer serves; a whole pass replaces it.
	Expired
)

var causeNames = [...]string{
	Unreachable:   "unreachable",
	Unauthorized:  "unauthorized",
	NotFound:      "not_found",
	Protocol:      "protocol",
	Unsupported:   "unsupported",
	UnknownSource: "unknown_source",
	Expired:       "expired",
}

// String is the snake_case value name, the spelling api's error codes and the
// source alert's text use; an undefined value is cause(<n>).
func (c Cause) String() string {
	if c == 0 || int(c) >= len(causeNames) {
		return fmt.Sprintf("cause(%d)", uint8(c))
	}
	return causeNames[c]
}

// SourceError is every failure of a source call. Op is the method name.
type SourceError struct {
	Err    error
	Source media.SourceID
	Op     string
	Cause  Cause
}

// Error is "source <id> <op>: <cause>: <err>", leaving out an unset Source,
// Op or Err.
func (e *SourceError) Error() string {
	var b strings.Builder
	b.WriteString("source")
	if e.Source != "" {
		b.WriteString(" " + string(e.Source))
	}
	if e.Op != "" {
		b.WriteString(" " + e.Op)
	}
	b.WriteString(": " + e.Cause.String())
	if e.Err != nil {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

// Unwrap returns Err, so errors.Is and httpx.IsTransient see the cause.
func (e *SourceError) Unwrap() error { return e.Err }

// ErrRescanOutcomeUnknown wraps a Rescan the source may have run; the caller
// never sends it again.
var ErrRescanOutcomeUnknown = errors.New("rescan outcome unknown")
