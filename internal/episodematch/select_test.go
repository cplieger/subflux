package episodematch

import (
	"fmt"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/language"
)

func choose(s Series, item int, v language.Variant, names ...string) Choice {
	return Select(&Input{Series: s, Members: members(names...), Item: item, Variant: v})
}

func wantTaken(t *testing.T, got Choice, member int, byNumber bool) {
	t.Helper()
	if got.Member != member || got.ByNumber != byNumber || got.Refusal != 0 || got.Compare != nil {
		t.Errorf("got %+v; want member %d taken, ByNumber %t", got, member, byNumber)
	}
}

func wantRefused(t *testing.T, got Choice, r Refusal) {
	t.Helper()
	if got.Refusal != r || got.Member != -1 || got.Compare != nil {
		t.Errorf("got %+v; want refusal %d", got, r)
	}
}

func wantCompare(t *testing.T, got Choice, compare ...int) {
	t.Helper()
	if !slices.Equal(got.Compare, compare) || got.Member != -1 || !got.ByNumber || got.Refusal != 0 {
		t.Errorf("got %+v; want members %v compared by number", got, compare)
	}
}

// numbered names a pack's members prefix S01E01 to S01E<n> with suffix.
func numbered(prefix string, season, n int, suffix string) []string {
	out := make([]string, n)
	for e := range n {
		out[e] = fmt.Sprintf("%sS%02dE%02d%s", prefix, season, e+1, suffix)
	}
	return out
}

func crossed(prefix string, season, n int, suffix string) []string {
	out := make([]string, n)
	for e := range n {
		out[e] = fmt.Sprintf("%s%dx%02d%s", prefix, season, e+1, suffix)
	}
	return out
}

