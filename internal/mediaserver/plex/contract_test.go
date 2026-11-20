package plex

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/mediaserver"
	"github.com/cplieger/subflux/internal/mediaserver/servertest"
)

func TestPlexSpellingTable(t *testing.T) {
	servertest.SpellingTable(t, "testdata/spellings.json")
}

func TestContractOverPlex(t *testing.T) {
	servertest.Contract(t, &harness{})
}

// harness serves the suite's library as Plex answers it: each episode a row
// of the recorded two-episode-file answer and each movie a row of the
// recorded movie-two-sidecars answer, with its keys replaced and one Media
// per file, whose part holds the recorded streams less their sidecars plus
// one sidecar stream per label of that file.
type harness struct{ f *fakePlex }

func (*harness) Kind() mediaserver.AdapterKind { return Kind }

func (*harness) ItemKey(n int) string { return strconv.Itoa(900000 + n) }

func (h *harness) Serve(t testing.TB, lib *servertest.Library) mediaserver.Config {
	t.Helper()
	h.f = newFakePlex()
	h.f.addSection("2", "show")
	h.f.addSection("1", "movie")
	episode, movie := recorded(t, "two-episode-file"), recorded(t, "movie-two-sidecars")
	for i := range lib.Items {
		it := &lib.Items[i]
		if it.Show == "" {
			h.f.put("1", itemRow(t, movie, it, lib.Sidecars))
		} else {
			h.f.put("2", itemRow(t, episode, it, lib.Sidecars))
		}
	}
	srv := httptest.NewServer(h.f)
	t.Cleanup(srv.Close)
	return plexConfig("", srv.URL, testToken)
}

func (h *harness) Refreshed() []string {
	var keys []string
	for _, r := range h.f.log() {
		if k, ok := strings.CutPrefix(r, "PUT /library/metadata/"); ok {
			keys = append(keys, strings.TrimSuffix(k, "/refresh"))
		}
	}
	return keys
}

// itemRow is a deep copy of tmpl standing for it.
func itemRow(t testing.TB, tmpl map[string]any, it *servertest.Item, sidecars map[string][]mediaserver.Label) map[string]any {
	t.Helper()
	row := deepCopy(t, tmpl)
	row["ratingKey"], row["key"] = it.Key, "/library/metadata/"+it.Key
	if it.Show != "" {
		row["grandparentRatingKey"], row["grandparentKey"] = it.Show, "/library/metadata/"+it.Show
	}
	tmplMedia := row["Media"].([]any)[0].(map[string]any)
	base, err := strconv.Atoi(it.Key)
	if err != nil {
		t.Fatal(err)
	}
	media := make([]any, len(it.Files))
	for i, file := range it.Files {
		m := deepCopy(t, tmplMedia)
		p := m["Part"].([]any)[0].(map[string]any)
		id := base*10 + i
		m["id"] = id
		p["id"], p["key"], p["file"] = id, "/library/parts/"+strconv.Itoa(id)+"/file.mkv", file
		var streams []any
		recordedStreams, _ := p["Stream"].([]any)
		for _, s := range recordedStreams {
			if s := s.(map[string]any); s["streamType"] != float64(3) || s["key"] == nil {
				streams = append(streams, s)
			}
		}
		for j, l := range sidecars[file] {
			sid := id*100 + j
			streams = append(streams, map[string]any{
				"id": sid, "streamType": 3, "key": "/library/streams/" + strconv.Itoa(sid), "codec": "srt",
				"languageTag": l.LanguageTag, "displayTitle": l.Display,
				"hearingImpaired": l.Flags.HI, "forced": l.Flags.Forced,
			})
		}
		p["Stream"] = streams
		m["Part"] = []any{p}
		media[i] = m
	}
	row["Media"] = media
	return row
}

func deepCopy(t testing.TB, v map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
