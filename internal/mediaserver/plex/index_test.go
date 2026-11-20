package plex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/plexapi/v3"
	"github.com/cplieger/subflux/internal/mediaserver"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

const (
	showPath  = "/media/tv/Show/S01E01.mkv"
	moviePath = "/media/movies/Film (2001)/Film.mkv"
)

// library is a fake Plex with one show and one movie section.
func library(t testing.TB) *fakePlex {
	t.Helper()
	f := newFakePlex()
	f.addSection("2", "show")
	f.addSection("1", "movie")
	f.addSection("15", "artist")
	now := time.Now().Unix()
	f.put("2", row("101", "episode", now-3600, now-3600, showPath))
	f.put("1", row("201", "movie", now-3600, now-3600, moviePath))
	return f
}

// plainListing reports a full section walk, as a build sends, apart from a
// merge's updated-since read.
func plainListing(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, "/all") && r.URL.Query().Get("sort") == ""
}

// locate is a's index lookup of path as its reads run it, under d.
func locate(t *testing.T, a *adapter, path string, d time.Duration) ([]string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	return run(ctx, a, func(ctx context.Context) ([]string, error) { return a.locate(ctx, keyOf(path)) })
}

func TestLocateBuildsThenAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		a := inProcess(t, f)
		for path, want := range map[string]string{showPath: "101", moviePath: "201"} {
			if keys, err := locate(t, a, path, time.Minute); err != nil || !slices.Equal(keys, []string{want}) {
				t.Errorf("locate(%s) = %v %v, want [%s]", path, keys, err, want)
			}
		}
		if keys, err := locate(t, a, "/media/other.mkv", time.Minute); err != nil || len(keys) != 0 {
			t.Errorf("locate of an absent path = %v %v, want empty", keys, err)
		}
		if n := f.count("GET /library/sections/15/"); n != 0 {
			t.Errorf("the artist section was read %d times, want none", n)
		}
		if n := f.countExact("GET /library/sections"); n != 1 {
			t.Errorf("%d section directory reads, want the one build's", n)
		}
	})
}

func (f *fakePlex) countExact(req string) int {
	n := 0
	for _, r := range f.log() {
		if r == req {
			n++
		}
	}
	return n
}

func TestTwoEpisodeFileLocatesBoth(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFakePlex()
		f.addSection("2", "show")
		b := recordedAll(t, "two-episode-file")
		for _, r := range b {
			f.put("2", r)
		}
		a := inProcess(t, f)
		keys, err := locate(t, a, files(b[0])[0], time.Minute)
		if slices.Sort(keys); err != nil || !slices.Equal(keys, []string{"634951", "634952"}) {
			t.Errorf("locate = %v %v, want both episodes of the file", keys, err)
		}
	})
}

// TestLabelsDuringFirstBuild: the first build runs on its own context, so a
// Labels call bounded to 5 s reports the server as building while the 30 s
// build goes on, and the next call answers.
func TestLabelsDuringFirstBuild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFakePlex()
		f.addSection("1", "movie")
		movie := recorded(t, "movie-two-sidecars")
		f.put("1", movie)
		f.hook = func(r *http.Request) int {
			if plainListing(r) {
				time.Sleep(30 * time.Second)
			}
			return 0
		}
		a := inProcess(t, f)
		set := setOver(t, a)
		got, err := set.Labels(t.Context(), files(movie)[0])
		if err != nil || len(got) != 1 || !strings.Contains(got[0].Err, "still being built") {
			t.Fatalf("Labels during the build = %+v %v, want the server reported building", got, err)
		}
		time.Sleep(30 * time.Second)
		synctest.Wait()
		got, err = set.Labels(t.Context(), files(movie)[0])
		if err != nil || len(got) != 1 || got[0].Err != "" || len(got[0].Labels) != 2 {
			t.Errorf("Labels after the build = %+v %v, want the two sidecars", got, err)
		}
		if n := f.count("GET /library/sections/1/all"); n != 1 {
			t.Errorf("%d section walks, want the one build's", n)
		}
	})
}