func TestSelectSteps(t *testing.T) {
	got := recorded(t, 121361, "official")
	gotPack := append([]string{"Game of Thrones - 1x00 - Inside Game of Thrones.srt"}, crossed("Game of Thrones - ", 1, 10, ".srt")...)

	t.Run("a special is taken only by title", func(t *testing.T) {
		special := itemAt(t, &got, 0, 1)
		wantRefused(t, choose(got, special, language.Standard, append([]string{"Game of Thrones - 01x00.srt"}, crossed("Game of Thrones - ", 1, 10, ".srt")...)...), NoMember)
		wantTaken(t, choose(got, special, language.Standard, gotPack...), 0, false)
	})

	t.Run("a confident title is taken whatever the count", func(t *testing.T) {
		firefly := recorded(t, 78874, "official")
		dvdNumbered := []string{"Firefly [S01 E02] - The Train Job.eng.srt", "Firefly [S01 E03] - Bushwhacked.eng.srt", "Firefly [S01 E04] - Shindig.eng.srt"}
		wantTaken(t, choose(firefly, itemAt(t, &firefly, 1, 2), language.Standard, dvdNumbered...), 1, false)
	})

	futurama := recorded(t, 73871, "official")
	futuramaDVD := recorded(t, 73871, "dvd")
	addic7ed := crossed("Futurama - ", 2, 19, ".srt")

	t.Run("the count refuses a pack short of the season", func(t *testing.T) {
		wantRefused(t, choose(futurama, itemAt(t, &futurama, 2, 5), language.Standard, addic7ed...), EpisodeCount)
	})

	t.Run("the count is over the season in the series' own order", func(t *testing.T) {
		wantTaken(t, choose(futuramaDVD, itemAt(t, &futuramaDVD, 2, 1), language.Standard, addic7ed...), 0, true)
	})

	t.Run("versions and an E00 special leave the count equal", func(t *testing.T) {
		pack := append(slices.Clone(addic7ed), "Futurama - 2x05.sdh.srt", "Futurama - 2x00.srt")
		wantTaken(t, choose(futuramaDVD, itemAt(t, &futuramaDVD, 2, 3), language.Standard, pack...), 2, true)
		wantTaken(t, choose(futuramaDVD, itemAt(t, &futuramaDVD, 2, 5), language.HI, pack...), 19, true)
	})

	t.Run("no order moves an episode", func(t *testing.T) {
		ser := newSeries(&got, 0)
		for i, e := range ser.assign(ReadPack(gotPack, 1)) {
			if e != unassigned && !ser.episodes[e].special() {
				t.Errorf("%s is assigned regular episode %d", gotPack[i], e)
			}
		}
		wantTaken(t, choose(got, itemAt(t, &got, 1, 7), language.Standard, gotPack...), 7, true)
		var titled []string
		for e := 1; e <= 10; e++ {
			titled = append(titled, fmt.Sprintf("Game of Thrones - 1x%02d - %s.srt", e, got.Items[itemAt(t, &got, 1, e)].Title))
		}
		wantTaken(t, choose(got, itemAt(t, &got, 1, 7), language.Standard, titled...), 6, true)
		mislabelled := crossed("Game of Thrones - ", 1, 10, ".srt")
		mislabelled[1] = "Game of Thrones - 1x02 - Winter Is Coming.srt"
		wantTaken(t, choose(got, itemAt(t, &got, 1, 2), language.Standard, mislabelled...), 1, true)
	})

	t.Run("the compatible orders agree", func(t *testing.T) {
		bebop := recorded(t, 76885, "official")
		pack := bebopProduction(t)
		pack[8] = "Cowboy Bebop (1998) - S01E05 - 005 [v3 Bluray-1080p][FLAC 2.0][JA+EN][x264 8bit]-JySzE.mkv"
		wantTaken(t, choose(bebop, itemAt(t, &bebop, 1, 15), language.Standard, pack...), 8, true)
	})

	fireflyDVD := recorded(t, 78874, "dvd")
	untitled := numbered("Firefly.", 1, 14, ".720p.HDTV.x264.srt")

	t.Run("no catalogue takes the series' own number", func(t *testing.T) {
		bare := fireflyDVD
		bare.Reading = nil
		wantTaken(t, choose(bare, itemAt(t, &bare, 1, 3), language.Standard, untitled...), 2, true)
	})

	t.Run("the compatible orders disagree", func(t *testing.T) {
		wantCompare(t, choose(fireflyDVD, itemAt(t, &fireflyDVD, 1, 1), language.Standard, untitled...), 0, 10)
	})

	t.Run("no title and no reference is undetermined", func(t *testing.T) {
		in := Input{Series: fireflyDVD, Members: members(untitled...), Item: itemAt(t, &fireflyDVD, 1, 1), Variant: language.Standard, NoReference: true}
		wantRefused(t, Select(&in), OrderUndetermined)
	})

	t.Run("no compatible order is undetermined", func(t *testing.T) {
		pack := slices.Clone(untitled)
		pack[0] = "Firefly.S01E01.Bushwhacked.720p.HDTV.x264.srt"
		wantRefused(t, choose(fireflyDVD, itemAt(t, &fireflyDVD, 1, 5), language.Standard, pack...), OrderUndetermined)
	})
}

func TestSelectManual(t *testing.T) {
	fireflyDVD := recorded(t, 78874, "dvd")
	untitled := numbered("Firefly.", 1, 14, ".720p.HDTV.x264.srt")
	pick := func(s Series, item int, names ...string) Choice {
		return Select(&Input{Series: s, Members: members(names...), Item: item, Variant: language.Standard, Manual: true})
	}

	t.Run("no compatible order takes the own order's group", func(t *testing.T) {
		pack := slices.Clone(untitled)
		pack[0] = "Firefly.S01E01.Bushwhacked.720p.HDTV.x264.srt"
		wantTaken(t, pick(fireflyDVD, itemAt(t, &fireflyDVD, 1, 5), pack...), 4, true)
	})

	t.Run("disagreeing orders take the own order's group", func(t *testing.T) {
		wantTaken(t, pick(fireflyDVD, itemAt(t, &fireflyDVD, 1, 1), untitled...), 0, true)
	})

	t.Run("a count that differs refuses nothing", func(t *testing.T) {
		futurama := recorded(t, 73871, "official")
		wantTaken(t, pick(futurama, itemAt(t, &futurama, 2, 5), crossed("Futurama - ", 2, 19, ".srt")...), 4, true)
	})

	t.Run("the agreed group is taken", func(t *testing.T) {
		bebop := recorded(t, 76885, "official")
		pack := bebopProduction(t)
		pack[8] = "Cowboy Bebop (1998) - S01E05 - 005 [v3 Bluray-1080p][FLAC 2.0][JA+EN][x264 8bit]-JySzE.mkv"
		wantTaken(t, pick(bebop, itemAt(t, &bebop, 1, 15), pack...), 8, true)
	})

	t.Run("an unavailable group refuses", func(t *testing.T) {
		pack := slices.Clone(untitled)
		pack[0] = "Firefly.S01E01.720p.HDTV.x264.sdh.srt"
		wantRefused(t, pick(fireflyDVD, itemAt(t, &fireflyDVD, 1, 1), pack...), NoMember)
	})
}

