package mediasource_test

import (
	"testing"

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
}
