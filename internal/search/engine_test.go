package search_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providers"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// reporting is an entry whose Data keeps each report it is given.
type reporting struct {
	reports []func(text string, active bool)
	mu      sync.Mutex
}

func (r *reporting) entry(id provider.ProviderID) provider.Entry {
	e := testEntry(id)
	e.Data = func(report func(string, bool)) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.reports = append(r.reports, report)
	}
	return e
}

func (r *reporting) registered() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reports)
}

func (r *reporting) latest() func(string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reports[len(r.reports)-1]
}

func TestEntrySummaryBounds(t *testing.T) {
	cases := []struct {
		name, summary string
		ok            bool
	}{
		{"empty", "", false},
		{"121 bytes", strings.Repeat("s", 121), false},
		{"two lines", "Subtitles\nfor films.", false},
		{"120 bytes", strings.Repeat("s", 120), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var r reporting
			e := r.entry("only")
			e.Summary = c.summary
			err := newEngine(t, newDeps(t), []provider.Entry{e}, []provider.Config{enabled("only")})
			if (err == nil) != c.ok {
				t.Errorf("New with a %d-byte Summary = %v, want ok %v", len(c.summary), err, c.ok)
			}
			if want := map[bool]int{true: 1, false: 0}[c.ok]; r.registered() != want {
				t.Errorf("Data registered %d times, want %d", r.registered(), want)
			}
		})
	}
	t.Run("an unconfigured entry's Summary is checked too", func(t *testing.T) {
		bad := testEntry("bad")
		bad.Summary = ""
		if err := newEngine(t, newDeps(t), []provider.Entry{testEntry("good"), bad}, []provider.Config{enabled("good")}); err == nil {
			t.Error("New accepted a registry holding an empty Summary")
		}
	})
}

// TestRegistryBuilds builds every production entry, enabled with its
// Required values set, so a registry fault fails here and not at a user's
// activation.
func TestRegistryBuilds(t *testing.T) {
	entries := providers.New().Entries
	var text strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&text, "%s:\n  settings: {", e.ID)
		for _, f := range e.Schema.Fields {
			if f.Required {
				fmt.Fprintf(&text, "%s: test-value, ", f.Key)
			}
		}
		text.WriteString("}\n")
	}
	cfgs, errs := provider.ParseConfigs(yamlNode(t, text.String()), entries)
	if errs != nil || len(cfgs) != len(entries) {
		t.Fatalf("ParseConfigs = %d configs, %v; want one per entry", len(cfgs), errs)
	}
	if err := newEngine(t, newDeps(t), entries, cfgs); err != nil {
		t.Fatalf("New over every production entry = %v", err)
	}
}

func TestNewRepeatsTheParsersChecks(t *testing.T) {
	en, _ := language.Parse("en")
	fr, _ := language.Parse("fr")
	keyed := testEntry("keyed")
	keyed.Schema = formschema.Schema{Fields: []formschema.Field{{Key: "api_key", Kind: formschema.Secret, Required: true}}}
	cases := []struct {
		edit func(*search.Deps, *search.Config, *[]provider.Config)
		name string
	}{
		{func(d *search.Deps, _ *search.Config, _ *[]provider.Config) { d.Writer = nil }, "a Deps field missing"},
		{func(_ *search.Deps, c *search.Config, _ *[]provider.Config) { c.MinFit = rank.Conflict }, "conflict as the minimum"},
		{func(_ *search.Deps, c *search.Config, _ *[]provider.Config) { c.MinFit = 0 }, "no minimum"},
		{func(_ *search.Deps, c *search.Config, _ *[]provider.Config) {
			c.TargetMinFit = map[language.Tag]rank.Fit{en: rank.Conflict}
		}, "conflict for a target"},
		{func(_ *search.Deps, c *search.Config, _ *[]provider.Config) {
			c.TargetMinFit = map[language.Tag]rank.Fit{fr: rank.Exact}
		}, "a fit for a tag no target names"},
		{func(_ *search.Deps, c *search.Config, _ *[]provider.Config) { c.UpgradeWindowDays = 366 }, "an upgrade window over a year"},
		{func(_ *search.Deps, c *search.Config, _ *[]provider.Config) { c.ScanDelay = time.Second }, "a scan delay under 5 s"},
		{func(_ *search.Deps, c *search.Config, _ *[]provider.Config) { c.ScanDelay = 11 * time.Minute }, "a scan delay over 10 minutes"},
		{func(_ *search.Deps, _ *search.Config, p *[]provider.Config) { *p = append(*p, enabled("bogus")) }, "an unknown provider"},
		{func(_ *search.Deps, _ *search.Config, p *[]provider.Config) { *p = append(*p, enabled("data")) }, "a provider twice"},
		{func(_ *search.Deps, _ *search.Config, p *[]provider.Config) {
			(*p)[0].Priority = provider.MaxPriority + 1
		}, "a priority over its bound"},
		{func(_ *search.Deps, _ *search.Config, p *[]provider.Config) { *p = append(*p, enabled("keyed")) }, "an enabled config Build refuses"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var r reporting
			deps, cfg, cfgs := newDeps(t), defaults(t), []provider.Config{enabled("data")}
			c.edit(&deps, &cfg, &cfgs)
			if _, err := search.New(deps, []provider.Entry{r.entry("data"), keyed}, cfgs, policy(t), cfg, timing.Config{}); err == nil {
				t.Error("New accepted it")
			}
			if r.registered() != 0 {
				t.Errorf("a refused New registered Data %d times", r.registered())
			}
		})
	}
}

func TestEntryDataReconciled(t *testing.T) {
	var data, off reporting
	plain := testEntry("plain")
	entries := []provider.Entry{data.entry("data"), plain, off.entry("off")}
	cfgs := []provider.Config{enabled("data"), enabled("plain"), {ID: "off", Values: formschema.Values{}}}
	deps := newDeps(t)
	if err := newEngine(t, deps, entries, cfgs); err != nil {
		t.Fatal(err)
	}
	src := status.ProviderDataAlert("data")
	data.latest()("the map could not be downloaded", true)
	if text, ok := alertText(deps.Status, src); !ok || text != "the map could not be downloaded" {
		t.Errorf("after an active report, alert %q raised %v; want its text", text, ok)
	}
	data.latest()("", false)
	if _, ok := alertText(deps.Status, src); ok {
		t.Error("an inactive report left the alert raised")
	}
	if err := newEngine(t, deps, entries, cfgs); err != nil {
		t.Fatal(err)
	}
	if data.registered() != 2 {
		t.Fatalf("Data registered %d times over two engines, want 2", data.registered())
	}
	data.latest()("stale since Monday", true)
	if text, ok := alertText(deps.Status, src); !ok || text != "stale since Monday" {
		t.Errorf("the second engine's report raised %q, %v; want its text", text, ok)
	}
	if off.registered() != 0 {
		t.Errorf("a disabled entry's Data registered %d times", off.registered())
	}
	snap, _ := deps.Status.Snapshot()
	if len(snap.Alerts) != 1 {
		t.Errorf("alerts %v, want only %s", snap.Alerts, src)
	}
}
