package app

import (
	"context"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/measure"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
	"github.com/cplieger/subflux/internal/mediasource/radarr"
	"github.com/cplieger/subflux/internal/mediasource/sonarr"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"go.yaml.in/yaml/v4"
)

// parseNode parses text, one YAML document, into its root node.
func parseNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

// sourcesOf parses text, a sources section, with kinds.
func sourcesOf(t *testing.T, kinds []mediasource.AdapterKind, text string) []mediasource.Config {
	t.Helper()
	cfgs, errs := mediasource.ParseConfigs(parseNode(t, text), kinds)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return cfgs
}

func TestChangedSources(t *testing.T) {
	kinds := []mediasource.AdapterKind{sonarr.Kind, radarr.Kind}
	const old = "[{id: tv, type: sonarr, url: 'http://sonarr:8989', api_key: k1}, {id: films, type: radarr, url: 'http://radarr:7878', api_key: k2}]"
	ids := func(s ...media.SourceID) []media.SourceID { return s }
	cases := []struct {
		name, cur        string
		changed, rekeyed []media.SourceID
	}{
		{"unchanged", old, nil, nil},
		{"moved", "[{id: films, type: radarr, url: 'http://radarr:7878', api_key: k2}, {id: tv, type: sonarr, url: 'http://sonarr:8989', api_key: k1}]", nil, nil},
		{"added", old[:len(old)-1] + ", {id: anime, type: sonarr, url: 'http://anime:8989', api_key: k3}]", ids("anime"), nil},
		{"removed", "[{id: tv, type: sonarr, url: 'http://sonarr:8989', api_key: k1}]", nil, nil},
		{"secret only", "[{id: tv, type: sonarr, url: 'http://sonarr:8989', api_key: k9}, {id: films, type: radarr, url: 'http://radarr:7878', api_key: k2}]", nil, ids("tv")},
		{"url", "[{id: tv, type: sonarr, url: 'http://sonarr:8989', api_key: k1}, {id: films, type: radarr, url: 'http://radarr2:7878', api_key: k2}]", ids("films"), nil},
		{"a secret and the url together", "[{id: tv, type: sonarr, url: 'http://nas:8989', api_key: k9}, {id: films, type: radarr, url: 'http://radarr:7878', api_key: k8}]", ids("tv"), ids("films")},
	}
	prev := sourcesOf(t, kinds, old)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			changed, rekeyed := changedSources(kinds, prev, sourcesOf(t, kinds, c.cur))
			if !slices.Equal(changed, c.changed) || !slices.Equal(rekeyed, c.rekeyed) {
				t.Errorf("changedSources = %v, %v; want %v, %v", changed, rekeyed, c.changed, c.rekeyed)
			}
		})
	}
}

// sourcesRig is a mediaSources over two fake sources a and b sharing one
// library whose one root folder is /media, its Scanner running.
type sourcesRig struct {
	m     *mediaSources
	fake  *sourcetest.Fake
	logs  *capture.Recorder
	kinds []mediasource.AdapterKind
	a     settings.Active
	reg   *measure.Registry
}

var rigSchedule = mediasource.Schedule{Change: time.Minute, Whole: 24 * time.Hour}

func newSourcesRig(t *testing.T) *sourcesRig {
	t.Helper()
	lib := &sourcetest.Library{}
	lib.SetFolders([]string{"/media"})
	f := sourcetest.NewFake(lib, mediasource.Capabilities{Kinds: []media.Kind{media.Movie}, Changes: true})
	kinds := []mediasource.AdapterKind{sourcetest.FakeKind("fake", f)}
	db, err := statedb.Open(filepath.Join(t.TempDir(), "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p, errs := language.ParsePolicy(parseNode(t, "default: [{tag: en}]"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := db.Activate(t.Context(), statedb.Activation{Sources: []media.SourceID{"a", "b"}, Languages: p}); err != nil {
		t.Fatal(err)
	}
	jobs, err := status.New(t.Context(), statustest.NewMemPersister())
	if err != nil {
		t.Fatal(err)
	}
	r := &sourcesRig{fake: f, kinds: kinds, reg: measure.NewRegistry("subflux")}
	r.a = settings.Active{Sources: sourcesOf(t, kinds, "[{id: a, type: fake, url: 'http://a', token: t1}, {id: b, type: fake, url: 'http://b', token: t2}]"), Scan: rigSchedule}
	var log *slog.Logger
	log, r.logs = capture.New()
	if r.m, err = newMediaSources(&r.a, kinds, mediasource.Deps{Logger: log}, sourcetest.OwnPlaces{}, scan.Deps{Store: db, Status: jobs, Logger: log}); err != nil {
		t.Fatal(err)
	}
	r.reg.MustRegister(r.m.scan.Collectors()...)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.m.scan.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run = %v", err)
		}
	})
	synctest.Wait()
	return r
}

