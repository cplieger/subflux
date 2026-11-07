package hdbits_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/hdbits"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/rank"
)

const (
	host    = "hdbits.org"
	passkey = "0123456789abcdef0123456789abcdef"
	values  = "username: user-1\npasskey: " + passkey
)

func tags(t *testing.T, ss ...string) []language.Tag {
	t.Helper()
	var out []language.Tag
	for _, s := range ss {
		tg, ok := language.Parse(s)
		if !ok {
			t.Fatalf("tag %q", s)
		}
		out = append(out, tg)
	}
	return out
}

// recorded serves the recorded answers: Inception's 16 torrents by IMDb id,
// their subtitle lists and one getdox body. list, when set, answers a
// torrent's list first, returning false to fall through.
type recorded struct {
	list   func(w http.ResponseWriter, torrent int) bool
	lists  map[string]json.RawMessage
	mu     sync.Mutex
	reqs   []string
	times  []time.Time
	bodies []map[string]any
}

func newRecorded(t *testing.T) *recorded {
	b, err := os.ReadFile("testdata/subtitles-by-torrent.json")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorded{}
	if err := json.Unmarshal(b, &rec.lists); err != nil {
		t.Fatal(err)
	}
	return rec
}

func (rec *recorded) requests() ([]string, []map[string]any) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return slices.Clone(rec.reqs), slices.Clone(rec.bodies)
}

func (rec *recorded) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	rec.mu.Lock()
	rec.reqs = append(rec.reqs, r.Method+" "+r.URL.Path)
	rec.times = append(rec.times, time.Now())
	rec.bodies = append(rec.bodies, body)
	rec.mu.Unlock()
	if r.URL.Path == "/getdox.php" {
		q := r.URL.Query()
		if q.Get("passkey") != passkey {
			_, _ = w.Write([]byte(`{"status":5,"message":"Invalid authentication credentials"}`))
			return
		}
		if q.Get("id") != "344222" {
			http.NotFound(w, r)
			return
		}
		b, _ := os.ReadFile("testdata/getdox-344222.srt")
		_, _ = w.Write(b)
		return
	}
	if body["username"] != "user-1" || body["passkey"] != passkey {
		b, _ := os.ReadFile("testdata/auth-failed.json")
		_, _ = w.Write(b)
		return
	}
	switch r.URL.Path {
	case "/api/test":
		_, _ = w.Write([]byte(`{"status":0,"message":"OK"}`))
	case "/api/torrents":
		if imdb, _ := body["imdb"].(map[string]any); imdb["id"] == float64(1375666) {
			b, _ := os.ReadFile("testdata/torrents-imdb-1375666.json")
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write([]byte(`{"status":0,"data":[]}`))
	case "/api/subtitles":
		id, _ := body["torrent_id"].(float64)
		if rec.list != nil && rec.list(w, int(id)) {
			return
		}
		if l, ok := rec.lists[strconv.Itoa(int(id))]; ok {
			_, _ = w.Write(l)
			return
		}
		_, _ = w.Write([]byte(`{"status":0,"data":[]}`))
	default:
		http.NotFound(w, r)
	}
}

func movie(t *testing.T, langs ...string) provider.Query {
	return provider.Query{Kind: media.Movie, Titles: []string{"Inception"}, Year: 2010, External: media.ExternalIDs{IMDb: 1375666, TMDB: 27205}, Langs: tags(t, langs...)}
}

func isKind(err error, k provider.FailureKind) bool {
	f, ok := errors.AsType[*provider.Failure](err)
	return ok && f.Kind == k
}

