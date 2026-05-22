package subsync

import (
	"context"
	"testing"
	"time"
)

func BenchmarkAudioSync(b *testing.B) {
	// Generate synthetic PCM data simulating speech patterns.
	// 16kHz sample rate, 30ms frames.
	const sampleRate = 16000
	const durationSec = 60

	pcm := make([]int16, sampleRate*durationSec)
	// Simulate alternating speech/silence: 2s speech, 1s silence.
	for i := range pcm {
		sec := i / sampleRate
		if sec%3 < 2 {
			// "Speech" — non-zero samples.
			pcm[i] = int16((i % 32767) - 16383)
		}
	}

	cases := []struct {
		name  string
		nCues int
	}{
		{"100_cues", 100},
		{"500_cues", 500},
		{"1000_cues", 1000},
	}

	for _, tc := range cases {
		cues := make([]Cue, tc.nCues)
		interval := time.Duration(durationSec) * time.Second / time.Duration(tc.nCues)
		for i := range cues {
			start := time.Duration(i) * interval
			cues[i] = Cue{Start: start, End: start + interval*2/3}
		}

		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				audioSyncFromPCM(context.Background(), cues, pcm, AudioSyncHints{})
			}
		})
	}
}