// setOver is a server Set holding a under id plex.
func setOver(t testing.TB, a *adapter) *mediaserver.Set {
	t.Helper()
	st, err := status.New(t.Context(), statustest.NewMemPersister())
	if err != nil {
		t.Fatal(err)
	}
	kind := mediaserver.AdapterKind{Type: "plex", Schema: schema, New: func(mediaserver.Config, mediasource.Deps) (mediaserver.Adapter, error) { return a, nil }}
	s, err := mediaserver.NewSet([]mediaserver.AdapterKind{kind}, []mediaserver.Config{{ID: "plex", Type: "plex"}}, mediasource.Deps{}, st)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCancelledCallerDoesNotCancelBuild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		f.hook = func(r *http.Request) int {
			if plainListing(r) {
				time.Sleep(10 * time.Second)
			}
			return 0
		}
		a := inProcess(t, f)
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(time.Second, cancel)
		if _, err := run(ctx, a, func(ctx context.Context) ([]string, error) { return a.locate(ctx, keyOf(showPath)) }); !errors.Is(err, errIndexBuilding) || !httpclient.IsTransient(err) {
			t.Errorf("locate on a cancelled caller = %v, want the transient building error", err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		f.hook = nil
		if keys, err := locate(t, a, showPath, time.Second); err != nil || !slices.Equal(keys, []string{"101"}) {
			t.Errorf("locate after the build = %v %v, want [101] from the build the caller left", keys, err)
		}
		if n := f.countExact("GET /library/sections"); n != 1 {
			t.Errorf("%d builds, want one", n)
		}
	})
}

func TestHitDuringRebuildAnswersAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		a := inProcess(t, f)
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(16 * time.Minute)
		release := make(chan struct{})
		f.hook = func(r *http.Request) int {
			if plainListing(r) {
				<-release
			}
			return 0
		}
		if _, err := locate(t, a, "/media/absent.mkv", time.Second); !errors.Is(err, errIndexBuilding) {
			t.Fatalf("a stale miss = %v, want it waiting on the rebuild it started", err)
		}
		start := time.Now()
		keys, err := locate(t, a, moviePath, time.Minute)
		if err != nil || !slices.Equal(keys, []string{"201"}) || time.Since(start) != 0 {
			t.Errorf("a hit during the rebuild = %v %v after %v, want [201] at once", keys, err, time.Since(start))
		}
		close(release)
	})
}

func TestFailedRebuildKeepsOldIndex(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		a := inProcess(t, f)
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(16 * time.Minute)
		f.hook = func(r *http.Request) int {
			if r.URL.Path == "/library/sections" {
				return http.StatusInternalServerError
			}
			return 0
		}
		if keys, err := locate(t, a, "/media/absent.mkv", time.Minute); err != nil || len(keys) != 0 {
			t.Errorf("a miss whose rebuild fails = %v %v, want empty", keys, err)
		}
		if n := f.countExact("GET /library/sections"); n != 2 {
			t.Fatalf("%d builds, want the first and the failed rebuild", n)
		}
		if keys, err := locate(t, a, showPath, time.Minute); err != nil || !slices.Equal(keys, []string{"101"}) {
			t.Errorf("locate after the failed rebuild = %v %v, want the old index's [101]", keys, err)
		}
	})
}

func TestStaleMissRebuildOncePerHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		a := inProcess(t, f)
		builds := func() int { return f.countExact("GET /library/sections") }
		miss := func() {
			t.Helper()
			if _, err := locate(t, a, "/media/absent.mkv", time.Minute); err != nil {
				t.Fatal(err)
			}
		}
		miss()
		time.Sleep(14 * time.Minute)
		if miss(); builds() != 1 {
			t.Errorf("%d builds after a miss on a 14-minute index, want 1", builds())
		}
		time.Sleep(2 * time.Minute)
		if miss(); builds() != 2 {
			t.Errorf("%d builds after a miss on a 16-minute index, want 2", builds())
		}
		time.Sleep(30 * time.Minute)
		if miss(); builds() != 2 {
			t.Errorf("%d builds after a second stale miss within the hour, want 2", builds())
		}
		time.Sleep(31 * time.Minute)
		if miss(); builds() != 3 {
			t.Errorf("%d builds after a stale miss an hour on, want 3", builds())
		}
	})
}

func TestFailedFirstBuildRetriesAfterAMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		down := true
		f.hook = func(r *http.Request) int {
			if down {
				return http.StatusServiceUnavailable
			}
			return 0
		}
		a := inProcess(t, f)
		_, err := locate(t, a, showPath, time.Minute)
		if !httpclient.IsTransient(err) || !strings.Contains(err.Error(), "could not be built") {
			t.Errorf("locate after a failed first build = %v, want a transient build failure", err)
		}
		down = false
		if _, err := locate(t, a, showPath, time.Minute); err == nil || f.countExact("GET /library/sections") != 1 {
			t.Errorf("locate within a minute = %v after %d builds, want the failure again and no new build", err, f.countExact("GET /library/sections"))
		}
		time.Sleep(time.Minute)
		if keys, err := locate(t, a, showPath, time.Minute); err != nil || !slices.Equal(keys, []string{"101"}) {
			t.Errorf("locate a minute on = %v %v, want [101] from a new build", keys, err)
		}
	})
}

// TestOldAdapterBuildCancelledOnRebuild: a replaced adapter's build must
// stop walking when its Shutdown returns, not run on to its 5-minute bound.
func TestOldAdapterBuildCancelledOnRebuild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		for _, k := range []string{"3", "4", "5", "6", "7"} {
			f.addSection(k, "show")
		}
		f.hook = func(r *http.Request) int {
			if plainListing(r) {
				select {
				case <-time.After(time.Minute):
				case <-r.Context().Done():
				}
			}
			return 0
		}
		a := inProcess(t, f)
		if _, err := locate(t, a, showPath, time.Second); !errors.Is(err, errIndexBuilding) {
			t.Fatalf("locate = %v, want the build still running", err)
		}
		start := time.Now()
		if err := a.Shutdown(t.Context()); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		if waited := time.Since(start); waited != 0 {
			t.Errorf("Shutdown waited %v, want the build cancelled at once", waited)
		}
		sent := len(f.log())
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		if after := f.log()[sent:]; len(after) != 0 {
			t.Errorf("requests after Shutdown returned: %v", after)
		}
		if _, err := locate(t, a, showPath, time.Minute); !errors.Is(err, errShutDown) || !httpclient.IsTransient(err) {
			t.Errorf("locate after Shutdown = %v, want the transient shut-down error", err)
		}
	})
}

// TestFailedStaleRebuildRetriesAfterAMinute: the hour between miss-driven
// rebuilds counts from one that succeeded; a failed one is retried a minute
// on, like any failed build.
func TestFailedStaleRebuildRetriesAfterAMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		a := inProcess(t, f)
		builds := func() int { return f.countExact("GET /library/sections") }
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(16 * time.Minute)
		var down atomic.Bool
		down.Store(true)
		f.hook = func(r *http.Request) int {
			if down.Load() && r.URL.Path == "/library/sections" {
				return http.StatusInternalServerError
			}
			return 0
		}
		miss := func() {
			t.Helper()
			time.Sleep(deltaReuse)
			if _, err := locate(t, a, "/media/absent.mkv", time.Minute); err != nil {
				t.Fatal(err)
			}
		}
		if miss(); builds() != 2 {
			t.Fatalf("%d builds after a stale miss, want the first and the failed rebuild", builds())
		}
		down.Store(false)
		if miss(); builds() != 2 {
			t.Errorf("%d builds after a miss within a minute of the failure, want 2", builds())
		}
		time.Sleep(time.Minute)
		if miss(); builds() != 3 {
			t.Errorf("%d builds after a miss a minute after the failure, want 3 (retried)", builds())
		}
		time.Sleep(16 * time.Minute)
		if miss(); builds() != 3 {
			t.Errorf("%d builds after a stale miss within the hour of a rebuild that succeeded, want 3", builds())
		}
	})
}

// TestLocateMissRacingShutdownAnswersShutDown: Shutdown cancels a miss's
// merge and waits for it, so the miss answers the transient shut-down error,
// never an empty success, and no request follows Shutdown's return.
func TestLocateMissRacingShutdownAnswersShutDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := library(t)
		a := inProcess(t, f)
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		release := make(chan struct{})
		f.hook = func(r *http.Request) int {
			if r.URL.Query().Get("sort") == "updatedAt:asc" {
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}
			return 0
		}
		done := make(chan error, 1)
		go func() {
			_, err := locate(t, a, "/media/absent.mkv", time.Hour)
			done <- err
		}()
		synctest.Wait()
		start := time.Now()
		if err := a.Shutdown(t.Context()); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		if waited := time.Since(start); waited != 0 {
			t.Errorf("Shutdown waited %v, want the merge cancelled at once", waited)
		}
		sent := len(f.log())
		close(release)
		if err := <-done; !errors.Is(err, errShutDown) || !httpclient.IsTransient(err) {
			t.Errorf("a miss racing Shutdown = %v, want the transient shut-down error", err)
		}
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		if after := f.log()[sent:]; len(after) != 0 {
			t.Errorf("requests after Shutdown returned: %v", after)
		}
	})
}

