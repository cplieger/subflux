package release

import "testing"

func FuzzParseReleaseName(f *testing.F) {
	f.Add("Show.S01E05.720p.WEB-DL.DDP5.1.H.264-GROUP")
	f.Add("Movie.2024.2160p.UHD.BluRay.x265.HDR.DTS-HD.MA.7.1-RELEASE")
	f.Add("[SubGroup] Anime Title - 25 [1080p][HEVC]")
	f.Add("Show.S03.COMPLETE.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb")
	f.Add("")
	f.Add("...---...///\\\\")
	f.Add("A.Very.Long.Title.With.Many.Dots.And.Numbers.123.456.789.S01E01.720p")
	f.Fuzz(func(t *testing.T, name string) {
		// Must not panic on any input.
		_ = ParseReleaseName(name)
	})
}
