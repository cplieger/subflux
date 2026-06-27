package subsync

import (
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestVoteOnCandidates(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	incSame := makeLongCues(30, 10*time.Minute)
	tests := []struct {
		check      func(t *testing.T, got SyncResult)
		name       string
		ref        []Cue
		inc        []Cue
		candidates []SyncResult
	}{
		{
			name: "single candidate passthrough",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				{Cues: inc, Offset: -2000, Confidence: 0.7, Method: MethodOffset},
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Method != MethodOffset {
					t.Errorf("method = %q, want %q", got.Method, MethodOffset)
				}
				if got.Offset != -2000 {
					t.Errorf("offset = %d, want -2000", got.Offset)
				}
			},
		},
		{
			name: "two agreeing strategies boost",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				{Cues: inc, Offset: -2000, Confidence: 0.6, Method: MethodOffset},
				{Cues: inc, Offset: -2100, Confidence: 0.7, Method: MethodCrosslang},
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Confidence != 0.7 {
					t.Errorf("confidence = %f, want 0.7", float64(got.Confidence))
				}
			},
		},
		{
			name: "three agreeing strategies stronger boost",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				{Cues: inc, Offset: -2000, Confidence: 0.5, Method: MethodOffset},
				{Cues: inc, Offset: -2100, Confidence: 0.6, Method: MethodCrosslang},
				{Cues: inc, Offset: -1900, Confidence: 0.7, Method: MethodFramerate},
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Confidence != 0.7 {
					t.Errorf("confidence = %f, want 0.7", float64(got.Confidence))
				}
			},
		},
		{
			name: "separate clusters picks higher weight",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				{Cues: inc, Offset: -2000, Confidence: 0.5, Method: MethodOffset},
				{Cues: inc, Offset: -2100, Confidence: 0.5, Method: MethodCrosslang},
				{Cues: inc, Offset: -50000, Confidence: 0.8, Method: MethodSplit},
			},
			check: func(t *testing.T, got SyncResult) {
				if abs64(got.Offset-(-2000)) > 3000 {
					t.Errorf("offset = %d, want near -2000", got.Offset)
				}
			},
		},
		{
			name: "large offset penalty similar duration",
			ref:  ref,
			inc:  incSame,
			candidates: []SyncResult{
				{Cues: incSame, Offset: 40000, Confidence: 0.7, Method: MethodOffset},
				{Cues: incSame, Offset: -1000, Confidence: 0.4, Method: MethodCrosslang},
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Offset == 40000 {
					t.Error("should not pick offset=40000 on similar-duration content")
				}
			},
		},
		{
			name: "returns valid result",
			ref:  ref,
			inc:  incSame,
			candidates: []SyncResult{
				{Cues: incSame, Offset: 0, Confidence: 0.5, Method: MethodOffset},
				{Cues: incSame, Offset: 500, Confidence: 0.5, Method: MethodCrosslang},
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Method == "" {
					t.Error("returned empty method")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := voteOnCandidates(tt.candidates, tt.ref, tt.inc)
			tt.check(t, got)
		})
	}
}

