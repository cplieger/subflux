package scanning

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/keyenc"
	"github.com/cplieger/subflux/internal/server/showskip"
	"github.com/cplieger/subflux/internal/subflux"
)

// oldShowSkipCacheKey is the `imdbID + "-" + lang` form showSkipCacheKey
// replaced, kept as the collision oracle for the pairs below.
func oldShowSkipCacheKey(imdbID, lang string) string { return imdbID + "-" + lang }

// exactShowCounter is a counter keyed on the EXACT (imdbID, lang) pair, unlike
// the shared mockShowCounter which itself concatenates with '-' and would
// therefore reproduce the very collision under test.
type exactShowCounter struct {
	counts map[[2]string]int
	calls  int
}

func (c *exactShowCounter) CountShowSubtitles(_ context.Context, q subflux.ShowSubtitleQuery) (int, error) {
	imdbID, lang := q.ImdbID, q.Language
	c.calls++
	return c.counts[[2]string{imdbID, lang}], nil
}

func (c *exactShowCounter) Name() subflux.ProviderID { return "opensubtitles" }

func (c *exactShowCounter) Search(_ context.Context, _ *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	return nil, nil
}

func (c *exactShowCounter) Download(_ context.Context, _ *subflux.Subtitle) ([]byte, error) {
	return nil, nil
}

// TestShowSkipCacheKeyCannotBeForged pins that two DIFFERENT (series, language)
// pre-checks never share one show-skip cache entry.
//
// Both components are free-form — imdbID is Sonarr's imdbId passed through
// unvalidated, lang is a config code checked only for non-emptiness — and the
// old '-' joined form was injective only by accident of ORDER: locale-style
// codes ("pt-BR", "zh-Hans") do carry the separator, and only their terminal
// position kept them from shifting the boundary. Each pair below collapses under
// that form (asserted first, so the case cannot silently stop being a hazard).
func TestShowSkipCacheKeyCannotBeForged(t *testing.T) {
	t.Parallel()

	cases := map[string][2][2]string{
		"locale code absorbs the imdb id boundary": {
			{"tt1", "pt-BR"},
			{"tt1-pt", "BR"},
		},
		"subflux's own episode-shaped media id shifts the split": {
			{"tt0903747-s01e01", "en"},
			{"tt0903747", "s01e01-en"},
		},
		"trailing separator swallows an empty language": {
			{"tt1-", ""},
			{"tt1", "-"},
		},
	}

	for name, pair := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, r := pair[0], pair[1]
			if oldShowSkipCacheKey(l[0], l[1]) != oldShowSkipCacheKey(r[0], r[1]) {
				t.Fatalf("case no longer exercises a real collision: %q vs %q",
					oldShowSkipCacheKey(l[0], l[1]), oldShowSkipCacheKey(r[0], r[1]))
			}
			if left, right := showSkipCacheKey(l[0], l[1]), showSkipCacheKey(r[0], r[1]); left == right {
				t.Errorf("distinct show pre-checks share the cache key %q", left)
			}
		})
	}
}

// TestShowSkipCacheKeyIsUnescapedForOrdinaryInput pins the shape of the key for
// the input this cache actually sees. The BYTES deliberately changed here ('-'
// became ':'), which is free because the show-skip cache is in-memory only —
// what must hold is that an ordinary pair still encodes as the plain
// separator-joined form rather than an escaped or hashed one.
func TestShowSkipCacheKeyIsUnescapedForOrdinaryInput(t *testing.T) {
	t.Parallel()

	if got, want := showSkipCacheKey("tt0903747", "en"), "tt0903747:en"; got != want {
		t.Errorf("showSkipCacheKey() = %q, want %q", got, want)
	}
	// A hyphenated locale code stays readable: '-' is not reserved.
	if got, want := showSkipCacheKey("tt0903747", "pt-BR"), "tt0903747:pt-BR"; got != want {
		t.Errorf("showSkipCacheKey() = %q, want %q", got, want)
	}
	if keyenc.IsHashed(showSkipCacheKey("tt0903747", "en")) {
		t.Error("an ordinary show-skip key must not be reduced to a hashed identity")
	}
}

// TestShowLevelSkipDoesNotShareVerdictAcrossForgedKeys is the consequence test:
// it drives showLevelSkip itself, not the key builder, and shows what the
// collision COSTS. The first series has no subtitles upstream and is skipped;
// the second, whose old key was identical, has full coverage and must still be
// scanned. Under the old form the second lookup was a cache hit on the first
// verdict — an entire series silently skipped for that language on a different
// series' evidence, with the counter never consulted.
func TestShowLevelSkipDoesNotShareVerdictAcrossForgedKeys(t *testing.T) {
	t.Parallel()

	const episodes = 10
	bare := [2]string{"tt1", "pt-BR"}    // 0 subtitles upstream -> skip
	covered := [2]string{"tt1-pt", "BR"} // fully covered -> must NOT be skipped

	if oldShowSkipCacheKey(bare[0], bare[1]) != oldShowSkipCacheKey(covered[0], covered[1]) {
		t.Fatalf("test no longer exercises a forged pair")
	}

	counter := &exactShowCounter{counts: map[[2]string]int{
		bare:    0,
		covered: episodes,
	}}
	st := newSeasonTracker(counter, showskip.New(time.Hour), seedDeps{})
	ctx := t.Context()

	if !st.showLevelSkip(ctx, bare[0], episodes, bare[1]) {
		t.Errorf("a series with no upstream subtitles should be skipped")
	}
	if st.showLevelSkip(ctx, covered[0], episodes, covered[1]) {
		t.Errorf("a fully covered series was skipped on another series' cached verdict")
	}
	if counter.calls != 2 {
		t.Errorf("counter consulted %d times, want 2 (the second lookup hit the first verdict's cache entry)", counter.calls)
	}
	// The verdicts must also survive a repeat lookup from the cache, each under
	// its own key.
	if !st.showLevelSkip(ctx, bare[0], episodes, bare[1]) || st.showLevelSkip(ctx, covered[0], episodes, covered[1]) {
		t.Errorf("cached verdicts changed on re-read")
	}
	if counter.calls != 2 {
		t.Errorf("counter consulted %d times after cached re-reads, want 2", counter.calls)
	}
}
