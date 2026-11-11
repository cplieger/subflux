package search_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/providercalls"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/searchtest"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
)

// plainHasher fingerprints a credential as itself.
type plainHasher struct{}

func (plainHasher) Hash(_ context.Context, c string) (string, error) { return c, nil }
func (plainHasher) Verify(_ context.Context, fp, c string) (bool, error) {
	return fp == c, nil
}

// noEvidence holds no series reading.
type noEvidence struct{}

func (noEvidence) Evidence(context.Context, media.SourceID, mediasource.NativeRef) (mediasource.SeriesEvidence, bool, error) {
	return mediasource.SeriesEvidence{}, false, nil
}

// noRoots lists no media root.
type noRoots struct{}

func (noRoots) MediaRoots(context.Context) []string { return nil }

// newDeps is every collaborator New requires, over a fresh store.
func newDeps(t *testing.T) search.Deps {
	t.Helper()
	db, err := statedb.Open(filepath.Join(t.TempDir(), "state.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := status.New(t.Context(), statustest.NewMemPersister())
	if err != nil {
		t.Fatal(err)
	}
	return search.Deps{
		Starts:   search.NewStarts(),
		Store:    db,
		Funnel:   providercalls.NewFunnel(db, plainHasher{}, st),
		Writer:   mediawrite.New(mediawrite.Deps{Store: db, Roots: noRoots{}, Alerts: st, Policy: db.Policy}),
		Prober:   embedded.New(db),
		Aligner:  searchtest.NewAligner(),
		Evidence: noEvidence{},
		Status:   st,
	}
}

func yamlNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

// policy targets en and pt-BR.
func policy(t *testing.T) language.Policy {
	t.Helper()
	p, errs := language.ParsePolicy(yamlNode(t, "default: [{tag: en}, {tag: pt-BR}]"))
	if errs != nil {
		t.Fatal(errs)
	}
	return p
}

// defaults is the search section at its defaults.
func defaults(t *testing.T) search.Config {
	t.Helper()
	cfg, errs := search.ParseConfig(nil, policy(t))
	if errs != nil {
		t.Fatal(errs)
	}
	return cfg
}

// idle is an adapter that is never asked.
type idle struct{}

func (idle) Search(context.Context, provider.Query) ([]provider.Candidate, error) { return nil, nil }

func (idle) Fetch(context.Context, provider.Ref) ([]byte, error) {
	return nil, nil
}

// testEntry is a provider with no settings and an idle adapter.
func testEntry(id provider.ProviderID) provider.Entry {
	return provider.Entry{
		ID:      id,
		Summary: "A provider the test builds.",
		Hosts:   []string{"api.example.test"},
		New:     func(provider.Client, formschema.Values) (provider.Adapter, error) { return idle{}, nil },
	}
}

func enabled(id provider.ProviderID) provider.Config {
	return provider.Config{ID: id, Enabled: true, Values: formschema.Values{}}
}

// newEngine is New's error over the defaults and policy.
func newEngine(t *testing.T, deps search.Deps, entries []provider.Entry, cfgs []provider.Config) error {
	t.Helper()
	_, err := search.New(deps, entries, cfgs, policy(t), defaults(t), timing.Config{})
	return err
}

// alertText is the text of src's alert in st, and whether it is raised.
func alertText(st *status.Store, src status.AlertSource) (string, bool) {
	snap, _ := st.Snapshot()
	for _, a := range snap.Alerts {
		if a.Source == src {
			return a.Text, true
		}
	}
	return "", false
}