// sidecarRow is an episode row over file whose one sidecar is displayed as
// "item <key>", so a Labels answer names the item it was read from.
func sidecarRow(key string, updatedAt int64, file string) map[string]any {
	r := row(key, "episode", updatedAt, updatedAt, file)
	r["Media"].([]any)[0].(map[string]any)["Part"].([]any)[0].(map[string]any)["Stream"] = []any{map[string]any{
		"id": 1, "streamType": 3, "key": "/library/streams/" + key, "languageTag": "en", "displayTitle": "item " + key,
	}}
	return r
}

// staleLibrary is library with item 101's part carrying its sidecar.
func staleLibrary(t testing.TB) *fakePlex {
	t.Helper()
	f := library(t)
	f.put("2", sidecarRow("101", time.Now().Unix()-3600, showPath))
	return f
}

// indexRead is one read answering from the index: the keys of the items it
// answered for, from Labels' sidecars or Refresh's requests.
type indexRead struct {
	name string
	do   func(ctx context.Context, a *adapter, f *fakePlex, path string) ([]string, error)
}

var indexReads = []indexRead{
	{"Labels", func(ctx context.Context, a *adapter, _ *fakePlex, path string) ([]string, error) {
		ls, err := a.Labels(ctx, path)
		var keys []string
		for _, l := range ls {
			keys = append(keys, strings.TrimPrefix(l.Display, "item "))
		}
		return keys, err
	}},
	{"Refresh", func(ctx context.Context, a *adapter, f *fakePlex, path string) ([]string, error) {
		sent := len(f.log())
		err := a.Refresh(ctx, path)
		var keys []string
		for _, r := range f.log()[sent:] {
			if k, ok := strings.CutPrefix(r, "PUT /library/metadata/"); ok {
				keys = append(keys, strings.TrimSuffix(k, "/refresh"))
			}
		}
		return keys, err
	}},
}

// eachRead runs body in a synctest bubble once per index read.
func eachRead(t *testing.T, body func(t *testing.T, rd indexRead)) {
	for _, rd := range indexReads {
		t.Run(rd.name, func(t *testing.T) { synctest.Test(t, func(t *testing.T) { body(t, rd) }) })
	}
}

func (a *adapter) generation() uint64 {
	a.idx.mu.Lock()
	defer a.idx.mu.Unlock()
	return a.idx.gen
}

// indexed reports whether the index holds key.
func (a *adapter) indexed(key string) bool {
	a.idx.mu.Lock()
	defer a.idx.mu.Unlock()
	_, ok := a.idx.cur.byKey[key]
	return ok
}

func indexedAt(t *testing.T, a *adapter, path string) []string {
	t.Helper()
	keys, _, err := a.lookup(keyOf(path))
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// TestStaleKeyReAddedUnderANewKey: Plex deleted item 101 and re-added its
// file as 102; the index still answers 101, alone on a hit or beside 102
// after another path's merge. Each read answers 102 and drops 101.
func TestStaleKeyReAddedUnderANewKey(t *testing.T) {
	for _, beside := range []bool{false, true} {
		t.Run(map[bool]string{false: "alone", true: "beside"}[beside], func(t *testing.T) {
			eachRead(t, func(t *testing.T, rd indexRead) {
				f := staleLibrary(t)
				a := inProcess(t, f)
				if _, err := locate(t, a, showPath, time.Minute); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Minute)
				f.remove("101")
				f.put("2", sidecarRow("102", time.Now().Unix(), showPath))
				want := []string{"101"}
				if beside {
					if _, err := locate(t, a, "/media/absent.mkv", time.Minute); err != nil {
						t.Fatal(err)
					}
					want = []string{"101", "102"}
				}
				if got := indexedAt(t, a, showPath); !slices.Equal(got, want) {
					t.Fatalf("the index holds %v for the path, want %v", got, want)
				}
				got, err := rd.do(t.Context(), a, f, showPath)
				if err != nil || !slices.Equal(got, []string{"102"}) {
					t.Errorf("%s = %v %v, want the re-added item 102", rd.name, got, err)
				}
				if got := indexedAt(t, a, showPath); !slices.Equal(got, []string{"102"}) || a.indexed("101") {
					t.Errorf("the index holds %v for the path (101 kept: %v), want [102] alone", got, a.indexed("101"))
				}
			})
		})
	}
}

