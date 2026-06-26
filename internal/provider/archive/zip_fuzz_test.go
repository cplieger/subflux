package archive

import (
	"strings"
	"testing"
)

// FuzzMatchesEpisode checks that directory components are ignored (a metamorphic
// property): matching depends only on filepath.Base(name), so prepending a
// directory whose own name contains an unrelated S##E## pattern must not change
// the verdict for a slash-free base name.
func FuzzMatchesEpisode(f *testing.F) {
	f.Add("Show.S01E05.srt", 1, 5)
	f.Add("Show.S02E01E02.srt", 2, 1)
	f.Add("random.txt", 1, 1)
	f.Add("S99E999.srt", 99, 999)
	f.Add("nope", 0, 0)

	f.Fuzz(func(t *testing.T, name string, season, episode int) {
		got := MatchesEpisode(name, season, episode)

		// Guard on non-empty, slash-free names so filepath.Base of the
		// prefixed path equals name exactly; then the directory's own
		// S99E99 pattern must not leak into the match.
		if name != "" && !strings.ContainsRune(name, '/') {
			prefixed := "Show.S99E99.1080p/" + name
			if MatchesEpisode(prefixed, season, episode) != got {
				t.Errorf("MatchesEpisode ignored-directory invariance violated: "+
					"base=%q season=%d episode=%d", name, season, episode)
			}
		}
	})
}

// FuzzMatchesMultiEpisodeRange checks the documented bound (a postcondition): a
// true result means the target episode lies inside a detected range [ep1, ep2]
// where ep1 >= 0 and the year/range guard caps ep2 at 999, so any matched
// episode must fall within [0, 999].
func FuzzMatchesMultiEpisodeRange(f *testing.F) {
	f.Add("Show.S01E01E02.srt", 1)
	f.Add("Show.S01E01-E05.srt", 3)
	f.Add("Show.S01E01-02.srt", 2)
	f.Add("Show.S01E01.E02.srt", 2)
	f.Add("E950E999", 975)
	f.Add("", 0)

	f.Fuzz(func(t *testing.T, base string, episode int) {
		if MatchesMultiEpisodeRange(base, episode) {
			if episode < 0 || episode > 999 {
				t.Errorf("MatchesMultiEpisodeRange(%q, %d) = true, but episode is outside [0, 999]",
					base, episode)
			}
		}
	})
}
