// Package archive unwraps a fetched subtitle payload: Decompress removes a
// gzip or xz layer and Extract takes one member out of a zip or rar.
//
// Every decoder allocation is bounded from the headers before the decoder
// runs (xz.go, zipwalk.go, rar.go), since a few crafted header bytes can
// otherwise declare gigabytes. ErrNotArchive is the only refusal a caller
// branches on; every refusal of bytes that did open is one line and
// httpclient.Permanent, so no retry fetches the same bytes again.
package archive

import (
	"errors"
	"fmt"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/textsafe"
)

// ErrNotArchive reports bytes that are neither zip nor rar: they are the
// subtitle candidate as they are.
var ErrNotArchive = errors.New("not a readable zip or rar archive")

const (
	// maxMember bounds one extracted member and Decompress's output.
	maxMember = 5 << 20
	// maxEntries bounds the zip central-directory records and rar headers
	// read; real subtitle packs hold a few dozen.
	maxEntries = 4096

	maxNameBytes  = 128
	maxCauseBytes = 512
)

// Extract returns the member choose picks, calling it once with every gated
// member's full name in archive order. Before zip.NewReader, which reads
// central-directory headers until a bad one whatever the end record
// declares, it walks the directory from its classic or ZIP64 offset,
// refusing at record maxEntries+1 or a count the walk contradicts, and
// checks len(r.File) after the open. Bytes neither zip nor rar give
// ErrNotArchive; any other error, a choose error or an index outside the
// list included, refuses an archive that did open.
func Extract(data []byte, choose func(names []string) (int, error)) ([]byte, error) {
	members, err := open(data)
	if err != nil {
		return nil, err
	}
	var gated []member
	for _, m := range members {
		if gate(m) {
			gated = append(gated, m)
		}
	}
	if len(gated) == 0 {
		return nil, refuse(nil, "archive holds no subtitle member among its %d", len(members))
	}
	names := make([]string, len(gated))
	for i := range gated {
		names[i] = gated[i].name
	}
	i, err := choose(names)
	if err != nil {
		return nil, refuse(err, "no member chosen of %d", len(gated))
	}
	if i < 0 || i >= len(gated) {
		return nil, refuse(nil, "member %d chosen of %d", i, len(gated))
	}
	return gated[i].read()
}

// open reads data's members by its magic, trying both containers when it
// carries neither at offset zero (a self-extracting or prefixed archive).
func open(data []byte) ([]member, error) {
	switch {
	case hasZIPMagic(data):
		ms, err := zipMembers(data)
		if errors.Is(err, errNoContainer) {
			return nil, refuse(nil, "zip has no end of central directory record")
		}
		return ms, err
	case hasRARMagic(data):
		ms, err := rarMembers(data)
		if errors.Is(err, errNoContainer) {
			return nil, refuse(nil, "rar signature unreadable")
		}
		return ms, err
	}
	ms, err := zipMembers(data)
	if !errors.Is(err, errNoContainer) {
		return ms, err
	}
	ms, err = rarMembers(data)
	if errors.Is(err, errNoContainer) {
		return nil, ErrNotArchive
	}
	return ms, err
}

// errNoContainer is a reader finding no trace of its container, which open
// turns into ErrNotArchive or, under that container's magic, a refusal.
var errNoContainer = errors.New("no container")

// refusal is every refusal of bytes that opened. Its text lands in a slog
// attribute, so it is one line rather than an errors.Join.
type refusal struct {
	err error
	msg string
}

func (r *refusal) Error() string {
	if r.err == nil {
		return r.msg
	}
	return r.msg + ": " + textsafe.SanitizeSingleLineBounded(r.err.Error(), maxCauseBytes)
}

func (r *refusal) Unwrap() error { return r.err }

func refuse(cause error, format string, args ...any) error {
	return httpclient.Permanent(&refusal{err: cause, msg: fmt.Sprintf(format, args...)})
}

func hasZIPMagic(data []byte) bool {
	return len(data) >= 4 && string(data[:4]) == "PK\x03\x04"
}

func hasRARMagic(data []byte) bool {
	return len(data) >= 6 && string(data[:6]) == "Rar!\x1a\x07"
}
