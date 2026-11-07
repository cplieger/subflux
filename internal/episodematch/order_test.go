package episodematch

import (
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource"
)

// orderTypes names the orders newSeries reads from a recorded series: the
// aired order, then the catalogue's.
func orderTypes(s *Series) []string {
	out := []string{"official"}
	for _, o := range s.Reading.Catalogue.Orders {
		out = append(out, o.Type)
	}
	return out
}

// TestCompatibleOrders checks the production packs named in another order
// than aired against their recorded SkyHook orders. The expected sets are
// those the packs' labelled episodes leave possible (an order is ruled out
// when it places another episode at a member's number), except X-Men's
// fifth season, whose two "Old Soldiers" members are unassigned as
// duplicates and so rule nothing out. The Office's fifth season, whose
// S05E08 and S05E09 are swapped against every order, leaves none.
func TestCompatibleOrders(t *testing.T) {
	want := map[string][]string{
		"Cowboy Bebop (1998) - S01E13":              {"dvd"},
		"X-Men The Animated Series (1992) - S00E01": {"official", "dvd"},
		"X-Men The Animated Series (1992) - S01E01": {"dvd"},
		"X-Men The Animated Series (1992) - S02E01": {"official", "dvd"},
		"X-Men The Animated Series (1992) - S03E01": {"dvd"},
		"X-Men The Animated Series (1992) - S04E01": {"dvd"},
		"X-Men The Animated Series (1992) - S05E01": {"dvd"},
		"The Office (US) (2005) - S01E01":           {"official", "altdvd", "alternate", "dvd", "regional"},
		"The Office (US) (2005) - S02E01":           {"official", "altdvd", "alternate", "dvd", "regional"},
		"The Office (US) (2005) - S03E01":           {"official", "dvd", "regional"},
		"The Office (US) (2005) - S04E01":           {"official", "alternate"},
		"The Office (US) (2005) - S05E01-E02":       nil,
		"The Office (US) (2005) - S06E01":           {"official", "alternate", "regional"},
		"The Office (US) (2005) - S07E01":           {"altdvd", "dvd"},
		"The Office (US) (2005) - S08E01":           {"official", "altdvd", "alternate", "dvd", "regional"},
		"The Office (US) (2005) - S09E01":           {"official", "altdvd", "dvd", "regional"},
	}
	c := loadCorpus(t)
	seen := 0
	for _, p := range c.Packs {
		if p.Kind != "prod" || p.Show == 78874 {
			continue
		}
		key, _, _ := strings.Cut(p.Members[0].Name, " - ")
		rest, _, _ := strings.Cut(strings.TrimPrefix(p.Members[0].Name, key+" - "), " - ")
		key += " - " + rest
		s := recorded(t, p.Show, "official")
		names := make([]string, len(p.Members))
		for i, m := range p.Members {
			names[i] = m.Name
		}
		ser := newSeries(&s, 0)
		read := ReadPack(names, -1)
		types := orderTypes(&s)
		var got []string
		for _, o := range ser.compatible(read, ser.assign(read)) {
			got = append(got, types[o])
		}
		w, ok := want[key]
		if !ok {
			t.Errorf("unexpected pack %q", key)
			continue
		}
		seen++
		if !slices.Equal(got, w) {
			t.Errorf("%s: compatible %v; want %v", key, got, w)
		}
	}
	if seen != len(want) {
		t.Errorf("read %d packs; want %d", seen, len(want))
	}
}

// TestSwappedPackNotFollowed: The Office's fifth-season pack fits no order,
// so its numbers decide nothing, while each titled member still serves its
// own episode.
func TestSwappedPackNotFollowed(t *testing.T) {
	office := recorded(t, 73244, "official")
	var pack []string
	for _, p := range loadCorpus(t).Packs {
		if p.Show == 73244 && p.Kind == "prod" && strings.HasPrefix(p.Members[0].Name, "The Office (US) (2005) - S05") {
			for _, m := range p.Members {
				pack = append(pack, m.Name)
			}
		}
	}
	businessTrip := slices.IndexFunc(pack, func(n string) bool { return strings.Contains(n, "S05E09 - Business Trip") })
	if businessTrip < 0 {
		t.Fatal("no S05E09 Business Trip member")
	}
	wantTaken(t, choose(office, itemAt(t, &office, 5, 7), language.Standard, pack...), businessTrip, false)
	untitled := slices.Clone(pack)
	untitled[businessTrip] = "The Office (US) (2005) - S05E09 [AMZN][WEBDL-1080p][EAC3 5.1][h264]-FLUX.mkv"
	wantRefused(t, choose(office, itemAt(t, &office, 5, 7), language.Standard, untitled...), OrderUndetermined)
}

