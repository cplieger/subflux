package anidb

import "testing"

func FuzzFindEpisodeInMapping(f *testing.F) {
	f.Add("1-1;2-2;3-3", 1)
	f.Add("1-1;2-2;3-3", 3)
	f.Add("1-1+2;3-3", 2)
	f.Add("", 1)
	f.Add(";;", 0)
	f.Add("abc-def", 1)
	f.Add("1-", 1)
	f.Add("-1", 1)
	f.Add("999999999999-999999999999", 999999999999)

	f.Fuzz(func(t *testing.T, text string, tvdbEpisode int) {
		result := findEpisodeInMapping(text, tvdbEpisode)
		if result < 0 {
			t.Fatalf("findEpisodeInMapping returned negative: %d", result)
		}
	})
}

func FuzzBestCandidate(f *testing.F) {
	f.Add(1, 10, 0, 5, 3)
	f.Add(0, 0, 0, 0, 1)
	f.Add(100, 5, 200, 10, 7)

	f.Fuzz(func(t *testing.T, id1, off1, id2, off2, episode int) {
		candidates := []candidate{
			{anidbID: id1, offset: off1},
			{anidbID: id2, offset: off2},
		}
		anidbID, epNo := bestCandidate(candidates, episode)
		if anidbID != 0 && epNo <= 0 {
			t.Fatalf("anidbID=%d but epNo=%d", anidbID, epNo)
		}
		if anidbID == 0 && epNo != 0 {
			t.Fatalf("anidbID=0 but epNo=%d", epNo)
		}
	})
}
