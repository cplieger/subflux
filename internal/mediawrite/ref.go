package mediawrite

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subtitleext"
)

// FileRef names one subtitle file beside an instance's video. Its zero
// value names nothing; DecodeRef and RefOf build it.
type FileRef struct {
	name string
	inst statedb.InstanceID
}

// Inst is the instance whose video the file is beside.
func (r FileRef) Inst() statedb.InstanceID { return r.inst }

// Name is the file's name in the video's directory.
func (r FileRef) Name() string { return r.name }

// RefOf is the reference of a stored record.
func RefOf(rec statedb.FileRecord) FileRef { //nolint:gocritic // hugeParam: a record is read, never kept
	return FileRef{inst: rec.Inst, name: rec.Name}
}

// DecodeRef builds the reference of name beside inst's video from wire
// input: ErrInvalidRef for the zero instance or a name ValidName refuses.
func DecodeRef(inst statedb.InstanceID, name string) (FileRef, error) {
	if inst == 0 {
		return FileRef{}, fmt.Errorf("%w: no instance", ErrInvalidRef)
	}
	if err := ValidName(name); err != nil {
		return FileRef{}, err
	}
	return FileRef{inst: inst, name: name}, nil
}

// ValidName is DecodeRef's syntax check with no instance: one local path
// element with an extension subtitleext.OnDisk accepts, else ErrInvalidRef.
func ValidName(name string) error {
	if !filepath.IsLocal(name) || strings.ContainsAny(name, `/\`+"\x00") || name == "." || !subtitleext.OnDisk(name) {
		return fmt.Errorf("%w: %q is not one subtitle file name", ErrInvalidRef, name)
	}
	return nil
}

// Expectation is the state of a file a caller saw: the record's generation
// (0 for an unmanaged file) and the bytes' SHA-256. The zero value is an
// unmanaged file listed without hashing, which every write refuses.
type Expectation struct {
	Generation uint64
	SHA256     [32]byte
}