// bebopProduction is the production Cowboy Bebop pack, named in DVD order.
func bebopProduction(t *testing.T) []string {
	t.Helper()
	c := loadCorpus(t)
	for _, p := range c.Packs {
		if p.Show == 76885 && p.Kind == "prod" {
			names := make([]string, len(p.Members))
			for i, m := range p.Members {
				names[i] = m.Name
			}
			return names
		}
	}
	t.Fatal("no production Cowboy Bebop pack")
	return nil
}

func TestCompatibleOrdersDisagreeAtItem(t *testing.T) {
	bebop := recorded(t, 76885, "dvd")
	pack := numbered("Cowboy Bebop - ", 1, 26, " [BD 1080p].mkv")
	pack[18] = "Cowboy Bebop - S01E19 - Wild Horses [BD 1080p].mkv"
	pack[19] = "Cowboy Bebop - S01E20 - Pierrot Le Fou [BD 1080p].mkv"
	item := itemAt(t, &bebop, 1, 1)
	got := choose(bebop, item, language.Standard, pack...)
	wantCompare(t, got, 0, 12)
	ser := newSeries(&bebop, item)
	if ident := ser.assign(ReadPack(pack, 1)); ident[18] == unassigned || ident[19] == unassigned {
		t.Errorf("the titled members are unassigned: %v", ident[18:20])
	}
}

func TestUnavailableGroupNeverServes(t *testing.T) {
	got := recorded(t, 121361, "official")
	pack := crossed("Game of Thrones - ", 1, 10, ".en.srt")
	pack[2] = "Game of Thrones - 1x03.sdh.en.srt"
	pack[3] = "Game of Thrones - 1x04.forced.en.srt"
	pack[4] = "Game of Thrones - 1x05.sdh.en.srt"
	pack = append(pack, "Game of Thrones - 1x05.en.srt")
	for _, c := range []struct {
		name    string
		episode int
		variant language.Variant
		member  int
	}{
		{"an HI-only group serves no Standard pair", 3, language.Standard, -1},
		{"an HI-only group serves HI", 3, language.HI, 2},
		{"a forced-only group serves no Standard pair", 4, language.Standard, -1},
		{"a forced-only group serves no HI pair", 4, language.HI, -1},
		{"a forced-only group serves Forced", 4, language.Forced, 3},
		{"a group yields its plain member for Standard", 5, language.Standard, 10},
		{"a group yields its HI member for HI", 5, language.HI, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := choose(got, itemAt(t, &got, 1, c.episode), c.variant, pack...)
			if c.member < 0 {
				wantRefused(t, got, NoMember)
			} else {
				wantTaken(t, got, c.member, true)
			}
		})
	}
}

