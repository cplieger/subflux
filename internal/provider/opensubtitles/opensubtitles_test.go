package opensubtitles_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/opensubtitles"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/rank"
)

const (
	apiHost  = "api.opensubtitles.com"
	vipHost  = "vip-api.opensubtitles.com"
	linkHost = "www.opensubtitles.com"
	key      = "os-api-key-0123456789"
	token    = "token-0123456789"
	values   = "username: user-1\npassword: os-password-0123\napi_key: " + key
	srtFile  = "testdata/Inception.2010.1080p.WEB-DLx264.fr.srt"
)

var hosts = []string{apiHost, vipHost, linkHost}

func tag(t *testing.T, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("tag %q", s)
	}
	return tg
}

func tags(t *testing.T, ss ...string) []language.Tag {
	var out []language.Tag
	for _, s := range ss {
		out = append(out, tag(t, s))
	}
	return out
}

// recorded serves the recorded answers: the login (VIP, base_url
// vip-api), Inception's two pages by TMDB id, Breaking Bad S01E01 by series
// IMDb id, a download link and its file. login and search, when set, replace
// the recorded login body and the search answer.
type recorded struct {
	login  func(w http.ResponseWriter)
	search func(w http.ResponseWriter, r *http.Request) bool
	mu     sync.Mutex
	reqs   []string
	logins int
}

func (rec *recorded) log(r *http.Request) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	line := r.Method + " " + r.Host + r.URL.Path
	if r.URL.RawQuery != "" {
		line += "?" + r.URL.RawQuery
	}
	rec.reqs = append(rec.reqs, line)
	if r.URL.Path == "/api/v1/login" {
		rec.logins++
	}
}

func (rec *recorded) requests() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return slices.Clone(rec.reqs)
}

func serveFile(w http.ResponseWriter, file string) {
	b, err := os.ReadFile(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(b)
}

func (rec *recorded) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec.log(r)
	if r.Host == linkHost {
		if r.Header.Get("Api-Key") != "" || r.Header.Get("Authorization") != "" {
			http.Error(w, "credentials sent to the link host", http.StatusBadRequest)
			return
		}
		serveFile(w, srtFile)
		return
	}
	if r.Header.Get("Api-Key") != key {
		w.WriteHeader(http.StatusForbidden)
		serveFile(w, "testdata/login-403.json")
		return
	}
	if r.URL.Path == "/api/v1/login" {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method != http.MethodPost || r.Host != apiHost || body["username"] != "user-1" || body["password"] != "os-password-0123":
			http.Error(w, `{"message":"Error, invalid username/password"}`, http.StatusUnauthorized)
		case rec.login != nil:
			rec.login(w)
		default:
			serveFile(w, "testdata/login.json")
		}
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+token {
		http.Error(w, `{"message":"invalid token"}`, http.StatusUnauthorized)
		return
	}
	switch q := r.URL.Query(); {
	case r.URL.Path == "/api/v1/download" && r.Method == http.MethodPost && r.Host == apiHost:
		serveFile(w, "testdata/download.json")
	case r.URL.Path != "/api/v1/subtitles" || r.Method != http.MethodGet:
		http.NotFound(w, r)
	case rec.search != nil && rec.search(w, r):
	case q.Get("tmdb_id") == "27205" && q.Get("page") == "2":
		serveFile(w, "testdata/search-tmdb-27205-page-2.json")
	case q.Get("tmdb_id") == "27205":
		serveFile(w, "testdata/search-tmdb-27205-page-1.json")
	case q.Get("parent_imdb_id") == "903747" && q.Get("season_number") == "1" && q.Get("episode_number") == "1":
		serveFile(w, "testdata/search-imdb-903747-s01e01.json")
	default:
		_, _ = w.Write([]byte(`{"total_pages":0,"total_count":0,"data":[]}`))
	}
}

func movie(t *testing.T, langs ...string) provider.Query {
	return provider.Query{
		Kind: media.Movie, Year: 2010, Titles: []string{"Inception"},
		External: media.ExternalIDs{TMDB: 27205, IMDb: 1375666}, Langs: tags(t, langs...),
	}
}

func episode(t *testing.T, langs ...string) provider.Query {
	return provider.Query{
		Kind: media.Episode, Year: 2008, Titles: []string{"Breaking Bad"},
		External:  media.ExternalIDs{IMDb: 903747, TVDB: 81189},
		Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}, Langs: tags(t, langs...),
	}
}

