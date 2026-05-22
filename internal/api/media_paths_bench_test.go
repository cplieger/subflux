package api

import (
	"bytes"
	"testing"
)

func BenchmarkValidateSubtitleData(b *testing.B) {
	for _, size := range []int{64, 512, 4096} {
		data := bytes.Repeat([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello world\n\n"), size/40+1)
		data = data[:size]
		b.Run(sizeLabel(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for range b.N {
				_ = ValidateSubtitleData(data)
			}
		})
	}
}

func BenchmarkCountNonTextBytes(b *testing.B) {
	// 512 bytes with ~5% non-text to simulate realistic subtitle headers.
	data := bytes.Repeat([]byte("Subtitle line with some text.\n"), 18)
	data = data[:512]
	// Inject a few non-text bytes.
	for i := range 25 {
		data[i*20] = 0x01
	}
	b.ReportAllocs()
	b.SetBytes(512)
	for range b.N {
		_ = CountNonTextBytes(data)
	}
}

func sizeLabel(n int) string {
	switch {
	case n >= 4096:
		return "4096B"
	case n >= 512:
		return "512B"
	default:
		return "64B"
	}
}

func BenchmarkBuildMediaID(b *testing.B) {
	req := &SearchRequest{
		MediaType: MediaTypeEpisode,
		TvdbID:    12345,
		ImdbID:    "tt1234567",
		Season:    3,
		Episode:   7,
	}
	b.ReportAllocs()
	for range b.N {
		_ = BuildMediaID(req)
	}
}

func BenchmarkBuildEpisodeID(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = BuildEpisodeID(98765, "tt7654321", 2, 14)
	}
}
