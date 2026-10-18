package media

import "testing"

func TestChoose(t *testing.T) {
	all := ExternalIDs{TVDB: 121, TMDB: 1399, IMDb: 944947}
	cases := []struct {
		name string
		kind Kind
		ids  ExternalIDs
		n    Numbering
		want string // "" unidentified
	}{
		{"episode by its numbering's scheme", Episode, all, Numbering{Scheme: TMDB, Season: 2, Episode: 3}, "tmdbtv-1399-s02e03"},
		{"episode under None", Episode, all, Numbering{Scheme: None, Season: 1, Episode: 1}, ""},
		{"episode under an unknown scheme", Episode, all, Numbering{Scheme: IMDb + 1, Season: 1, Episode: 1}, ""},
		{"episode whose scheme's id is unknown", Episode, ExternalIDs{TMDB: 1399}, Numbering{Scheme: TVDB, Season: 1, Episode: 1}, ""},
		{"episode season 0 holds specials", Episode, all, Numbering{Scheme: TVDB}, "tvdb-121-s00e00"},
		{"movie by TMDB first, numbering ignored", Movie, all, Numbering{Scheme: TVDB, Season: -1, Episode: 70000}, "tmdb-1399"},
		{"movie never by TVDB", Movie, ExternalIDs{TVDB: 121}, Numbering{}, ""},
		{"movie with id 0 everywhere", Movie, ExternalIDs{}, Numbering{}, ""},
		{"zero kind", 0, all, Numbering{Scheme: TVDB, Season: 1, Episode: 1}, ""},
		{"unknown kind", Movie + 1, all, Numbering{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k, ok := Choose(c.kind, c.ids, c.n)
			if ok != (c.want != "") || k.String() != c.want {
				t.Errorf("Choose = %q, %v; want %q", k, ok, c.want)
			}
			if !ok && k != (Key{}) {
				t.Errorf("an unidentified Choose returned %+v; want the zero Key", k)
			}
		})
	}
}

func TestValidBounds(t *testing.T) {
	for id, want := range map[uint64]bool{0: false, 1: true, 1<<53 - 1: true, 1 << 53: false} {
		if got := ValidExternalID(id); got != want {
			t.Errorf("ValidExternalID(%d) = %v; want %v", id, got, want)
		}
	}
	for n, want := range map[int]bool{-1: false, 0: true, 65535: true, 65536: false} {
		if got := ValidEpisodeNumber(n); got != want {
			t.Errorf("ValidEpisodeNumber(%d) = %v; want %v", n, got, want)
		}
	}
}