func TestSearchMovieRecorded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorded{}
		ctx := providertest.Serve(t, t.Context(), rec, hosts...)
		got, err := providertest.Build(t, opensubtitles.NewEntry(), values).Search(ctx, movie(t, "fr", "en"))
		if err != nil {
			t.Fatal(err)
		}
		var en, fr, hi int
		for _, c := range got {
			switch c.Lang {
			case tag(t, "en"):
				en++
			case tag(t, "fr"):
				fr++
			}
			if c.Flags.HI {
				hi++
			}
			if c.MatchedBy != rank.ID || c.Title != "" {
				t.Errorf("candidate %+v, want an id match with no title", c)
			}
		}
		if en != 56 || fr != 15 || hi < 19 {
			t.Errorf("%d English, %d French, %d HI; want 56, 15 and at least the 19 the answers flag", en, fr, hi)
		}
		if got[0].Ref.Token() != "12835077" || got[0].Release != "Inception.2010.1080p.WEB-DLx264" || got[0].FPS != 0 {
			t.Errorf("first candidate %+v", got[0])
		}
		if i := slices.IndexFunc(got, func(c provider.Candidate) bool { return c.Ref.Token() == "87886" }); i < 0 || got[i].FPS != 23.976 {
			t.Errorf("page 2's file 87886 missing or not at its recorded 23.976 fps")
		}
		want := []string{
			"POST " + apiHost + "/api/v1/login",
			"GET " + vipHost + "/api/v1/subtitles?ai_translated=exclude&languages=en%2Cfr&tmdb_id=27205",
			"GET " + vipHost + "/api/v1/subtitles?ai_translated=exclude&languages=en%2Cfr&page=2&tmdb_id=27205",
		}
		if reqs := rec.requests(); !slices.Equal(reqs, want) {
			t.Errorf("requests\n%q\nwant\n%q", reqs, want)
		}
	})
}

func TestSearchEpisodeRecorded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorded{}
		ctx := providertest.Serve(t, t.Context(), rec, hosts...)
		got, err := providertest.Build(t, opensubtitles.NewEntry(), values).Search(ctx, episode(t, "zh-Hans", "pt-PT", "en"))
		if err != nil {
			t.Fatal(err)
		}
		count := map[string]int{}
		for _, c := range got {
			count[c.Lang.String()]++
		}
		if count["en"] != 16 || count["pt-PT"] != 8 || count["zh-Hans"] != 3 {
			t.Errorf("candidates per tag %v, want en 16, pt-PT 8, zh-Hans 3", count)
		}
		if reqs := rec.requests(); len(reqs) != 2 || !strings.HasSuffix(reqs[1], "?ai_translated=exclude&episode_number=1&languages=en%2Cpt-pt%2Czh-cn&parent_imdb_id=903747&season_number=1") {
			t.Errorf("requests %q", reqs)
		}
	})
}

// TestSearchAbsoluteNumbering: an episode with an absolute number is asked
// under it as season 1 too, each file once.
func TestSearchAbsoluteNumbering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorded{search: func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Query().Get("episode_number") == "8" {
				serveFile(w, "testdata/search-imdb-903747-s01e01.json")
				return true
			}
			return false
		}}
		ctx := providertest.Serve(t, t.Context(), rec, hosts...)
		q := episode(t, "en")
		q.Numbering = media.Numbering{Scheme: media.TVDB, Season: 2, Episode: 1}
		q.Absolute = 8
		got, err := providertest.Build(t, opensubtitles.NewEntry(), values).Search(ctx, q)
		if err != nil || len(got) != 16 {
			t.Fatalf("%d candidates, %v; want the 16 English files of the absolute answer", len(got), err)
		}
		reqs := rec.requests()
		if len(reqs) != 4 || !strings.Contains(reqs[1], "episode_number=1&") || !strings.Contains(reqs[1], "season_number=2") ||
			!strings.Contains(reqs[3], "episode_number=8&") || !strings.Contains(reqs[3], "season_number=1") {
			t.Errorf("requests %q: want S02E01 by id and title, then S01E08 by id", reqs)
		}
	})
}

