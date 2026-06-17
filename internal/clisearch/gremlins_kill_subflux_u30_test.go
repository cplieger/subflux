package clisearch

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// Kills clisearch.go:76:12 CONDITIONALS_BOUNDARY (len(s) >= len(prefix), >= -> >).
// At the boundary len(s) == len(prefix) with s == prefix, the original keeps
// the prefix branch and returns ("", true). The mutated ">" makes
// len(s) > len(prefix) false, short-circuiting to (s, false).
func Test_gk_subflux_u30_CutPrefixEqualLength(t *testing.T) {
	got, ok := cutPrefix("--", "--")
	if got != "" || !ok {
		t.Errorf("cutPrefix(%q, %q) = (%q, %v), want (%q, %v)", "--", "--", got, ok, "", true)
	}
}

// Kills resolve.go:132:12 CONDITIONALS_BOUNDARY (tmdbInt > 0, > -> >=).
// With tmdbInt == 0, a movie whose TmdbID == 0, and no imdb/title match, the
// original "> 0" leaves the TMDB clause false so matchMovie returns false. The
// mutated ">= 0" makes 0 >= 0 true and 0 == 0 true, flipping the result to true.
func Test_gk_subflux_u30_MatchMovieZeroTmdb(t *testing.T) {
	got := matchMovie(&api.Movie{}, "", 0, "")
	if got {
		t.Errorf("matchMovie(zero movie, \"\", 0, \"\") = %v, want false", got)
	}
}
