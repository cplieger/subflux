package archive

import (
	"bytes"
	"fmt"
	"io"

	"github.com/nwaples/rardecode/v2"
)

// maxRAREntries caps how many entries the walk will inspect before giving up.
// Bounds worst-case iteration cost on pathological archives.
const maxRAREntries = 4096

// rarMembers opens data as a RAR (v4 or v5) and returns a sequence over its
// entries.
//
// The sequence is a STREAM: each member is readable only while it is current, so
// the selection loop must read before it advances. That is why the loop calls
// read inside the body rather than collecting members first, and it is what
// keeps this path from decompressing entries nobody asked for.
//
// The walk stops at the first Next error, which is also how a healthy archive
// ends, so a truncated archive reports whatever the members it did reach support
// rather than a separate transport complaint.
func rarMembers(data []byte) (func(func(member) bool), error) {
	r, err := rardecode.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotArchive, err)
	}

	return func(yield func(member) bool) {
		for range maxRAREntries {
			hdr, err := r.Next()
			if err != nil {
				return
			}
			if !yield(rarMember(r, hdr)) {
				return
			}
		}
	}, nil
}

// rarMember describes the current stream entry as a member. Separate from the
// walk for the same reason zipMember is: the entry gate is exercised against a
// real header rather than against a test's copy of this mapping.
//
// read is only valid while this entry is current, which is the constraint that
// makes the selection loop read before it advances.
func rarMember(r io.Reader, hdr *rardecode.FileHeader) member {
	return member{
		name:        hdr.Name,
		packed:      hdr.PackedSize,
		unpacked:    hdr.UnPackedSize,
		sizeUnknown: hdr.UnKnownSize,
		isDir:       hdr.IsDir,
		read:        func() ([]byte, error) { return readRAREntry(r, hdr.Name) },
	}
}

// readRAREntry reads the current entry's content, capped at maxExtractSize. The
// error is reported for the same reason readZipEntry reports its own: a member
// the reader cannot decompress must not read as an archive holding nothing for
// this episode.
func readRAREntry(r io.Reader, name string) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(r, maxExtractSize+1))
	return checkMemberContent(name, content, err)
}