func TestSearchRecorded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorded(t)
		ctx := providertest.Serve(t, t.Context(), rec, host)
		got, err := providertest.Build(t, hdbits.NewEntry(), values).Search(ctx, movie(t, "en", "el"))
		if err != nil {
			t.Fatal(err)
		}
		var en, el []string
		for _, c := range got {
			if c.MatchedBy != rank.ID {
				t.Errorf("candidate %+v, want an id match", c)
			}
			s := c.Ref.Token()
			if c.Flags.HI {
				s += " HI"
			}
			switch c.Lang {
			case tags(t, "en")[0]:
				en = append(en, s)
			case tags(t, "el")[0]:
				el = append(el, s)
			}
		}
		slices.Sort(en)
		slices.Sort(el)
		// uk is English; the SDH file is hearing impaired, "[retail+HI
		// removed]" is not; the Extras archive is left out.
		if want := []string{"478421.srt", "532620.srt", "532621.srt HI"}; !slices.Equal(en, want) {
			t.Errorf("English %q, want %q", en, want)
		}
		if len(el) != 3 {
			t.Errorf("Greek %q, want the three subtitles that are not extras", el)
		}
		reqs, bodies := rec.requests()
		if len(reqs) != 17 || reqs[0] != "POST /api/torrents" || bodies[0]["imdb"] == nil {
			t.Errorf("%d requests, first %q %v; want the torrent search and 16 lists", len(reqs), reqs[0], bodies[0])
		}
	})
}

func TestSearchLookup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorded(t)
		ctx := providertest.Serve(t, t.Context(), rec, host)
		a := providertest.Build(t, hdbits.NewEntry(), values)
		ep := provider.Query{Kind: media.Episode, External: media.ExternalIDs{TVDB: 81189, IMDb: 903747}, Numbering: media.Numbering{Scheme: media.TVDB, Season: 2, Episode: 3}, Langs: tags(t, "en")}
		if _, err := a.Search(ctx, ep); err != nil {
			t.Fatal(err)
		}
		_, bodies := rec.requests()
		if tvdb, _ := bodies[0]["tvdb"].(map[string]any); tvdb["id"] != float64(81189) || tvdb["season"] != float64(2) || bodies[0]["imdb"] != nil {
			t.Errorf("episode lookup %v, want the series' TVDB id and season", bodies[0])
		}
		ep.External.TVDB = 0
		noIMDb := movie(t, "en")
		noIMDb.External.IMDb = 0
		for _, q := range []provider.Query{ep, noIMDb} {
			if _, err := a.Search(ctx, q); !errors.As(err, new(*provider.Skipped)) {
				t.Errorf("%+v: %v, want *Skipped", q.External, err)
			}
		}
		if reqs, _ := rec.requests(); len(reqs) != 1 {
			t.Errorf("requests %q, want only the first lookup", reqs)
		}
	})
}

// TestHDBitsListCached: a found torrent search or subtitle list is asked
// once every 6 hours, an empty one every 15 minutes; a list is sent at most
// every half second, and a cached one waits for nothing. Inception's 16
// recorded lists hold 10 empty ones.
func TestHDBitsListCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorded(t)
		ctx := providertest.Serve(t, t.Context(), rec, host)
		a := providertest.Build(t, hdbits.NewEntry(), values)
		other := movie(t, "en")
		other.External.IMDb = 2
		searches := func() (torrents, lists int) {
			for _, q := range []provider.Query{movie(t, "en"), other} {
				if _, err := a.Search(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			reqs, _ := rec.requests()
			all := strings.Join(reqs, "\n")
			return strings.Count(all, "/api/torrents"), strings.Count(all, "/api/subtitles")
		}
		check := func(when string, torrents, lists, wantTorrents, wantLists int) {
			t.Helper()
			if torrents != wantTorrents || lists != wantLists {
				t.Errorf("%s: %d torrent searches and %d lists, want %d and %d", when, torrents, lists, wantTorrents, wantLists)
			}
		}
		n, l := searches()
		check("first", n, l, 2, 16)
		rec.mu.Lock()
		var last time.Time
		for i, r := range rec.reqs {
			if r == "POST /api/subtitles" {
				if !last.IsZero() && rec.times[i].Sub(last) < 500*time.Millisecond {
					t.Errorf("list %d sent %v after the one before, want at least 500ms", i, rec.times[i].Sub(last))
				}
				last = rec.times[i]
			}
		}
		rec.mu.Unlock()
		start := time.Now()
		n, l = searches()
		check("again", n, l, 2, 16)
		if d := time.Since(start); d != 0 {
			t.Errorf("a cached search took %v, want no wait", d)
		}
		time.Sleep(15 * time.Minute)
		n, l = searches()
		check("after 15 minutes", n, l, 3, 26)
		time.Sleep(6*time.Hour - 15*time.Minute)
		n, l = searches()
		check("after 6 hours", n, l, 5, 42)
	})
}

