// Package episodematch decides which member of a season pack, and whether a
// single name, is an item's episode, from evidence alone: a pack's numbers
// follow whatever order its uploader used, so a number is trusted only when
// the evidence leaves no other reading.
//
// A name gives its claims (through epmarker) and its episode title. Titles
// are assigned to episodes one to one by the measured normaliser and
// thresholds; a member's identity is its assigned episode, else its claims.
// The known orders that no assigned member contradicts, and whose count of
// the pack's distinct episodes matches the item's season, then decide by
// number, or name the members a reference track must settle. It is pure
// over its inputs and imports no provider.
package episodematch
