package archive

import (
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"

	"github.com/cplieger/subflux/internal/httputil"
	"github.com/ulikunitz/xz"
)

// Decompress detects compressed data by magic bytes and decompresses it.
// Supports xz (via ulikunitz/xz) and gzip. Passes through plain data
// unchanged. Decompressed output is capped at 5 MB (decompression bomb guard).
func Decompress(data []byte) []byte {
	if isXZ(data) {
		return decompressXZ(data)
	}
	if isGzip(data) {
		return decompressGzip(data)
	}
	return data
}

// isXZ checks for the xz magic number: FD 37 7A 58 5A 00.
func isXZ(data []byte) bool {
	return len(data) > 6 &&
		data[0] == 0xFD && data[1] == 0x37 && data[2] == 0x7A &&
		data[3] == 0x58 && data[4] == 0x5A && data[5] == 0x00
}

// isGzip checks for the gzip magic number: 1F 8B.
func isGzip(data []byte) bool {
	return len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b
}

// decompressXZ decompresses xz data with a 5 MB limit.
// Returns the original data on any decompression error.
func decompressXZ(data []byte) []byte {
	r, err := xz.NewReader(bytes.NewReader(data))
	if err != nil {
		slog.Debug("archive: xz header invalid, "+
			"returning raw data", "error", err, "bytes", len(data))
		return data
	}
	decompressed, err := io.ReadAll(
		io.LimitReader(r, httputil.MaxJSONResponseBytes))
	if err != nil {
		slog.Debug("archive: xz decompression failed, "+
			"returning raw data", "error", err, "bytes", len(data))
		return data
	}
	return decompressed
}

// decompressGzip decompresses gzip data with a 5 MB limit.
// Returns the original data on any decompression error.
func decompressGzip(data []byte) []byte {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		slog.Debug("archive: gzip header invalid, "+
			"returning raw data", "error", err, "bytes", len(data))
		return data
	}
	defer gr.Close()
	decompressed, err := io.ReadAll(
		io.LimitReader(gr, httputil.MaxJSONResponseBytes))
	if err != nil {
		slog.Debug("archive: gzip decompression failed, "+
			"returning raw data", "error", err, "bytes", len(data))
		return data
	}
	return decompressed
}
