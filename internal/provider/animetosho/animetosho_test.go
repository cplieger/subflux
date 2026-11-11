package animetosho_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
	"github.com/cplieger/subflux/internal/provider/animetosho"
	"github.com/cplieger/subflux/internal/provider/archive"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/rank"
)

// frieren is AniDB's Sousou no Frieren on its TVDB series.
const (
	frieren       = 17617
	frierenSeries = 424536
)

func TestContract(t *testing.T) {
	q := episodeQuery(t, 0, 1, 5)
	q.Titles = []string{"Sousou no Frieren"}
	providertest.Contract(t, providertest.Harness{
		Entry:    animetosho.NewEntry(animemap.New()),
		Query:    q,
		Recorded: recorded(t),
	})
}

// TestAnimeToshoFixtureNet runs the three modes against the recorded .net
// answers (testdata: the lists keep the entries whose detail was recorded).
func TestAnimeToshoFixtureNet(t *testing.T) {
	t.Run("eid", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			d := doc(mapread.Anime{
				AniDBID: frieren, Type: "TV", TVDBID: frierenSeries, Last: mapread.Last{Special: 23}, EpisodesListed: true,
				Episodes: []mapread.Run{{Kind: mapread.Special, Start: 23, End: 23, AniDBEpisodeIDs: []int{276544}, TVDB: targets(at(0, 23))}},
			})
			r := newRig(t, newGitHub(t, "v2026.11.01", d), recorded(t))
			got := r.search(t, episodeQuery(t, frierenSeries, 0, 23))
			if q := r.at.queries(); !slices.Equal(q, []string{"eid=276544"}) {
				t.Errorf("queries %q; want eid=276544", q)
			}
			if len(got) != 1 || got[0].MatchedBy != rank.ID || got[0].Unconfirmed ||
				!slices.Equal(got[0].Covers, []media.Numbering{{Scheme: media.TVDB, Season: 0, Episode: 23}}) ||
				!strings.HasPrefix(got[0].Release, "[Piyoko] Sousou no Frieren - Marumaru no Mahou (Mini Anime) - 23") {
				t.Errorf("candidates %+v; want entry 598481's one subtitle on S00E23", got)
			}
		})
	})
	t.Run("aid", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// No AniDB episode list: regular 5 is ListingUnknown, so ?aid=.
			d := doc(mapread.Anime{
				AniDBID: frieren, Type: "TV", TVDBID: frierenSeries, Last: mapread.Last{Regular: 28},
				Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 28, TVDB: targets(at(1, 1))}},
			})
			r := newRig(t, newGitHub(t, "v2026.11.01", d), recorded(t))
			got := r.search(t, episodeQuery(t, frierenSeries, 1, 5))
			if q := r.at.queries(); !slices.Equal(q, []string{"aid=17617"}) {
				t.Errorf("queries %q; want aid=17617", q)
			}
			// The answer files season 2's releases (619131, 603482) under this
			// anime: their S02E05 files are not S01E05.
			want := []string{
				"[BlackRabbit] Frieren - Beyond Journey's End (2023) - S01 [Bluray-1080p][Opus 2.0][Dual Audio][AV1]",
				"[BlackRabbit] Frieren - Beyond Journey's End (2023) - S01 v2 [Bluray-1080p][Opus 2.0][Dual Audio][AV1]",
			}
			if len(got) != 4 {
				t.Fatalf("candidates %v; want the two S01E05 files' two subtitles each", placed(got))
			}
			for i := range got {
				c := &got[i]
				if !strings.Contains(fileOf(c), "S01E05") || !slices.Contains(want, c.Release) ||
					!slices.Equal(c.Covers, []media.Numbering{{Scheme: media.TVDB, Season: 1, Episode: 5}}) || c.MatchedBy != rank.ID {
					t.Errorf("candidate %s %q covers %v", fileOf(c), c.Release, c.Covers)
				}
			}
		})
	})
	t.Run("title", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, newGitHub(t, "v2026.11.01", doc()), recorded(t))
			q := episodeQuery(t, 0, 1, 5)
			q.Titles = []string{"Sousou no Frieren"}
			got := r.search(t, q)
			if q := r.at.queries(); !slices.Equal(q, []string{"q=Sousou+no+Frieren+S01"}) {
				t.Errorf("queries %q; want the title", q)
			}
			if len(got) != 4 {
				t.Fatalf("candidates %v; want 687355's and 619034's S01E05 subtitles", placed(got))
			}
			for i := range got {
				if c := &got[i]; !strings.Contains(fileOf(c), "S01E05") || c.MatchedBy != rank.Title || c.Covers != nil {
					t.Errorf("candidate %s matched %v covers %v", fileOf(c), c.MatchedBy, c.Covers)
				}
			}
			if n := len(r.gh.requests()); n != 0 {
				t.Errorf("a query with no TVDB series asked GitHub %d times", n)
			}
		})
	})
	t.Run("storage link on another host", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newTosho()
			f.entry("q=Show S01", 7, "[Grp] Show - 05", "[Grp] Show - 05.mkv")
			f.details[7] = strings.Replace(f.details[7], storageHost, "storage.animetosho.example", 1)
			r := newRig(t, newGitHub(t, "v2026.11.01", doc()), f)
			q := episodeQuery(t, 0, 1, 5)
			if got := r.search(t, q); len(got) != 0 {
				t.Errorf("candidates %v; want none", placed(got))
			}
			before := len(f.reqs)
			if _, err := r.a.Fetch(r.ctx, provider.NewRef("https://storage.animetosho.example/attachments/1/a.xz")); err == nil {
				t.Error("a link on another host fetched")
			}
			if len(f.reqs) != before {
				t.Error("a link on another host reached the fake")
			}
		})
	})
}

func TestFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTosho()
		r := newRig(t, newGitHub(t, "v2026.11.01", doc()), f)
		link := "https://" + storageHost + "/attachments/002ad/92f.xz?filename=Show+-+05_track3.eng.ass.xz"
		p, err := r.a.Fetch(r.ctx, provider.NewRef(link))
		if err != nil {
			t.Fatal(err)
		}
		text, err := archive.Decompress(p)
		if !bytes.HasPrefix(p, []byte("\xFD7zXZ\x00")) || err != nil ||
			!strings.Contains(string(text), "attachment /attachments/002ad/92f.xz") {
			t.Errorf("payload % x, decompressed %q, %v", p[:min(len(p), 8)], text, err)
		}
		for _, tok := range []string{"", "x", "http://" + storageHost + "/a.xz", "https://feed.animetosho.net/a.xz", "https://u:p@" + storageHost + "/a.xz"} {
			if _, err := r.a.Fetch(r.ctx, provider.NewRef(tok)); err == nil {
				t.Errorf("token %q fetched", tok)
			}
		}
		if len(f.reqs) != 1 {
			t.Errorf("requests %q, want one", f.reqs)
		}
	})
}

func TestSearchSkips(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTosho()
		r := newRig(t, newGitHub(t, "v2026.11.01", doc()), f)
		untitled := episodeQuery(t, 0, 1, 1)
		untitled.Titles = nil
		other := episodeQuery(t, 0, 1, 1)
		other.Kind = media.Kind(99)
		for _, q := range []provider.Query{untitled, other} {
			if _, err := r.a.Search(r.ctx, q); !errors.As(err, new(*provider.Skipped)) {
				t.Errorf("%v: %v, want *Skipped", q.Kind, err)
			}
		}
		if len(f.reqs) != 0 {
			t.Errorf("requests %q, want none", f.reqs)
		}
	})
}

// TestFailedEntry: a rate limit on an entry ends the search; another
// failure comes back beside the other entries' candidates.
func TestFailedEntry(t *testing.T) {
	for status, kind := range map[int]provider.FailureKind{http.StatusTooManyRequests: provider.RateLimited, http.StatusNotFound: provider.NotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTosho()
				f.entry("q=Show S01", 11, "[Grp] Show - 05", "[Grp] Show - 05.mkv")
				f.entry("q=Show S01", 12, "[Grp] Show - 05 [720p]", "[Grp] Show - 05 [720p].mkv")
				r := newRig(t, newGitHub(t, "v2026.11.01", doc()), f)
				srv := providertest.Serve(t, r.ctx, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.URL.Query().Get("id") == "12" {
						w.WriteHeader(status)
						return
					}
					f.ServeHTTP(w, req)
				}), feedHost, storageHost)
				got, err := r.a.Search(srv, episodeQuery(t, 0, 1, 5))
				if fl, ok := errors.AsType[*provider.Failure](err); !ok || fl.Kind != kind {
					t.Errorf("%v, want %v", err, kind)
				}
				if kind == provider.NotFound && len(got) != 1 {
					t.Errorf("%d candidates, want entry 11's", len(got))
				}
			})
		})
	}
}

