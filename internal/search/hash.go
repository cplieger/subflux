package search

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

// hashBlock is the size of each end of the file the OpenSubtitles hash reads.
const hashBlock = 64 << 10

var errHashTooSmall = errors.New("search: video too small to hash")

// videoHash is the OpenSubtitles hash of f: its size plus the little-endian
// uint64 sums of its first and last 64 KiB
// (https://trac.opensubtitles.org/projects/opensubtitles/wiki/HashSourceCodes).
// It reads f with ReadAt, so f's offset is left alone.
func videoHash(f *os.File) (uint64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("search: hash: %w", err)
	}
	size := info.Size()
	if size < 2*hashBlock {
		return 0, fmt.Errorf("%w: %d bytes", errHashTooSmall, size)
	}
	sum := uint64(size)
	buf := make([]byte, hashBlock)
	for _, off := range [2]int64{0, size - hashBlock} {
		if n, err := f.ReadAt(buf, off); n < hashBlock {
			return 0, fmt.Errorf("search: hash: read %d of %d bytes at %d: %w", n, hashBlock, off, err)
		}
		for i := 0; i < hashBlock; i += 8 {
			sum += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return sum, nil
}
