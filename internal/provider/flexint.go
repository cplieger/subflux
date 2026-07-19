package provider

import (
	"fmt"

	"github.com/cplieger/jsonx"
)

// ParseFlexInt parses a JSON value that may be either a number or a quoted
// string into an int, as a thin shim over jsonx.Strict(): bare or quoted
// decimal integers anywhere in int64, null and "" tolerated as 0, everything
// else an error (wrapped to keep the historical "flexint:" message prefix).
// The jsonx policy reproduces this decoder's pinned behavior — TestParseFlexInt
// remains the acceptance spec. Provider-specific FlexInt types that differ in
// strictness (hdbits, subsource) compose their own jsonx policies directly.
func ParseFlexInt(data []byte) (int, error) {
	v, err := jsonx.ParseInt64(data, jsonx.Strict())
	if err != nil {
		return 0, fmt.Errorf("flexint: %w", err)
	}
	return int(v), nil
}