// TestSearchFallsBackToTitle: an id finding nothing hands the search to the
// first title, whose candidates carry the feature's title and year.
func TestSearchFallsBackToTitle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorded{search: func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Query().Get("query") == "Inception" {
				serveFile(w, "testdata/search-tmdb-27205-page-2.json")
				return true
			}
			return false
		}}
		ctx := providertest.Serve(t, t.Context(), rec, hosts...)
		q := movie(t, "fr")
		q.External = media.ExternalIDs{IMDb: 1}
		got, err := providertest.Build(t, opensubtitles.NewEntry(), values).Search(ctx, q)
		if err != nil || len(got) != 6 {
			t.Fatalf("%d candidates, %v; want the 6 French ones", len(got), err)
		}
		for _, c := range got {
			if c.MatchedBy != rank.Title || c.Title != "Inception" || c.Year != 2010 {
				t.Errorf("candidate %+v, want a title match on Inception (2010)", c)
			}
		}
		reqs := rec.requests()
		if len(reqs) != 4 || !strings.Contains(reqs[1], "imdb_id=1") || !strings.Contains(reqs[2], "query=Inception") || strings.Contains(reqs[2], "imdb") {
			t.Errorf("requests %q, want the id search then the title search's two pages", reqs)
		}
		q.Titles = nil
		if got, err := providertest.Build(t, opensubtitles.NewEntry(), values).Search(ctx, q); err != nil || len(got) != 0 {
			t.Errorf("no title: %d candidates, %v; want none", len(got), err)
		}
	})
}

// TestSearchHash: the video's hash is asked when use_hash is on, and a file
// the answer says matches it is a hash match.
func TestSearchHash(t *testing.T) {
	answer := `{"total_pages":1,"data":[
		{"attributes":{"language":"en","release":"Inception.2010.1080p","moviehash_match":true,"files":[{"file_id":1}]}},
		{"attributes":{"language":"en","release":"Inception.2010.720p","files":[{"file_id":2}]}}]}`
	for _, tc := range []struct {
		values string
		hash   string
	}{
		{values, "moviehash=00000000000000ff"},
		{values + "\nuse_hash: false", ""},
	} {
		synctest.Test(t, func(t *testing.T) {
			var asked string
			rec := &recorded{search: func(w http.ResponseWriter, r *http.Request) bool {
				asked = r.URL.Query().Get("moviehash")
				_, _ = w.Write([]byte(answer))
				return true
			}}
			ctx := providertest.Serve(t, t.Context(), rec, hosts...)
			q := movie(t, "en")
			q.Hash = provider.VideoHash{Size: 1 << 30, OSHash: 0xff}
			got, err := providertest.Build(t, opensubtitles.NewEntry(), tc.values).Search(ctx, q)
			if err != nil || len(got) != 2 {
				t.Fatalf("%d candidates, %v", len(got), err)
			}
			if want := strings.TrimPrefix(tc.hash, "moviehash="); asked != want {
				t.Errorf("asked moviehash %q, want %q", asked, want)
			}
			if got[0].MatchedBy != rank.Hash || got[1].MatchedBy != rank.ID {
				t.Errorf("matched by %v and %v, want Hash and ID", got[0].MatchedBy, got[1].MatchedBy)
			}
		})
	}
}

// TestTranslationsOptIn: AI and machine translations are left out unless
// the user opts into their kind, and each opt-in asks only its non-default
// direction.
func TestTranslationsOptIn(t *testing.T) {
	answer := `{"total_pages":1,"data":[
		{"attributes":{"language":"en","release":"human","files":[{"file_id":1}]}},
		{"attributes":{"language":"en","release":"ai","ai_translated":true,"files":[{"file_id":2}]}},
		{"attributes":{"language":"en","release":"mt","machine_translated":true,"files":[{"file_id":3}]}},
		{"attributes":{"language":"en","release":"no file","files":[]}},
		{"attributes":{"language":"de","release":"not asked","files":[{"file_id":4}]}},
		{"attributes":{"language":"xx","release":"unknown label","files":[{"file_id":5}]}}]}`
	for _, tc := range []struct {
		values string
		query  string
		want   []string
	}{
		{values, "ai_translated=exclude&", []string{"human"}},
		{values + "\ninclude_ai_translated: true", "", []string{"human", "ai"}},
		{values + "\ninclude_machine_translated: true", "machine_translated=include", []string{"human", "mt"}},
	} {
		synctest.Test(t, func(t *testing.T) {
			var raw string
			rec := &recorded{search: func(w http.ResponseWriter, r *http.Request) bool {
				raw = r.URL.RawQuery
				_, _ = w.Write([]byte(answer))
				return true
			}}
			ctx := providertest.Serve(t, t.Context(), rec, hosts...)
			got, err := providertest.Build(t, opensubtitles.NewEntry(), tc.values).Search(ctx, movie(t, "en"))
			if err != nil {
				t.Fatal(err)
			}
			var rels []string
			for _, c := range got {
				rels = append(rels, c.Release)
			}
			if !slices.Equal(rels, tc.want) {
				t.Errorf("%q: kept %q, want %q", tc.values, rels, tc.want)
			}
			if tc.query != "" && !strings.Contains(raw, tc.query) || tc.query == "" && strings.Contains(raw, "translated") {
				t.Errorf("%q: asked %q", tc.values, raw)
			}
		})
	}
}

