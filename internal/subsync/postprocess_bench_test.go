package subsync

import (
	"strings"
	"testing"
	"time"
)

func BenchmarkPostProcess(b *testing.B) {
	// Build 200 cues with HI annotations and tags.
	cues := make([]Cue, 200)
	for i := range cues {
		start := time.Duration(i) * time.Second
		cues[i] = Cue{
			Start: start,
			End:   start + 900*time.Millisecond,
			Text:  "[Music] <i>Hello</i> - How are you?\n(laughing) Fine, thanks.",
		}
	}
	opts := PostProcessOptions{
		StripHI:   true,
		StripTags: true,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = PostProcess(cues, opts)
	}
}

func BenchmarkPostProcessBytes(b *testing.B) {
	var sb strings.Builder
	for i := 1; i <= 200; i++ {
		sb.WriteString("1\n00:00:01,000 --> 00:00:02,000\n[Music] <i>Hello</i>\n\n")
	}
	data := []byte(sb.String())
	opts := PostProcessOptions{StripHI: true, StripTags: true}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = PostProcessBytes(data, opts)
	}
}
