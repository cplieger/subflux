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