// TestOpenSubtitlesMachineTranslatedMarked reads two recorded answers:
// Inception in Albanian, whose file 7282844 is machine_translated, and
// Oppenheimer in Latvian, whose 5 files are all ai_translated.
func TestOpenSubtitlesMachineTranslatedMarked(t *testing.T) {
	const mt = "7282844"
	for _, tc := range []struct {
		name, values, file string
		tmdb               uint64
		lang               string
		kept, marked       int
	}{
		{"machine translations off", values, "testdata/search-tmdb-27205-sq-translations.json", 27205, "sq", 8, 0},
		{"machine translations on", values + "\ninclude_machine_translated: true", "testdata/search-tmdb-27205-sq-translations.json", 27205, "sq", 9, 1},
		{"AI translations off", values, "testdata/search-tmdb-872585-lv-translations.json", 872585, "lv", 0, 0},
		{"AI translations on", values + "\ninclude_ai_translated: true", "testdata/search-tmdb-872585-lv-translations.json", 872585, "lv", 5, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := &recorded{search: func(w http.ResponseWriter, _ *http.Request) bool {
					serveFile(w, tc.file)
					return true
				}}
				ctx := providertest.Serve(t, t.Context(), rec, hosts...)
				q := movie(t, tc.lang)
				q.External = media.ExternalIDs{TMDB: tc.tmdb}
				got, err := providertest.Build(t, opensubtitles.NewEntry(), tc.values).Search(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				marked := 0
				for _, c := range got {
					if c.MachineTranslated {
						marked++
					}
					if tc.lang == "sq" && c.MachineTranslated != (c.Ref.Token() == mt) {
						t.Errorf("file %s: MachineTranslated %v, want it only on %s", c.Ref.Token(), c.MachineTranslated, mt)
					}
				}
				if len(got) != tc.kept || marked != tc.marked {
					t.Errorf("%d kept, %d marked; want %d and %d", len(got), marked, tc.kept, tc.marked)
				}
			})
		})
	}
}

// TestPartialPagesCarryFailure: a page failing after one was read answers
// the read candidates beside its failure.
func TestPartialPagesCarryFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorded{search: func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Query().Get("page") == "2" {
				http.Error(w, "bad request", http.StatusBadRequest)
				return true
			}
			return false
		}}
		ctx := providertest.Serve(t, t.Context(), rec, hosts...)
		got, err := providertest.Build(t, opensubtitles.NewEntry(), values).Search(ctx, movie(t, "en", "fr"))
		if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Permanent || len(got) != 50 {
			t.Errorf("%d candidates, %v; want page 1's 50 and a Permanent failure", len(got), err)
		}
	})
}

func TestLoginRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorded{}
		ctx := providertest.Serve(t, t.Context(), rec, hosts...)
		for _, v := range []string{
			"username: user-1\npassword: os-password-0123\napi_key: wrong-key",
			"username: user-1\npassword: wrong\napi_key: " + key,
		} {
			a := providertest.Build(t, opensubtitles.NewEntry(), v)
			if _, err := a.Search(ctx, movie(t, "en")); !isKind(err, provider.Auth) {
				t.Errorf("search: %v, want Auth", err)
			}
			if err := a.(provider.Tester).Test(ctx); !isKind(err, provider.Auth) {
				t.Errorf("Test: %v, want Auth", err)
			}
		}
		if err := providertest.Build(t, opensubtitles.NewEntry(), values).(provider.Tester).Test(ctx); err != nil {
			t.Errorf("Test refused a good account: %v", err)
		}
		for _, r := range rec.requests() {
			if !strings.HasPrefix(r, "POST "+apiHost+"/api/v1/login") {
				t.Errorf("Test or a refused login sent %q", r)
			}
		}
	})
}

