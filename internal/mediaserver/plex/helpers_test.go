package plex

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/plexapi/v3"
	"github.com/cplieger/subflux/internal/mediaserver"
	"github.com/cplieger/subflux/internal/mediasource"
	"go.yaml.in/yaml/v4"
)

const testToken = "test-token"

// fakePlex is a Plex library served in process: sections, their leaves as
// raw answer rows, and the request log. hook, when set, runs first and may
// block or answer a status of its own (0 lets the request through).
type fakePlex struct {
	hook     func(r *http.Request) int
	sections []plexapi.Section
	rows     map[string][]map[string]any // section key -> leaves
	mu       sync.Mutex
	requests []string
}

func newFakePlex() *fakePlex { return &fakePlex{rows: map[string][]map[string]any{}} }

func (f *fakePlex) addSection(key, typ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sections = append(f.sections, plexapi.Section{Key: key, Type: typ})
}

// remove deletes the item key from every section, as Plex deletes one.
func (f *fakePlex) remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for sec, rows := range f.rows {
		f.rows[sec] = slices.DeleteFunc(rows, func(r map[string]any) bool { return r["ratingKey"] == key })
	}
}

// put adds row to section, replacing a row with the same ratingKey.
func (f *fakePlex) put(section string, row map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := slices.DeleteFunc(f.rows[section], func(r map[string]any) bool { return r["ratingKey"] == row["ratingKey"] })
	f.rows[section] = append(rows, row)
}

func (f *fakePlex) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func (f *fakePlex) count(prefix string) int {
	n := 0
	for _, r := range f.log() {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func (f *fakePlex) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()
	if r.Header.Get("X-Plex-Token") != testToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.hook != nil {
		if code := f.hook(r); code != 0 {
			w.WriteHeader(code)
			return
		}
	}
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && p == "/":
		writeMC(w, map[string]any{"machineIdentifier": "machine-id-0", "friendlyName": "Plex"})
	case r.Method == http.MethodGet && p == "/library/sections":
		f.mu.Lock()
		writeMC(w, map[string]any{"Directory": f.sections})
		f.mu.Unlock()
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/library/sections/") && strings.HasSuffix(p, "/all"):
		f.list(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "/library/sections/"), "/all"))
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/library/metadata/") && strings.HasSuffix(p, "/refresh"):
		if f.item(strings.TrimSuffix(strings.TrimPrefix(p, "/library/metadata/"), "/refresh")) == nil {
			w.WriteHeader(http.StatusNotFound)
		}
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/library/metadata/"):
		f.metadata(w, strings.TrimPrefix(p, "/library/metadata/"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// list answers a section listing: the type filter, an addedAt or updatedAt
// lower bound with its sort, and the container window.
func (f *fakePlex) list(w http.ResponseWriter, r *http.Request, section string) {
	q := r.URL.Query()
	f.mu.Lock()
	rows := slices.Clone(f.rows[section])
	f.mu.Unlock()
	typ := map[string]string{"1": "movie", "4": "episode"}[q.Get("type")]
	rows = slices.DeleteFunc(rows, func(row map[string]any) bool { return typ != "" && row["type"] != typ })
	for _, field := range []string{"addedAt", "updatedAt"} {
		if v := q.Get(field + ">"); v != "" {
			bound, _ := strconv.ParseInt(v, 10, 64)
			rows = slices.DeleteFunc(rows, func(row map[string]any) bool { return num(row[field]) < bound })
		}
	}
	slices.SortStableFunc(rows, func(a, b map[string]any) int {
		switch q.Get("sort") {
		case "updatedAt:asc":
			return cmp.Or(cmp.Compare(num(a["updatedAt"]), num(b["updatedAt"])), cmp.Compare(a["ratingKey"].(string), b["ratingKey"].(string)))
		case "addedAt:desc":
			return cmp.Compare(num(b["addedAt"]), num(a["addedAt"]))
		}
		return cmp.Compare(a["ratingKey"].(string), b["ratingKey"].(string))
	})
	total := len(rows)
	if s := q.Get("X-Plex-Container-Start"); s != "" {
		start, _ := strconv.Atoi(s)
		size, _ := strconv.Atoi(q.Get("X-Plex-Container-Size"))
		rows = rows[min(start, len(rows)):min(start+size, len(rows))]
	}
	writeMC(w, map[string]any{"totalSize": total, "size": len(rows), "Metadata": rows})
}

// metadata answers the item, or a 404 as Plex answers a key it no longer
// holds.
func (f *fakePlex) metadata(w http.ResponseWriter, key string) {
	row := f.item(key)
	if row == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeMC(w, map[string]any{"size": 1, "Metadata": []any{row}})
}

func (f *fakePlex) item(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rows := range f.rows {
		for _, row := range rows {
			if row["ratingKey"] == key {
				return row
			}
		}
	}
	return nil
}

func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

func writeMC(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"MediaContainer": body})
}

// row is a leaf answer row holding files as its parts.
func row(key, typ string, addedAt, updatedAt int64, files ...string) map[string]any {
	parts := make([]any, len(files))
	for i, f := range files {
		parts[i] = map[string]any{"id": i + 1, "file": f}
	}
	return map[string]any{
		"ratingKey": key, "type": typ, "addedAt": addedAt, "updatedAt": updatedAt,
		"Media": []any{map[string]any{"id": 1, "Part": parts}},
	}
}

// recorded answers the first Metadata row of a recorded answer in testdata.
func recorded(t testing.TB, name string) map[string]any {
	t.Helper()
	return recordedAll(t, name)[0]
}

// recordedAll answers every Metadata row of a recorded answer in testdata.
func recordedAll(t testing.TB, name string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var mc struct {
		MediaContainer struct{ Metadata []map[string]any } `json:"MediaContainer"`
	}
	if err := json.Unmarshal(b, &mc); err != nil {
		t.Fatal(err)
	}
	return mc.MediaContainer.Metadata
}

func files(row map[string]any) []string {
	var out []string
	for _, m := range row["Media"].([]any) {
		for _, p := range m.(map[string]any)["Part"].([]any) {
			out = append(out, p.(map[string]any)["file"].(string))
		}
	}
	return out
}

// handlerTransport serves requests in process, so a fake Plex runs inside a
// synctest bubble, where a socket would block it.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, r)
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	return rec.Result(), nil
}

func plexConfig(id, url, token string) mediaserver.Config {
	vals, errs := schema.Decode(&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "url"},
		{Kind: yaml.ScalarNode, Value: url},
		{Kind: yaml.ScalarNode, Value: "token"},
		{Kind: yaml.ScalarNode, Value: token},
	}})
	if len(errs) > 0 {
		panic(fmt.Sprint(errs))
	}
	return mediaserver.Config{ID: id, Type: "plex", Values: vals}
}

// inProcess is an adapter built by newPlex whose client reaches f in process,
// with plexapi's retries left at one attempt so a fault answers at once.
func inProcess(t testing.TB, f *fakePlex) *adapter {
	t.Helper()
	a, err := newPlex(plexConfig("plex", "http://localhost:32400", testToken), mediasource.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	a.c, err = plexapi.New("http://localhost:32400", testToken,
		plexapi.WithHTTPClient(&http.Client{Transport: handlerTransport{f}}), plexapi.WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	return a
}