// TestFilenameNamesRelease: a file listed with no title takes its file
// name's stem as its release; a titled one keeps its title.
func TestFilenameNamesRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorded(t)
		rec.list = func(w http.ResponseWriter, torrent int) bool {
			if torrent != 792434 {
				return false
			}
			_, _ = w.Write([]byte(`{"status":0,"data":[
				{"id":1,"title":"","filename":"Inception.2010.2160p.WEB-DL.DTS-HD.MA.5.1-TheFarm.srt","language":"uk"},
				{"id":2,"title":" ","filename":"Inception.2010.2160p.WEB-DL-TheFarm.SDH.srt","language":"uk"}]}`))
			return true
		}
		ctx := providertest.Serve(t, t.Context(), rec, host)
		got, err := providertest.Build(t, hdbits.NewEntry(), values).Search(ctx, movie(t, "en"))
		if err != nil {
			t.Fatal(err)
		}
		rels := map[string]string{}
		for _, c := range got {
			rels[c.Ref.Token()] = c.Release
		}
		want := map[string]string{
			"1.srt":      "Inception.2010.2160p.WEB-DL.DTS-HD.MA.5.1-TheFarm",
			"2.srt":      "Inception.2010.2160p.WEB-DL-TheFarm.SDH",
			"532620.srt": "Inception 2010 2160p UHD BluRay DTS-HD MA 5.1 HDR x265-CtrlHD",
		}
		for tok, rel := range want {
			if rels[tok] != rel {
				t.Errorf("%s: release %q, want %q", tok, rels[tok], rel)
			}
		}
	})
}

// TestRefusedAccount: a refused account ends the search on the torrent
// lookup or on a list, with the lists read before it.
func TestRefusedAccount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorded(t)
		ctx := providertest.Serve(t, t.Context(), rec, host)
		a := providertest.Build(t, hdbits.NewEntry(), "username: user-1\npasskey: wrong")
		if _, err := a.Search(ctx, movie(t, "en")); !isKind(err, provider.Auth) {
			t.Errorf("search: %v, want Auth", err)
		}
		if err := a.(provider.Tester).Test(ctx); !isKind(err, provider.Auth) {
			t.Errorf("Test: %v, want Auth", err)
		}
		if err := providertest.Build(t, hdbits.NewEntry(), values).(provider.Tester).Test(ctx); err != nil {
			t.Errorf("Test refused a good account: %v", err)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorded(t)
		rec.list = func(w http.ResponseWriter, torrent int) bool {
			if torrent == 86413 {
				_, _ = w.Write([]byte(`{"status":5,"message":"Invalid authentication credentials"}`))
				return true
			}
			return false
		}
		ctx := providertest.Serve(t, t.Context(), rec, host)
		got, err := providertest.Build(t, hdbits.NewEntry(), values).Search(ctx, movie(t, "en", "el", "cs"))
		reqs, _ := rec.requests()
		if !isKind(err, provider.Auth) || len(got) == 0 || len(reqs) >= 17 {
			t.Errorf("%d candidates, %v after %d requests; want the read lists, Auth and the later lists unasked", len(got), err, len(reqs))
		}
	})
}

