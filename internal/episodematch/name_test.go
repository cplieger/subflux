package episodematch

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/language"
)

// TestExtractTitle holds the extractor to the measured one: each title is
// titlematch/tm.py extract_title's answer for the name.
func TestExtractTitle(t *testing.T) {
	for _, c := range []struct{ name, title string }{
		{"Star Trek - S01E04 - El enemigo interior [WEBDL-1080p][EAC3 5.1][h264]-FLUX.en.srt", "El enemigo interior"},
		{"X-Men The Animated Series (1992) - S05E02 - The Phalanx Covenant 2 [DSNP][WEBDL-480p][AAC 2.0][h264]-HONE.mkv", "The Phalanx Covenant 2"},
		{"S01E05-Celui qui lave plus blanc [6976D05C].mkv", "Celui qui lave plus blanc"},
		{"[Judas] Doctor Who (2005) - S01E02.mkv", ""},
		{"Firefly.S01E10.1080p.BluRay.x264-REWARD.srt", ""},
		{"Lost S01E01 1080p DSNP WEB-DL DDP5.1 H.264-FLUX.eng.srt", ""},
		{"[Group] Sousou no Frieren - 26 - Mahou No Takami.mkv", "Mahou No Takami"},
		{"[SubsPlease] How I Met Your Mother - 03 (1080p) [11C13C4E].mkv", ""},
		{"[amZero] Friends - 10v2 [1080p AV1 10Bit][Dual Audio][MultiSubs].mkv", ""},
		{"How I Met Your Mother - 02x04 - Ted Mosby, architect.English.HI.srt", "Ted Mosby, architect"},
		{"Breaking Bad - 01x02 - El gato está en la bolsa....English.HI.srt", "El gato está en la bolsa"},
		{"Cowboy Bebop (1998) - S01E13 - 013 - Session #13 Jupiter Jazz 2 [v3 Bluray-1080p][FLAC 2.0][JA+EN][x264 8bit]-JySzE.mkv", "Session #13 Jupiter Jazz 2"},
		{"Firefly.S01E01.The.Train.Job.1080p.WEB-DL.DDP5.1.H.264-FLUX.srt", "The Train Job"},
		{"Firefly [S01 E01] - Serenity.eng.srt", "Serenity"},
	} {
		if got := extractTitle(c.name); got != c.title {
			t.Errorf("extractTitle(%q) = %q; want %q", c.name, got, c.title)
		}
	}
}

// TestNoTitleForDigits: a name whose rest is digits or tags has no title, so
// Doctor Who's "42" is taken by the number the aired and alternate orders
// agree on (the DVD order counts 12 of season 3's 13 and is dropped).
func TestNoTitleForDigits(t *testing.T) {
	for _, n := range []string{
		"Doctor Who (2005) - 03x07 - 42.English.HI.srt",
		"Doctor.Who.2005.S03E07.42.720p.HDTV.x264.srt",
		"Doctor.Who.2005.S03E07.720p.HDTV.x264-FoV.srt",
	} {
		if got := ReadName(n); got.Title != "" || !slices.Equal(got.Claims, []epmarker.Marker{{Season: 3, Episode: 7}}) {
			t.Errorf("ReadName(%q) = %+v; want S03E07 and no title", n, got)
		}
	}
	who := recorded(t, 78804, "official")
	pack := numbered("Doctor.Who.2005.", 3, 13, ".720p.HDTV.x264.srt")
	pack[6] = "Doctor.Who.2005.S03E07.42.720p.HDTV.x264.srt"
	wantTaken(t, choose(who, itemAt(t, &who, 3, 7), language.Standard, pack...), 6, true)
	ser := newSeries(&who, 0)
	if e := ser.assign(ReadPack(pack, 3))[6]; e != unassigned {
		t.Errorf("the 42 member is assigned episode %d", e)
	}
}

func TestReadName(t *testing.T) {
	for _, c := range []struct {
		name    string
		claims  []epmarker.Marker
		special bool
	}{
		{"Season 1/Show.S01E02.Title.srt", []epmarker.Marker{{Season: 1, Episode: 2}}, false},
		{`Pack\Show - 1x00 - Inside.srt`, []epmarker.Marker{{Season: 1, Episode: 0}}, true},
		{"Show.S00E03.srt", []epmarker.Marker{{Season: 0, Episode: 3}}, true},
		{"Show - Christmas Special.srt", nil, true},
		{"Show.Specialist.S01E02.srt", []epmarker.Marker{{Season: 1, Episode: 2}}, false},
		{"Show.S01E01-E02.srt", []epmarker.Marker{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}}, false},
		{"03 - Title.srt", nil, false},
	} {
		got := ReadName(c.name)
		if !slices.Equal(got.Claims, c.claims) || got.Special != c.special {
			t.Errorf("ReadName(%q) = %+v; want claims %v, special %t", c.name, got, c.claims, c.special)
		}
	}
}

func TestReadPackBareNumber(t *testing.T) {
	t.Run("an unmarked pack reads its leading numbers under its season", func(t *testing.T) {
		got := ReadPack([]string{"01 - Winter Is Coming.srt", "02 - The Kingsroad.srt", "Extras.srt"}, 1)
		want := [][]epmarker.Marker{{{Season: 1, Episode: 1}}, {{Season: 1, Episode: 2}}, nil}
		for i := range got {
			if !slices.Equal(got[i].Claims, want[i]) || got[i].Bare != (want[i] != nil) {
				t.Errorf("member %d claims %v bare %v; want %v bare %v", i, got[i].Claims, got[i].Bare, want[i], want[i] != nil)
			}
		}
	})
	t.Run("a marked member turns the reading off", func(t *testing.T) {
		got := ReadPack([]string{"01 - Winter Is Coming.srt", "Show.S01E02.srt"}, 1)
		if got[0].Claims != nil || got[0].Bare {
			t.Errorf("the bare member reads %+v", got[0])
		}
		if got[1].Bare {
			t.Errorf("the marked member is read bare")
		}
	})
	t.Run("no season reads none", func(t *testing.T) {
		if got := ReadPack([]string{"01 - Winter Is Coming.srt"}, -1); got[0].Claims != nil {
			t.Errorf("claims %v", got[0].Claims)
		}
	})
	t.Run("season 0 reads specials", func(t *testing.T) {
		if got := ReadPack([]string{"03 - Inside.srt"}, 0); !got[0].Special || !slices.Equal(got[0].Claims, []epmarker.Marker{{Season: 0, Episode: 3}}) {
			t.Errorf("got %+v; want special S00E03", got[0])
		}
	})
}
