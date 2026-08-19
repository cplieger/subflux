package search

import (
	"testing"

	"github.com/cplieger/keyenc"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/provider"
)

// TestBuildSearchKeyCannotBeForged pins the property buildSearchKey exists to
// hold: two DIFFERENT sweep requests must never produce one singleflight key.
// A collision here is not a cache miss but a wrong answer — the two callers
// share one flight and the second receives the first request's subtitle results
// for its own video, writing them next to the wrong file.
//
// The naive '|'-joined form this replaced was forgeable through the middle of
// the key, where the media id, the language list and the video path sit
// adjacent and any of the three can carry a separator: a configured language
// code containing '|' or ',' (config.yaml language codes are checked only for
// non-emptiness) or a malformed arr media id shifts the split. Each case below
// is a pair that the naive form collapsed.
func TestBuildSearchKeyCannotBeForged(t *testing.T) {
	providers := []provider.Provider{&mockProvider{name: "prov1"}}

	req := func(mediaID string, langs []string, path, hash string) *api.SearchRequest {
		return &api.SearchRequest{
			MediaType: api.MediaTypeEpisode,
			ImdbID:    mediaID,
			Languages: langs,
			VideoPath: path,
			VideoHash: hash,
		}
	}

	cases := map[string][2]*api.SearchRequest{
		"separator moves from the language list into the video path": {
			req("tt1", []string{"fr"}, "|x/a.mkv", "h"),
			req("tt1", []string{"fr|"}, "x/a.mkv", "h"),
		},
		"list separator merges two language codes": {
			req("tt1", []string{"fr", "en"}, "p", "h"),
			req("tt1", []string{"fr,en"}, "p", "h"),
		},
		"separator moves from the media id into the language list": {
			req("tt1|fr", nil, "p", "h"),
			req("tt1", []string{"fr"}, "p", "h"),
		},
		"separator moves between the path and the hash": {
			req("tt1", []string{"fr"}, "a|b", "h"),
			req("tt1", []string{"fr"}, "a", "b|h"),
		},
		"escape character does not shift the split": {
			req("tt1", []string{"fr"}, `x\`, "h"),
			req("tt1", []string{"fr"}, "x", `\h`),
		},
	}

	for name, pair := range cases {
		t.Run(name, func(t *testing.T) {
			left := buildSearchKey(pair[0], providers)
			right := buildSearchKey(pair[1], providers)
			if left == right {
				t.Errorf("distinct sweep requests share the singleflight key %q", left)
			}
		})
	}
}

// TestBuildSearchKeyIsStableAndUnescapedForOrdinaryInput pins that adopting
// keyenc did not change the key for the inputs the sweep actually sees: no
// ordinary field carries a reserved character, so the key stays the plain
// separator-joined form and remains readable in a log line. It also pins the
// sort that makes the key order-independent.
func TestBuildSearchKeyIsStableAndUnescapedForOrdinaryInput(t *testing.T) {
	providers := []provider.Provider{&mockProvider{name: "b"}, &mockProvider{name: "a"}}
	base := &api.SearchRequest{
		MediaType: api.MediaTypeEpisode,
		ImdbID:    "tt0903747",
		Season:    1,
		Episode:   2,
		Languages: []string{"fr", "en"},
		VideoPath: "/media/tv/show/s01e02.mkv",
		VideoHash: "0123456789abcdef",
	}
	got := buildSearchKey(base, providers)

	want := keyenc.Join(
		string(api.MediaTypeEpisode),
		mediaid.Build(base),
		"en:fr",
		"/media/tv/show/s01e02.mkv",
		"0123456789abcdef",
		"a:b",
	)
	if got != want {
		t.Errorf("buildSearchKey() = %q, want %q", got, want)
	}
	if keyenc.IsHashed(got) {
		t.Error("an ordinary sweep key must not be reduced to a hashed identity")
	}

	// Language and provider order must not change the key.
	reordered := *base
	reordered.Languages = []string{"en", "fr"}
	if other := buildSearchKey(&reordered, []provider.Provider{&mockProvider{name: "a"}, &mockProvider{name: "b"}}); other != got {
		t.Errorf("key depends on input order: %q vs %q", got, other)
	}
}

// TestBuildSearchKeyIsBounded pins that a hostile-length component cannot
// inflate the key: the sweep key embeds a filesystem path, and these keys index
// the singleflight map for the lifetime of a flight.
func TestBuildSearchKeyIsBounded(t *testing.T) {
	providers := []provider.Provider{&mockProvider{name: "prov1"}}
	huge := make([]byte, keyenc.MaxComponentBytes+1)
	for i := range huge {
		huge[i] = 'x'
	}
	key := buildSearchKey(&api.SearchRequest{
		MediaType: api.MediaTypeEpisode,
		ImdbID:    "tt1",
		VideoPath: string(huge),
	}, providers)
	if !keyenc.IsHashed(key) {
		t.Errorf("an oversized component must reduce the key to a hashed identity, got %d bytes", len(key))
	}
}
