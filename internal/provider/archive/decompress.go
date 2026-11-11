package archive

import (
	"bytes"
	"compress/gzip"
	"io"
)

// Decompress unwraps gzip or one xz stream, found by magic, and returns any
// other bytes as they are. Output past maxMember is a refusal, never a
// truncation, and so is a stream that does not decode.
func Decompress(data []byte) ([]byte, error) {
	switch {
	case bytes.HasPrefix(data, xzMagic):
		return decompressXZ(data)
	case bytes.HasPrefix(data, gzipMagic):
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, refuse(err, "gzip")
		}
		return readCapped("gzip", gr)
	}
	return data, nil
}

var gzipMagic = []byte{0x1f, 0x8b}

func readCapped(format string, r io.Reader) ([]byte, error) {
	out, err := io.ReadAll(io.LimitReader(r, maxMember+1))
	switch {
	case err != nil:
		return nil, refuse(err, "%s", format)
	case len(out) > maxMember:
		return nil, refuse(nil, "%s output is above the %d-byte cap", format, maxMember)
	}
	return out, nil
}
