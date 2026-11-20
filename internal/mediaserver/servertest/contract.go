package servertest

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediaserver"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

// Harness adapts one server kind to the suite.
type Harness interface {
	Kind() mediaserver.AdapterKind
	// Serve starts a fake server holding lib for the test's lifetime and
	// returns an entry pointing at it, its Values decoded by Kind's Schema
	// as ParseConfigs would.
	Serve(t testing.TB, lib *Library) mediaserver.Config
	// ItemKey is the n-th item key, n from 1, in the server's key scheme.
	ItemKey(n int) string
	// Refreshed lists the keys of the items the server was asked to
	// refresh so far, in request order.
	Refreshed() []string
}

const (
	contractID = "contract"
	serverRoot = "/srv/media"
	localRoot  = "/data/media"
	showDir    = "/tv/Show (2001)/Season 01/"
	movieDir   = "/movies/Film (2001)/"
)

// Contract runs the suite through a Set over h's kind, the entry mapping
// /data/media locally to /srv/media on the server: two episodes of one show
// in their own files, each refreshed and labelled alone; two episodes in one
// file, refreshed together and labelled with that file's sidecars once; two
// versions of one movie, each file refreshing the movie and labelled with
// its own sidecars only; and a path no item holds, refreshing nothing and
// labelled with nothing, both successes.
func Contract(t *testing.T, h Harness) {
	t.Helper()
	show := h.ItemKey(1)
	ep1, ep2, ep3, ep4, movie := h.ItemKey(2), h.ItemKey(3), h.ItemKey(4), h.ItemKey(5), h.ItemKey(6)
	server := func(dir, name string) string { return serverRoot + dir + name }
	lib := &Library{
		Items: []Item{
			{Key: ep1, Show: show, Files: []string{server(showDir, "S01E01.mkv")}},
			{Key: ep2, Show: show, Files: []string{server(showDir, "S01E02.mkv")}},
			{Key: ep3, Show: show, Files: []string{server(showDir, "S01E03-E04.mkv")}},
			{Key: ep4, Show: show, Files: []string{server(showDir, "S01E03-E04.mkv")}},
			{Key: movie, Files: []string{server(movieDir, "Film 1080p.mkv"), server(movieDir, "Film 2160p.mkv")}},
		},
		Sidecars: map[string][]mediaserver.Label{
			server(showDir, "S01E01.mkv"):      {{LanguageTag: "en", Display: "English"}},
			server(showDir, "S01E02.mkv"):      {{LanguageTag: "fr", Display: "Français", Flags: language.Flags{Forced: true}}},
			server(showDir, "S01E03-E04.mkv"):  {{LanguageTag: "de", Display: "Deutsch SDH", Flags: language.Flags{HI: true}}, {LanguageTag: "pt-BR", Display: "Português"}},
			server(movieDir, "Film 1080p.mkv"): {{LanguageTag: "en", Display: "English"}},
			server(movieDir, "Film 2160p.mkv"): {{LanguageTag: "ja", Display: "日本語"}},
		},
	}
	kind := h.Kind()
	cfg := h.Serve(t, lib.clone())
	cfg.ID, cfg.Type = contractID, kind.Type
	cfg.PathMappings = []mediasource.PathMapping{{From: serverRoot, To: localRoot}}
	c := &contract{h: h, set: contractSet(t, kind, &cfg)}
	check := func(t *testing.T, dir, name string, keys []string) {
		t.Helper()
		c.check(t, dir+name, keys, lib.Sidecars[server(dir, name)])
	}
	t.Run("two episodes under one show", func(t *testing.T) {
		check(t, showDir, "S01E01.mkv", []string{ep1})
		check(t, showDir, "S01E02.mkv", []string{ep2})
	})
	t.Run("two episodes in one file", func(t *testing.T) { check(t, showDir, "S01E03-E04.mkv", []string{ep3, ep4}) })
	t.Run("two versions of one movie", func(t *testing.T) {
		check(t, movieDir, "Film 1080p.mkv", []string{movie})
		check(t, movieDir, "Film 2160p.mkv", []string{movie})
	})
	t.Run("a miss", func(t *testing.T) { check(t, showDir, "S01E05.mkv", nil) })
}

type contract struct {
	h   Harness
	set *mediaserver.Set
}

// check refreshes and labels the local path under /data/media, wanting
// exactly the items keys refreshed, in any order, and labels answered.
func (c *contract) check(t *testing.T, rel string, keys []string, labels []mediaserver.Label) {
	t.Helper()
	local := localRoot + rel
	before := len(c.h.Refreshed())
	res := c.set.Refresh(t.Context(), local, []string{contractID})
	if len(res) != 1 || res[0].Err != nil {
		t.Errorf("Refresh(%s) = %+v, want one success", rel, res)
	}
	got := slices.Sorted(slices.Values(c.h.Refreshed()[before:]))
	if want := slices.Sorted(slices.Values(keys)); !slices.Equal(got, want) {
		t.Errorf("Refresh(%s) refreshed %v, want %v", rel, got, want)
	}
	sl, err := c.set.Labels(t.Context(), local)
	if err != nil || len(sl) != 1 || sl[0].Err != "" {
		t.Fatalf("Labels(%s) = %+v %v, want one server's answer", rel, sl, err)
	}
	if g, w := byDisplay(sl[0].Labels), byDisplay(labels); !slices.Equal(g, w) {
		t.Errorf("Labels(%s) = %+v, want %+v", rel, g, w)
	}
}

