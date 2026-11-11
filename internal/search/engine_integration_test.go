package search_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/provider/gestdown"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

// recordedGestdown serves Gestdown's recorded Breaking Bad answers, a
// subtitle for every download and a 404 for anything else, and records
// each request's path.
type recordedGestdown struct {
	t     *testing.T
	paths []string
	mu    sync.Mutex
}

// bbShow is Gestdown's recorded show id for TVDB series 81189, and
// ctrlHD its recorded Bluray-CtrlHD English subtitle of S01E01.
const (
	bbShow = "1WLU6zJky4nlJQa5oojv5R"
	ctrlHD = "5p45mvzz1jBNntcbV3ghQX"
)

func (g *recordedGestdown) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.paths = append(g.paths, r.URL.Path)
	g.mu.Unlock()
	recorded := map[string]string{
		"/shows/external/tvdb/81189": "shows-tvdb-81189.json",
		"/shows/" + bbShow + "/1/en": "season-1-English.json",
	}
	if name, ok := recorded[r.URL.Path]; ok {
		b, err := os.ReadFile(filepath.Join("..", "provider", "gestdown", "testdata", name))
		if err != nil {
			g.t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/subtitles/download/") {
		_, _ = w.Write(srtBytes)
		return
	}
	http.NotFound(w, r)
}

func (g *recordedGestdown) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.paths)
}

// breakingBad is the series root Gestdown's recordings answer for.
var breakingBad = mediasource.Root{
	Native: mediasource.NativeRef{ID: "81189", Kind: media.Episode}, Title: "Breaking Bad",
	External: media.ExternalIDs{TVDB: 81189}, Year: 2008, Order: mediasource.Order{Type: "official", Name: "Aired", Aired: true},
}

// bbEpisode is Breaking Bad's episode S<season>E<n>, its video a CtrlHD
// BluRay release.
func bbEpisode(season, n int, title string) mediasource.Item {
	name := "Breaking.Bad.S" + two(season) + "E" + two(n) + ".720p.BluRay.x264-CtrlHD"
	at := media.Numbering{Scheme: media.TVDB, Season: season, Episode: n}
	return mediasource.Item{
		Native: mediasource.NativeID("bb" + two(season) + two(n)), Title: title,
		VideoPath: filepath.Join("Breaking Bad", "Season "+two(season), name+".mkv"), SceneName: name,
		Numbering: at, SeriesNumbering: at,
	}
}

// TestEngineScanDeliversAndBacksOff drives the engine end to end over a
// real store, the real writer and Gestdown's recorded answers: Scan work
// for an episode Gestdown has subtitles for is delivered, and for one it
// has none for books a no-result row, so a rescan after a restart asks
// Gestdown nothing for it.
func TestEngineScanDeliversAndBacksOff(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		g := &recordedGestdown{t: t}
		ctx := providertest.Serve(t, t.Context(), g, "api.gestdown.info")
		entry := gestdown.NewEntry()
		r.entries = append(r.entries, entry)
		r.cfgs = append(r.cfgs, providertest.Config(t, entry, ""))
		pilot, next := bbEpisode(1, 1, "Pilot"), bbEpisode(2, 1, "Seven Thirty-Seven")
		ids := r.seed(breakingBad, nil, pilot, next)
		served, empty := ids[0], ids[1]
		r.script(alignedAs(t, timing.Verified))

		stop := drainUnder(ctx, r.engine())
		time.Sleep(time.Hour)
		r.enqueue(statedb.Scan, served, empty)
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Drain = %v, want context.Canceled", err)
		}

		stem := strings.TrimSuffix(filepath.Base(pilot.VideoPath), ".mkv")
		if subs := r.subtitles(served); !slices.Equal(subs, []string{stem + ".en.srt"}) {
			t.Fatalf("subtitles %q, want the unnumbered %s.en.srt", subs, stem)
		}
		rec := r.record(served)
		p := rec.Provenance
		if rec.Ordinal != 0 || rec.Locked || p.Manual {
			t.Errorf("record %+v, want an automatic delivery: no ordinal, unlocked", rec)
		}
		if p.Provider != string(gestdown.ID) || p.Release != "Bluray-CtrlHD" || p.MatchedBy != rank.ID || p.Fit != rank.SameRelease {
			t.Errorf("provenance %+v, want gestdown's Bluray-CtrlHD matched by id at same_release", p)
		}
		if p.Facts.Source != rank.Same || p.Facts.Group != rank.Same {
			t.Errorf("facts %+v, want the source and the group the video's", p.Facts)
		}
		if p.Timing.Verdict != timing.Verified {
			t.Errorf("timing %+v, want the aligner's verified outcome", p.Timing)
		}
		if d := rowOf(t, r.history(keyOf(t, breakingBad, pilot)), string(gestdown.ID), ctrlHD); !d.Delivered || d.Attempt != 1 {
			t.Errorf("download row %+v, want attempt 1 delivered", d)
		}
		noRows := r.noResults(breakingBad, 2)
		if len(noRows) != 1 || noRows[0].Provider != string(gestdown.ID) || noRows[0].Tag != mustTag("en") || noRows[0].Count != 1 {
			t.Fatalf("season 2 no-result rows %+v, want one of gestdown's for en, count 1", noRows)
		}
		if subs := r.subtitles(empty); len(subs) != 0 {
			t.Errorf("subtitles %q beside the unanswered episode", subs)
		}
		if rows := r.due(); len(rows) != 0 {
			t.Errorf("work %+v left, want both rows acknowledged", rows)
		}

		r.reopen()
		before := len(g.requests())
		stop = drainUnder(ctx, r.engine())
		r.enqueue(statedb.Scan, empty)
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("rescan Drain = %v, want context.Canceled", err)
		}
		if asked := g.requests()[before:]; len(asked) != 0 {
			t.Errorf("the rescan sent %q, want nothing while the no-result row holds", asked)
		}
		if rows := r.due(); len(rows) != 0 {
			t.Errorf("work %+v left after the rescan, want it acknowledged", rows)
		}
	})
}
