package subsync

import "testing"

func FuzzBuildClustersPartition(f *testing.F) {
	f.Add(int64(100), int64(200), int64(105), int64(5000), int64(3000))
	f.Add(int64(0), int64(0), int64(0), int64(0), int64(1000))
	f.Add(int64(-500), int64(500), int64(1000), int64(-1000), int64(100))

	f.Fuzz(func(t *testing.T, o1, o2, o3, o4, clusterMs int64) {
		if clusterMs <= 0 {
			clusterMs = 1
		}
		if clusterMs > 100_000 {
			clusterMs = 100_000
		}
		candidates := []SyncResult{
			{Offset: o1, Confidence: 0.5},
			{Offset: o2, Confidence: 0.7},
			{Offset: o3, Confidence: 0.9},
			{Offset: o4, Confidence: 0.3},
		}
		clusters := buildClusters(candidates, clusterMs)

		// Partition property: total members across all clusters == input length.
		total := 0
		for _, c := range clusters {
			total += len(c.members)
		}
		if total != len(candidates) {
			t.Errorf("partition violated: got %d members, want %d", total, len(candidates))
		}

		// Each cluster has positive weight.
		for i, c := range clusters {
			if c.weight <= 0 {
				t.Errorf("cluster %d has non-positive weight: %f", i, c.weight)
			}
		}
	})
}
