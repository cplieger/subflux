package media

import "testing"

var keySeeds = []string{
	"tmdb-1399", "tt0111161", "tt12345678", "tvdb-121-s01e02", "tmdbtv-1399-s00e00", "imdbtv-tt0944947-s65535e65535",
	"tvdb-121", "tmdbtv-1399", "imdbtv-tt0944947", "tvdb-0121-s01e01", "tvdb-121-s001e01", "tt00111161",
	"tmdb-9007199254740991", "tmdb-9007199254740992", "", "tvdb-121-s01e01-s01e01",
}

func FuzzParseKey_roundtrip(f *testing.F) {
	for _, s := range keySeeds {
		f.Add(s, uint8(Episode), uint8(TVDB), uint64(121), 1, 2)
	}
	f.Add("", uint8(Movie), uint8(IMDb), uint64(1<<53-1), 0, 0)
	f.Fuzz(func(t *testing.T, s string, kind, scheme uint8, id uint64, season, episode int) {
		if k, err := ParseKey(s); err == nil {
			if k.String() != s || len(k.Bytes()) != keyBytes {
				t.Fatalf("ParseKey(%q) = %q (%d bytes); want it to re-render byte-equal", s, k, len(k.Bytes()))
			}
		} else if k != (Key{}) {
			t.Fatalf("ParseKey(%q) refused with a non-zero key", s)
		}
		ids := ExternalIDs{TVDB: id, TMDB: id, IMDb: id}
		k, ok := Choose(Kind(kind), ids, Numbering{Scheme: Scheme(scheme), Season: season, Episode: episode})
		if !ok {
			return
		}
		if back, err := ParseKey(k.String()); err != nil || back != k {
			t.Fatalf("ParseKey(%q) = %q, %v; want the chosen key", k, back, err)
		}
		if len(k.String()) > maxKeyText {
			t.Fatalf("Choose built %q, over %d bytes", k, maxKeyText)
		}
	})
}

func FuzzParseRootKey_roundtrip(f *testing.F) {
	for _, s := range keySeeds {
		f.Add(s, uint8(Episode), uint8(TMDB), uint64(1399))
	}
	f.Fuzz(func(t *testing.T, s string, kind, scheme uint8, id uint64) {
		if r, err := ParseRootKey(s); err == nil {
			if r.String() != s || len(r.Bytes()) != rootKeyBytes {
				t.Fatalf("ParseRootKey(%q) = %q (%d bytes); want it to re-render byte-equal", s, r, len(r.Bytes()))
			}
		} else if r != (RootKey{}) {
			t.Fatalf("ParseRootKey(%q) refused with a non-zero key", s)
		}
		k, ok := Choose(Kind(kind), ExternalIDs{TVDB: id, TMDB: id, IMDb: id}, Numbering{Scheme: Scheme(scheme)})
		if !ok {
			return
		}
		if back, err := ParseRootKey(k.Root().String()); err != nil || back != k.Root() {
			t.Fatalf("ParseRootKey(%q) = %q, %v; want the chosen root", k.Root(), back, err)
		}
	})
}
