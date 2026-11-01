package media

import "errors"

// SourceID names one configured media source: 1 to 32 bytes of [a-z0-9-].
type SourceID string

const maxSourceID = 32

// errSourceID is the one refusal of a source id, worded as the field error the
// sources section reports at an entry's id.
var errSourceID = errors.New("must be 1 to 32 characters of a-z, 0-9 and -, such as sonarr-4k")

// MarshalText refuses an id outside [a-z0-9-]{1,32}, so no id is written that
// UnmarshalText would refuse.
func (id SourceID) MarshalText() ([]byte, error) {
	if !validSourceID(string(id)) {
		return nil, errSourceID
	}
	return []byte(id), nil
}

// UnmarshalText refuses anything outside [a-z0-9-]{1,32}, leaving id unchanged.
func (id *SourceID) UnmarshalText(b []byte) error {
	if !validSourceID(string(b)) {
		return errSourceID
	}
	*id = SourceID(b)
	return nil
}

func validSourceID(s string) bool {
	if s == "" || len(s) > maxSourceID {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
