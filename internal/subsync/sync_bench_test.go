package subsync

import (
	"context"
	"testing"
	"time"
)

func BenchmarkSyncWithOptions(b *testing.B) {
	cases := []struct {
		name string
		opts SyncOptions
		refN int
		incN int
	}{
		{
			name: "50_cues_offset",
			refN: 50, incN: 50,
			opts: SyncOptions{EnableFramerate: false, EnableSplits: false},
		},
		{
			name: "200_cues_framerate",
			refN: 200, incN: 200,
			opts: SyncOptions{EnableFramerate: true, EnableSplits: false},
		},
		{
			name: "500_cues_splits",
			refN: 500, incN: 500,
			opts: SyncOptions{EnableFramerate: true, EnableSplits: true},
		},
	}

	for _, tc := range cases {
		ref := makeBenchCues(tc.refN, 0)
		// Offset incorrect cues by 500ms to simulate a constant offset scenario.
		inc := makeBenchCues(tc.incN, 500*time.Millisecond)
		opts := tc.opts
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				SyncWithOptions(context.Background(), ref, inc, &opts)
			}
		})
	}
}
