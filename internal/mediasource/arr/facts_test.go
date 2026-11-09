package arr_test

import (
	"slices"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource/arr"
)

func TestIMDb(t *testing.T) {
	cases := map[string]uint64{"tt0123456": 123456, "tt": 0, "nm1": 0, "": 0, "tt12a": 0, "tt-1": 0, "tt99999999999999999999": 0}
	for in, want := range cases {
		if got := arr.IMDb(in); got != want {
			t.Errorf("IMDb(%q) = %d, want %d", in, got, want)
		}
	}
}

func tag(t *testing.T, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("Parse(%q) failed", s)
	}
	return tg
}

func TestOriginalLang(t *testing.T) {
	if got := arr.OriginalLang(&arrapi.Language{Name: "Japanese", ID: 8}); got != tag(t, "ja") {
		t.Errorf("OriginalLang(Japanese) = %v, want ja", got)
	}
	for name, l := range map[string]*arrapi.Language{"nil": nil, "unknown": {Name: "Unknown"}, "unnamed": {Name: "Klingonese"}} {
		if got := arr.OriginalLang(l); got != (language.Tag{}) {
			t.Errorf("OriginalLang(%s) = %v, want the zero tag", name, got)
		}
	}
}

func TestAltTitles(t *testing.T) {
	got := arr.AltTitles("Firefly", []string{"", "firefly", "Serenity", "SERENITY", "Firefly - Der Aufbruch", "serenity"})
	if want := []string{"Serenity", "Firefly - Der Aufbruch"}; !slices.Equal(got, want) {
		t.Errorf("AltTitles = %q, want %q", got, want)
	}
	if got := arr.AltTitles("Firefly", nil); got != nil {
		t.Errorf("AltTitles of none = %q, want nil", got)
	}
}

func TestAudioLangs(t *testing.T) {
	got := arr.AudioLangs(&arrapi.MediaInfo{AudioLanguages: []string{"jpn", "english", "eng", "xx-nonsense", "fre", "Japanese"}})
	if want := []language.Tag{tag(t, "ja"), tag(t, "en"), tag(t, "fr")}; !slices.Equal(got, want) {
		t.Errorf("AudioLangs = %v, want %v", got, want)
	}
	if got := arr.AudioLangs(nil); got != nil {
		t.Errorf("AudioLangs(nil) = %v, want nil", got)
	}
}
