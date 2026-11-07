package provider_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
)

func TestPayloadName(t *testing.T) {
	cases := []struct {
		disposition, url, want string
	}{
		{`attachment; filename=Show.S01E01.en.srt; filename*=UTF-8''Show.S01E01.en.srt`, "/subtitles/download/1", "Show.S01E01.en.srt"},
		{`attachment; filename*=UTF-8''SUBDL%3A%3Ainception-2714566.zip`, "/subtitle/1.zip", "SUBDL::inception-2714566.zip"},
		{`attachment; filename="../../etc/passwd"`, "/x", "passwd"},
		{`attachment; filename="a\\b.srt"`, "/x", "b.srt"},
		{"", "/opensubtitles/breaking.bad.s01.e01.zip", "breaking.bad.s01.e01.zip"},
		{"", "/", ""},
		{`attachment; filename="` + strings.Repeat("n", 600) + `"`, "/x", strings.Repeat("n", provider.MaxText)},
		{"not a disposition;;", "/file.ass", "file.ass"},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+tc.url, http.NoBody)
		resp := &http.Response{Header: http.Header{}, Request: req}
		if tc.disposition != "" {
			resp.Header.Set("Content-Disposition", tc.disposition)
		}
		if got := provider.PayloadName(resp); got != tc.want {
			t.Errorf("PayloadName(%q, %q) = %q, want %q", tc.disposition, tc.url, got, tc.want)
		}
	}
}

func TestWirePairs(t *testing.T) {
	m := language.MustProviderMap(provider.WirePairs("English", "en", "Brazilian", "pt-BR"))
	if tag, ok := m.Tag("Brazilian"); !ok || tag.String() != "pt-BR" {
		t.Errorf("Brazilian = %v %v", tag, ok)
	}
	for _, bad := range [][]string{{"English"}, {"Nothing", "xx-not-a-tag-at-all"}, {"Empty", ""}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("WirePairs(%q) did not panic", bad)
				}
			}()
			provider.WirePairs(bad...)
		}()
	}
}
