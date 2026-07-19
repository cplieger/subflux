package subsync

import "testing"

// FuzzClusterCandidatesPartition fuzzes the pure-shift clustering family:
// clustering must always partition the input (every candidate lands in
// exactly one cluster) and honor complete linkage (every pair inside a
// cluster agrees pairwise).
func FuzzClusterCandidatesPartition(f *testing.F) {
	f.Add(int64(100), int64(200), int64(105), int64(5000))
	f.Add(int64(0), int64(0), int64(0), int64(0))
	f.Add(int64(-500), int64(500), int64(1000), int64(-1000))
	f.Add(int64(0), int64(2900), int64(5800), int64(8700))

	f.Fuzz(func(t *testing.T, o1, o2, o3, o4 int64) {
		offsets := []int64{o1, o2, o3, o4}
		sources := []CandidateSource{SourceCrosslang, SourceFramerate, SourceOffset, SourceSplit}
		candidates := make([]SyncResult, len(offsets))
		for i, o := range offsets {
			candidates[i] = SyncResult{
				Offset:     o,
				Confidence: 0.5,
				Source:     sources[i],
				Transform:  Transform{Kind: TransformShift, Shift: o},
			}
		}

		clusters := clusterCandidates(candidates)

		// Partition property: total members across all clusters == input length.
		total := 0
		for _, c := range clusters {
			total += len(c.members)
		}
		if total != len(candidates) {
			t.Errorf("partition violated: got %d members, want %d", total, len(candidates))
		}

		// Complete linkage: every pair within a cluster agrees pairwise.
		for ci := range clusters {
			ms := clusters[ci].members
			for i := range ms {
				for j := i + 1; j < len(ms); j++ {
					if !candidatesAgree(&ms[i], &ms[j]) {
						t.Errorf("cluster %d members %d and %d disagree (shifts %d, %d)",
							ci, i, j, ms[i].Transform.Shift, ms[j].Transform.Shift)
					}
				}
			}
		}
	})
}
