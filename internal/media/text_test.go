package media

import (
	"encoding"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

var (
	_ encoding.TextMarshaler   = Key{}
	_ encoding.TextUnmarshaler = (*Key)(nil)
	_ encoding.TextMarshaler   = RootKey{}
	_ encoding.TextUnmarshaler = (*RootKey)(nil)
	_ encoding.TextMarshaler   = Kind(0)
	_ encoding.TextUnmarshaler = (*Kind)(nil)
	_ encoding.TextMarshaler   = Scheme(0)
	_ encoding.TextUnmarshaler = (*Scheme)(nil)
	_ encoding.TextMarshaler   = SourceID("")
	_ encoding.TextUnmarshaler = (*SourceID)(nil)
)

func TestTextCodecs(t *testing.T) {
	t.Run("keys and enums round-trip through encoding/json", func(t *testing.T) {
		type doc struct {
			Key    Key
			Root   RootKey
			Kind   Kind
			Scheme Scheme
		}
		ep, err := ParseKey("tvdb-121-s01e02")
		if err != nil {
			t.Fatal(err)
		}
		in := doc{Key: ep, Root: ep.Root(), Kind: Movie, Scheme: None}
		b, err := json.Marshal(in)
		const want = `{"Key":"tvdb-121-s01e02","Root":"tvdb-121","Kind":"movie","Scheme":"none"}`
		if err != nil || string(b) != want {
			t.Fatalf("json.Marshal = %s, %v; want %s", b, err, want)
		}
		var out doc
		if err := json.Unmarshal(b, &out); err != nil || out != in {
			t.Errorf("json.Unmarshal = %+v, %v; want %+v", out, err, in)
		}
	})
	t.Run("every enum spelling", func(t *testing.T) {
		for v, s := range map[Kind]string{Episode: "episode", Movie: "movie"} {
			var got Kind
			if b, err := v.MarshalText(); err != nil || string(b) != s || got.UnmarshalText(b) != nil || got != v {
				t.Errorf("Kind %d: %q, %v, back %d", v, b, err, got)
			}
		}
		for v, s := range map[Scheme]string{None: "none", TVDB: "tvdb", TMDB: "tmdb", IMDb: "imdb"} {
			var got Scheme
			if b, err := v.MarshalText(); err != nil || string(b) != s || got.UnmarshalText(b) != nil || got != v {
				t.Errorf("Scheme %d: %q, %v, back %d", v, b, err, got)
			}
		}
	})
	t.Run("refusals", func(t *testing.T) {
		movie, err := ParseKey("tmdb-1399")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"", "tmdb-01399", "tvdb-121", "TMDB-1399"} {
			k := movie
			if err := k.UnmarshalText([]byte(s)); err == nil || k != movie {
				t.Errorf("Key.UnmarshalText(%q) = %v, key %q; want a refusal leaving the key", s, err, k)
			}
		}
		for _, s := range []string{"", "tvdb-121-s01e01", "tvdb-0121"} {
			r := movie.Root()
			if err := r.UnmarshalText([]byte(s)); err == nil || r != movie.Root() {
				t.Errorf("RootKey.UnmarshalText(%q) = %v, key %q; want a refusal leaving the key", s, err, r)
			}
		}
		for _, s := range []string{"", "Episode", "series", "0"} {
			k := Movie
			if err := k.UnmarshalText([]byte(s)); err == nil || k != Movie {
				t.Errorf("Kind.UnmarshalText(%q) = %v; want a refusal", s, err)
			}
		}
		for _, s := range []string{"", "IMDb", "anidb"} {
			sc := TVDB
			if err := sc.UnmarshalText([]byte(s)); err == nil || sc != TVDB {
				t.Errorf("Scheme.UnmarshalText(%q) = %v; want a refusal", s, err)
			}
		}
	})
	t.Run("an enum refusal never echoes its input", func(t *testing.T) {
		long := []byte(strings.Repeat("x", 1<<16))
		var k Kind
		var sc Scheme
		for _, err := range []error{k.UnmarshalText(long), sc.UnmarshalText(long)} {
			if err == nil || len(err.Error()) > 64 {
				t.Errorf("UnmarshalText(64 KiB) = %d-byte error; want a short refusal", len(fmt.Sprint(err)))
			}
		}
	})
	t.Run("values with no text refuse to marshal", func(t *testing.T) {
		if b, err := (Key{}).MarshalText(); err == nil {
			t.Errorf("zero Key marshalled as %q", b)
		}
		if b, err := (RootKey{}).MarshalText(); err == nil {
			t.Errorf("zero RootKey marshalled as %q", b)
		}
		for _, k := range []Kind{0, Movie + 1} {
			if b, err := k.MarshalText(); err == nil {
				t.Errorf("Kind %d marshalled as %q", k, b)
			}
		}
		if b, err := (IMDb + 1).MarshalText(); err == nil {
			t.Errorf("Scheme %d marshalled as %q", IMDb+1, b)
		}
	})
}