func TestBuildClusters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		candidates   []SyncResult
		wantMembers  []int
		wantWeight   []float64
		wantOffset   []int64
		clusterMs    int64
		wantClusters int
	}{
		{
			name:         "single candidate",
			candidates:   []SyncResult{{Offset: 1000, Confidence: 0.7, Method: MethodOffset}},
			clusterMs:    3000,
			wantClusters: 1,
			wantMembers:  []int{1},
			wantWeight:   []float64{0.7},
			wantOffset:   []int64{1000},
		},
		{
			name: "two within threshold",
			candidates: []SyncResult{
				{Offset: 1000, Confidence: 0.5, Method: MethodOffset},
				{Offset: 2500, Confidence: 0.6, Method: MethodCrosslang},
			},
			clusterMs:    3000,
			wantClusters: 1,
			wantMembers:  []int{2},
			wantWeight:   []float64{1.1},
		},
		{
			name: "two beyond threshold",
			candidates: []SyncResult{
				{Offset: 1000, Confidence: 0.5, Method: MethodOffset},
				{Offset: 5000, Confidence: 0.6, Method: MethodCrosslang},
			},
			clusterMs:    3000,
			wantClusters: 2,
			wantMembers:  []int{1, 1},
			wantOffset:   []int64{1000, 5000},
		},
		{
			name: "exact boundary same cluster",
			candidates: []SyncResult{
				{Offset: 0, Confidence: 0.5, Method: MethodOffset},
				{Offset: 3000, Confidence: 0.6, Method: MethodCrosslang},
			},
			clusterMs:    3000,
			wantClusters: 1,
			wantMembers:  []int{2},
		},
		{
			name: "one beyond boundary new cluster",
			candidates: []SyncResult{
				{Offset: 0, Confidence: 0.5, Method: MethodOffset},
				{Offset: 3001, Confidence: 0.6, Method: MethodCrosslang},
			},
			clusterMs:    3000,
			wantClusters: 2,
			wantMembers:  []int{1, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusters := buildClusters(tt.candidates, tt.clusterMs)
			if len(clusters) != tt.wantClusters {
				t.Fatalf("clusters = %d, want %d", len(clusters), tt.wantClusters)
			}
			for i, wm := range tt.wantMembers {
				if len(clusters[i].members) != wm {
					t.Errorf("cluster[%d] members = %d, want %d", i, len(clusters[i].members), wm)
				}
			}
			for i, ww := range tt.wantWeight {
				if clusters[i].weight != ww {
					t.Errorf("cluster[%d] weight = %f, want %f", i, clusters[i].weight, ww)
				}
			}
			for i, wo := range tt.wantOffset {
				if clusters[i].offset != wo {
					t.Errorf("cluster[%d] offset = %d, want %d", i, clusters[i].offset, wo)
				}
			}
		})
	}
}

func TestApplyAlignmentCheck(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(20, 10*time.Minute)
	incSame := makeLongCues(20, 10*time.Minute)
	incShifted := ShiftCues(ref, 2*time.Second)
	tests := []struct {
		name       string
		ref        []Cue
		inc        []Cue
		clusters   []voteCluster
		wantWeight float64
	}{
		{
			name:       "penalty large first diff",
			ref:        ref,
			inc:        incSame,
			clusters:   []voteCluster{{members: []SyncResult{{Offset: 50000}}, weight: 1.0, offset: 50000}},
			wantWeight: 0.5,
		},
		{
			name:       "boost well aligned",
			ref:        ref,
			inc:        incShifted,
			clusters:   []voteCluster{{members: []SyncResult{{Offset: -2000}}, weight: 1.0, offset: -2000}},
			wantWeight: 1.2,
		},
		{
			name:       "no change moderate misalignment",
			ref:        ref,
			inc:        incSame,
			clusters:   []voteCluster{{members: []SyncResult{{Offset: 10000}}, weight: 1.0, offset: 10000}},
			wantWeight: 1.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusters := make([]voteCluster, len(tt.clusters))
			copy(clusters, tt.clusters)
			applyAlignmentCheck(clusters, tt.ref, tt.inc)
			if clusters[0].weight != tt.wantWeight {
				t.Errorf("weight = %f, want %f", clusters[0].weight, tt.wantWeight)
			}
		})
	}
}

func TestPenalizeLargeOffsets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		clusters        []voteCluster
		similarDuration bool
		durationDiff    int64
		wantWeight      float64
	}{
		{
			name:            "similar duration penalizes large offset",
			clusters:        []voteCluster{{members: []SyncResult{{Offset: 40000}}, weight: 1.0, offset: 40000}},
			similarDuration: true,
			durationDiff:    5000,
			wantWeight:      0.3,
		},
		{
			name:            "different duration skips penalty",
			clusters:        []voteCluster{{members: []SyncResult{{Offset: 40000}}, weight: 1.0, offset: 40000}},
			similarDuration: false,
			durationDiff:    120000,
			wantWeight:      1.0,
		},
		{
			name:            "small offset not penalized",
			clusters:        []voteCluster{{members: []SyncResult{{Offset: 5000}}, weight: 1.0, offset: 5000}},
			similarDuration: true,
			durationDiff:    5000,
			wantWeight:      1.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusters := make([]voteCluster, len(tt.clusters))
			copy(clusters, tt.clusters)
			penalizeLargeOffsets(clusters, tt.similarDuration, tt.durationDiff)
			if clusters[0].weight != tt.wantWeight {
				t.Errorf("weight = %f, want %f", clusters[0].weight, tt.wantWeight)
			}
		})
	}
}

