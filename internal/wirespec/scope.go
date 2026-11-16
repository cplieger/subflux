package wirespec

import "fmt"

// Scope is an endpoint's access level and a key's or principal's effective
// level, ordered Public < Read < Operate < Admin.
type Scope uint8

// The scopes, lowest first.
const (
	Public Scope = iota + 1
	Read
	Operate
	Admin
)

var scopeNames = []string{Public: "public", Read: "read", Operate: "operate", Admin: "admin"}

// MarshalText answers public, read, operate or admin and refuses any other
// value, zero included.
func (s Scope) MarshalText() ([]byte, error) { return marshalName(scopeNames, "scope", uint8(s)) }

// UnmarshalText reads MarshalText's spellings and refuses every other text.
func (s *Scope) UnmarshalText(b []byte) error { return unmarshalName(s, scopeNames, "scope", b) }

// marshalName answers names[v], refusing a v with no name.
func marshalName(names []string, typ string, v uint8) ([]byte, error) {
	if int(v) >= len(names) || names[v] == "" {
		return nil, fmt.Errorf("wirespec: %s %d has no name", typ, v)
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
	return fmt.Errorf("wirespec: unknown %s %q", typ, b)
}
