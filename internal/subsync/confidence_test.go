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
		{"split with confidence", SyncResult{Method: MethodSplit, Confidence: ConfidenceModerate}, true},
		{"split with zero confidence", SyncResult{Method: MethodSplit, Confidence: ConfidenceNone}, false},
		{"non-split with zero offset", SyncResult{Method: MethodOffset, Confidence: ConfidenceStrong}, false},
		{"split with nonzero offset returns true via offset", SyncResult{Method: MethodSplit, Offset: 100, Confidence: ConfidenceStrong}, true},
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
		{"weak", ConfidenceWeak, false},
		{"just below threshold", Confidence(0.499), false},
		{"at threshold", 0.5, true},
		{"moderate", ConfidenceModerate, true},
		{"strong", ConfidenceStrong, true},
		{"perfect", ConfidencePerfect, true},
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
