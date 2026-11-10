package provider

import (
	"testing"

	"github.com/cplieger/subflux/internal/language"
)

func TestFlags(t *testing.T) {
	hi := language.Flags{HI: true}
	forced := language.Flags{Forced: true}
	cases := []struct {
		texts   []string
		flagged language.Flags
		want    language.Flags
	}{
		{[]string{"Show.S01E01.non-SDH.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Show.S01E01.Non SDH.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Show S01E01 NonSDH"}, language.Flags{}, language.Flags{}},
		{[]string{"Show S01E01 HI removed"}, language.Flags{}, language.Flags{}},
		{[]string{"Show S01E01 non-HI"}, language.Flags{}, language.Flags{}},
		{[]string{"Show S01E01 SDH removed"}, language.Flags{}, language.Flags{}},
		{[]string{"Show.S01E01.SDH.srt"}, language.Flags{}, hi},
		{[]string{"Show.S01E01.[HI].srt"}, language.Flags{}, hi},
		{[]string{"Show S01E01 (HI)"}, language.Flags{}, hi},
		{[]string{"Show S01E01 Hearing Impaired"}, language.Flags{}, hi},
		{[]string{"Show.S01E01.hi.srt"}, language.Flags{}, hi},
		{[]string{"Show_S01E01_HI_"}, language.Flags{}, hi},
		{[]string{"Show S01E01 closed captions"}, language.Flags{}, hi},
		{[]string{"Show.S01E01.cc.srt"}, language.Flags{}, hi},
		{[]string{"Show.S01E01.High.Quality.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Show.S01E01.Chinese.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Show.Hi10p.1080p.BluRay.x264-Group.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Hi.Score.Girl.S01E01.1080p.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Show.S01E01.srt"}, hi, hi},
		{[]string{"Show.S01E01.non-SDH.srt"}, hi, hi},
		{[]string{"plain comment", "release.SDH"}, language.Flags{}, hi},
		{[]string{"non-SDH version", "release.SDH"}, language.Flags{}, language.Flags{}},
		{[]string{"release.SDH", "non-SDH version"}, language.Flags{}, language.Flags{}},
		{nil, language.Flags{}, language.Flags{}},

		{[]string{"Movie.2020.FORCED.srt"}, language.Flags{}, forced},
		{[]string{"foreign parts only"}, language.Flags{}, forced},
		{[]string{"Non-HI.\nForeign parts on this release are hardcoded [English]."}, language.Flags{}, language.Flags{}},
		{[]string{"forced", "foreign parts hard-coded"}, language.Flags{}, language.Flags{}},
		{[]string{"foreign parts hard coded"}, forced, forced},
		{[]string{"Movie.2020.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Movie.2020.srt"}, forced, forced},
		{[]string{"Movie.2020.Unforced.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Movie 2020 non-forced"}, language.Flags{}, language.Flags{}},
		{[]string{"Movie.2020.Non.Forced.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Movie_2020_non_forced"}, language.Flags{}, language.Flags{}},
		{[]string{"full subtitle, not forced"}, language.Flags{}, language.Flags{}},
		{[]string{"Movie.2020.NonForced.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Movie.2020.unforced.srt"}, forced, forced},
		{[]string{"The.Foreigner.2017.1080p.BluRay.x264.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Reinforced.Concrete.2020.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Law.Enforced.S01E01.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Movie.Not.Forced.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Movie_Not_Forced.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Movie.NotForced.srt"}, language.Flags{}, language.Flags{}},
		{[]string{"Movie non-foreign"}, language.Flags{}, language.Flags{}},
		{[]string{"not foreign parts"}, language.Flags{}, language.Flags{}},

		{[]string{"Show.S01E01.SDH.FORCED.srt"}, language.Flags{}, language.Flags{HI: true, Forced: true}},
		{[]string{"Show.S01E01.FORCED.srt"}, hi, language.Flags{HI: true, Forced: true}},
		{[]string{"Show.S01E01.SDH.srt"}, forced, language.Flags{HI: true, Forced: true}},
	}
	for _, tc := range cases {
		if got := Flags(tc.flagged, tc.texts...); got != tc.want {
			t.Errorf("Flags(%+v, %q) = %+v, want %+v", tc.flagged, tc.texts, got, tc.want)
		}
	}
}
