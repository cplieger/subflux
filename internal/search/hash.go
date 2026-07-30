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

	"github.com/cplieger/pathinside"
)

const hashBlockSize = 65536

var hashBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, hashBlockSize)
		return &b
	},
}

// hashFile computes the OpenSubtitles hash for a video file.
// The hash is based on the file size and the first and last 64KB of the file.
// ctx is checked between the two I/O operations for shutdown cancellation.
func hashFile(ctx context.Context, path string) (hashStr string, fileSize int64, err error) {
	// Validate the path locally so CodeQL's go/path-injection analyzer
	// can prove safety without tracking the media-root scan that
	// produced `path`. Read-only hashing still warrants the guard:
	// reject non-absolute paths and ".." traversal segments. Only whole
	// ".." segments are traversal — a filename merely containing ".."
	// (e.g. "Show.S01E01..720p.mkv") is legitimate and must still hash,
	// which is exactly pathinside.HasDotDot's component-precise rule.
	// The test runs on the CLEANED path deliberately: `path` here is
	// machine-supplied (an arr-reported file path from the media scan),
	// not a human-written config value, so a traversal that normalizes
	// away is a spelling of a legitimate location rather than a
	// suspicious one — the hygiene axis's "a human would not have
	// written it that way" argument does not apply to this input class.
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

	// Read first 64KB.
	if _, err := io.ReadFull(f, buf); err != nil {
		return "", size, fmt.Errorf("read head: %w", err)
	}
	for i := range hashBlockSize / 8 {
		hash += binary.LittleEndian.Uint64(buf[i*8 : (i+1)*8])
	}

	// Check for cancellation between I/O operations.
	if err := ctx.Err(); err != nil {
		return "", size, err
	}

	// Read last 64KB.
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
