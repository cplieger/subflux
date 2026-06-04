package subsync

import "testing"

func FuzzBuildClusters(f *testing.F) {
	f.Add(int64(100), int64(200), int64(5000), float64(0.8), float64(0.6), int64(3000))
	f.Add(int64(0), int64(0), int64(0), float64(0.5), float64(0.5), int64(1000))
	f.Add(int64(-5000), int64(10000), int64(-5000), float64(0.9), float64(0.1), int64(500))

	f.Fuzz(func(t *testing.T, off1, off2, off3 int64, conf1, conf2 float64, clusterMs int64) {
		if clusterMs <= 0 || clusterMs > 100000 {
			return
		}
		if conf1 < 0 || conf1 > 1 || conf2 < 0 || conf2 > 1 {
			return
		}

		candidates := []SyncResult{
			{Offset: off1, Confidence: Confidence(conf1), Method: MethodOffset},
			{Offset: off2, Confidence: Confidence(conf2), Method: MethodOffset},
			{Offset: off3, Confidence: Confidence(conf1), Method: MethodAudio},
		}

		clusters := buildClusters(candidates, clusterMs)

		if len(clusters) == 0 {
			t.Fatal("buildClusters returned 0 clusters for non-empty input")
		}
		if len(clusters) > len(candidates) {
			t.Fatalf("buildClusters returned %d clusters for %d candidates", len(clusters), len(candidates))
		}

		totalMembers := 0
		for _, c := range clusters {
			if len(c.members) == 0 {
				t.Fatal("cluster has 0 members")
			}
			if c.weight < 0 {
				t.Fatalf("cluster weight = %f, want >= 0", c.weight)
			}
			totalMembers += len(c.members)
		}
		if totalMembers != len(candidates) {
			t.Fatalf("total members = %d, want %d", totalMembers, len(candidates))
		}
	})
}
