package language

// Closeness is how far an offered tag sits from a target, nearest first.
// Zero is invalid.
type Closeness uint8

// The closenesses. OtherScript sits after Intelligible because automation
// never crosses scripts, whatever langtag's tier order says.
const (
	Identical Closeness = iota + 1
	SameLanguage
	OtherRegion
	Intelligible
	OtherScript
	ClosenessUnrelated
)

// Class is what subflux may do with an offered tag for a target. Zero is
// invalid.
type Class uint8

// The classes: Exact and Substitute may be automatic (Policy.Automatic),
// ManualOnly is listed for a person to pick, Unrelated is never listed.
const (
	Exact Class = iota + 1
	Substitute
	ManualOnly
	ClassUnrelated
)

// Grade is one target's judgement of one offered tag; Reason is one English
// line describing it.
type Grade struct {
	Reason    string
	Closeness Closeness
	Class     Class
}