// TestTitleSearchFiles chooses a title answer's files through the claim
// reader.
func TestTitleSearchFiles(t *testing.T) {
	type entry struct {
		files []string
		want  []string
	}
	ep := func(s, e, abs int) provider.Query {
		q := episodeQuery(t, 0, s, e)
		q.Absolute = abs
		return q
	}
	movie := episodeQuery(t, 0, 0, 0)
	movie.Kind, movie.Numbering = media.Movie, media.Numbering{}
	tests := []struct {
		name  string
		q     provider.Query
		entry entry
	}{
		{"a single file whatever its name", ep(2, 1, 26), entry{[]string{"pack disc a.mkv"}, []string{"pack disc a.mkv"}}},
		{"a batch by absolute number", ep(2, 1, 26), entry{[]string{"[Grp] Show - 25 [1080p].mkv", "[Grp] Show - 26 [1080p].mkv", "[Grp] Show - 27 [1080p].mkv"}, []string{"[Grp] Show - 26 [1080p].mkv"}}},
		{"a batch with no absolute", ep(2, 1, 0), entry{[]string{"[Grp] Show - 25 [1080p].mkv", "[Grp] Show - 26 [1080p].mkv", "[Grp] Show - 27 [1080p].mkv"}, nil}},
		{"a marked pack", ep(1, 2, 0), entry{[]string{"Show S01E01.mkv", "Show S01E02.mkv", "Show S01E03.mkv"}, []string{"Show S01E02.mkv"}}},
		{"an episode number", ep(2, 1, 26), entry{[]string{"Show - 01.mkv", "Show - 02.mkv"}, []string{"Show - 01.mkv"}}},
		{"a marker shape reads no absolute", ep(2, 1, 26), entry{[]string{"Show S01E01 - 26.mkv", "Show S01E02 - 27.mkv"}, nil}},
		{"one numbering per batch", ep(2, 1, 26), entry{numbered("[Grp] Show - %02d [1080p].mkv", 1, 26), []string{"[Grp] Show - 26 [1080p].mkv"}}},
		{"a season pack in its own numbering", ep(2, 1, 26), entry{numbered("[Grp] Show 2nd Season - %02d.mkv", 1, 12), []string{"[Grp] Show 2nd Season - 01.mkv"}}},
		{"no bare leading number on the title path", ep(2, 1, 26), entry{[]string{"01 - Title.mkv", "26 - Title.mkv"}, nil}},
		{"a movie keeps what claims no episode", movie, entry{[]string{"Show Movie 02 [BD 1080p].mkv", "Show Movie NCED [BD 1080p].mkv"}, []string{"Show Movie 02 [BD 1080p].mkv"}}},
		{"a pack claiming nothing", ep(1, 2, 0), entry{[]string{"pack disc a.mkv", "pack disc b.mkv"}, nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := capture.Default(t)
				f := newTosho()
				query := "q=Show"
				if tt.q.Kind == media.Episode {
					query += " S" + pad(tt.q.Numbering.Season)
				}
				f.entry(query, 1, "[Grp] Show", tt.entry.files...)
				r := newRig(t, newGitHub(t, "v2026.11.01", doc()), f)
				var got []string
				for _, c := range r.search(t, tt.q) {
					got = append(got, fileOf(&c))
				}
				slices.Sort(got)
				if !slices.Equal(got, tt.entry.want) {
					t.Errorf("kept %q; want %q", got, tt.entry.want)
				}
				dropped := len(tt.entry.files) > 1 && tt.entry.want == nil
				if n := rec.CountLevel(slog.LevelDebug, "no file of the entry claims the episode"); n != boolInt(dropped) {
					t.Errorf("%d DEBUG lines for a dropped entry; want %d", n, boolInt(dropped))
				}
				if dropped && !rec.HasAttr("no file of the entry claims the episode", "files", itoa(len(tt.entry.files))) {
					t.Errorf("DEBUG line names no file count: %q", rec.Messages())
				}
			})
		})
	}
}