func TestCountPerCompatibleOrder(t *testing.T) {
	t.Run("an order placing an untitled claim at an assigned episode is dropped", func(t *testing.T) {
		s := synthetic(t, "official", []mediasource.Item{item(1, 1, "Alpha"), item(1, 2, "Beta")},
			map[string][][2]int{"o2": {{1, 2}, {1, 1}}})
		pack := []string{"Show.2001.Alpha.en.srt", "S01E01.en.srt"}
		ser := newSeries(&s, 0)
		read := ReadPack(pack, 1)
		ident := ser.assign(read)
		if c1, c2 := ser.count(0, read, ident, 1), ser.count(1, read, ident, 1); c1 != 1 || c2 != 2 {
			t.Errorf("counts O1 %d, O2 %d; want 1 and 2", c1, c2)
		}
		wantTaken(t, choose(s, 1, language.Standard, pack...), 1, true)
		wantTaken(t, choose(s, 0, language.Standard, pack...), 0, false)
	})

	t.Run("two orders kept by their count disagree", func(t *testing.T) {
		s := synthetic(t, "official", []mediasource.Item{item(1, 1, "Alpha"), item(1, 2, "Beta"), item(1, 3, "Gamma")},
			map[string][][2]int{"o2": {{1, 3}, {1, 1}, {1, 2}}, "o3": {{1, 3}, {1, 2}, {1, 1}}})
		pack := []string{"Show.2001.Alpha.en.srt", "S01E01.en.srt", "S01E02.en.srt"}
		wantCompare(t, choose(s, 1, language.Standard, pack...), 1, 2)
		in := Input{Series: s, Members: members(pack...), Item: 1, Variant: language.Standard, NoReference: true}
		wantRefused(t, Select(&in), OrderUndetermined)
	})

	t.Run("a claim counts only where the own order places its episode", func(t *testing.T) {
		s := synthetic(t, "official", []mediasource.Item{item(1, 1, "Alpha"), item(1, 2, "Beta"), item(2, 1, "Gamma")},
			map[string][][2]int{"o": {{1, 1}, {2, 1}, {1, 2}}})
		pack := []string{"S01E01.en.srt", "S01E02.en.srt"}
		ser := newSeries(&s, 0)
		read := ReadPack(pack, 1)
		ident := ser.assign(read)
		if own, o := ser.count(0, read, ident, 1), ser.count(1, read, ident, 1); own != 2 || o != 1 {
			t.Errorf("counts own %d, O %d; want 2 and 1", own, o)
		}
		wantTaken(t, choose(s, 1, language.Standard, pack...), 1, true)
		wantTaken(t, choose(s, 0, language.Standard, pack...), 0, true)
	})
}

func TestCountOverEpisodes(t *testing.T) {
	t.Run("a title assigned to another season counts nothing", func(t *testing.T) {
		s := greek(14, 3)
		pack := append(numbered("", 1, 13, ".en.srt"), "Show.2001.Omicron.en.srt")
		ser := newSeries(&s, 0)
		read := ReadPack(pack, 1)
		if n := ser.count(ser.own, read, ser.assign(read), 1); n != 13 {
			t.Errorf("count %d; want 13", n)
		}
		wantRefused(t, choose(s, 4, language.Standard, pack...), EpisodeCount)
	})

	t.Run("an untitled copy of an assigned episode counts once", func(t *testing.T) {
		firefly := recorded(t, 78874, "dvd")
		pack := []string{"S01E02.The.Train.Job.en.srt", "S01E02.en.srt"}
		ser := newSeries(&firefly, 0)
		read := ReadPack(pack, 1)
		ident := ser.assign(read)
		if got := ser.compatible(read, ident); !slices.Equal(got, []int{1}) {
			t.Fatalf("compatible %v; want only dvd", got)
		}
		if n := ser.count(1, read, ident, 1); n != 1 {
			t.Errorf("count %d; want 1", n)
		}
	})
}

func TestDistinctTitlesOneClaimCountTwo(t *testing.T) {
	s := greek(14)
	ser := newSeries(&s, 0)
	for _, c := range []struct {
		pack []string
		want int
	}{
		{[]string{"S01E01.Alpha.en.srt", "S01E01.Beta.sdh.en.srt", "S01E01.forced.en.srt"}, 2},
		{[]string{"S01E01.Alpha.en.srt", "S01E01.Alpha.sdh.en.srt"}, 1},
	} {
		read := ReadPack(c.pack, 1)
		if n := ser.count(ser.own, read, ser.assign(read), 1); n != c.want {
			t.Errorf("%v: count %d; want %d", c.pack, n, c.want)
		}
	}
}
