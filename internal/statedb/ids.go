// Package statedb is subflux's one store: the core namespace of a bolttable
// file holding source facts, ownership, coverage, locks, the write protocol's
// rows, history, work, jobs and credentials (the storage spec's design
// sections 3 to 10).
package statedb

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/cplieger/subflux/internal/language"
)

// InstanceID names one video file's instance. Zero is invalid.
type InstanceID uint64

// String is the canonical base-10 text ParseInstanceID reads.
func (id InstanceID) String() string { return strconv.FormatUint(uint64(id), 10) }

var errInstanceID = errors.New("statedb: an instance id is a non-zero decimal with no sign, space or leading zero")

// ParseInstanceID is String's inverse for a non-zero id, refusing every other
// spelling: a sign, a space, a leading zero, a base prefix, a value past
// 2^64-1.
func ParseInstanceID(s string) (InstanceID, error) {
	if s == "" || s[0] < '1' || s[0] > '9' {
		return 0, errInstanceID
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errInstanceID
		}
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, errInstanceID
	}
	return InstanceID(v), nil
}

// MarshalText refuses the zero id, which ParseInstanceID would refuse.
func (id InstanceID) MarshalText() ([]byte, error) {
	if id == 0 {
		return nil, errInstanceID
	}
	return []byte(id.String()), nil
}

// UnmarshalText reads what ParseInstanceID reads, leaving id unchanged on a
// refusal.
func (id *InstanceID) UnmarshalText(b []byte) error {
	v, err := ParseInstanceID(string(b))
	if err != nil {
		return err
	}
	*id = v
	return nil
}

// OrdinalKey names one sequence of numbered files of an instance: the files
// of one tag and flags.
type OrdinalKey struct {
	Tag   language.Tag
	Inst  InstanceID
	Flags language.Flags
}

// Coverage is how well an item's files serve one configured pair. Zero is
// invalid.
type Coverage uint8

// The coverages, worst first.
const (
	Missing Coverage = iota + 1
	Substitute
	Exact
)

var coverageText = [...]string{Missing: "missing", Substitute: "substitute", Exact: "exact"}

// MarshalText is missing, substitute or exact, refusing any other value.
func (c Coverage) MarshalText() ([]byte, error) {
	if c < Missing || c > Exact {
		return nil, fmt.Errorf("statedb: coverage %d is not missing, substitute or exact", c)
	}
	return []byte(coverageText[c]), nil
}

// UnmarshalText reads exactly what MarshalText writes, leaving c unchanged on
// a refusal.
func (c *Coverage) UnmarshalText(b []byte) error {
	for v := Missing; v <= Exact; v++ {
		if string(b) == coverageText[v] {
			*c = v
			return nil
		}
	}
	return fmt.Errorf("statedb: coverage %q is not missing, substitute or exact", b)
}

// WorkCause is why an instance is queued for work; Item outranks Import,
// which outranks Scan. Zero is invalid.
type WorkCause uint8

// The work causes.
const (
	Scan WorkCause = iota + 1
	Import
	Item
)

// Op is a pending write-protocol operation. Zero is invalid.
type Op uint8

// The operations.
const (
	Deliver Op = iota + 1
	Retime
	Delete
)
