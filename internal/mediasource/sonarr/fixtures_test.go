package sonarr_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
	"github.com/cplieger/subflux/internal/mediasource/sonarr"
)

// The recorded servers; testdata/README.md says how each was recorded.
const (
	v5Dir = "testdata/v5-api-v5"
	v4Dir = "testdata/v4-api-v3"

	base = "http://sonarr.test"

	shoah, firefly, monogatari = 254, 2188, 2189 // ids on Sonarr v5 (API v5)
	fireflyV4, monogatariV4    = 133, 697        // ids on Sonarr v4 (API v3)
)

func TestFixturesHoldNoSecret(t *testing.T) { arrtest.HoldsNoSecret(t, "testdata") }

func TestHistoryNamesRecordedRoots(t *testing.T) {
	arrtest.HistoryNamesListed(t, "testdata", "series.json", "seriesId")
}

// logs is a concurrency-safe slog sink.
type logs struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// adapter is an Adapter for srv, reached in process; publicURL "" leaves
// public_url unset.
func adapter(t *testing.T, srv *arrtest.Server, publicURL string) (*sonarr.Adapter, *logs) {
	t.Helper()
	l := &logs{}
	a, err := sonarr.Kind.New(arrtest.Config(t, "Sonarr", base, publicURL), mediasource.Deps{HTTP: srv.Client(), Logger: slog.New(slog.NewTextHandler(l, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return a.(*sonarr.Adapter), l
}

// walk is one Items call's answer.
type walk struct {
	err    error
	items  map[int][]mediasource.Item
	roots  []mediasource.Root
	unread map[int]mediasource.Cause
}

func (w *walk) root(t *testing.T, id int) mediasource.Root {
	t.Helper()
	for _, r := range w.roots {
		if r.Native.ID == mediasource.NativeID(itoa(id)) {
			return r
		}
	}
	t.Fatalf("walk yielded no root %d: %v", id, w.ids())
	return mediasource.Root{}
}

func (w *walk) ids() []string {
	out := make([]string, len(w.roots))
	for i, r := range w.roots {
		out[i] = string(r.Native.ID)
	}
	return out
}

func walkItems(t *testing.T, a mediasource.Adapter) *walk {
	t.Helper()
	w := &walk{items: map[int][]mediasource.Item{}, unread: map[int]mediasource.Cause{}}
	w.err = a.Items(t.Context(), func(r mediasource.Root, items []mediasource.Item) error {
		w.roots = append(w.roots, r)
		w.items[atoi(t, string(r.Native.ID))] = items
		return nil
	}, func(ref mediasource.NativeRef, err *mediasource.SourceError) error {
		w.unread[atoi(t, string(ref.ID))] = err.Cause
		return nil
	})
	return w
}

// item is the listed episode at season s, number e in the series' order.
func item(t *testing.T, items []mediasource.Item, s, e int) mediasource.Item {
	t.Helper()
	for _, it := range items {
		if it.SeriesNumbering.Season == s && it.SeriesNumbering.Episode == e {
			return it
		}
	}
	t.Fatalf("no item S%02dE%02d among %d", s, e, len(items))
	return mediasource.Item{}
}

func causeOf(err error) mediasource.Cause {
	var se *mediasource.SourceError
	if !errors.As(err, &se) {
		return 0
	}
	return se.Cause
}

// rewrite answers path with the recorded file after edit changes the
// decoded JSON in place; an array answer's edit sees every element.
func rewrite(t *testing.T, srv *arrtest.Server, path, file string, edit func(map[string]any)) {
	t.Helper()
	var doc any
	if err := json.Unmarshal(srv.Body(file), &doc); err != nil {
		t.Fatal(err)
	}
	switch d := doc.(type) {
	case []any:
		for _, e := range d {
			edit(e.(map[string]any))
		}
	case map[string]any:
		edit(d)
	}
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	srv.Handle(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			srv.Answer(w, r)
			return
		}
		srv.Write(w, http.StatusOK, body)
	})
}

// frenchMetadata is Monogatari as a Sonarr v5 (API v5) whose metadata
// language is French lists it: the title in French, French its language.
func frenchMetadata(s map[string]any) {
	if s["id"] != float64(monogatari) {
		return
	}
	s["language"] = map[string]any{"id": 2, "name": "French"}
	s["title"] = "Les Monogatari"
	for _, tr := range s["translations"].([]any) {
		tr := tr.(map[string]any)
		if tr["language"].(map[string]any)["name"] == "French" {
			tr["title"] = "Les Monogatari"
		}
	}
}