func byDisplay(ls []mediaserver.Label) []mediaserver.Label {
	return slices.SortedFunc(slices.Values(ls), func(a, b mediaserver.Label) int { return cmp.Compare(a.Display, b.Display) })
}

// probeTable is one server's recorded ingest probe: each row one sidecar,
// <stem>.<segment>[.<hi_token>].srt, written beside a test movie by itself,
// and the tag and hearing-impaired flag the server stored for it.
type probeTable struct {
	Server   string     `json:"server"`
	Recorded string     `json:"recorded"`
	Written  []probeRow `json:"written"`
	Read     []probeRow `json:"read"`
}

// probeRow is one sidecar; Gap, on a written row, records why the server
// stores another tag, which the row must then still show.
type probeRow struct {
	Tag       string `json:"tag"`
	Segment   string `json:"segment"`
	HIToken   string `json:"hi_token,omitempty"`
	ServerTag string `json:"server_tag"`
	Gap       string `json:"gap,omitempty"`
	ServerHI  bool   `json:"server_hi"`
}

// SpellingTable checks the server's recorded ingest probe at path (decision
// 16) against what subflux writes today. Each written row's segment is
// FilenameTag of its tag, its hi_token "", "hi" or "sdh", and it passes: the
// stored tag grades Exact for the tag written and the stored flag is set
// exactly when a token was. A row recording a gap must still fail, so a gap
// the server closed is noticed. Each read row is a spelling subflux reads but
// never writes, and the server misread it.
func SpellingTable(t *testing.T, path string) {
	t.Helper()
	tab := readProbe(t, path)
	for i := range tab.Written {
		t.Run(tab.Written[i].spelling(), func(t *testing.T) { checkWritten(t, &tab.Written[i]) })
	}
	for i := range tab.Read {
		t.Run("read "+tab.Read[i].spelling(), func(t *testing.T) { checkRead(t, &tab.Read[i]) })
	}
}

func readProbe(t *testing.T, path string) probeTable {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var tab probeTable
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&tab); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if tab.Server == "" || tab.Recorded == "" || len(tab.Written) == 0 {
		t.Fatalf("%s names no server, no recording or no written row", path)
	}
	seen := map[string]bool{}
	all := slices.Concat(tab.Written, tab.Read)
	for i := range all {
		sp := all[i].spelling()
		if seen[sp] {
			t.Fatalf("%s records %s twice", path, sp)
		}
		seen[sp] = true
	}
	return tab
}

func (r *probeRow) spelling() string {
	if r.HIToken == "" {
		return r.Segment
	}
	return r.Segment + "." + r.HIToken
}

func checkWritten(t *testing.T, r *probeRow) {
	written, stored := mustParse(t, r.Tag), mustParse(t, r.ServerTag)
	if got := language.FilenameTag(written); got != r.Segment {
		t.Errorf("FilenameTag(%s) = %q, but the probe wrote %q", r.Tag, got, r.Segment)
	}
	if !slices.Contains([]string{"", "hi", "sdh"}, r.HIToken) {
		t.Errorf("hi_token %q is not a hearing-impaired token", r.HIToken)
	}
	g := language.Policy{}.Grade(written, stored)
	pass := g.Class == language.Exact && r.ServerHI == (r.HIToken != "")
	if pass != (r.Gap == "") {
		class, _ := g.Class.MarshalText()
		t.Errorf("%s stored as %s (HI %v) grades %s: pass %v, but the row records gap %q", r.spelling(), r.ServerTag, r.ServerHI, class, pass, r.Gap)
	}
}

func checkRead(t *testing.T, r *probeRow) {
	read := mustParse(t, r.Tag)
	if language.FilenameTag(read) == r.Segment {
		t.Errorf("subflux writes %s for %s, but the server misreads it", r.Segment, r.Tag)
	}
	if g := (language.Policy{}).Grade(read, mustParse(t, r.ServerTag)); g.Class == language.Exact {
		t.Errorf("%s stored as %q grades Exact for %s, but the row records a misread", r.Segment, r.ServerTag, r.Tag)
	}
}

// mustParse parses a recorded tag, "" being no language.
func mustParse(t *testing.T, raw string) language.Tag {
	t.Helper()
	tag, ok := language.Parse(raw)
	if !ok {
		t.Fatalf("the recorded tag %q does not parse", raw)
	}
	return tag
}

func contractSet(t *testing.T, kind mediaserver.AdapterKind, cfg *mediaserver.Config) *mediaserver.Set {
	t.Helper()
	alerts, err := status.New(t.Context(), statustest.NewMemPersister())
	if err != nil {
		t.Fatalf("status.New = %v", err)
	}
	set, err := mediaserver.NewSet([]mediaserver.AdapterKind{kind}, []mediaserver.Config{*cfg}, mediasource.Deps{Logger: slog.New(slog.DiscardHandler)}, alerts)
	if err != nil {
		t.Fatalf("NewSet = %v", err)
	}
	if got := set.Servers(); !slices.Equal(got, []string{contractID}) {
		t.Fatalf("Servers = %v, want the contract entry built", got)
	}
	t.Cleanup(func() {
		// t.Context is already cancelled when cleanups run.
		if err := set.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown = %v", err)
		}
	})
	return set
}
