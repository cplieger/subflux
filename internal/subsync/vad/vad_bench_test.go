package vad

import (
	"fmt"
	"testing"
)

func BenchmarkVADProcessFrame(b *testing.B) {
	for _, mode := range []Mode{ModeQuality, ModeLowBitrate, ModeAggressive, ModeVeryAggressive} {
		b.Run(fmt.Sprintf("mode_%d", mode), func(b *testing.B) {
			v := newVADInst(mode)
			frame := make([]int16, 160)
			for i := range frame {
				frame[i] = int16(i % 1000)
			}
			b.ResetTimer()
			for range b.N {
				v.processFrameLLR(frame)
			}
		})
	}
}
