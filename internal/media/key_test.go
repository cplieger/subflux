package media

import (
	"bytes"
	"strings"
	"testing"
)

func mustChoose(t *testing.T, kind Kind, ids ExternalIDs, n Numbering) Key {
	t.Helper()
	k, ok := Choose(kind, ids, n)
	if !ok {
		t.Fatalf("Choose(%d, %+v, %+v) = unidentified", kind, ids, n)
	}
	return k
}

func TestKeyTexts(t *testing.T) {
	t.Run("movie and series of one TMDB id are distinct", func(t *testing.T) {
		movie := mustChoose(t, Movie, ExternalIDs{TMDB: 1399}, Numbering{})
		episode := mustChoose(t, Episode, ExternalIDs{TMDB: 1399}, Numbering{Scheme: TMDB, Season: 1, Episode: 2})
		if movie.String() != "tmdb-1399" || episode.Root().String() != "tmdbtv-1399" || episode.String() != "tmdbtv-1399-s01e02" {
			t.Errorf("texts = %q, %q, %q; want tmdb-1399, tmdbtv-1399, tmdbtv-1399-s01e02", movie, episode.Root(), episode)
		}
		if bytes.Equal(movie.Root().Bytes(), episode.Root().Bytes()) {
			t.Errorf("tmdb-1399 and tmdbtv-1399 share bytes %x", movie.Root().Bytes())
		}
	})
	t.Run("an IMDb id has one text", func(t *testing.T) {
		k := mustChoose(t, Movie, ExternalIDs{IMDb: 111161}, Numbering{})
		if k.String() != "tt0111161" {
			t.Errorf("String = %q; want tt0111161", k)
		}
		for _, s := range []string{"tt111161", "tt00111161"} {
			if _, err := ParseKey(s); err == nil {
				t.Errorf("ParseKey(%q) accepted a non-canonical IMDb width", s)
			}
		}
		series := mustChoose(t, Episode, ExternalIDs{IMDb: 944947}, Numbering{Scheme: IMDb, Season: 1, Episode: 1})
		if series.String() != "imdbtv-tt0944947-s01e01" {
			t.Errorf("String = %q; want imdbtv-tt0944947-s01e01", series)
		}
	})
	t.Run("episode 65,535 keys", func(t *testing.T) {
		k := mustChoose(t, Episode, ExternalIDs{TVDB: 121}, Numbering{Scheme: TVDB, Season: 65535, Episode: 65535})
		if k.String() != "tvdb-121-s65535e65535" {
			t.Errorf("String = %q", k)
		}
	})
	t.Run("-1 and 65,536 are unidentified", func(t *testing.T) {
		for _, n := range []Numbering{
			{Scheme: TVDB, Season: -1, Episode: 1},
			{Scheme: TVDB, Season: 1, Episode: -1},
			{Scheme: TVDB, Season: 65536, Episode: 1},
			{Scheme: TVDB, Season: 1, Episode: 65536},
		} {
			if k, ok := Choose(Episode, ExternalIDs{TVDB: 121}, n); ok {
				t.Errorf("Choose(%+v) = %q; want unidentified", n, k)
			}
		}
	})
	t.Run("an id of 2^53 is unknown, so the movie keys by IMDb", func(t *testing.T) {
		k := mustChoose(t, Movie, ExternalIDs{TMDB: 1 << 53, IMDb: 111161}, Numbering{})
		if k.String() != "tt0111161" {
			t.Errorf("String = %q; want tt0111161", k)
		}
		if k, ok := Choose(Movie, ExternalIDs{TMDB: 1<<53 - 1}, Numbering{}); !ok || k.String() != "tmdb-9007199254740991" {
			t.Errorf("Choose(2^53-1) = %q, %v; want tmdb-9007199254740991", k, ok)
		}
	})
	t.Run("ParseKey refuses", func(t *testing.T) {
		for _, s := range []string{
			"tvdb-121-s001e01", "tvdb-121-s1e01", "tvdb-121-s01e001", "tvdb-0121-s01e01", "tvdb-121",
			"tmdb-0", "tmdb-9007199254740992", "tmdb-12-s01e01", "tmdb-", "TMDB-12", "tmdb-+12", "tmdb-12 ",
			"tvdb-121-s01e65536", "tvdb-121-s01e01x", "", "tt0111161" + strings.Repeat("0", 56),
		} {
			if k, err := ParseKey(s); err == nil {
				t.Errorf("ParseKey(%q) = %q; want a refusal", s, k)
			}
		}
	})
	t.Run("ParseRootKey refuses an episode key", func(t *testing.T) {
		if r, err := ParseRootKey("tvdb-121-s01e01"); err == nil {
			t.Errorf("ParseRootKey = %q; want a refusal", r)
		}
		if r, err := ParseRootKey("tvdb-121"); err != nil || r.String() != "tvdb-121" {
			t.Errorf("ParseRootKey(tvdb-121) = %q, %v", r, err)
		}
	})
	t.Run("65 bytes are refused before parsing", func(t *testing.T) {
		long := "tvdb-1-s01e" + strings.Repeat("0", 52) + "1"
		if len(long) != 64 {
			t.Fatalf("fixture is %d bytes", len(long))
		}
		if _, err := ParseKey(long); err == nil || strings.Contains(err.Error(), "at most") {
			t.Errorf("ParseKey(64 bytes) = %v; want the non-canonical refusal", err)
		}
		if _, err := ParseKey(long + "1"); err == nil || !strings.Contains(err.Error(), "at most 64 bytes") {
			t.Errorf("ParseKey(65 bytes) = %v; want the length refusal", err)
		}
		if _, err := ParseRootKey("tvdb-" + strings.Repeat("1", 60)); err == nil || !strings.Contains(err.Error(), "at most 64 bytes") {
			t.Errorf("ParseRootKey(65 bytes) = %v; want the length refusal", err)
		}
	})
	t.Run("every accepted text re-renders", func(t *testing.T) {
		for _, s := range []string{"tmdb-1399", "tt0111161", "tt12345678", "tvdb-121-s00e00", "tmdbtv-1399-s01e02", "imdbtv-tt0944947-s10e100"} {
			k, err := ParseKey(s)
			if err != nil || k.String() != s {
				t.Errorf("ParseKey(%q) = %q, %v", s, k, err)
			}
		}
	})
}

