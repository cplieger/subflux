package gestdown

import (
	"fmt"
	"testing"

	"github.com/cplieger/keyenc"
)

// The fmt.Sprintf forms the two cache keys replaced, kept as byte-identity and
// collision oracles for the tests below.
func oldShowCacheKey(tvdbID int) string { return fmt.Sprintf("show:%d", tvdbID) }

func oldSeasonCacheKey(showID string, season int, gestLang string) string {
	return fmt.Sprintf("season:%s:%d:%s", showID, season, gestLang)
}

// TestSeasonCacheKeyCannotBeForged pins that two DIFFERENT season lookups never
// share one subCache entry. The cached value is a whole season's subtitle list,
// and searchSeasonCached narrows it by episode NUMBER alone — so a collision
// hands this episode another show's or another language's S01E02 file, which is
// then scored and written next to this video (an English subtitle recorded as
// French, or a different series' dialogue entirely).
//
// showID is the component that carries the risk: it is gestdown's own `id`
// string, copied verbatim out of the shows JSON, so its alphabet is the remote
// API's choice. The pairs below all collapse under the fmt.Sprintf form (the
// test asserts that first, so it keeps exercising the real hazard). The second
// tuple of each pair puts a ':' in gestLang, which today's callers cannot do —
// it is a LangRegistry value like "French" — and that is exactly the point: the
// old form's injectivity RESTED on that upstream fact plus the int season
// pinning the tail, while keyenc escapes element-wise and needs neither.
func TestSeasonCacheKeyCannotBeForged(t *testing.T) {
	t.Parallel()

	type tuple struct {
		showID   string
		gestLang string
		season   int
	}

	cases := map[string][2]tuple{
		"separator moves from the show id into the language": {
			{showID: "abc:1", season: 2, gestLang: "French"},
			{showID: "abc", season: 1, gestLang: "2:French"},
		},
		"show id absorbs the season boundary": {
			{showID: "abc:3:French", season: 4, gestLang: "German"},
			{showID: "abc", season: 3, gestLang: "French:4:German"},
		},
	}

	for name, pair := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, r := pair[0], pair[1]
			if oldSeasonCacheKey(l.showID, l.season, l.gestLang) != oldSeasonCacheKey(r.showID, r.season, r.gestLang) {
				t.Fatalf("case no longer exercises a real collision: %q vs %q",
					oldSeasonCacheKey(l.showID, l.season, l.gestLang),
					oldSeasonCacheKey(r.showID, r.season, r.gestLang))
			}
			left := seasonCacheKey(l.showID, l.season, l.gestLang)
			right := seasonCacheKey(r.showID, r.season, r.gestLang)
			if left == right {
				t.Errorf("distinct season lookups share the cache key %q", left)
			}
		})
	}
}

// TestSeasonCacheKeyKeepsBytesForOrdinaryInput pins that adopting keyenc did not
// change the key for what gestdown actually returns: show ids are GUIDs and the
// language is a registry name, so neither carries a reserved character and the
// key stays the plain separator-joined form.
func TestSeasonCacheKeyKeepsBytesForOrdinaryInput(t *testing.T) {
	t.Parallel()

	const showID = "8f8b1c2d-4e5f-4a6b-9c8d-0e1f2a3b4c5d"
	got := seasonCacheKey(showID, 3, "French")
	if want := oldSeasonCacheKey(showID, 3, "French"); got != want {
		t.Errorf("seasonCacheKey() = %q, want the unchanged %q", got, want)
	}
	if keyenc.IsHashed(got) {
		t.Error("an ordinary season key must not be reduced to a hashed identity")
	}

	// Season and language must both stay part of the key: one season list per
	// language is what the cache holds, and filterByEpisode cannot tell them
	// apart afterwards.
	if a, b := seasonCacheKey(showID, 1, "French"), seasonCacheKey(showID, 2, "French"); a == b {
		t.Errorf("two seasons share the cache key %q", a)
	}
	if a, b := seasonCacheKey(showID, 1, "French"), seasonCacheKey(showID, 1, "German"); a == b {
		t.Errorf("two languages share the cache key %q", a)
	}
}

// TestShowCacheKeyIsInjectiveAndUnchanged covers the package's other key. No
// component of it can carry a separator — the literal is fixed and tvdbID is an
// int — so there is no tuple pair the fmt.Sprintf form collapsed and none is
// asserted here; what the test pins is that the shared grammar left the bytes
// alone and still separates distinct series, since a collision would return
// another series' show-id list and every season lookup below it would query the
// wrong show.
func TestShowCacheKeyIsInjectiveAndUnchanged(t *testing.T) {
	t.Parallel()

	for _, tvdbID := range []int{0, 1, 121361, -7} {
		got := showCacheKey(tvdbID)
		if want := oldShowCacheKey(tvdbID); got != want {
			t.Errorf("showCacheKey(%d) = %q, want the unchanged %q", tvdbID, got, want)
		}
		if keyenc.IsHashed(got) {
			t.Errorf("showCacheKey(%d) must not be a hashed identity", tvdbID)
		}
	}
	if a, b := showCacheKey(1), showCacheKey(11); a == b {
		t.Errorf("two TVDB ids share the cache key %q", a)
	}
	// The two caches are separate maps, but the key literals must still not
	// alias each other if they are ever merged into one namespace.
	if showCacheKey(1) == seasonCacheKey("1", 0, "") {
		t.Error("show and season keys alias each other")
	}
}