func TestPickWinner(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		wantMethod     SyncMethod
		clusters       []voteCluster
		wantOffset     int64
		wantConfidence Confidence
	}{
		{
			name: "selects highest weight cluster",
			clusters: []voteCluster{
				{members: []SyncResult{{Offset: 1000, Confidence: 0.9, Method: MethodOffset}}, weight: 0.5},
				{members: []SyncResult{{Offset: 5000, Confidence: 0.3, Method: MethodCrosslang}}, weight: 1.0},
			},
			wantOffset: 5000,
		},
		{
			name: "selects highest confidence within cluster",
			clusters: []voteCluster{
				{
					members: []SyncResult{
						{Offset: 1000, Confidence: 0.5, Method: MethodOffset},
						{Offset: 1100, Confidence: 0.8, Method: MethodCrosslang},
						{Offset: 900, Confidence: 0.6, Method: MethodFramerate},
					},
					weight: 1.9,
				},
			},
			wantConfidence: 0.8,
			wantMethod:     MethodCrosslang,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pickWinner(tt.clusters)
			if tt.wantOffset != 0 && got.Offset != tt.wantOffset {
				t.Errorf("offset = %d, want %d", got.Offset, tt.wantOffset)
			}
			if tt.wantConfidence != 0 && got.Confidence != tt.wantConfidence {
				t.Errorf("confidence = %f, want %f", float64(got.Confidence), float64(tt.wantConfidence))
			}
			if tt.wantMethod != "" && got.Method != tt.wantMethod {
				t.Errorf("method = %q, want %q", got.Method, tt.wantMethod)
			}
		})
	}
}

func TestProperty_voteOnCandidates(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "numCandidates")
		methods := []SyncMethod{MethodOffset, MethodCrosslang, MethodFramerate, MethodSplit, MethodAudio}

		ref := makeLongCues(30, 10*time.Minute)
		inc := ShiftCues(ref, 2*time.Second)

		candidates := make([]SyncResult, n)
		for i := range n {
			candidates[i] = SyncResult{
				Cues:       inc,
				Offset:     rapid.Int64Range(-60000, 60000).Draw(t, "offset"),
				Confidence: Confidence(rapid.Float64Range(0.01, 1.0).Draw(t, "confidence")),
				Method:     methods[rapid.IntRange(0, len(methods)-1).Draw(t, "methodIdx")],
			}
		}

		got := voteOnCandidates(candidates, ref, inc)

		// Invariant 1: returned method is one of the input candidates' methods.
		found := false
		for _, c := range candidates {
			if c.Method == got.Method {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("returned method %q not in input candidates", got.Method)
		}

		// Invariant 2: returned confidence >= 0.
		if got.Confidence < 0 {
			t.Fatalf("returned confidence %f < 0", float64(got.Confidence))
		}

		// Invariant 3: single candidate returns itself.
		if n == 1 {
			if got.Offset != candidates[0].Offset {
				t.Fatalf("single candidate: offset = %d, want %d", got.Offset, candidates[0].Offset)
			}
		}

		// Invariant 4: returned offset is one of the input candidates' offsets.
		offsetFound := false
		for _, c := range candidates {
			if c.Offset == got.Offset {
				offsetFound = true
				break
			}
		}
		if !offsetFound {
			t.Fatalf("returned offset %d not in input candidates", got.Offset)
		}
	})
}
