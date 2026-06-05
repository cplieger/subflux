package anidb

import (
	"testing"
)

// FuzzFindEpisodeInMapping exercises the ";1-1;2-2;3-3;" mapping text parser
// with arbitrary inputs ensuring it never panics and respects basic invariants.
func FuzzFindEpisodeInMapping(f *testing.F) {
	f.Add(";1-1;2-2;3-3;", 1)
	f.Add("", 0)
	f.Add(";5-10;", 10)
	f.Add(";1-2+3;", 2)
	f.Add("garbage", 1)
	f.Add(";-1-1;", 1)
	f.Add(";999999999999-1;", 1)
	f.Add(";1-999999999999;", 999999999999)
	f.Add("1-1;2-2;3-3", 3)
	f.Add("1-1+2;3-3", 2)
	f.Add(";;", 0)
	f.Add("abc-def", 1)
	f.Add("1-", 1)
	f.Add("-1", 1)

	f.Fuzz(func(t *testing.T, text string, tvdbEpisode int) {
		result := findEpisodeInMapping(text, tvdbEpisode)

		// Invariant 1: never panics (implicit).

		// Invariant 2: result is always >= 0.
		if result < 0 {
			t.Fatalf("findEpisodeInMapping(%q, %d) = %d, want >= 0", text, tvdbEpisode, result)
		}
	})
}

// FuzzDecompressIfGzipped exercises the gzip decompression path with arbitrary
// byte sequences to ensure it never panics on malformed input.
func FuzzDecompressIfGzipped(f *testing.F) {
	f.Add([]byte{}, int64(1024))
	f.Add([]byte("not gzip"), int64(1024))
	f.Add([]byte{0x1f, 0x8b, 0x08, 0x00}, int64(1024)) // truncated gzip
	f.Add([]byte{0x1f, 0x8b}, int64(0))                 // zero limit

	f.Fuzz(func(t *testing.T, data []byte, maxBytes int64) {
		if maxBytes < 0 {
			return
		}
		// Must not panic regardless of input.
		_, _ = decompressIfGzipped(data, maxBytes)
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
