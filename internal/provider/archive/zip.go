package archive

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
)

// maxZipEntries caps how many central-directory entries a zip may declare.
// A crafted archive can advertise millions of stub entries and force Go's
// zip.Reader to allocate a large []*File. The cap bounds worst-case memory
// without affecting real subtitle packs (largest observed: ~30 files).
const maxZipEntries = 4096

// zipMembers opens data as a zip and returns a sequence over its entries.
//
// A zip is random-access, so the sequence is just a walk of the central
// directory and every member stays readable for as long as the reader lives.
// The rar sequence is a stream and is not, which is why the selection loop reads
// a member before advancing.
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

// zipMember describes one central-directory entry as a member. Separate from the
// walk so the entry gate can be exercised against a real *zip.File without a
// test rebuilding this mapping and drifting from it.
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

// readZipEntry reads one entry's content, capped at maxExtractSize.
//
// The error is reported rather than folded into a nil result because it is the
// one thing a caller cannot re-derive. Go's archive/zip implements Store and
// Deflate only, so a member an uploader compressed with LZMA or PPMd opens with
// zip.ErrAlgorithm even though the central directory read perfectly. Swallowing
// that made an unsupported compression method indistinguishable from an archive
// holding no subtitle for the requested episode, and a real pack was diagnosed
// as the wrong one of those two for exactly that reason.
func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open member %s: %w", summarizeNames([]string{f.Name}), err)
	}
	content, err := io.ReadAll(io.LimitReader(rc, maxExtractSize+1))
	_ = rc.Close()
	return checkMemberContent(f.Name, content, err)
}