// TestStaleKeyWithNoHolder: the one located item is gone from Plex, or no
// longer holds the path; each read locates again, misses and answers none,
// never a not-found and never the stale item.
func TestStaleKeyWithNoHolder(t *testing.T) {
	const moved = "/media/tv/Show/S01E01 [2160p].mkv"
	for name, change := range map[string]func(f *fakePlex){
		"deleted":            func(f *fakePlex) { f.remove("101") },
		"moved off the path": func(f *fakePlex) { f.put("2", sidecarRow("101", time.Now().Unix(), moved)) },
	} {
		t.Run(name, func(t *testing.T) {
			eachRead(t, func(t *testing.T, rd indexRead) {
				f := staleLibrary(t)
				a := inProcess(t, f)
				if _, err := locate(t, a, showPath, time.Minute); err != nil {
					t.Fatal(err)
				}
				change(f)
				got, err := rd.do(t.Context(), a, f, showPath)
				if err != nil || len(got) != 0 {
					t.Errorf("%s = %v %v, want none and a success", rd.name, got, err)
				}
				if got := indexedAt(t, a, showPath); len(got) != 0 {
					t.Errorf("the index still holds %v for the path", got)
				}
				if name == "moved off the path" && !slices.Equal(indexedAt(t, a, moved), []string{"101"}) {
					t.Errorf("the index holds %v for the moved path, want Plex's answer [101]", indexedAt(t, a, moved))
				}
			})
		})
	}
}

// TestStaleKeyDropRacingMergeCommit: a merge whose walk listed 101 before
// Plex deleted it commits after a read dropped 101; the commit must not put
// it back.
func TestStaleKeyDropRacingMergeCommit(t *testing.T) {
	eachRead(t, func(t *testing.T, rd indexRead) {
		f := staleLibrary(t)
		a := inProcess(t, f)
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		base := time.Now().Unix() + 10
		f.put("2", sidecarRow("101", base, showPath))
		for i := range pageSize {
			f.put("2", row(strconv.Itoa(1000+i), "episode", base, base+1+int64(i), fmt.Sprintf("/media/tv/Big/E%03d.mkv", i)))
		}
		pageGate := make(chan struct{})
		var pages atomic.Int32
		f.hook = func(r *http.Request) int {
			if r.URL.Path == "/library/sections/2/all" && r.URL.Query().Get("sort") == "updatedAt:asc" && pages.Add(1) == 2 {
				select {
				case <-pageGate:
				case <-r.Context().Done():
				}
			}
			return 0
		}
		time.Sleep(deltaReuse)
		merged := make(chan error, 1)
		go func() {
			_, err := locate(t, a, "/media/absent.mkv", time.Hour)
			merged <- err
		}()
		synctest.Wait()
		f.remove("101")
		answered := make(chan error, 1)
		var got []string
		go func() {
			var err error
			got, err = rd.do(t.Context(), a, f, showPath)
			answered <- err
		}()
		synctest.Wait()
		close(pageGate)
		if err := <-merged; err != nil {
			t.Fatal(err)
		}
		if err := <-answered; err != nil || len(got) != 0 {
			t.Errorf("%s = %v %v, want none and a success", rd.name, got, err)
		}
		if keys := indexedAt(t, a, showPath); len(keys) != 0 || a.indexed("101") {
			t.Errorf("the merge put 101 back: the path holds %v", keys)
		}
		if n := f.countExact("GET /library/metadata/101"); n != 1 {
			t.Errorf("101 read %d times, want once", n)
		}
	})
}

