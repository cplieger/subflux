package archive

import (
	"bytes"
	"fmt"
	"io"

	"github.com/nwaples/rardecode/v2"
)

// maxRAREntries caps how many entries the walk will inspect before giving up.
const maxRAREntries = 4096

// rarMembers opens data as a RAR (v4 or v5) and returns a sequence over its
// entries.
//
// The sequence is a STREAM: each member is readable only while it is current,
// so the selection loop must read before it advances.
//
// The walk stops at the first Next error, which is also how a healthy archive
// ends, so a truncated archive reports whatever the members it did reach
// support rather than a separate transport complaint.
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

// read is only valid while this entry is current, which is the constraint
// that makes the selection loop read before it advances.
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

func readRAREntry(r io.Reader, name string) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(r, maxExtractSize+1))
	return checkMemberContent(name, content, err)
}
