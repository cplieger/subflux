package subsync

import "testing"

// FuzzBuildClustersConservation verifies that buildClusters places every
// candidate into exactly one cluster (partition/conservation invariant).
func FuzzBuildClustersConservation(f *testing.F) {
	f.Add(int64(0), int64(3000), int64(6000), int64(3000))
	f.Add(int64(100), int64(100), int64(100), int64(1000))
	f.Fuzz(func(t *testing.T, off1, off2, off3 int64, clusterMs int64) {
		if clusterMs <= 0 || clusterMs > 100000 {
			return
		}
		candidates := []SyncResult{
			{Offset: off1, Confidence: 0.5},
			{Offset: off2, Confidence: 0.7},
			{Offset: off3, Confidence: 0.9},
		}
		clusters := buildClusters(candidates, clusterMs)
		var total int
		for _, c := range clusters {
			total += len(c.members)
		}
		if total != len(candidates) {
			t.Fatalf("member count %d != input count %d", total, len(candidates))
		}
	})
}
