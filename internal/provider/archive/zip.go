package archive

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
)

// maxZipEntries caps how many central-directory entries a zip may declare.
// A crafted archive can advertise millions of stub entries and force Go's
// zip.Reader to allocate a large []*File; the cap bounds worst-case memory
// without affecting real subtitle packs (largest observed: ~30 files).
const maxZipEntries = 4096

// zipMembers opens data as a zip and returns a sequence over its entries.
//
// A zip is random-access, so every member stays readable for as long as the
// reader lives. The rar sequence is a stream and is not, which is why the
// selection loop reads a member before advancing.
func zipMembers(data []byte) (func(func(member) bool), error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotArchive, err)
	}
	if len(r.File) > maxZipEntries {
		return nil, fmt.Errorf("zip declares %d entries, above the %d cap",
			len(r.File), maxZipEntries)
	}

	return func(yield func(member) bool) {
		for _, f := range r.File {
			if !yield(zipMember(f)) {
				return
			}
		}
	}, nil
}

func zipMember(f *zip.File) member {
	packed, packedOK := declaredSize(f.CompressedSize64)
	unpacked, unpackedOK := declaredSize(f.UncompressedSize64)
	return member{
		name:        f.Name,
		packed:      packed,
		unpacked:    unpacked,
		sizeUnknown: !packedOK || !unpackedOK,
		isDir:       f.FileInfo().IsDir(),
		read:        func() ([]byte, error) { return readZipEntry(f) },
	}
}

// Go's archive/zip implements Store and Deflate only, so a member compressed
// with LZMA or PPMd opens with zip.ErrAlgorithm even though the central
// directory read perfectly; the error is reported rather than folded into a
// nil result so that case is not read as no subtitle for the episode.
func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open member %s: %w", summarizeNames([]string{f.Name}), err)
	}
	content, err := io.ReadAll(io.LimitReader(rc, maxExtractSize+1))
	_ = rc.Close()
	return checkMemberContent(f.Name, content, err)
}
