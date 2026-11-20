package mediaserver_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediaserver"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"go.yaml.in/yaml/v4"
)

var fakeSchema = formschema.Schema{Fields: []formschema.Field{
	{Key: "url", Kind: formschema.String, Required: true},
	{Key: "token", Kind: formschema.Secret},
}}

// fake is a scripted server: labels by server path, and the server paths
// it was asked to refresh. A nil func answers success, or from byPath.
type fake struct {
	refresh  func(ctx context.Context, p string) error
	labels   func(ctx context.Context, p string) ([]mediaserver.Label, error)
	ping     func(ctx context.Context) error
	shutdown func(ctx context.Context) error
	byPath   map[string][]mediaserver.Label

	mu        sync.Mutex
	refreshed []string
}

var _ mediaserver.Adapter = (*fake)(nil)

func (f *fake) Refresh(ctx context.Context, p string) error {
	f.mu.Lock()
	f.refreshed = append(f.refreshed, p)
	f.mu.Unlock()
	if f.refresh != nil {
		return f.refresh(ctx, p)
	}
	return nil
}

func (f *fake) Labels(ctx context.Context, p string) ([]mediaserver.Label, error) {
	if f.labels != nil {
		return f.labels(ctx, p)
	}
	return slices.Clone(f.byPath[p]), nil
}

func (f *fake) Ping(ctx context.Context) error {
	if f.ping != nil {
		return f.ping(ctx)
	}
	return nil
}

func (f *fake) Shutdown(ctx context.Context) error {
	if f.shutdown != nil {
		return f.shutdown(ctx)
	}
	return nil
}

func (f *fake) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.refreshed)
}

var errBadURL = errors.New("the url must be an absolute http or https URL")

// fakeKind answers adapters[cfg.ID] from New, refusing a url of "bad".
func fakeKind(adapters map[string]*fake) mediaserver.AdapterKind {
	return mediaserver.AdapterKind{Type: "fake", Schema: fakeSchema, New: func(cfg mediaserver.Config, _ mediasource.Deps) (mediaserver.Adapter, error) {
		if cfg.Values.String("url") == "bad" {
			return nil, errBadURL
		}
		if a, ok := adapters[cfg.ID]; ok {
			return a, nil
		}
		return &fake{}, nil
	}}
}

func newStore(t testing.TB) *status.Store {
	t.Helper()
	st, err := status.New(t.Context(), statustest.NewMemPersister())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func cfg(id, url string, maps ...mediasource.PathMapping) mediaserver.Config {
	vals, errs := fakeSchema.Decode(&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "url"}, {Kind: yaml.ScalarNode, Value: url},
	}})
	if len(errs) > 0 {
		panic(errs)
	}
	return mediaserver.Config{ID: id, Type: "fake", Values: vals, PathMappings: maps}
}

func newSet(t testing.TB, st *status.Store, adapters map[string]*fake, cfgs ...mediaserver.Config) *mediaserver.Set {
	t.Helper()
	s, err := mediaserver.NewSet([]mediaserver.AdapterKind{fakeKind(adapters)}, cfgs, mediasource.Deps{}, st)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// alert answers the server's alert text and whether it is raised.
func alert(st *status.Store, id string) (string, bool) {
	snap, _ := st.Snapshot()
	i := slices.IndexFunc(snap.Alerts, func(a status.Alert) bool { return a.Source == status.MediaServerAlert(id) })
	if i < 0 {
		return "", false
	}
	return snap.Alerts[i].Text, true
}

func parseYAML(t testing.TB, doc string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Content) == 0 {
		return nil
	}
	return n.Content[0]
}

func errorAt(errs []formschema.FieldError, path string) (string, bool) {
	i := slices.IndexFunc(errs, func(e formschema.FieldError) bool { return e.Path == path })
	if i < 0 {
		return "", false
	}
	return errs[i].Message, true
}