// TestStaleKeyDropRacingRebuildFinish: a rebuild that listed 101 before Plex
// deleted it finishes after a read dropped 101 from the old index; the new
// index leaves 101 out and keeps what else the rebuild found.
func TestStaleKeyDropRacingRebuildFinish(t *testing.T) {
	eachRead(t, func(t *testing.T, rd indexRead) {
		f := staleLibrary(t)
		a := inProcess(t, f)
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(16 * time.Minute)
		buildGate := make(chan struct{})
		f.hook = func(r *http.Request) int {
			if plainListing(r) && r.URL.Path == "/library/sections/1/all" {
				select {
				case <-buildGate:
				case <-r.Context().Done():
				}
			}
			return 0
		}
		if _, err := locate(t, a, "/media/absent.mkv", time.Second); !errors.Is(err, errIndexBuilding) {
			t.Fatalf("stale miss = %v, want it waiting on the rebuild", err)
		}
		synctest.Wait()
		f.remove("101")
		got, err := rd.do(t.Context(), a, f, showPath)
		if err != nil || len(got) != 0 {
			t.Errorf("%s during the rebuild = %v %v, want none and a success", rd.name, got, err)
		}
		gen := a.generation()
		close(buildGate)
		synctest.Wait()
		if a.generation() == gen {
			t.Fatal("the rebuild did not finish")
		}
		if keys := indexedAt(t, a, showPath); len(keys) != 0 || a.indexed("101") {
			t.Errorf("the rebuild put 101 back: the path holds %v", keys)
		}
		if keys := indexedAt(t, a, moviePath); !slices.Equal(keys, []string{"201"}) {
			t.Errorf("the rebuild's movie path holds %v, want [201]", keys)
		}
	})
}

// TestStaleKeyRelocateRacingShutdown: Shutdown cancels a read's re-locate
// after a dropped key, so the read answers the transient shut-down error and
// no request follows Shutdown's return.
func TestStaleKeyRelocateRacingShutdown(t *testing.T) {
	eachRead(t, func(t *testing.T, rd indexRead) {
		f := staleLibrary(t)
		a := inProcess(t, f)
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		f.remove("101")
		release := make(chan struct{})
		f.hook = func(r *http.Request) int {
			if r.URL.Query().Get("sort") == "updatedAt:asc" {
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}
			return 0
		}
		answered := make(chan error, 1)
		go func() {
			_, err := rd.do(t.Context(), a, f, showPath)
			answered <- err
		}()
		synctest.Wait()
		if n := f.countExact("GET /library/metadata/101"); n != 1 {
			t.Fatalf("101 read %d times before the re-locate, want once", n)
		}
		start := time.Now()
		if err := a.Shutdown(t.Context()); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		if waited := time.Since(start); waited != 0 {
			t.Errorf("Shutdown waited %v, want the re-locate cancelled at once", waited)
		}
		sent := len(f.log())
		close(release)
		if err := <-answered; !errors.Is(err, errShutDown) || !httpclient.IsTransient(err) {
			t.Errorf("%s racing Shutdown = %v, want the transient shut-down error", rd.name, err)
		}
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		if after := f.log()[sent:]; len(after) != 0 {
			t.Errorf("requests after Shutdown returned: %v", after)
		}
	})
}

