package archive

import (
	"archive/zip"
	"bytes"
	"testing"
)

func FuzzExtract(f *testing.F) {
	// Seed: valid SRT
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 1, 5)
	// Seed: RAR magic prefix + garbage
	f.Add([]byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x00, 0xFF, 0xFE}, 1, 1)
	// Seed: raw SRT content
	f.Add([]byte("1\n00:00:00,000 --> 00:00:01,000\nTest\n"), 0, 0)
	// Seed: empty
	f.Add([]byte{}, 1, 1)
	// Seed: all zeros
	f.Add(make([]byte, 16), 1, 1)
	// Seed: ZIP magic + truncated
	f.Add([]byte{'P', 'K', 3, 4, 0, 0}, 1, 1)
	// Seed: RAR magic + truncated
	f.Add([]byte{'R', 'a', 'r', '!', 0x1a, 0x07}, 2, 3)
	// Seed: valid subtitle with BOM
	f.Add(append([]byte{0xEF, 0xBB, 0xBF}, []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nHi\n")...), 1, 1)
	// Seed: minimal valid ZIP with SRT inside
	f.Add(makeMinimalZipSRT(), 1, 5)

	f.Fuzz(func(t *testing.T, data []byte, season, episode int) {
		result := Extract(data, season, episode)
		if result != nil {
			if !LooksLikeSubtitle(result) {
				t.Errorf("Extract returned non-subtitle content")
			}
			if len(result) > MaxExtractSize {
				t.Errorf("Extract returned %d bytes, exceeds MaxExtractSize", len(result))
			}
		}
	})
}

func makeMinimalZipSRT() []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("Show.S01E05.srt")
	_, _ = f.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"))
	_ = w.Close()
	return buf.Bytes()
}

// FuzzHasArchiveSignature checks the reported signature against the known
// archive magic bytes in both directions (an oracle property): data beginning
// with the ZIP (PK\x03\x04) or RAR (Rar!\x1a\x07) magic must be reported true,
// and anything reported true must begin with one of those magic sequences.
func FuzzHasArchiveSignature(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("PK\x03\x04some zip content"))
	f.Add([]byte("Rar!\x1a\x07\x00"))
	f.Add([]byte("Rar!\x1a\x07\x01\x00"))
	f.Add([]byte("not an archive"))
	f.Add([]byte{0x1f, 0x8b}) // gzip magic — not an archive

	f.Fuzz(func(t *testing.T, data []byte) {
		result := HasArchiveSignature(data)

		isZIP := len(data) >= 4 &&
			data[0] == 'P' && data[1] == 'K' && data[2] == 3 && data[3] == 4
		isRAR := len(data) >= 6 &&
			data[0] == 'R' && data[1] == 'a' && data[2] == 'r' &&
			data[3] == '!' && data[4] == 0x1a && data[5] == 0x07

		if (isZIP || isRAR) && !result {
			t.Errorf("HasArchiveSignature = false for data with archive magic (zip=%v, rar=%v, len=%d)",
				isZIP, isRAR, len(data))
		}
		if result && !isZIP && !isRAR {
			t.Errorf("HasArchiveSignature = true but data has no archive magic (len=%d, prefix=%x)",
				len(data), data[:min(8, len(data))])
		}
	})
}

// FuzzLooksLikeSubtitle checks BOM-insensitivity (a metamorphic property):
// prepending a UTF-8 BOM to data that does not already start with one must not
// change the verdict, because LooksLikeSubtitle strips a single leading BOM
// before probing.
func FuzzLooksLikeSubtitle(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"))
	f.Add([]byte("[Script Info]\nTitle: Test"))
	f.Add([]byte("WEBVTT\n\n00:00.000 --> 00:01.000\nHi"))
	f.Add([]byte("plain text without any signature"))
	f.Add(make([]byte, 4096))

	bom := []byte{0xEF, 0xBB, 0xBF}
	f.Fuzz(func(t *testing.T, data []byte) {
		got := LooksLikeSubtitle(data)

		if !bytes.HasPrefix(data, bom) {
			withBOM := append(append([]byte{}, bom...), data...)
			if LooksLikeSubtitle(withBOM) != got {
				t.Errorf("LooksLikeSubtitle BOM-insensitivity violated for %q", data)
			}
		}
	})
}
