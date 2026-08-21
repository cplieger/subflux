package subsource

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cplieger/keyenc"
	"github.com/cplieger/subflux/internal/subflux"
)

// oldTitleCacheKey is the fmt.Sprintf form titleCacheKey replaced. It is kept
// here as the byte-identity oracle (a key built from ordinary fields must not
// have changed) and as the collision oracle (the pairs below must collapse
// under it and stay apart under the current form).
func oldTitleCacheKey(req *subflux.SearchRequest) string {
	season := 0
	if req.MediaType == subflux.MediaTypeEpisode {
		season = req.Season
	}
	return fmt.Sprintf("title:%s:%s:%d", req.ImdbID, strings.ToLower(req.Title), season)
}

// TestTitleCacheKeyCannotBeForged pins the property titleCacheKey exists to
// hold: two DIFFERENT title lookups must never share one cache entry. The
// cached value is the SubSource title id that scopes every subsequent subtitle
// query, so a collision is not a miss — Search hands the wrong title id to
// querySubtitles and another film's or show's subtitle list is scored against
// this video's release name and written next to it.
//
// The forgeable seam is the adjacency of the two free-form components: the arr
// IMDb id (passed through from Sonarr/Radarr unvalidated) sits directly before
// the lowercased title. A ':' in the id moves the boundary between them. Each
// case asserts the pair collapses under the old form, so the test fails if it
// ever stops exercising the real hazard.
func TestTitleCacheKeyCannotBeForged(t *testing.T) {
	t.Parallel()

	req := func(imdbID, title string) *subflux.SearchRequest {
		return &subflux.SearchRequest{MediaType: subflux.MediaTypeMovie, ImdbID: imdbID, Title: title}
	}

	cases := map[string][2]*subflux.SearchRequest{
		"separator moves from the imdb id into the title": {
			req("tt1:the wire", "s01"),
			req("tt1", "the wire:s01"),
		},
		"separator at the end of the imdb id swallows an empty title": {
			req("tt1:", ""),
			req("tt1", ":"),
		},
	}

	for name, pair := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if oldTitleCacheKey(pair[0]) != oldTitleCacheKey(pair[1]) {
				t.Fatalf("case no longer exercises a real collision: %q vs %q",
					oldTitleCacheKey(pair[0]), oldTitleCacheKey(pair[1]))
			}
			left, right := titleCacheKey(pair[0]), titleCacheKey(pair[1])
			if left == right {
				t.Errorf("distinct title lookups share the cache key %q", left)
			}
		})
	}

	// The escape character is the seam escaping itself introduces: these pairs
	// were never forgeable under fmt.Sprintf (which escapes nothing), and must
	// not become forgeable now that '\' is meaningful.
	escapePairs := map[string][2]*subflux.SearchRequest{
		"escaped separator vs literal separator": {
			req(`tt1\`, ":x"),
			req(`tt1\:x`, ""),
		},
		"doubled escape does not shift the split": {
			req(`tt1\`, `\x`),
			req(`tt1\\`, "x"),
		},
	}
	for name, pair := range escapePairs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if left, right := titleCacheKey(pair[0]), titleCacheKey(pair[1]); left == right {
				t.Errorf("distinct title lookups share the cache key %q", left)
			}
		})
	}
}

// TestTitleCacheKeyKeepsBytesForOrdinaryInput pins that adopting keyenc did not
// change the key for the input this provider actually sees: no ordinary IMDb id
// or title carries a reserved character, so the key stays the plain
// separator-joined form — byte-identical to the fmt.Sprintf it replaced and
// still readable in a debug log. Season scoping is pinned alongside it, since an
// S01-scoped title id must not answer an S02 lookup.
func TestTitleCacheKeyKeepsBytesForOrdinaryInput(t *testing.T) {
	t.Parallel()

	cases := map[string]*subflux.SearchRequest{
		"movie": {MediaType: subflux.MediaTypeMovie, ImdbID: "tt0133093", Title: "The Matrix"},
		"episode with season": {
			MediaType: subflux.MediaTypeEpisode, ImdbID: "tt0903747",
			Title: "Breaking Bad", Season: 2, Episode: 5,
		},
		"apostrophes and punctuation in the title": {
			MediaType: subflux.MediaTypeMovie, ImdbID: "tt0117951", Title: "Trainspotting (1996) - Director's Cut",
		},
	}

	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := titleCacheKey(req)
			if want := oldTitleCacheKey(req); got != want {
				t.Errorf("titleCacheKey() = %q, want the unchanged %q", got, want)
			}
			if keyenc.IsHashed(got) {
				t.Error("an ordinary title key must not be reduced to a hashed identity")
			}
		})
	}

	// The season must stay part of the key: SubSource serves per-season title
	// pages, so an S01 id answering an S02 lookup is a wrong-season result.
	s1 := titleCacheKey(&subflux.SearchRequest{MediaType: subflux.MediaTypeEpisode, ImdbID: "tt1", Title: "x", Season: 1})
	s2 := titleCacheKey(&subflux.SearchRequest{MediaType: subflux.MediaTypeEpisode, ImdbID: "tt1", Title: "x", Season: 2})
	if s1 == s2 {
		t.Errorf("two seasons share the title cache key %q", s1)
	}
	// Case folding is part of the key, not of the caller.
	if lower, upper := titleCacheKey(&subflux.SearchRequest{ImdbID: "tt1", Title: "the wire"}),
		titleCacheKey(&subflux.SearchRequest{ImdbID: "tt1", Title: "The Wire"}); lower != upper {
		t.Errorf("title case changed the key: %q vs %q", lower, upper)
	}
}
