package gestdown

import (
	"testing"

	"github.com/cplieger/subflux/internal/language"
)

func TestCandidates(t *testing.T) {
	en, _ := language.Parse("en")
	eps := []seasonEpisode{
		{Number: 1, Subtitles: []subtitle{
			{SubtitleID: "a", Version: " DVDRip, ORPHEUS ", DownloadURI: "/subtitles/download/a", Completed: true},
			{SubtitleID: "b", Version: "720p.SDH", DownloadURI: "/subtitles/download/b", Completed: true},
			{SubtitleID: "c", Version: "WEB", DownloadURI: "/subtitles/download/c", Completed: true, HearingImpaired: true},
			{SubtitleID: "d", DownloadURI: "/subtitles/download/d"},
			{SubtitleID: "e", DownloadURI: "https://elsewhere.example/e", Completed: true},
			{SubtitleID: "", DownloadURI: "/subtitles/download/", Completed: true},
		}},
		{Number: 2, Subtitles: []subtitle{{SubtitleID: "f", DownloadURI: "/subtitles/download/f", Completed: true}}},
	}
	got := candidates(eps, 1, en)
	want := []struct {
		ref, release string
		hi           bool
	}{{"a", "DVDRip ORPHEUS", false}, {"b", "720p.SDH", true}, {"c", "WEB", true}}
	if len(got) != len(want) {
		t.Fatalf("%d candidates, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if c := got[i]; c.Ref.Token() != w.ref || c.Release != w.release || c.Flags.HI != w.hi || c.Lang != en {
			t.Errorf("candidate %d: %+v, want %+v", i, c, w)
		}
	}
}

func FuzzCandidates(f *testing.F) {
	f.Add("a", "/subtitles/download/a", "DVDRip, X", true, false, 1)
	f.Add("", "", "", false, false, 0)
	f.Add("x/../y", "/subtitles/download/x/../y", "a,b,,c", true, true, -1)
	f.Fuzz(func(t *testing.T, id, uri, version string, completed, hi bool, number int) {
		en, _ := language.Parse("en")
		eps := []seasonEpisode{{Number: number, Subtitles: []subtitle{{SubtitleID: id, DownloadURI: uri, Version: version, Completed: completed, HearingImpaired: hi}}}}
		for _, c := range candidates(eps, number, en) {
			if !completed || c.Ref.Token() != id || id == "" || uri != "/subtitles/download/"+id {
				t.Fatalf("kept %+v from id %q uri %q completed %v", c, id, uri, completed)
			}
			if hi && !c.Flags.HI {
				t.Fatal("dropped the provider's HI flag")
			}
		}
	})
}