// TestStaleKeyDropAfterFailedRebuild: the old index a failed rebuild kept
// takes the drop, and the drop leaves the failure's retry state alone: the
// re-locate starts no build within buildRetry, and one a minute on does.
func TestStaleKeyDropAfterFailedRebuild(t *testing.T) {
	eachRead(t, func(t *testing.T, rd indexRead) {
		f := staleLibrary(t)
		a := inProcess(t, f)
		builds := func() int { return f.countExact("GET /library/sections") }
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(16 * time.Minute)
		var down atomic.Bool
		down.Store(true)
		f.hook = func(r *http.Request) int {
			if down.Load() && r.URL.Path == "/library/sections" {
				return http.StatusInternalServerError
			}
			return 0
		}
		if _, err := locate(t, a, "/media/absent.mkv", time.Minute); err != nil || builds() != 2 {
			t.Fatalf("stale miss = %v after %d builds, want the failed rebuild", err, builds())
		}
		a.idx.mu.Lock()
		failedAt, missRebuild, buildErr := a.idx.failedAt, a.idx.missRebuild, a.idx.buildErr
		a.idx.mu.Unlock()
		f.remove("101")
		time.Sleep(deltaReuse)
		got, err := rd.do(t.Context(), a, f, showPath)
		if err != nil || len(got) != 0 {
			t.Errorf("%s = %v %v, want none and a success", rd.name, got, err)
		}
		if keys := indexedAt(t, a, showPath); len(keys) != 0 || a.indexed("101") {
			t.Errorf("the kept index holds %v for the path, want 101 dropped", keys)
		}
		a.idx.mu.Lock()
		same := a.idx.failedAt.Equal(failedAt) && a.idx.missRebuild.Equal(missRebuild) && a.idx.buildErr == buildErr
		a.idx.mu.Unlock()
		if !same || builds() != 2 {
			t.Errorf("the drop changed the failure's retry state (unchanged %v) or started a build (%d builds)", same, builds())
		}
		down.Store(false)
		time.Sleep(time.Minute)
		if _, err := locate(t, a, "/media/absent.mkv", time.Minute); err != nil || builds() != 3 {
			t.Errorf("a stale miss a minute on = %v after %d builds, want the retried rebuild", err, builds())
		}
	})
}

// TestRefreshNotFoundDropsTheKey: an item Plex deletes between the read and
// its refresh answers the refresh not-found; Refresh drops it and succeeds.
func TestRefreshNotFoundDropsTheKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := staleLibrary(t)
		a := inProcess(t, f)
		if _, err := locate(t, a, showPath, time.Minute); err != nil {
			t.Fatal(err)
		}
		f.hook = func(r *http.Request) int {
			if r.Method == http.MethodPut {
				return http.StatusNotFound
			}
			return 0
		}
		if err := a.Refresh(t.Context(), showPath); err != nil {
			t.Errorf("Refresh = %v, want a success", err)
		}
		if keys := indexedAt(t, a, showPath); len(keys) != 0 || a.indexed("101") {
			t.Errorf("the index holds %v for the path, want 101 dropped", keys)
		}
	})
}

