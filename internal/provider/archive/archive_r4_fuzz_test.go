package archive

import "testing"

// FuzzHasArchiveSignature tests that HasArchiveSignature is consistent with
// the Extract function's early-exit logic. Bug class: if HasArchiveSignature
// returns false for data that actually has an archive magic prefix, Extract
// may skip archive probing and incorrectly treat binary archive data as
// non-subtitle, losing valid content.
// Invariant: if first 4 bytes are "PK\x03\x04" or first 6 bytes are "Rar!\x1a\x07",
// HasArchiveSignature MUST return true.
func FuzzHasArchiveSignature(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("PK\x03\x04some zip content"))
	f.Add([]byte("Rar!\x1a\x07\x00"))
	f.Add([]byte("Rar!\x1a\x07\x01\x00"))
	f.Add([]byte("not an archive"))
	f.Add([]byte{0x1f, 0x8b}) // gzip magic - not an archive

	f.Fuzz(func(t *testing.T, data []byte) {
		result := HasArchiveSignature(data)

		// Verify consistency: check known magic bytes.
		isZIP := len(data) >= 4 &&
			data[0] == 'P' && data[1] == 'K' && data[2] == 3 && data[3] == 4
		isRAR := len(data) >= 6 &&
			data[0] == 'R' && data[1] == 'a' && data[2] == 'r' &&
			data[3] == '!' && data[4] == 0x1a && data[5] == 0x07

		if (isZIP || isRAR) && !result {
			t.Errorf("HasArchiveSignature returned false for data with known archive magic (zip=%v, rar=%v, len=%d)",
				isZIP, isRAR, len(data))
		}
		if result && !isZIP && !isRAR {
			t.Errorf("HasArchiveSignature returned true but data does not have known archive magic (len=%d, prefix=%x)",
				len(data), data[:min(8, len(data))])
		}
	})
}
