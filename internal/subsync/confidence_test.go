package subsync

import (
	"testing"
)

func TestSyncResult_Applied(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result SyncResult
		want   bool
	}{
		{"zero offset and rate", SyncResult{Offset: 0, Rate: 1.0}, false},
		{"zero offset no rate", SyncResult{Offset: 0, Rate: 0}, false},
		{"nonzero offset", SyncResult{Offset: 500}, true},
		{"negative offset", SyncResult{Offset: -200}, true},
		{"nonzero rate", SyncResult{Rate: 1.001}, true},
		{"rate below 1", SyncResult{Rate: 0.999}, true},
		{"split with confidence", SyncResult{Method: MethodSplit, Confidence: 0.6}, true},
		{"split with zero confidence", SyncResult{Method: MethodSplit, Confidence: ConfidenceNone}, false},
		{"non-split with zero offset", SyncResult{Method: MethodOffset, Confidence: 0.8}, false},
		{"split with nonzero offset returns true via offset", SyncResult{Method: MethodSplit, Offset: 100, Confidence: 0.8}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.result.Applied(); got != tt.want {
				t.Fatalf("SyncResult%+v.Applied() = %v, want %v", tt.result, got, tt.want)
			}
		})
	}
}

func TestSyncResult_ShouldApply(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		confidence Confidence
		want       bool
	}{
		{"zero", ConfidenceNone, false},
		{"weak", 0.3, false},
		{"just below threshold", ShouldApplyThreshold - 0.001, false},
		{"at threshold", ShouldApplyThreshold, true},
		{"just above threshold", ShouldApplyThreshold + 0.001, true},
		{"moderate", 0.6, true},
		{"strong", 0.8, true},
		{"perfect", 1.0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := SyncResult{Confidence: tt.confidence}
			if got := r.ShouldApply(); got != tt.want {
				t.Fatalf("SyncResult{Confidence: %v}.ShouldApply() = %v, want %v", tt.confidence, got, tt.want)
			}
		})
	}
}