// TestCorrectionOrdering: of a correction and a walk over one key, the
// later answer wins in either order, and a not-found keeps the key out of
// every walk that may have listed it before Plex removed it.
func TestCorrectionOrdering(t *testing.T) {
	const pathA, pathB = "/media/a.mkv", "/media/b.mkv"
	item := func(updated int64, file string) plexapi.Item {
		return plexapi.Item{RatingKey: "7", UpdatedAt: updated, Media: []plexapi.Media{{Part: []plexapi.Part{{File: file}}}}}
	}
	start := time.Unix(1000, 0)
	fresh := func(items ...plexapi.Item) *built {
		b := &built{byPath: map[pathKey][]string{}, byKey: map[string]entry{}, sections: []section{{key: "2"}}}
		for i := range items {
			b.put(&items[i])
		}
		return b
	}
	newIndex := func() *index {
		ix := &index{gone: map[string]goneKey{}}
		ix.finish(start, fresh(item(100, pathA)), nil)
		return ix
	}
	at := func(ix *index, p string) []string { return ix.cur.byPath[keyOf(p)] }
	t.Run("an older correction after a commit", func(t *testing.T) {
		ix := newIndex()
		ix.commit("2", []plexapi.Item{item(200, pathB)}, 200, ix.gen)
		ix.correct("7", new(item(150, pathA)), start.Add(time.Second))
		if len(at(ix, pathA)) != 0 || !slices.Equal(at(ix, pathB), []string{"7"}) {
			t.Errorf("a = %v, b = %v; want the commit's newer b kept", at(ix, pathA), at(ix, pathB))
		}
	})
	t.Run("an older commit after a correction", func(t *testing.T) {
		ix := newIndex()
		ix.correct("7", new(item(200, pathB)), start.Add(time.Second))
		ix.commit("2", []plexapi.Item{item(150, pathA)}, 150, ix.gen)
		if len(at(ix, pathA)) != 0 || !slices.Equal(at(ix, pathB), []string{"7"}) {
			t.Errorf("a = %v, b = %v; want the correction's newer b kept", at(ix, pathA), at(ix, pathB))
		}
	})
	t.Run("an answer with no updatedAt", func(t *testing.T) {
		ix := newIndex()
		ix.correct("7", new(item(0, pathB)), start.Add(time.Second))
		if len(at(ix, pathA)) != 0 || !slices.Equal(at(ix, pathB), []string{"7"}) {
			t.Errorf("a = %v, b = %v; want the unordered answer taken", at(ix, pathA), at(ix, pathB))
		}
	})
	t.Run("a commit after a not-found", func(t *testing.T) {
		ix := newIndex()
		ix.correct("7", nil, start.Add(time.Second))
		ix.commit("2", []plexapi.Item{item(150, pathA)}, 150, ix.gen)
		if len(at(ix, pathA)) != 0 {
			t.Errorf("the commit put the not-found key back: %v", at(ix, pathA))
		}
	})
	t.Run("a not-found older than the index's build", func(t *testing.T) {
		ix := newIndex()
		ix.correct("7", nil, start.Add(-time.Second))
		if !slices.Equal(at(ix, pathA), []string{"7"}) || len(ix.gone) != 0 {
			t.Errorf("a = %v, gone %v; want the build's newer listing kept", at(ix, pathA), ix.gone)
		}
	})
	t.Run("a not-found across builds", func(t *testing.T) {
		ix := newIndex()
		ix.correct("7", nil, start.Add(time.Second))
		ix.finish(start, fresh(item(100, pathA)), nil)
		if len(at(ix, pathA)) != 0 {
			t.Errorf("a build started before the not-found put the key back: %v", at(ix, pathA))
		}
		ix.finish(start.Add(time.Hour), fresh(item(100, pathA)), errors.New("down"))
		if _, kept := ix.gone["7"]; !kept || len(at(ix, pathA)) != 0 {
			t.Errorf("a failed build changed the drop (kept %v, a = %v)", kept, at(ix, pathA))
		}
		ix.finish(start.Add(time.Hour), fresh(item(100, pathA)), nil)
		if _, kept := ix.gone["7"]; kept || !slices.Equal(at(ix, pathA), []string{"7"}) {
			t.Errorf("a build started after the not-found: gone kept %v, a = %v; want the walk taken", kept, at(ix, pathA))
		}
	})
	t.Run("an answer after a not-found", func(t *testing.T) {
		ix := newIndex()
		ix.correct("7", nil, start.Add(time.Second))
		ix.correct("7", new(item(300, pathB)), start.Add(2*time.Second))
		if _, kept := ix.gone["7"]; kept || !slices.Equal(at(ix, pathB), []string{"7"}) {
			t.Errorf("gone kept %v, b = %v; want Plex's later answer taken", kept, at(ix, pathB))
		}
	})
	t.Run("an earlier read's answer after a not-found", func(t *testing.T) {
		ix := newIndex()
		ix.correct("7", nil, start.Add(2*time.Second))
		ix.correct("7", new(item(100, pathA)), start.Add(time.Second))
		if _, kept := ix.gone["7"]; !kept || len(at(ix, pathA)) != 0 {
			t.Errorf("gone kept %v, a = %v; want the key kept out", kept, at(ix, pathA))
		}
	})
	t.Run("a body served before the delete after a not-found", func(t *testing.T) {
		ix := newIndex()
		ix.correct("7", new(item(200, pathA)), start.Add(time.Second))
		ix.correct("7", nil, start.Add(2*time.Second))
		ix.correct("7", new(item(150, pathB)), start.Add(3*time.Second))
		if _, kept := ix.gone["7"]; !kept || len(at(ix, pathA)) != 0 || len(at(ix, pathB)) != 0 {
			t.Errorf("gone kept %v, a = %v, b = %v; want the key kept out", kept, at(ix, pathA), at(ix, pathB))
		}
	})
	t.Run("a walk's listing raises the floor", func(t *testing.T) {
		ix := newIndex()
		ix.correct("7", nil, start.Add(time.Second))
		ix.finish(start, fresh(item(250, pathA)), nil)
		ix.correct("7", new(item(200, pathB)), start.Add(2*time.Second))
		if _, kept := ix.gone["7"]; !kept || len(at(ix, pathA)) != 0 || len(at(ix, pathB)) != 0 {
			t.Errorf("gone kept %v, a = %v, b = %v; want the key kept out", kept, at(ix, pathA), at(ix, pathB))
		}
	})
}
