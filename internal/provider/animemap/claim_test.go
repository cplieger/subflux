package animemap_test

import (
	"slices"
	"testing"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/provider/animemap"
)

func regular(step animemap.Step, eps ...int) animemap.Claim {
	return animemap.Claim{Step: step, Kind: mapread.Regular, Episodes: eps}
}

func special(step animemap.Step, eps ...int) animemap.Claim {
	return animemap.Claim{Step: step, Kind: mapread.Special, Episodes: eps}
}

func marked(c animemap.Claim, ms ...epmarker.Marker) animemap.Claim {
	c.Markers = ms
	return c
}

var (
	credit    = animemap.Claim{Step: animemap.ByLabel, Kind: mapread.Credit}
	promotion = animemap.Claim{Step: animemap.ByLabel, Kind: mapread.Promotion}
	none      = animemap.Claim{}
)

func sameClaim(a, b animemap.Claim) bool {
	return a.Step == b.Step && a.Kind == b.Kind && slices.Equal(a.Episodes, b.Episodes) && slices.Equal(a.Markers, b.Markers)
}

// TestReadClaim reads each name in a one-file entry on the id path, which
// reads a bare leading number.
func TestReadClaim(t *testing.T) {
	s1e := func(s, e int) epmarker.Marker { return epmarker.Marker{Season: s, Episode: e} }
	tests := []struct {
		name string
		want animemap.Claim
	}{
		{"Show 08 [720p].mkv", regular(animemap.ByAbsolute, 8)},
		{"Show 03 [720p].mkv", regular(animemap.ByAbsolute, 3)},
		{"Show - 100.mkv", regular(animemap.ByAbsolute, 100)},
		{"日e08.mkv", regular(animemap.ByAbsolute, 8)},
		{"e08 - Show Title.mkv", regular(animemap.ByAbsolute, 8)},
		{"Release01.mkv", none},
		{"Premiere08.mkv", none},
		{"Sae08.mkv", none},
		{"Blaze08.mkv", none},
		{"Show.S09E09.S01E05.mkv", marked(regular(animemap.ByMarker, 5, 9), s1e(1, 5), s1e(9, 9))},
		{"Show S01E01 - 26.mkv", marked(regular(animemap.ByMarker, 1), s1e(1, 1))},
		{"Show S99999999999999999999E01 - 26.mkv", none},
		{"Show S99999999999999999999E01 NCED1.mkv", none},
		{"[Grp] Show 2nd Season - 05 [1080p]", regular(animemap.ByAbsolute, 5)},
		// epmarker reads " - NN" after a season statement as that season's.
		{"[SubsPlease] Show S2 - 05 (1080p)", marked(regular(animemap.ByMarker, 5), s1e(2, 5))},
		{"[Grp] Show - 05v2 [1080p]", regular(animemap.ByAbsolute, 5)},
		{"Show e05", regular(animemap.ByAbsolute, 5)},
		{"05 - Title - 06", regular(animemap.ByMarker, 5)},
		{"[Grp] Show S01E03 - 05", marked(regular(animemap.ByMarker, 3), s1e(1, 3))},
		{"[Grp] Movie [BD 1080p][E5A2C3D1]", none},
		{"Show - S01E05 (NCED)", marked(regular(animemap.ByMarker, 5), s1e(1, 5))},
		{"Show - S01E05 [OP]", marked(regular(animemap.ByMarker, 5), s1e(1, 5))},
		{"Show - 05 (NCED)", regular(animemap.ByAbsolute, 5)},
		{"Show - 05 [OP]", regular(animemap.ByAbsolute, 5)},
		{"Show - S00E02 (NCED)", marked(special(animemap.ByMarker, 2), s1e(0, 2))},
		{"Show - S00E03 [PV]", marked(special(animemap.ByMarker, 3), s1e(0, 3))},
		{"NCED1", credit},
		{"OP 2", credit},
		{"Show - NCOP 01", credit},
		{"Show_NCOP_01", credit},
		{"Extras/NCs/[Judas] Frieren - NCED01a.mkv", credit},
		{"Extras/[DB]Sousou no Frieren_-_NCOP01_(10bit_BD1080p_x265).mkv", credit},
		{"Extras/[DB]Sousou no Frieren_-_SPNCED_(10bit_BD1080p_x265).mkv", credit},
		{"SPNCOPX", none},
		{"PV2", promotion},
		{"Show trailer", promotion},
		{"Show CM 03", promotion},
		{"EDGE", none},
		{"OPUS", none},
		{"CMYK", none},
		{"Show op", none},
		{"Show ed", none},
		{"Show pv", none},
		{"Show cm", none},
		{"X op 02 X", regular(animemap.ByAbsolute, 2)},
		{"X OP 02 X", credit},
		{"Mob Psycho 100 05 [1080p].mkv", regular(animemap.ByAbsolute, 5)},
		{"Mob Psycho 100 05", regular(animemap.ByAbsolute, 5)},
		{"Mob Psycho 100 05.mkv", regular(animemap.ByAbsolute, 5)},
		{"Title 02 VOSTFR 10 bits", regular(animemap.ByAbsolute, 2)},
		{"Show 02 10 bits.mkv", regular(animemap.ByAbsolute, 2)},
		{"Show 10 bit x265.mkv", none},
		{"[Grp] Show [01 - 26] [BD]", none},
		{"Show S01E01.mkv", marked(regular(animemap.ByMarker, 1), s1e(1, 1))},
		{"show s01e01.mkv", marked(regular(animemap.ByMarker, 1), s1e(1, 1))},
		{"Mob Psycho 100 (2016) - S01E12 - Title [Bluray].mkv", marked(regular(animemap.ByMarker, 12), s1e(1, 12))},
		{"", none},
		{"some random file.mkv", none},
		{"Show - 08.mkv", regular(animemap.ByAbsolute, 8)},
		{"Show - 012.mkv", regular(animemap.ByAbsolute, 12)},
		{"[Group] Show - 1080p.mkv", none},
		{"Show [01 - 12].mkv", none},
		{"Premiere08 e08.mkv", regular(animemap.ByAbsolute, 8)},
		{"Show e08x.mkv", none},
		{"Show.S02.x264-E05.mkv", marked(regular(animemap.ByMarker, 5), s1e(2, 5))},
		{"Show Episode 08.mkv", regular(animemap.ByAbsolute, 8)},
		{"[Grp] Show EP08 [1080p].mkv", regular(animemap.ByAbsolute, 8)},
		{"[Group] Show - 26 [1080p].mkv", regular(animemap.ByAbsolute, 26)},
		{"Show - 00.mkv", special(animemap.ByAbsolute, 0)},
		{"Show Special - 03.mkv", special(animemap.ByAbsolute, 3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := animemap.ReadEntry([]string{tt.name}, true)[0]; !sameClaim(got, tt.want) {
				t.Errorf("claim %+v; want %+v", got, tt.want)
			}
		})
	}
}

// TestReadEntryBare: only the id path reads a bare leading number, and only
// in an entry where no name claims a marker; a title search reads the form.
func TestReadEntryBare(t *testing.T) {
	if got := animemap.ReadEntry([]string{"05 - Title - 06"}, false)[0]; !sameClaim(got, regular(animemap.ByAbsolute, 6)) {
		t.Errorf("title path: %+v; want step 2's 6", got)
	}
	got := animemap.ReadEntry([]string{"05 - Title - 06", "Show S01E02.mkv"}, true)
	if !sameClaim(got[0], regular(animemap.ByAbsolute, 6)) {
		t.Errorf("beside a marker: %+v; want step 2's 6", got[0])
	}
	if got := animemap.ReadEntry([]string{"Season 01/01 - Title.mkv"}, true)[0]; !sameClaim(got, regular(animemap.ByMarker, 1)) {
		t.Errorf("in a folder: %+v; want the bare 1", got)
	}
}
