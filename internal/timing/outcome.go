package timing

import "fmt"

// Action is what a timing pass does with a subtitle.
type Action uint8

// The actions; the zero Action is none.
const (
	Skip Action = iota + 1
	ActionReference
	ReferenceThenAudio
	// RigidReference syncs by Shift or Linear only and runs no audio: the
	// answer for a flagged different cut.
	RigidReference
)

// SkipReason says why a Decision skips; it is SkipReasonNone exactly when
// the Action is not Skip.
type SkipReason uint8

// The skip reasons: a validated hash, the same release, a forced variant,
// or sync turned off.
const (
	SkipReasonNone SkipReason = iota
	Hash
	SameRelease
	Forced
	Disabled
)

// Decision is a timing pass's plan for one subtitle.
type Decision struct {
	Action Action
	Skip   SkipReason
}

// Method is what decided a delivered Timing.
type Method uint8

// The methods; MethodNone when the pass was skipped or had no reference.
const (
	MethodNone Method = iota
	MethodReference
	Audio
)

// Verdict is how far a delivered Timing is trusted.
type Verdict uint8

// The verdicts: Trusted when the pass was skipped, Verified when a gate
// passed or a user edited the timing, Unverified otherwise (shown to users
// as "Timing unverified"). The zero Verdict is none.
const (
	Trusted Verdict = iota + 1
	Verified
	Unverified
)

// Outcome records one timing pass. Confidence is finite in [0, 1] and is
// read per Method, never compared across methods; OffsetMs is within ±6 h.
type Outcome struct {
	Confidence float64
	OffsetMs   int64
	Decision   Decision
	Method     Method
	Verdict    Verdict
}

// The text codecs spell each value's snake_case name; an index with an
// empty name is not a value.
var (
	actionNames     = []string{"", "skip", "reference", "reference_then_audio", "rigid_reference"}
	skipReasonNames = []string{"none", "hash", "same_release", "forced", "disabled"}
	methodNames     = []string{"none", "reference", "audio"}
	verdictNames    = []string{"", "trusted", "verified", "unverified"}
	formatNames     = []string{"", "srt", "ass", "ssa"}
)

func marshalName(names []string, typ string, v uint8) ([]byte, error) {
	if int(v) >= len(names) || names[v] == "" {
		return nil, fmt.Errorf("timing: %s %d has no name", typ, v)
	}
	return []byte(names[v]), nil
}

// unmarshalName leaves *dst unchanged on error.
func unmarshalName[T ~uint8](dst *T, names []string, typ string, b []byte) error {
	for i, n := range names {
		if n != "" && n == string(b) {
			*dst = T(i)
			return nil
		}
	}
	return fmt.Errorf("timing: unknown %s %q", typ, b)
}

// MarshalText answers the action's snake_case name and refuses the zero Action.
func (a Action) MarshalText() ([]byte, error) { return marshalName(actionNames, "Action", uint8(a)) }

// UnmarshalText reads a name MarshalText writes and leaves a on error.
func (a *Action) UnmarshalText(b []byte) error { return unmarshalName(a, actionNames, "Action", b) }

// MarshalText answers the reason's snake_case name, "none" included.
func (s SkipReason) MarshalText() ([]byte, error) {
	return marshalName(skipReasonNames, "SkipReason", uint8(s))
}

// UnmarshalText reads a name MarshalText writes and leaves s on error.
func (s *SkipReason) UnmarshalText(b []byte) error {
	return unmarshalName(s, skipReasonNames, "SkipReason", b)
}

// MarshalText answers the method's snake_case name, "none" included.
func (m Method) MarshalText() ([]byte, error) { return marshalName(methodNames, "Method", uint8(m)) }

// UnmarshalText reads a name MarshalText writes and leaves m on error.
func (m *Method) UnmarshalText(b []byte) error { return unmarshalName(m, methodNames, "Method", b) }

// MarshalText answers the verdict's snake_case name and refuses the zero Verdict.
func (v Verdict) MarshalText() ([]byte, error) {
	return marshalName(verdictNames, "Verdict", uint8(v))
}

// UnmarshalText reads a name MarshalText writes and leaves v on error.
func (v *Verdict) UnmarshalText(b []byte) error {
	return unmarshalName(v, verdictNames, "Verdict", b)
}

// MarshalText answers srt, ass or ssa and refuses the zero Format.
func (f Format) MarshalText() ([]byte, error) { return marshalName(formatNames, "Format", uint8(f)) }

// UnmarshalText reads a name MarshalText writes and leaves f on error.
func (f *Format) UnmarshalText(b []byte) error { return unmarshalName(f, formatNames, "Format", b) }