func isKind(err error, k provider.FailureKind) bool {
	f, ok := errors.AsType[*provider.Failure](err)
	return ok && f.Kind == k
}

// TestRefusedTokenLogsInAgain: a 401 to a search drops the login, and the
// next call logs in again; a held login is reused.
func TestRefusedTokenLogsInAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		refuse := true
		rec := &recorded{search: func(w http.ResponseWriter, _ *http.Request) bool {
			if refuse {
				refuse = false
				http.Error(w, `{"message":"invalid token"}`, http.StatusUnauthorized)
				return true
			}
			return false
		}}
		ctx := providertest.Serve(t, t.Context(), rec, hosts...)
		a := providertest.Build(t, opensubtitles.NewEntry(), values)
		if _, err := a.Search(ctx, movie(t, "en")); !isKind(err, provider.Auth) {
			t.Fatalf("first search: %v, want Auth", err)
		}
		for range 2 {
			if _, err := a.Search(ctx, movie(t, "en")); err != nil {
				t.Fatal(err)
			}
		}
		if rec.logins != 2 {
			t.Errorf("%d logins, want 2", rec.logins)
		}
	})
}

// TestSearchHostFromLogin: searches go to the login's base_url only when it
// is the listed VIP host.
func TestSearchHostFromLogin(t *testing.T) {
	for base, want := range map[string]string{
		vipHost:                    vipHost,
		"":                         apiHost,
		"evil.opensubtitles.com":   apiHost,
		"vip-api.opensubtitles.co": apiHost,
		apiHost:                    apiHost,
	} {
		synctest.Test(t, func(t *testing.T) {
			rec := &recorded{login: func(w http.ResponseWriter) {
				_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "base_url": base, "user": map[string]any{"vip": false}})
			}}
			ctx := providertest.Serve(t, t.Context(), rec, hosts...)
			if _, err := providertest.Build(t, opensubtitles.NewEntry(), values).Search(ctx, movie(t, "en")); err != nil {
				t.Fatal(err)
			}
			if reqs := rec.requests(); !strings.HasPrefix(reqs[1], "GET "+want+"/") {
				t.Errorf("base_url %q: searched %q, want %s", base, reqs[1], want)
			}
		})
	}
}

func TestFetchRecorded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var body []byte
		rec := &recorded{}
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/download" {
				body, _ = io.ReadAll(r.Body)
				r.Body = io.NopCloser(strings.NewReader(string(body)))
			}
			rec.ServeHTTP(w, r)
		}), hosts...)
		p, err := providertest.Build(t, opensubtitles.NewEntry(), values).Fetch(ctx, provider.NewRef("12835077"))
		if err != nil {
			t.Fatal(err)
		}
		want, _ := os.ReadFile(srtFile)
		if p.Name != "Inception.2010.1080p.WEB-DLx264.fr.srt" || string(p.Bytes) != string(want) {
			t.Errorf("payload %q, %d bytes", p.Name, len(p.Bytes))
		}
		if string(body) != `{"sub_format":"srt","file_id":12835077}` {
			t.Errorf("download body %s", body)
		}
		reqs := rec.requests()
		if len(reqs) != 3 || reqs[1] != "POST "+apiHost+"/api/v1/download" || !strings.HasPrefix(reqs[2], "GET "+linkHost+"/download/") {
			t.Errorf("requests %q", reqs)
		}
	})
}

