package provider

import (
	"os"
	"strings"
	"testing"
)

// The pattern cases are Google's published examples
// (https://developers.google.com/search/docs/crawling-indexing/robots/robots_txt#url-matching-based-on-path-values)
// and the percent-encoding ones RFC 9309 2.2.2 and 2.2.3's figures 4 and 6.
func TestRobotsMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		match   []string
		miss    []string
	}{
		{"/fish", []string{"/fish", "/fish.html", "/fish/salmon.html", "/fishheads", "/fishheads/yummy.html", "/fish.php?id=anything"}, []string{"/Fish.asp", "/catfish", "/?id=fish", "/desert/fish"}},
		{"/fish*", []string{"/fish", "/fish.html", "/fish/salmon.html", "/fishheads"}, []string{"/Fish.asp", "/catfish"}},
		{"/fish/", []string{"/fish/", "/fish/?id=anything", "/fish/salmon.htm"}, []string{"/fish", "/fish.html", "/animals/fish/", "/Fish/Salmon.asp"}},
		{"/*.php", []string{"/index.php", "/filename.php", "/folder/filename.php", "/folder/filename.php?parameters", "/folder/any.php.file.html", "/filename.php/"}, []string{"/", "/windows.PHP"}},
		{"/*.php$", []string{"/filename.php", "/folder/filename.php"}, []string{"/filename.php?parameters", "/filename.php/", "/filename.php5", "/windows.PHP"}},
		{"/fish*.php", []string{"/fish.php", "/fishheads/catfish.php?parameters"}, []string{"/Fish.PHP"}},
		{"/foo/bar/%62%61%7A", []string{"/foo/bar/baz"}, nil},
		{"/foo/bar/ツ", []string{"/foo/bar/%E3%83%84", "/foo/bar/%e3%83%84"}, nil},
		{"/path/file-with-a-%2A.html", []string{"/path/file-with-a-*.html"}, []string{"/path/file-with-a-x.html"}},
		{"/path/foo-%24", []string{"/path/foo-$"}, []string{"/path/foo-"}},
	} {
		p := normalize(tc.pattern, false)
		for _, path := range tc.match {
			if !robotsMatch(p, normalize(path, true)) {
				t.Errorf("%q does not match %q", tc.pattern, path)
			}
		}
		for _, path := range tc.miss {
			if robotsMatch(p, normalize(path, true)) {
				t.Errorf("%q matches %q", tc.pattern, path)
			}
		}
	}
}

func TestParseRobots(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		allowed    []string
		refused    []string
	}{
		{
			name:    "the longest match wins, allow a tie",
			body:    "User-agent: *\nAllow: /p\nDisallow: /\nAllow: /folder\nDisallow: /folder\nAllow: /page\nDisallow: /*.htm\n",
			allowed: []string{"/page", "/folder/page"},
			refused: []string{"/page.htm", "/other"},
		},
		{
			name:    "allow wins a tie whichever rule comes first",
			body:    "User-agent: *\nDisallow: /folder\nAllow: /folder\n",
			allowed: []string{"/folder/page"},
		},
		{
			name:    "an anchored allow",
			body:    "user-agent: *\nallow: /$\ndisallow: /\n",
			allowed: []string{"/"},
			refused: []string{"/page.htm"},
		},
		{
			name:    "only the star group applies to an unnamed agent (RFC 9309 figure 3)",
			body:    "user-agent: *\ndisallow: /foo\ndisallow: /bar\n\nuser-agent: BazBot\ndisallow: /baz\n",
			allowed: []string{"/baz"},
			refused: []string{"/foo", "/bar/x"},
		},
		{
			name:    "subflux's groups merge and override the star group",
			body:    "User-agent: *\nDisallow: /\n\nUser-agent: SUBFLUX/2.0\nDisallow: /a\n\nUser-agent: other\nUser-agent: subflux\nDisallow: /b # comment\n",
			allowed: []string{"/c"},
			refused: []string{"/a", "/b"},
		},
		{
			name:    "rules outside a group and other records are ignored",
			body:    "Disallow: /\nSitemap: https://example.test/s.xml\nUser-agent: *\nCrawl-delay: 5\nDISALLOW: /x\r\nDisallow:\n",
			allowed: []string{"/", "/y"},
			refused: []string{"/x"},
		},
		{
			name:    "robots.txt itself is always allowed",
			body:    "User-agent: *\nDisallow: /\n",
			allowed: []string{"/robots.txt"},
			refused: []string{"/robots.txt.bak"},
		},
		{
			name:    "no group allows everything",
			body:    "# nothing\n",
			allowed: []string{"/", "/x"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := parseRobots([]byte(tc.body))
			for _, u := range tc.allowed {
				if !rs.allowed(u) {
					t.Errorf("%q refused", u)
				}
			}
			for _, u := range tc.refused {
				if rs.allowed(u) {
					t.Errorf("%q allowed", u)
				}
			}
		})
	}
}

// FuzzParseRobots_otherGroupIgnored: a group for another agent placed before
// any file never changes a verdict, and robots.txt stays allowed.
func FuzzParseRobots_otherGroupIgnored(f *testing.F) {
	recorded, err := os.ReadFile("yifysubtitles/testdata/robots.txt")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(string(recorded), "/movie-imdb/tt1375666")
	f.Add("User-agent: *\nDisallow: /*.php$\nAllow: /a%2Ab\n", "/x.php")
	f.Add("user-agent: subflux\nallow: /ツ\ndisallow: /\n", "/%E3%83%84?q=1")
	f.Fuzz(func(t *testing.T, body, uri string) {
		uri = "/" + strings.TrimPrefix(uri, "/")
		before := parseRobots([]byte(body)).allowed(uri)
		after := parseRobots([]byte("User-agent: otherbot\nDisallow: /\n\n" + body)).allowed(uri)
		if before != after {
			t.Errorf("another agent's group changed %q from %v to %v", uri, before, after)
		}
		if !parseRobots([]byte(body)).allowed("/robots.txt") {
			t.Error("robots.txt refused")
		}
	})
}
