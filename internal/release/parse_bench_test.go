package release

import (
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/media"
)

func BenchmarkParse(b *testing.B) {
	names := goldenNames(b)
	tables()
	b.ReportAllocs()
	for b.Loop() {
		for _, n := range names {
			Parse(n, media.Episode)
			Parse(n, media.Movie)
		}
	}
}

// BenchmarkParseWorstCase runs names at MaxNameLen built to make the
// lookaround patterns scan to the end from every position.
func BenchmarkParseWorstCase(b *testing.B) {
	tables()
	for _, unit := range []string{"ma ", "a-", "www.", "[a ", "Special.", "-x"} {
		name := strings.Repeat(unit, MaxNameLen/len(unit)+1)[:MaxNameLen]
		b.Run(strings.TrimSpace(unit), func(b *testing.B) {
			for b.Loop() {
				Parse(name, media.Episode)
				Parse(name, media.Movie)
			}
		})
	}
}
