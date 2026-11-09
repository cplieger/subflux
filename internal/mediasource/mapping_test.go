package mediasource_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

func TestMapping(t *testing.T) {
	cfg := mediasource.Config{PathMappings: []mediasource.PathMapping{
		{From: "/data", To: "/m1"},
		{From: "/data/tv", To: "/m2"},
		{From: "/arr", To: "/media"},
	}}
	set, _ := oneSet(t, &script{caps: allCaps}, cfg)
	cases := map[string]string{
		"/data/tv/a.mkv":    "/m2/a.mkv",
		"/data/tv":          "/m2",
		"/data/tvx/a.mkv":   "/m1/tvx/a.mkv",
		"/data/..foo/a.mkv": "/m1/..foo/a.mkv",
		"/other/a.mkv":      "/other/a.mkv",
	}
	for in, want := range cases {
		if got, err := set.LocalPath("src", in); err != nil || got != want {
			t.Errorf("LocalPath(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"/media/x.mkv", "/media", "media/x.mkv", "/data/../x"} {
		if got, err := set.LocalPath("src", in); err == nil || causeOf(t, err) != mediasource.Protocol {
			t.Errorf("LocalPath(%q) = %q %v, want protocol", in, got, err)
		}
	}
	_, err := set.LocalPath("src", "/media/x.mkv")
	if ae, ok := errors.AsType[*mediasource.AliasedPathError](err); !ok || ae.Path != "/media/x.mkv" || ae.To != "/media" {
		t.Errorf("LocalPath(/media/x.mkv) = %v, want an AliasedPathError naming the path and /media", err)
	}
}

func TestLocalPath(t *testing.T) {
	fixtures := map[string][]mediasource.PathMapping{
		"one mapping": {{From: "/tv", To: "/media/tv"}},
		"nested":      {{From: "/data", To: "/m1"}, {From: "/data/tv", To: "/m2"}, {From: "/arr", To: "/media"}},
	}
	remotes := []string{
		"/tv/a.mkv", "/tv2/a.mkv", "/tv", "/data/tv/x.mkv", "/data/tvx/x.mkv", "/data/..foo/x.mkv",
		"/other/x.mkv", "/media/x.mkv", "/media/tv/x.mkv", "tv/a.mkv", "/tv/../a.mkv", "/tv/a.mkv\n",
	}
	for name, maps := range fixtures {
		t.Run(name, func(t *testing.T) {
			var remote string
			s := &script{caps: allCaps, items: func(_ context.Context, y func(mediasource.Root, []mediasource.Item) error, _ mediasource.Unread) error {
				return y(mediasource.Root{Native: mediasource.NativeRef{ID: "1", Kind: media.Movie}, Title: "M", External: media.ExternalIDs{TMDB: 1}},
					[]mediasource.Item{{Native: "1", VideoPath: remote, External: media.ExternalIDs{TMDB: 1}}})
			}}
			set, _ := oneSet(t, s, mediasource.Config{PathMappings: maps})
			for _, remote = range remotes {
				var mapped string
				var unread error
				err := set.Items(t.Context(), "src", func(_ mediasource.Root, items []mediasource.Item) error {
					mapped = items[0].VideoPath
					return nil
				}, func(_ mediasource.NativeRef, se *mediasource.SourceError) error {
					unread = se
					return nil
				})
				if err != nil {
					t.Fatalf("Items over %q = %v", remote, err)
				}
				got, lerr := set.LocalPath("src", remote)
				switch {
				case unread == nil && (lerr != nil || got != mapped):
					t.Errorf("LocalPath(%q) = %q %v, want the Set's video mapping %q", remote, got, lerr, mapped)
				case unread != nil && lerr == nil:
					t.Errorf("LocalPath(%q) = %q, want a refusal as the Set's %v", remote, got, unread)
				case unread != nil && causeOf(t, lerr) != mediasource.Protocol:
					t.Errorf("LocalPath(%q) = %v, want Protocol", remote, lerr)
				case unread != nil && errors.As(unread, new(*mediasource.AliasedPathError)) != errors.As(lerr, new(*mediasource.AliasedPathError)):
					t.Errorf("LocalPath(%q) = %v, the Set refused %v: they disagree on the aliased path", remote, lerr, unread)
				}
			}
		})
	}
	t.Run("a sibling of a mapped folder passes through", func(t *testing.T) {
		set, _ := oneSet(t, &script{caps: allCaps}, mediasource.Config{PathMappings: fixtures["one mapping"]})
		if got, err := set.LocalPath("src", "/tv2/a.mkv"); err != nil || got != "/tv2/a.mkv" {
			t.Errorf("LocalPath(/tv2/a.mkv) under a /tv mapping = %q %v, want it unmapped", got, err)
		}
	})
	t.Run("an unknown source", func(t *testing.T) {
		set, _ := oneSet(t, &script{caps: allCaps}, mediasource.Config{})
		if _, err := set.LocalPath("nope", "/tv"); causeOf(t, err) != mediasource.UnknownSource {
			t.Errorf("LocalPath for an unknown source = %v, want UnknownSource", err)
		}
	})
}
