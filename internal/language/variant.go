package language

// Variant is what a target asks for. Zero is invalid.
type Variant uint8

// Standard asks for full subtitles, HI for subtitles for the deaf and hard of
// hearing, Forced for only the lines in another language or on screen.
const (
	Standard Variant = iota + 1
	HI
	Forced
)

// Flags is what a file, track or candidate carries; all four combinations are
// valid.
type Flags struct{ HI, Forced bool }

// Serves reports whether a file carrying f serves a target asking for v: HI
// needs HI set, Forced needs Forced set, Standard needs neither. No flags
// serve an invalid Variant.
func (f Flags) Serves(v Variant) bool {
	switch v {
	case Standard:
		return !f.HI && !f.Forced
	case HI:
		return f.HI
	case Forced:
		return f.Forced
	}
	return false
}

// Flags is the flags that serve v with nothing extra, which rank first among
// the files serving it; the zero Flags for an invalid Variant.
func (v Variant) Flags() Flags {
	return Flags{HI: v == HI, Forced: v == Forced}
}