func TestIdentityBothNumberings(t *testing.T) {
	firefly := recorded(t, 78874, "dvd")
	serenity := itemAt(t, &firefly, 1, 1)
	for _, c := range []struct {
		name string
		file string
		want Identity
	}{
		{"an aired claim fits the DVD item", "Firefly.S01E11.Serenity.1080p.WEB-DL.DDP5.1.H.264-FLUX.srt", Identity{ByNumber: true}},
		{"a title naming another episode refutes", "Firefly.S01E01.The.Train.Job.1080p.WEB-DL.DDP5.1.H.264-FLUX.srt", Identity{Refuted: true}},
		{"an untitled DVD claim rests on its number", "Firefly.S01E01.1080p.WEB-DL.DDP5.1.H.264-FLUX.srt", Identity{ByNumber: true}},
		{"an untitled claim of neither numbering refutes", "Firefly.S01E02.1080p.WEB-DL.DDP5.1.H.264-FLUX.srt", Identity{Refuted: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Identify(&firefly, serenity, c.file); got != c.want {
				t.Errorf("Identify(%q) = %+v; want %+v", c.file, got, c.want)
			}
		})
	}
	t.Run("a title naming the item wins over its numbers", func(t *testing.T) {
		reynolds := itemAt(t, &firefly, 1, 6)
		if got := Identify(&firefly, reynolds, "Firefly.S01E05.Our.Mrs.Reynolds.720p.srt"); got != (Identity{Titled: true}) {
			t.Errorf("got %+v; want titled", got)
		}
	})
	t.Run("an index-false series never reads the title", func(t *testing.T) {
		got := recorded(t, 121361, "official")
		kingsroad := itemAt(t, &got, 1, 2)
		if id := Identify(&got, kingsroad, "Game.of.Thrones.S01E02.Winter.Is.Coming.720p.srt"); id != (Identity{}) {
			t.Errorf("got %+v; want trusted by number", id)
		}
		if id := Identify(&got, kingsroad, "Game.of.Thrones.S01E03.The.Kingsroad.720p.srt"); id != (Identity{Refuted: true}) {
			t.Errorf("got %+v; want refuted by number", id)
		}
	})
	t.Run("a number with no season fits the aired absolute", func(t *testing.T) {
		bebop := recorded(t, 76885, "official")
		asteroid := itemAt(t, &bebop, 1, 13)
		if id := Identify(&bebop, asteroid, "[Grp] Cowboy Bebop - 01 [1080p].mkv"); id != (Identity{ByNumber: true}) {
			t.Errorf("absolute 1: got %+v; want resting on its number", id)
		}
		if id := Identify(&bebop, asteroid, "[Grp] Cowboy Bebop - 07 [1080p].mkv"); id != (Identity{Refuted: true}) {
			t.Errorf("absolute 7: got %+v; want refuted", id)
		}
	})
}

func TestMixedTitlesInOneGroup(t *testing.T) {
	firefly := recorded(t, 78874, "dvd")
	trainJob, bushwhacked := itemAt(t, &firefly, 1, 2), itemAt(t, &firefly, 1, 3)
	pack := []string{"S01E02.Bushwhacked.en.srt", "S01E02.The.Train.Job.sdh.en.srt"}
	for _, reversed := range []bool{false, true} {
		names := slices.Clone(pack)
		at := func(i int) int { return i }
		if reversed {
			slices.Reverse(names)
			at = func(i int) int { return 1 - i }
		}
		t.Run(fmt.Sprintf("reversed %t", reversed), func(t *testing.T) {
			wantTaken(t, choose(firefly, trainJob, language.HI, names...), at(1), false)
			if got := choose(firefly, trainJob, language.Standard, names...); got.Member >= 0 || got.Compare != nil {
				t.Errorf("The Train Job Standard: got %+v; want nothing taken", got)
			}
			wantTaken(t, choose(firefly, bushwhacked, language.Standard, names...), at(0), false)
			if got := choose(firefly, bushwhacked, language.HI, names...); got.Member >= 0 || got.Compare != nil {
				t.Errorf("Bushwhacked HI: got %+v; want nothing taken", got)
			}
		})
	}
}

// greek is a season of no-reading items titled by Greek letters.
func greek(seasons ...int) Series {
	letters := []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta", "Eta", "Theta", "Iota", "Kappa", "Lambda", "Mu", "Nu", "Xi", "Omicron", "Pi", "Rho", "Sigma", "Tau", "Upsilon"}
	var s Series
	k := 0
	for season, n := range seasons {
		for e := range n {
			s.Items = append(s.Items, item(season+1, e+1, letters[k]))
			k++
		}
	}
	return s
}

func TestTitledVersionsBypassCount(t *testing.T) {
	s := greek(14)
	pack := []string{"S01E01.Alpha.en.srt", "S01E01.Alpha.sdh.en.srt"}
	wantTaken(t, choose(s, 0, language.Standard, pack...), 0, false)
	wantTaken(t, choose(s, 0, language.HI, pack...), 1, false)
}
