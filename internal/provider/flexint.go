package provider

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ParseFlexInt parses a JSON value that may be either a number or a quoted
// string into an int. Returns the parsed value and any error. This is the
// shared core for provider-specific FlexInt types that differ in strictness
// (e.g. whether zero/negative values or null are acceptable).
func ParseFlexInt(data []byte) (int, error) {
	// Try number first (most common).
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		return n, nil
	}
	// Try quoted string.
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return 0, fmt.Errorf("flexint: cannot unmarshal %s", string(data))
	}
	if s == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("flexint: non-numeric string %q", s)
	}
	return v, nil
}
