package archive

import (
	"testing"

	"github.com/cplieger/subflux/internal/httputil"
)

func FuzzDecompress(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("plain text"))
	f.Add([]byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00, 0xFF}) // xz magic + garbage
	f.Add([]byte{0x1f, 0x8b, 0x08, 0x00})                   // gzip magic + truncated
	f.Add(make([]byte, 128))

	f.Fuzz(func(t *testing.T, data []byte) {
		result := Decompress(data)
		if result == nil {
			t.Fatal("Decompress returned nil")
		}
		if int64(len(result)) > httputil.MaxJSONResponseBytes {
			t.Fatalf("output %d exceeds MaxJSONResponseBytes", len(result))
		}
	})
}

func FuzzMatchesEpisode(f *testing.F) {
	f.Add("Show.S01E05.srt", 1, 5)
	f.Add("Show.S02E01E02.srt", 2, 1)
	f.Add("", 0, 0)
	f.Add("random.txt", 1, 1)
	f.Add("S99E999.srt", 99, 999)

	f.Fuzz(func(t *testing.T, name string, season, episode int) {
		_ = MatchesEpisode(name, season, episode)
	})
}

func FuzzMatchesMultiEpisodeRange(f *testing.F) {
	f.Add("Show.S01E01E02.srt", 1)
	f.Add("Show.S01E01-E05.srt", 3)
	f.Add("Show.S01E01-02.srt", 2)
	f.Add("Show.S01E01.E02.srt", 2)
	f.Add("", 0)

	f.Fuzz(func(t *testing.T, base string, episode int) {
		_ = MatchesMultiEpisodeRange(base, episode)
	})
}

func FuzzLooksLikeSubtitle(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"))
	f.Add([]byte("[Script Info]\nTitle: Test"))
	f.Add([]byte("WEBVTT\n\n00:00.000 --> 00:01.000\nHi"))
	f.Add(append([]byte{0xEF, 0xBB, 0xBF}, []byte("text")...))
	f.Add(make([]byte, 4096))

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = LooksLikeSubtitle(data)
	})
}
