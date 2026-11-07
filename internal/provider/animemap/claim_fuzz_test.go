package animemap_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/episodematch"
	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/provider/animemap"
)

// FuzzReadClaim_bounded: any name reads one claim; a label's claim carries no
// number, and a step-2 claim comes only for a name with no marker shape that
// step 1 leaves unclaimed, its number one of the name's digit runs, outside
// every bracket unless an E, EP or Episode prefix writes it.
func FuzzReadClaim_bounded(f *testing.F) {
	for _, s := range []string{
		"[SubGroup] Anime Title - 05 [1080p].mkv", "Show.S01E05.720p.WEB-DL.mkv", "[Group] Title - S2E12 [720p].ass",
		"e05.srt", "Title - 105.srt", "Season Pack S01 Complete", "", "random garbage !@#$%", "Show [EP05] x",
		"X OP 02 X", "NCED01a", "Mob Psycho 100 05 [1080p].mkv", "05 - Title - 06", "Show - 0005v2 (NCED 02)",
		"Mob Psycho 100 05.mkv", "Show 05.", "Show 05.eng.forced.srt",
	} {
		f.Add(s, true)
		f.Add(s, false)
	}
	f.Fuzz(func(t *testing.T, name string, bare bool) {
		c := animemap.ReadEntry([]string{name}, bare)[0]
		base := name[strings.LastIndexAny(name, `/\`)+1:]
		switch c.Step {
		case animemap.ByLabel:
			if c.Episodes != nil || c.Markers != nil {
				t.Fatalf("%q: label claim %+v carries numbers", name, c)
			}
		case animemap.ByAbsolute:
			season := -1
			if bare {
				season = 1
			}
			if epmarker.Present(base) || len(episodematch.ReadPack([]string{name}, season)[0].Claims) > 0 {
				t.Fatalf("%q: step-2 claim %+v for a name step 1 reads", name, c)
			}
			if len(c.Episodes) != 1 || !digitRun(base, c.Episodes[0]) {
				t.Fatalf("%q: step-2 claim %+v is no digit run of the name", name, c)
			}
		case animemap.NoClaim:
			if c.Episodes != nil || c.Markers != nil || c.Kind != "" {
				t.Fatalf("%q: no claim but %+v", name, c)
			}
		}
	})
}

// digitRun reports whether n is a run of name's digits standing outside
// brackets and parentheses, or after an E, EP or Episode.
func digitRun(name string, n int) bool {
	depth := 0
	for i := 0; i < len(name); i++ {
		switch b := name[i]; {
		case b == '[' || b == '(':
			depth++
		case b == ']' || b == ')':
			depth = max(depth-1, 0)
		case b >= '0' && b <= '9':
			end := i
			for end < len(name) && name[end] >= '0' && name[end] <= '9' {
				end++
			}
			if v, err := strconv.Atoi(name[i:end]); err == nil && v == n && (depth == 0 || lettered(name[:i])) {
				return true
			}
			i = end - 1
		}
	}
	return false
}

func lettered(before string) bool {
	b := strings.ToLower(strings.TrimRight(before, " ._-"))
	return strings.HasSuffix(b, "e") || strings.HasSuffix(b, "ep") || strings.HasSuffix(b, "episode")
}
