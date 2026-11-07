package episodematch

import (
	"fmt"
	"testing"

	"github.com/cplieger/subflux/internal/language"
)

// BenchmarkAssignFirefly selects from the 14-member production Firefly pack
// against Firefly's 20 episodes.
func BenchmarkAssignFirefly(b *testing.B) {
	s := recorded(b, 78874, "dvd")
	var pack []string
	for _, p := range loadCorpus(b).Packs {
		if p.Show == 78874 && p.Kind == "prod" {
			for _, m := range p.Members {
				pack = append(pack, m.Name)
			}
		}
	}
	in := Input{Series: s, Members: members(pack...), Item: itemAt(b, &s, 1, 1), Variant: language.Standard}
	b.ReportAllocs()
	for b.Loop() {
		Select(&in)
	}
}

// BenchmarkAssignFuturamaSeason selects from a 26-member pack, season 2's 20
// titled episodes and six HI copies, against Futurama's 170 episodes.
func BenchmarkAssignFuturamaSeason(b *testing.B) {
	s := recorded(b, 73871, "official")
	var pack []string
	for _, it := range s.Items {
		if it.SeriesNumbering.Season == 2 {
			pack = append(pack, fmt.Sprintf("Futurama - S02E%02d - %s [WEBDL-1080p].en.srt", it.SeriesNumbering.Episode, it.Title))
		}
	}
	for e := 1; e <= 6; e++ {
		pack = append(pack, fmt.Sprintf("Futurama - S02E%02d - %s [WEBDL-1080p].sdh.en.srt", e, s.Items[itemAt(b, &s, 2, e)].Title))
	}
	in := Input{Series: s, Members: members(pack...), Item: itemAt(b, &s, 2, 5), Variant: language.HI}
	b.ReportAllocs()
	for b.Loop() {
		Select(&in)
	}
}
