package skyhook

import (
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/media"
)

// FuzzShow_bounded: any 200 body decodes to episodes inside media's bounds,
// or fails with a one-line error; nothing panics.
func FuzzShow_bounded(f *testing.F) {
	f.Add(recorded(f, "78874-eng-dvd.json"))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"episodes":[null]}`))
	f.Add([]byte(`{"episodes":[{"tvdbId":1,"seasonNumber":0,"episodeNumber":0,"absoluteEpisodeNumber":0}]}`))
	f.Add([]byte(`{"seasonTypes":[null,{"type":"dvd"}],"episodes":[{"tvdbId":9007199254740991,"seasonNumber":65535,"episodeNumber":65535}]}`))
	f.Add([]byte(`{"episodes":[{"tvdbId":1,"seasonNumber":1,"episodeNumber":1,"title":"\u0000\n"}]}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		fake := &fakeSkyHook{answers: []answer{ok(body)}}
		s, found, err := fakeClient(fake).Show(t.Context(), 78874, "eng", "official")
		if err != nil {
			if found || strings.ContainsAny(err.Error(), "\r\n") {
				t.Fatalf("Show = %v, %q", found, err)
			}
			return
		}
		if !found {
			t.Fatal("a 200 answered absent")
		}
		for _, e := range s.Episodes {
			if !media.ValidExternalID(e.ID) || !media.ValidEpisodeNumber(e.Season) ||
				!media.ValidEpisodeNumber(e.Number) || !media.ValidEpisodeNumber(e.Absolute) {
				t.Fatalf("episode %+v is outside media's bounds", e)
			}
		}
	})
}
