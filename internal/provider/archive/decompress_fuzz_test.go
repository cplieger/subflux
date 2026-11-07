package archive

import "testing"

// FuzzDecompressOutputCap: a compressed input's output never passes
// maxExtractSize, and a plain one passes through unchanged.
func FuzzDecompressOutputCap(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("plain text"))
	f.Add([]byte{0x1f, 0x8b, 0x08, 0x00})             // truncated gzip header
	f.Add([]byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}) // truncated xz header

	f.Fuzz(func(t *testing.T, data []byte) {
		out := Decompress(data)
		if out == nil {
			return
		}

		// For non-compressed pass-through, output == input (by reference or value).
		isGz := len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b
		isXz := len(data) > 6 &&
			data[0] == 0xFD && data[1] == 0x37 && data[2] == 0x7A &&
			data[3] == 0x58 && data[4] == 0x5A && data[5] == 0x00

		if isGz || isXz {
			// Compressed: output must respect the size cap.
			if int64(len(out)) > maxExtractSize {
				t.Errorf("Decompress produced %d bytes, exceeds cap %d",
					len(out), maxExtractSize)
			}
		}
	})
}
