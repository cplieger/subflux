package sonarr_test

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
	"github.com/cplieger/subflux/internal/mediasource/sonarr"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
)

func TestContract(t *testing.T) {
	for _, h := range []*harness{
		{dir: v4Dir, prefix: "/api/v3"},
		{dir: v5Dir, prefix: "/api/v5"},
	} {
		t.Run(h.dir, func(t *testing.T) { sourcetest.Contract(t, h) })
	}
}

// harness renders a sourcetest.Library as the recorded Sonarr answers it,
// each series and episode the recorded Shoah with the library's fields set
// over it, and each library event as a file-imported history record dated
// when the server first answered it.
type harness struct {
	srv     *arrtest.Server
	lib     *sourcetest.Library
	dated   map[uint64]time.Time
	dir     string
	prefix  string
	series  map[string]any
	episode map[string]any
	record  map[string]any
	mu      sync.Mutex
}

func (*harness) Kind() mediasource.AdapterKind { return sonarr.Kind }

func (h *harness) Requests() int                     { return len(h.srv.Requests()) }
func (*harness) NativeID(n int) mediasource.NativeID { return mediasource.NativeID(itoa(n)) }

func (h *harness) Serve(t testing.TB, lib *sourcetest.Library) mediasource.Config {
	h.srv, h.lib, h.dated = arrtest.Load(t, h.dir), lib, map[uint64]time.Time{}
	h.series = recorded(t, h.srv, "series.json", func(s map[string]any) bool { return s["id"] == float64(shoah) })
	h.episode = recorded(t, h.srv, "episode-"+itoa(shoah)+".json", func(e map[string]any) bool { return e["episodeFile"] != nil })
	h.record = recorded(t, h.srv, "history-since.json", func(map[string]any) bool { return true })
	h.srv.Handle(h.prefix+"/series", h.list)
	h.srv.Handle(h.prefix+"/series/", h.one)
	h.srv.Handle(h.prefix+"/episode", h.episodes)
	h.srv.Handle(h.prefix+"/tag", h.tags)
	h.srv.Handle(h.prefix+"/rootfolder", h.folders)
	h.srv.Handle(h.prefix+"/history/since", h.history)
	h.srv.Handle(h.prefix+"/command", func(w http.ResponseWriter, _ *http.Request) { h.srv.Write(w, http.StatusCreated, []byte(`{"id":1}`)) })
	h.srv.Handle(h.prefix+"/mediacover/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Application-Version", h.srv.Version())
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("\xff\xd8\xff\xe0"))
	})
	return arrtest.Config(t, "Sonarr", h.srv.Start(t), "")
}

func (h *harness) Refuse(t testing.TB) mediasource.Config {
	srv := arrtest.Load(t, h.dir)
	srv.Handle("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	return arrtest.Config(t, "Sonarr", srv.Start(t), "")
}

// recorded is a deep copy of the first element of file's array keep picks.
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

func (h *harness) write(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		h.srv.Write(w, http.StatusInternalServerError, nil)
		return
	}
	h.srv.Write(w, http.StatusOK, b)
}

func (h *harness) seriesOf(r *sourcetest.LibRoot) map[string]any {
	s := clone(h.series)
	s["id"], s["title"], s["titleSlug"] = number(string(r.Root.Native.ID)), r.Root.Title, "series-"+string(r.Root.Native.ID)
	tags := []any{}
	if len(r.Items) > 0 {
		for _, tg := range r.Items[0].Tags {
			tags = append(tags, number(string(tg)))
		}
	}
	s["tags"] = tags
	return s
}

func number(id string) int {
	n, _ := strconv.Atoi(id)
	return n
}

func (h *harness) list(w http.ResponseWriter, _ *http.Request) {
	roots, _, _, _ := h.lib.Snapshot()
	out := make([]any, len(roots))
	for i := range roots {
		out[i] = h.seriesOf(&roots[i])
	}
	h.write(w, out)
}

func (h *harness) find(id string) (sourcetest.LibRoot, bool) {
	roots, _, _, _ := h.lib.Snapshot()
	for _, r := range roots {
		if string(r.Root.Native.ID) == id {
			return r, true
		}
	}
	return sourcetest.LibRoot{}, false
}

func (h *harness) one(w http.ResponseWriter, r *http.Request) {
	root, ok := h.find(strings.TrimPrefix(r.URL.Path, h.prefix+"/series/"))
	if !ok {
		h.srv.Write(w, http.StatusNotFound, nil)
		return
	}
	h.write(w, h.seriesOf(&root))
}

func (h *harness) episodes(w http.ResponseWriter, r *http.Request) {
	root, ok := h.find(r.URL.Query().Get("seriesId"))
	switch {
	case !ok:
		h.write(w, []any{})
		return
	case root.ReadFails:
		h.srv.Write(w, http.StatusInternalServerError, nil)
		return
	}
	out := make([]any, len(root.Items))
	for i := range root.Items {
		it := &root.Items[i]
		e := clone(h.episode)
		id := number(string(it.Native))
		e["id"], e["seriesId"], e["title"] = id, number(string(root.Root.Native.ID)), it.Title
		e["seasonNumber"], e["episodeNumber"] = it.SeriesNumbering.Season, it.SeriesNumbering.Episode
		e["episodeFileId"] = id
		f := e["episodeFile"].(map[string]any)
		f["id"], f["seriesId"], f["path"], f["sceneName"] = id, e["seriesId"], it.VideoPath, it.SceneName
		out[i] = e
	}
	h.write(w, out)
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
		rec["id"], rec["seriesId"], rec["date"], rec["eventType"] = ev.Seq, number(string(ev.Root.ID)), at.Format(time.RFC3339), "downloadFolderImported"
		out = append(out, rec)
	}
	h.write(w, out)
}
