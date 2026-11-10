package language

import (
	"slices"
	"testing"
)

func TestReadFlags(t *testing.T) {
	cases := []struct {
		texts []string
		want  Flags
	}{
		{[]string{"The.Foreigner.2017.1080p.BluRay.x264.srt"}, Flags{}},
		{[]string{"Reinforced.Concrete.2020.srt"}, Flags{}},
		{[]string{"Law.Enforced.S01E01.srt"}, Flags{}},
		{[]string{"Movie.Not.Forced.srt"}, Flags{}},
		{[]string{"Movie.NotForced.srt"}, Flags{}},
		{[]string{"Movie non-foreign"}, Flags{}},
		{[]string{"English Unforced"}, Flags{}},
		{[]string{"SDH"}, Flags{HI: true}},
		{[]string{"Hearing Impaired"}, Flags{HI: true}},

		{[]string{"English (Forced)"}, Flags{Forced: true}},
		{[]string{"Foreign parts only"}, Flags{Forced: true}},
		{[]string{"Movie.2020.FORCED.srt"}, Flags{Forced: true}},
		{[]string{"Forcedsubs"}, Flags{}},
		{[]string{"Movie_2020_non_forced"}, Flags{}},
		{[]string{"full subtitle, not forced"}, Flags{}},
		{[]string{"Non-HI.\nForeign parts on this release are hardcoded [English]."}, Flags{}},
		{[]string{"foreign parts hard-coded"}, Flags{}},
		{[]string{"English Hearing-Impaired"}, Flags{HI: true}},
		{[]string{"Hard of Hearing"}, Flags{HI: true}},
		{[]string{"English [HI]"}, Flags{HI: true}},
		{[]string{"Show S01E01 (HI)"}, Flags{HI: true}},
		{[]string{"Show_S01E01_HI_"}, Flags{HI: true}},
		{[]string{"Show.S01E01.cc.srt"}, Flags{HI: true}},
		{[]string{"Show S01E01 closed captions"}, Flags{HI: true}},
		{[]string{"Show.S01E01.High.Quality.srt"}, Flags{}},
		{[]string{"Show.S01E01.Chinese.srt"}, Flags{}},
		{[]string{"English (non-SDH)"}, Flags{}},
		{[]string{"Show S01E01 NonSDH"}, Flags{}},
		{[]string{"Show S01E01 HI removed"}, Flags{}},
		{[]string{"Show S01E01 SDH removed"}, Flags{}},
		{[]string{"SDH Forced"}, Flags{HI: true, Forced: true}},
		{[]string{"Forced, non-SDH"}, Flags{Forced: true}},
		{[]string{"SDH, non-SDH"}, Flags{}},
		{[]string{"non-SDH, SDH"}, Flags{}},
		{[]string{"plain comment", "release.SDH"}, Flags{HI: true}},
		{[]string{"non-SDH version", "release.SDH"}, Flags{}},
		{[]string{"forced", "foreign parts hard-coded"}, Flags{}},
		{[]string{"SDH", "Forced"}, Flags{HI: true, Forced: true}},
		{[]string{"Movie.Forced.srt", "NotForced"}, Flags{}},
		{[]string{"Forced", "English Unforced"}, Flags{}},
		{[]string{"release.SDH", "NonSDH"}, Flags{}},
		{[]string{"Show.Hi10p.1080p.BluRay.x264-Group.srt"}, Flags{}},
		{[]string{"Show [Hi10p]"}, Flags{}},
		{[]string{"Show x264-Hi10p"}, Flags{}},
		{[]string{"Show.Hi10.720p.srt"}, Flags{}},
		{[]string{"Show.HI10P.1080p.srt"}, Flags{}},
		{[]string{"Show.Hi444p.1080p.srt"}, Flags{}},
		{[]string{"Hi-Score"}, Flags{}},
		{[]string{"Hi.Score.Girl.S01E01.1080p.srt"}, Flags{}},
		{[]string{"[SubsPlease] Hi Score Girl - 01 (1080p).mkv"}, Flags{}},
		{[]string{"Hi.Bye.Mama.S01E01.srt"}, Flags{}},
		{[]string{"Oh.Hi.Mark.srt"}, Flags{}},
		{[]string{"Show.S01E01.SDH.srt", "Non-Hi"}, Flags{}},
		{[]string{"SDH version, Non-Hi"}, Flags{}},
		{[]string{"Show.S01E01.SDH.srt", "Hi removed"}, Flags{}},
		{[]string{"Show.S01E01.SDH.srt", "Not Hi"}, Flags{}},
		{[]string{"Show.S01E01.HI.srt"}, Flags{HI: true}},
		{[]string{"Show.S01E01.en.hi.srt"}, Flags{HI: true}},
		// A recorded residual: lower-case hi is the sidecar tag, so the
		// greeting written lower case reads HI.
		{[]string{"oh.hi.mark.srt"}, Flags{HI: true}},
		{[]string{""}, Flags{}},
		{nil, Flags{}},
	}
	for _, tc := range cases {
		if got := ReadFlags(tc.texts...); got != tc.want {
			t.Errorf("ReadFlags(%q) = %+v, want %+v", tc.texts, got, tc.want)
		}
		reversed := slices.Clone(tc.texts)
		slices.Reverse(reversed)
		if got := ReadFlags(reversed...); got != tc.want {
			t.Errorf("ReadFlags(%q) = %+v, want %+v in any order", reversed, got, tc.want)
		}
	}
}
