package radarr_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
	"github.com/cplieger/subflux/internal/mediasource/radarr"
)

// The recorded server; testdata/README.md says how it was recorded.
const (
	dir  = "testdata/api-v3"
	base = "http://radarr.test"

	totoro, fastForever, scrapper = 152, 2807, 3017
)

func TestFixturesHoldNoSecret(t *testing.T) { arrtest.HoldsNoSecret(t, "testdata") }

func TestHistoryNamesRecordedRoots(t *testing.T) {
	arrtest.HistoryNamesListed(t, "testdata", "movie.json", "movieId")
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
func adapter(t *testing.T, srv *arrtest.Server, publicURL string) (*radarr.Adapter, *logs) {
	t.Helper()
	l := &logs{}
	a, err := radarr.Kind.New(arrtest.Config(t, "Radarr", base, publicURL), mediasource.Deps{HTTP: srv.Client(), Logger: slog.New(slog.NewTextHandler(l, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return a.(*radarr.Adapter), l
}

func first[A, B any](a A, _ B) A { return a }

func itoa(n int) string { return strconv.Itoa(n) }

func ref(id int) mediasource.NativeRef {
	return mediasource.NativeRef{ID: mediasource.NativeID(itoa(id)), Kind: media.Movie}
}

// movie is one root Items yielded and its items.
type movie struct {
	root  mediasource.Root
	items []mediasource.Item
}

func walkItems(t *testing.T, a mediasource.Adapter) (map[int]movie, []int, error) {
	t.Helper()
	got := map[int]movie{}
	var order []int
	err := a.Items(t.Context(), func(r mediasource.Root, items []mediasource.Item) error {
		id, err := strconv.Atoi(string(r.Native.ID))
		if err != nil {
			t.Fatalf("native id %q is not decimal", r.Native.ID)
		}
		got[id] = movie{root: r, items: items}
		order = append(order, id)
		return nil
	}, func(ref mediasource.NativeRef, err *mediasource.SourceError) error {
		t.Errorf("the Radarr adapter reported %v unread: %v", ref, err)
		return nil
	})
	return got, order, err
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
