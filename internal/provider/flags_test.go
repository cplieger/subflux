package provider

import "testing"

func TestHINegativesWin(t *testing.T) {
	cases := []struct {
		texts []string
		flag  bool
		want  bool
	}{
		{[]string{"Show.S01E01.non-SDH.srt"}, false, false},
		{[]string{"Show.S01E01.Non SDH.srt"}, false, false},
		{[]string{"Show S01E01 NonSDH"}, false, false},
		{[]string{"Show S01E01 HI removed"}, false, false},
		{[]string{"Show S01E01 non-HI"}, false, false},
		{[]string{"Show S01E01 SDH removed"}, false, false},
		{[]string{"Show.S01E01.SDH.srt"}, false, true},
		{[]string{"Show.S01E01.[HI].srt"}, false, true},
		{[]string{"Show S01E01 (HI)"}, false, true},
		{[]string{"Show S01E01 Hearing Impaired"}, false, true},
		{[]string{"Show.S01E01.hi.srt"}, false, true},
		{[]string{"Show_S01E01_HI_"}, false, true},
		{[]string{"Show S01E01 closed captions"}, false, true},
		{[]string{"Show.S01E01.cc.srt"}, false, true},
		{[]string{"Show.S01E01.High.Quality.srt"}, false, false},
		{[]string{"Show.S01E01.Chinese.srt"}, false, false},
		{[]string{"Show.S01E01.srt"}, true, true},
		{[]string{"Show.S01E01.non-SDH.srt"}, true, true},
		{[]string{"plain comment", "release.SDH"}, false, true},
		{[]string{"non-SDH version", "release.SDH"}, false, false},
		{[]string{"release.SDH", "non-SDH version"}, false, false},
		{nil, false, false},
	}
	for _, tc := range cases {
		if got := HearingImpaired(tc.flag, tc.texts...); got != tc.want {
			t.Errorf("HearingImpaired(%v, %q) = %v, want %v", tc.flag, tc.texts, got, tc.want)
		}
	}
}

func TestForced(t *testing.T) {
	cases := []struct {
		texts []string
		flag  bool
		want  bool
	}{
		{[]string{"Movie.2020.FORCED.srt"}, false, true},
		{[]string{"foreign parts only"}, false, true},
		{[]string{"Non-HI.\nForeign parts on this release are hardcoded [English]."}, false, false},
		{[]string{"forced", "foreign parts hard-coded"}, false, false},
		{[]string{"foreign parts hard coded"}, true, true},
		{[]string{"Movie.2020.srt"}, false, false},
		{[]string{"Movie.2020.srt"}, true, true},
	}
	for _, tc := range cases {
		if got := Forced(tc.flag, tc.texts...); got != tc.want {
			t.Errorf("Forced(%v, %q) = %v, want %v", tc.flag, tc.texts, got, tc.want)
		}
	}
}