func TestBytesExactPrefix(t *testing.T) {
	for _, pair := range [][2]string{{"tmdb-12", "tmdb-120"}, {"tvdb-12", "tvdb-120"}} {
		short, err1 := ParseRootKey(pair[0])
		long, err2 := ParseRootKey(pair[1])
		if err1 != nil || err2 != nil {
			t.Fatalf("ParseRootKey: %v, %v", err1, err2)
		}
		if bytes.HasPrefix(long.Bytes(), short.Bytes()) {
			t.Errorf("%s's bytes %x prefix %s's %x", pair[0], short.Bytes(), pair[1], long.Bytes())
		}
	}
	ep, err := ParseKey("tvdb-12-s01e02")
	if err != nil {
		t.Fatal(err)
	}
	if b := ep.Bytes(); len(b) != 14 || !bytes.HasPrefix(b, ep.Root().Bytes()) || !bytes.Equal(b[10:], []byte{0, 1, 0, 2}) {
		t.Errorf("Bytes = %x; want the root's ten bytes, then 0001 0002", b)
	}
	movie, err := ParseKey("tmdb-12")
	if err != nil {
		t.Fatal(err)
	}
	if b := movie.Bytes(); len(b) != 14 || !bytes.Equal(b[:10], []byte{byte(Movie), byte(TMDB), 0, 0, 0, 0, 0, 0, 0, 12}) {
		t.Errorf("Bytes = %x; want kind, scheme, big-endian 12, then four zero bytes", b)
	}
}
