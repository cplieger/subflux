package search

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/cplieger/pathinside/v2"
)

const hashBlockSize = 65536

var hashBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, hashBlockSize)
		return &b
	},
}

// hashFile computes the OpenSubtitles hash for a video file: file size plus
// the first and last 64KB. ctx is checked between the two I/O operations for
// shutdown cancellation.
func hashFile(ctx context.Context, path string) (hashStr string, fileSize int64, err error) {
	// Validated locally so CodeQL's go/path-injection analyzer can prove
	// safety without tracking the media-root scan that produced path.
	// path is machine-supplied (an arr-reported file path), so a ".."
	// component that normalizes away is a legitimate location, not a
	// suspicious one; only whole ".." segments count as traversal, so a
	// filename merely containing ".." still hashes.
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || pathinside.HasDotDot(clean) {
		return "", 0, fmt.Errorf("hashFile: unsafe path %q", path)
	}
	f, err := os.Open(clean)
	if err != nil {
		return "", 0, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat: %w", err)
	}
	size := fi.Size()

	if size < hashBlockSize*2 {
		return "", size, fmt.Errorf("file too small for hash: %d bytes", size)
	}

	hash := uint64(size)

	bufp, _ := hashBufPool.Get().(*[]byte)
	buf := *bufp
	defer hashBufPool.Put(bufp)

	if _, err := io.ReadFull(f, buf); err != nil {
		return "", size, fmt.Errorf("read head: %w", err)
	}
	for i := range hashBlockSize / 8 {
		hash += binary.LittleEndian.Uint64(buf[i*8 : (i+1)*8])
	}

	if err := ctx.Err(); err != nil {
		return "", size, err
	}

	if _, err := f.Seek(-hashBlockSize, io.SeekEnd); err != nil {
		return "", size, fmt.Errorf("seek tail: %w", err)
	}
	if _, err := io.ReadFull(f, buf); err != nil {
		return "", size, fmt.Errorf("read tail: %w", err)
	}
	for i := range hashBlockSize / 8 {
		hash += binary.LittleEndian.Uint64(buf[i*8 : (i+1)*8])
	}

	hashStr = fmt.Sprintf("%016x", hash)
	slog.Debug("video hash computed", "hash", hashStr, "size", size)
	return hashStr, size, nil
}