// TestFetchRefused: a token that is no file id sends nothing, and a link on
// an unlisted host is never fetched.
func TestFetchRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorded{}
		ctx := providertest.Serve(t, t.Context(), rec, hosts...)
		a := providertest.Build(t, opensubtitles.NewEntry(), values)
		for _, tok := range []string{"", "abc", "0", "-1", "4294967296", "https://" + linkHost + "/x"} {
			if _, err := a.Fetch(ctx, provider.NewRef(tok)); !isKind(err, provider.Permanent) {
				t.Errorf("token %q: %v, want Permanent", tok, err)
			}
		}
		if reqs := rec.requests(); len(reqs) != 0 {
			t.Errorf("requests %q, want none", reqs)
		}
	})
	for _, answer := range []string{
		`{"link":"https://dl.example.org/Inception.srt","requests":1,"remaining":9}`,
		`{"link":"","requests":1,"remaining":9}`,
		`{"link":"http://` + linkHost + `/download/x","requests":1,"remaining":9}`,
	} {
		synctest.Test(t, func(t *testing.T) {
			rec := &recorded{}
			ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/download" {
					rec.log(r)
					_, _ = w.Write([]byte(answer))
					return
				}
				rec.ServeHTTP(w, r)
			}), hosts...)
			_, err := providertest.Build(t, opensubtitles.NewEntry(), values).Fetch(ctx, provider.NewRef("12835077"))
			if !isKind(err, provider.Permanent) {
				t.Errorf("%s: %v, want Permanent", answer, err)
			}
			if reqs := rec.requests(); len(reqs) != 2 {
				t.Errorf("%s: requests %q, want the login and the download POST", answer, reqs)
			}
		})
	}
}

// TestQuotaSpent: a 406 to the download POST is a rate limit until the next
// UTC midnight, or its Retry-After.
func TestQuotaSpent(t *testing.T) {
	// The bubble's clock starts at midnight UTC, so the quota renews a day
	// later, less the virtual time the login's pacing took.
	for retryAfter, want := range map[string][2]time.Duration{"": {23*time.Hour + 59*time.Minute, 24 * time.Hour}, "120": {2 * time.Minute, 2 * time.Minute}} {
		synctest.Test(t, func(t *testing.T) {
			rec := &recorded{}
			ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/download" {
					if retryAfter != "" {
						w.Header().Set("Retry-After", retryAfter)
					}
					w.WriteHeader(http.StatusNotAcceptable)
					return
				}
				rec.ServeHTTP(w, r)
			}), hosts...)
			_, err := providertest.Build(t, opensubtitles.NewEntry(), values).Fetch(ctx, provider.NewRef("12835077"))
			f, ok := errors.AsType[*provider.Failure](err)
			if !ok || f.Kind != provider.RateLimited || f.RetryAfter < want[0] || f.RetryAfter > want[1] {
				t.Errorf("Retry-After %q: %v, want RateLimited for %v", retryAfter, err, want)
			}
		})
	}
}

func TestProviderMaps(t *testing.T) {
	m := opensubtitles.NewEntry().Languages
	var listed struct {
		Data []struct {
			Code string `json:"language_code"`
		} `json:"data"`
	}
	b, err := os.ReadFile("testdata/infos-languages.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &listed); err != nil {
		t.Fatal(err)
	}
	unread := []string{"at", "ze", "zh-ca", "pr", "ex", "ma", "me", "pm", "sx", "az-zb", "sp", "sy", "tm-td", "tp"}
	for _, d := range listed.Data {
		if _, ok := m.Tag(d.Code); ok == slices.Contains(unread, d.Code) {
			t.Errorf("listed code %q: mapped %v", d.Code, ok)
		}
	}
	for _, file := range []string{"testdata/search-tmdb-27205-page-1.json", "testdata/search-tmdb-27205-page-2.json", "testdata/search-imdb-903747-s01e01.json"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			Data []struct {
				Attributes struct {
					Language string `json:"language"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		for _, d := range r.Data {
			if _, ok := m.Tag(strings.ToLower(d.Attributes.Language)); !ok {
				t.Errorf("%s: label %q maps to no tag", file, d.Attributes.Language)
			}
		}
	}
	for _, tg := range m.Tags() {
		w, ok := m.Wire(tg)
		if back, _ := m.Tag(w); !ok || back != tg {
			t.Errorf("%v does not round-trip through %q", tg, w)
		}
	}
	for wire, want := range map[string]string{"zh-cn": "zh-Hans", "zh-tw": "zh-Hant", "pt-pt": "pt-PT", "pt-br": "pt-BR", "ea": "es-419", "az-az": "az"} {
		if got, _ := m.Tag(wire); got != tag(t, want) {
			t.Errorf("%q maps to %v, want %s", wire, got, want)
		}
	}
}
