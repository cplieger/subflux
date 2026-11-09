package radarr_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
	"github.com/cplieger/subflux/internal/mediasource/radarr"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
)

const prefix = "/api/v3"

func TestContract(t *testing.T) { sourcetest.Contract(t, &harness{}) }

// harness renders a sourcetest.Library as the recorded Radarr answers it,
// each movie the recorded Scrapper with the library's fields set over it,
// and each library event as a folder-imported history record dated when
// the server first answered it. One list read carries every movie, so a
// ReadFails movie is listed with a relative file path, which the Set's path
// rule refuses for that root alone, and its own read answers 500.
type harness struct {
	srv    *arrtest.Server
	lib    *sourcetest.Library
	dated  map[uint64]time.Time
	movie  map[string]any
	record map[string]any
	mu     sync.Mutex
}

func (*harness) Kind() mediasource.AdapterKind { return radarr.Kind }

func (h *harness) Requests() int                     { return len(h.srv.Requests()) }
func (*harness) NativeID(n int) mediasource.NativeID { return mediasource.NativeID(itoa(n)) }

func (h *harness) Serve(t testing.TB, lib *sourcetest.Library) mediasource.Config {
	h.srv, h.lib, h.dated = arrtest.Load(t, dir), lib, map[uint64]time.Time{}
	h.movie = recorded(t, h.srv, "movie.json", func(m map[string]any) bool { return m["id"] == float64(scrapper) })
	h.record = recorded(t, h.srv, "history-since.json", func(map[string]any) bool { return true })
	h.srv.Handle(prefix+"/movie", h.list)
	h.srv.Handle(prefix+"/movie/", h.one)
	h.srv.Handle(prefix+"/tag", h.tags)
	h.srv.Handle(prefix+"/rootfolder", h.folders)
	h.srv.Handle(prefix+"/history/since", h.history)
	h.srv.Handle(prefix+"/command", func(w http.ResponseWriter, _ *http.Request) { h.srv.Write(w, http.StatusCreated, []byte(`{"id":1}`)) })
	h.srv.Handle(prefix+"/mediacover/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Application-Version", h.srv.Version())
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("\xff\xd8\xff\xe0"))
	})
	return arrtest.Config(t, "Radarr", h.srv.Start(t), "")
}

func (*harness) Refuse(t testing.TB) mediasource.Config {
	srv := arrtest.Load(t, dir)
	srv.Handle("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	return arrtest.Config(t, "Radarr", srv.Start(t), "")
}

// recorded is the first element of file's array keep picks.
func recorded(t testing.TB, srv *arrtest.Server, file string, keep func(map[string]any) bool) map[string]any {
	t.Helper()
	var all []map[string]any
	if err := json.Unmarshal(srv.Body(file), &all); err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		if keep(e) {
			return e
		}
	}
	t.Fatalf("%s holds no template", file)
	return nil
}

func clone(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func number(id string) int {
	n, _ := strconv.Atoi(id)
	return n
}

func (h *harness) write(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		h.srv.Write(w, http.StatusInternalServerError, nil)
		return
	}
	h.srv.Write(w, http.StatusOK, b)
}

func (h *harness) movieOf(r *sourcetest.LibRoot) map[string]any {
	m := clone(h.movie)
	id := number(string(r.Root.Native.ID))
	m["id"], m["title"], m["tmdbId"], m["titleSlug"] = id, r.Root.Title, id, itoa(id)
	tags := []any{}
	m["hasFile"] = false
	delete(m, "movieFile")
	if len(r.Items) > 0 {
		it := &r.Items[0]
		for _, tg := range it.Tags {
			tags = append(tags, number(string(tg)))
		}
		if it.VideoPath != "" {
			path := it.VideoPath
			if r.ReadFails {
				path = strings.TrimPrefix(path, "/")
			}
			f := clone(h.movie["movieFile"].(map[string]any))
			f["id"], f["movieId"], f["path"], f["sceneName"] = id, id, path, it.SceneName
			m["movieFile"], m["hasFile"] = f, true
		}
	}
	m["tags"] = tags
	return m
}

func (h *harness) list(w http.ResponseWriter, _ *http.Request) {
	roots, _, _, _ := h.lib.Snapshot()
	out := make([]any, len(roots))
	for i := range roots {
		out[i] = h.movieOf(&roots[i])
	}
	h.write(w, out)
}

// one answers a movie's own read; ReadFails fails it, though the list
// read every walk makes still carries the movie.
func (h *harness) one(w http.ResponseWriter, r *http.Request) {
	roots, _, _, _ := h.lib.Snapshot()
	id := strings.TrimPrefix(r.URL.Path, prefix+"/movie/")
	for i := range roots {
		if string(roots[i].Root.Native.ID) != id {
			continue
		}
		if roots[i].ReadFails {
			h.srv.Write(w, http.StatusInternalServerError, nil)
			return
		}
		h.write(w, h.movieOf(&roots[i]))
		return
	}
	h.srv.Write(w, http.StatusNotFound, nil)
}

func (h *harness) tags(w http.ResponseWriter, _ *http.Request) {
	_, tags, _, _ := h.lib.Snapshot()
	out := []any{}
	for id, label := range tags {
		out = append(out, map[string]any{"id": number(string(id)), "label": label})
	}
	h.write(w, out)
}

func (h *harness) folders(w http.ResponseWriter, _ *http.Request) {
	_, _, folders, _ := h.lib.Snapshot()
	out := make([]any, len(folders))
	for i, p := range folders {
		out[i] = map[string]any{"id": i + 1, "path": p, "accessible": true}
	}
	h.write(w, out)
}

func (h *harness) history(w http.ResponseWriter, r *http.Request) {
	since, err := time.Parse(time.RFC3339, r.URL.Query().Get("date"))
	if err != nil {
		h.srv.Write(w, http.StatusBadRequest, nil)
		return
	}
	_, _, _, events := h.lib.Snapshot()
	now := time.Now().UTC().Truncate(time.Second)
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []any{}
	for _, ev := range events {
		at, ok := h.dated[ev.Seq]
		if !ok {
			at = now
			h.dated[ev.Seq] = at
		}
		if at.Before(since) {
			continue
		}
		rec := clone(h.record)
		rec["id"], rec["movieId"], rec["date"], rec["eventType"] = ev.Seq, number(string(ev.Root.ID)), at.Format(time.RFC3339), "downloadFolderImported"
		out = append(out, rec)
	}
	h.write(w, out)
}