// series answers the scan families' samples by name and source label, the
// interval's under "".
func (r *sourcesRig) series(t *testing.T) map[string]map[string]float64 {
	t.Helper()
	fams, err := r.reg.Gather(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]float64{}
	for _, f := range fams {
		out[f.Name] = map[string]float64{}
		for _, s := range f.Samples {
			label := ""
			if len(s.Labels) > 0 {
				label = s.Labels[0].Value
			}
			out[f.Name][label] = s.Value
		}
	}
	return out
}

// edit answers r's Active with sources replaced by text when it is not
// empty and the scan section by sched.
func (r *sourcesRig) edit(t *testing.T, text string, sched mediasource.Schedule) settings.Active {
	t.Helper()
	next := r.a
	if text != "" {
		next.Sources = sourcesOf(t, r.kinds, text)
	}
	next.Scan = sched
	return next
}

// apply runs both reload steps the change loop runs for a commit.
func (r *sourcesRig) apply(t *testing.T, next settings.Active) {
	t.Helper()
	r.m.reloadSources(&r.a, &next)
	r.m.reloadSchedule(&r.a, &next)
	r.a = next
	synctest.Wait()
}

const (
	lastComplete  = "subflux_scan_last_complete_timestamp_seconds"
	wholeInterval = "subflux_scan_whole_interval_seconds"
)

func TestScanChangeCallsSetScheduleOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newSourcesRig(t)
		set, before := r.m.set.Load(), r.series(t)[lastComplete]
		time.Sleep(10 * time.Second)
		r.apply(t, r.edit(t, "", mediasource.Schedule{Change: 2 * time.Minute, Whole: 12 * time.Hour}))
		got := r.series(t)
		if r.m.set.Load() != set {
			t.Error("a scan-only edit published a new Set")
		}
		if !maps.Equal(got[lastComplete], before) {
			t.Errorf("last complete %v, want the loops' start seeds %v kept", got[lastComplete], before)
		}
		if got[wholeInterval][""] != (12 * time.Hour).Seconds() {
			t.Errorf("whole interval %v, want 12h", got[wholeInterval][""])
		}
	})
}

func TestReloadSourcesRestartsChangedOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newSourcesRig(t)
		set, before := r.m.set.Load(), r.series(t)[lastComplete]
		time.Sleep(10 * time.Second)
		r.apply(t, r.edit(t, "[{id: a, type: fake, url: 'http://a2', token: t1}, {id: b, type: fake, url: 'http://b', token: t9}]", rigSchedule))
		got := r.series(t)
		if r.m.set.Load() == set {
			t.Error("a sources edit kept the old Set")
		}
		if got[lastComplete]["a"] <= before["a"] {
			t.Errorf("a's series %v, want a restart after %v", got[lastComplete]["a"], before["a"])
		}
		if got[lastComplete]["b"] != before["b"] {
			t.Errorf("b's series %v, want the rekeyed loop's seed %v kept", got[lastComplete]["b"], before["b"])
		}
		if got[wholeInterval][""] != rigSchedule.Whole.Seconds() {
			t.Errorf("whole interval %v, want the unchanged %v", got[wholeInterval][""], rigSchedule.Whole)
		}
	})
}

func TestReloadSourcesInheritsRoots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newSourcesRig(t)
		if got := r.m.set.Load().MediaRoots(t.Context()); !slices.Equal(got, []string{"/media"}) {
			t.Fatalf("MediaRoots = %v, want [/media]", got)
		}
		r.fake.Fail("RootFolders", &mediasource.SourceError{Cause: mediasource.Unreachable})
		r.apply(t, r.edit(t, "[{id: a, type: fake, url: 'http://a', token: t1}, {id: b, type: fake, url: 'http://b', token: t9}]", rigSchedule))
		if got := r.m.set.Load().MediaRoots(t.Context()); !slices.Equal(got, []string{"/media"}) {
			t.Errorf("MediaRoots after a key-only edit with the source down = %v, want the cached [/media]", got)
		}
	})
}

func TestReloadSourcesKeepsSetOnRefusal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newSourcesRig(t)
		set := r.m.set.Load()
		next := r.a
		next.Sources = append(slices.Clone(next.Sources), mediasource.Config{ID: "c", Type: "unregistered"})
		r.apply(t, next)
		if r.m.set.Load() != set {
			t.Fatal("a refused rebuild replaced the live Set")
		}
		if got := r.m.set.Load().Sources(); !slices.Equal(got, []media.SourceID{"a", "b"}) {
			t.Errorf("live sources %v, want [a b]", got)
		}
		recs := slices.DeleteFunc(r.logs.Records(), func(rec slog.Record) bool {
			return rec.Message != "media sources: the rebuilt set was refused; the previous one stays live"
		})
		if len(recs) != 1 || recs[0].Level != slog.LevelError {
			t.Errorf("logs %v, want one refusal at ERROR", r.logs.Messages())
		}
	})
}

