package vad

import "testing"

func BenchmarkDetectorLLR(b *testing.B) {
	d := NewDetector()
	frame := make([]int16, FrameSamples)
	for i := range frame {
		frame[i] = int16(i % 1000)
	}
	for b.Loop() {
		d.LLR(frame)
	}
}