// creditToken is a whole token carrying a credit notation, as the recorded
// names write it ("NCOP01", "NCED1b", "SPNCED").
var creditToken = regexp.MustCompile(`(^|[^A-Za-z0-9])(SP)?NC(OP|ED)[0-9]*[a-z]?([^A-Za-z0-9]|$)`)

// TestNameKindLabels reads the credit names of the recorded .net answers:
// each NC file is a credit, with no number taken from its notation, and no
// recorded name carries its only episode number inside brackets.
func TestNameKindLabels(t *testing.T) {
	f := recorded(t)
	credits := 0
	for id, d := range f.details {
		for _, name := range fileNamesOf(t, d) {
			c := animemap.ReadEntry([]string{name}, false)[0]
			if creditToken.MatchString(name) {
				credits++
				if c.Kind != mapread.Credit || c.Episodes != nil {
					t.Errorf("%d %q: %+v; want a credit with no number", id, name, c)
				}
			}
			if c.Step == animemap.NoClaim && bracketedOnly(name) {
				t.Errorf("%d %q states its episode only inside brackets", id, name)
			}
		}
	}
	if credits != 10 {
		t.Errorf("%d credit names; the capture holds 10", credits)
	}
}

func TestAttachmentLanguage(t *testing.T) {
	tests := []struct {
		code, name string
		want       string // "" dropped
	}{
		{"eng", "English", "en"},
		{"", "Signs", "en"},
		{"jpn", "Japanese", "ja"},
		{"por", "Portuguese (Brazil)", "pt-BR"},
		{"spa", "Spanish (Latin America)", "es-419"},
		{"und", "Unknown", ""},
		{"xx-bogus", "?", ""},
	}
	for _, tt := range tests {
		t.Run(tt.code+" "+tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTosho()
				f.entry("q=Show S01", 1, "[Grp] Show - 05", "[Grp] Show - 05.mkv")
				f.details[1] = strings.Replace(strings.Replace(f.details[1], `"eng"`, `"`+tt.code+`"`, 1), `"English"`, `"`+tt.name+`"`, 1)
				r := newRig(t, newGitHub(t, "v2026.11.01", doc()), f)
				q := episodeQuery(t, 0, 1, 5)
				q.Langs = allTags()
				got := r.search(t, q)
				switch {
				case tt.want == "" && len(got) != 0:
					t.Errorf("candidates in %v; want none", got[0].Lang)
				case tt.want != "" && (len(got) != 1 || got[0].Lang != tag(t, tt.want)):
					t.Errorf("candidates %+v; want one in %s", got, tt.want)
				}
			})
		})
	}
}

// TestEntriesRead: every entry AnimeTosho did not skip is read, an entry
// whose processing status is unknown included, since such entries carry
// attachments (the recorded 494892); an attachment two entries share is
// offered once.
func TestEntriesRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTosho()
		f.entry("q=Show S01", 1, "[A] Show - 05", "[A] Show - 05.mkv")
		f.entry("q=Show S01", 2, "[B] Show - 05", "[B] Show - 05.mkv")
		f.entry("q=Show S01", 3, "[C] Show - 05", "[C] Show - 05.mkv")
		f.entry("q=Show S01", 4, "[D] Show - 05", "[A] Show - 05.mkv")
		f.lists["q=Show S01"] = strings.Replace(strings.Replace(f.lists["q=Show S01"], `"id":2,"status":"complete"`, `"id":2,"status":"unknown"`, 1),
			`"id":3,"status":"complete"`, `"id":3,"status":"skipped"`, 1)
		f.details[4] = strings.ReplaceAll(f.details[4], "/attachments/4/0.xz", "/attachments/1/0.xz")
		r := newRig(t, newGitHub(t, "v2026.11.01", doc()), f)
		var got []string
		for _, c := range r.search(t, episodeQuery(t, 0, 1, 5)) {
			got = append(got, c.Release)
		}
		if want := []string{"[A] Show - 05", "[B] Show - 05"}; !slices.Equal(got, want) {
			t.Errorf("releases %q; want %q", got, want)
		}
	})
}