// gathered reads g's samples of family name by their first label's value.
func gathered(t *testing.T, g func(context.Context) ([]measure.Family, error), name string) map[string]float64 {
	t.Helper()
	fams, err := g(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, f := range fams {
		if f.Name != name {
			continue
		}
		for _, s := range f.Samples {
			label := ""
			if len(s.Labels) > 0 {
				label = s.Labels[0].Value
			}
			out[label] = s.Value
		}
	}
	return out
}

// sourcesBoot boots Run on text, handing back the Gather and counting the
// Scanners built.
func sourcesBoot(t *testing.T, text string) (*booted, func(context.Context) ([]measure.Family, error), *atomic.Int32) {
	t.Helper()
	var gather func(context.Context) ([]measure.Family, error)
	built := &atomic.Int32{}
	b := boot(t, text, func(c *Config) {
		c.Gather = func(g func(context.Context) ([]measure.Family, error)) { gather = g }
		c.Scanner = func(*scan.Scanner) { built.Add(1) }
		c.Orders = sourcetest.OwnPlaces{}
	})
	return b, gather, built
}

func TestRetainsSourceAlerts(t *testing.T) {
	cfg := validLanguages + "sources:\n  - {id: tv, type: sonarr, url: 'http://sonarr.invalid', api_key: k1}\n  - {id: films, type: radarr, url: 'http://radarr.invalid', api_key: k2}\n"
	b := boot(t, cfg, func(c *Config) { c.Orders = sourcetest.OwnPlaces{} })
	for _, src := range []status.AlertSource{status.SourceAlert("tv"), status.SourceAlert("films"), status.SourceOverlapAlert("films"), status.SourceAlert("gone"), status.SourceOverlapAlert("gone")} {
		b.status.Reconcile(src, true, "test")
	}
	for src, want := range map[status.AlertSource]bool{status.SourceAlert("tv"): true, status.SourceAlert("films"): true, status.SourceOverlapAlert("films"): true, status.SourceAlert("gone"): false, status.SourceOverlapAlert("gone"): false} {
		if got := hasAlert(b.status, src); got != want {
			t.Errorf("after boot, %s raised = %v, want %v", src, got, want)
		}
	}
	saveText(t, b.svc, validLanguages+"sources:\n  - {id: tv, type: sonarr, url: 'http://sonarr.invalid', api_key: k1}\n")
	for src, want := range map[status.AlertSource]bool{status.SourceAlert("tv"): true, status.SourceAlert("films"): false, status.SourceOverlapAlert("films"): false} {
		if got := hasAlert(b.status, src); got != want {
			t.Errorf("after films' removal, %s raised = %v, want %v", src, got, want)
		}
	}
}

func TestSourcesBuiltOnFirstActive(t *testing.T) {
	b, gather, built := sourcesBoot(t, "")
	if n := built.Load(); n != 0 {
		t.Fatalf("an unconfigured boot built %d Scanners, want none", n)
	}
	saveText(t, b.svc, validLanguages+"scan:\n  whole_interval: 2h\n")
	if !eventually(func() bool { return gathered(t, gather, wholeInterval)[""] == 7200 }) {
		t.Fatalf("after the first Active save %s = %v, want 7200", wholeInterval, gathered(t, gather, wholeInterval))
	}
	saveText(t, b.svc, validLanguages+"scan:\n  whole_interval: 3h\n")
	if !eventually(func() bool { return gathered(t, gather, wholeInterval)[""] == 10800 }) {
		t.Errorf("after a scan edit %s = %v, want 10800", wholeInterval, gathered(t, gather, wholeInterval))
	}
	if n := built.Load(); n != 1 {
		t.Errorf("two saves built %d Scanners, want 1", n)
	}
}

func TestSourceEditsReachScanner(t *testing.T) {
	srv := arrtest.Load(t, filepath.Join("..", "mediasource", "sonarr", "testdata", "v5-api-v5"))
	url := srv.Start(t)
	b, gather, _ := sourcesBoot(t, validLanguages)
	if got := gathered(t, gather, lastComplete); len(got) != 0 {
		t.Fatalf("a boot with no sources exports %s %v", lastComplete, got)
	}
	saveText(t, b.svc, validLanguages+"sources:\n  - {id: tv, type: sonarr, url: '"+url+"', api_key: "+arrtest.Key+"}\n")
	if !eventually(func() bool { _, ok := gathered(t, gather, lastComplete)["tv"]; return ok }) {
		t.Fatalf("an added source started no loop: %s %v", lastComplete, gathered(t, gather, lastComplete))
	}
	saveText(t, b.svc, validLanguages)
	if !eventually(func() bool { return len(gathered(t, gather, lastComplete)) == 0 }) {
		t.Errorf("a removed source kept its series: %s %v", lastComplete, gathered(t, gather, lastComplete))
	}
}
