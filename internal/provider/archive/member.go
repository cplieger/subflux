package archive

import (
	"io"
	"math"
	"path/filepath"
	"strings"

	"github.com/cplieger/go/textsafe"
	"github.com/cplieger/subflux/internal/subtitleext"
)

// bombRatio is the largest expansion a member may declare: subtitle text
// compresses well but not that well, and the ratio is the cheapest
// decompression-bomb guard before any read.
const bombRatio = 50

// member is one archive entry, whichever container produced it.
type member struct {
	// read returns the member's content, at most maxMember bytes, or a
	// refusal.
	read func() ([]byte, error)

	name string
	// packed and unpacked are the sizes the container declares. sizeUnknown
	// means it declared none, or one this program cannot represent.
	packed      int64
	unpacked    int64
	sizeUnknown bool
	isDir       bool
}

// declaredSize converts a container's unsigned size, reporting whether it
// fits: a silent wrap would turn a bomb into a negative size the ratio
// guard reads as harmless.
func declaredSize(n uint64) (int64, bool) {
	if n > math.MaxInt64 {
		return 0, false
	}
	return int64(n), true
}

// gate is whether a member is offered to choose at all.
func gate(m member) bool {
	switch {
	case m.isDir, !subtitleext.ArchiveInput(m.name):
		return false
	// The __MACOSX/._name.srt noise a macOS-made archive carries.
	case strings.HasPrefix(filepath.Base(m.name), "."):
		return false
	case m.sizeUnknown || m.packed < 0 || m.unpacked < 0 || m.unpacked > maxMember:
		return false
	case m.packed == 0 && m.unpacked > 0:
		return false
	case m.packed > 0 && m.unpacked/m.packed > bombRatio:
		return false
	}
	return true
}

// readMember reads one member's content whole, refusing it past maxMember
// rather than truncating it.
func readMember(name string, r io.Reader) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(r, maxMember+1))
	safe := textsafe.SanitizeSingleLineBounded(name, maxNameBytes)
	switch {
	case err != nil:
		return nil, refuse(err, "read member %s", safe)
	case len(content) == 0:
		return nil, refuse(nil, "member %s is empty", safe)
	case len(content) > maxMember:
		return nil, refuse(nil, "member %s is above the %d-byte cap", safe, maxMember)
	}
	return content, nil
}