// TestFailedListReported: a list failing otherwise is sent once, the other
// lists are still read, and the failure comes back beside them.
func TestFailedListReported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorded(t)
		var asked int
		rec.list = func(w http.ResponseWriter, torrent int) bool {
			if torrent == 792434 {
				asked++
				w.WriteHeader(http.StatusServiceUnavailable)
				return true
			}
			return false
		}
		ctx := providertest.Serve(t, t.Context(), rec, host)
		got, err := providertest.Build(t, hdbits.NewEntry(), values).Search(ctx, movie(t, "en"))
		if !isKind(err, provider.Transient) || len(got) != 3 || asked != 1 {
			t.Errorf("%d candidates, %v, the failing list asked %d times; want 3, Transient and once", len(got), err, asked)
		}
	})
}

func TestFetchRecorded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorded(t)
		ctx := providertest.Serve(t, t.Context(), rec, host)
		a := providertest.Build(t, hdbits.NewEntry(), values)
		want, _ := os.ReadFile("testdata/getdox-344222.srt")
		for _, tok := range []string{"344222.srt", "344222"} {
			p, err := a.Fetch(ctx, provider.NewRef(tok))
			if err != nil {
				t.Fatal(err)
			}
			if p.Name != tok || string(p.Bytes) != string(want) {
				t.Errorf("payload %q of %d bytes", p.Name, len(p.Bytes))
			}
		}
		if reqs, _ := rec.requests(); len(reqs) != 1 || reqs[0] != "GET /getdox.php" {
			t.Errorf("requests %q, want one download served twice", reqs)
		}
		for _, tok := range []string{"", "abc", "0", "-1", "0344222", "344222.", "344222.s/rt", "344222.toolongext", "4294967296"} {
			if _, err := a.Fetch(ctx, provider.NewRef(tok)); !isKind(err, provider.Permanent) {
				t.Errorf("token %q: %v, want Permanent", tok, err)
			}
		}
	})
}

// TestFetchVerdictNotCached: a getdox body that is the API's refusal is
// Auth, quotes no passkey, and is not served again from the cache.
func TestFetchVerdictNotCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorded(t)
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/getdox.php" {
				rec.ServeHTTP(w, r)
				return
			}
		}), host)
		a := providertest.Build(t, hdbits.NewEntry(), "username: user-1\npasskey: wrongpasskey0123456789")
		for range 2 {
			_, err := a.Fetch(ctx, provider.NewRef("344222.srt"))
			if !isKind(err, provider.Auth) || strings.Contains(err.Error(), "wrongpasskey0123456789") {
				t.Errorf("%v, want Auth without the passkey", err)
			}
		}
		if reqs, _ := rec.requests(); len(reqs) != 2 {
			t.Errorf("requests %q, want both fetches sent", reqs)
		}
	})
}

func TestProviderMaps(t *testing.T) {
	m := hdbits.NewEntry().Languages
	b, err := os.ReadFile("testdata/subtitles-by-torrent.json")
	if err != nil {
		t.Fatal(err)
	}
	var lists map[string]struct {
		Data []struct {
			Language string `json:"language"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &lists); err != nil {
		t.Fatal(err)
	}
	for id, l := range lists {
		for _, d := range l.Data {
			if _, ok := m.Tag(d.Language); !ok {
				t.Errorf("torrent %s: label %q maps to no tag", id, d.Language)
			}
		}
	}
	for _, tg := range m.Tags() {
		w, ok := m.Wire(tg)
		if back, _ := m.Tag(w); !ok || back != tg {
			t.Errorf("%v does not round-trip through %q", tg, w)
		}
	}
	for wire, want := range map[string]string{"uk": "en", "se": "sv", "si": "sl", "br": "pt-BR", "hk": "zh-Hant", "cn": "zh-Hans", "il": "he", "gr": "el"} {
		if got, _ := m.Tag(wire); got != tags(t, want)[0] {
			t.Errorf("%q maps to %v, want %s", wire, got, want)
		}
	}
}
