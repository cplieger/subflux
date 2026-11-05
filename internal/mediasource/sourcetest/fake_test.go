package sourcetest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"go.yaml.in/yaml/v3"
)

// fakeHarness serves a Library through a Fake, counting the adapter calls
// as the upstream's requests; an entry whose token is "refused" answers
// every call Unauthorized.
type fakeHarness struct {
	caps     mediasource.Capabilities
	id       func(n int) mediasource.NativeID
	lib      *sourcetest.Library
	requests atomic.Int64
}

func (h *fakeHarness) Kind() mediasource.AdapterKind {
	return mediasource.AdapterKind{Type: "fake", Schema: sourcetest.FakeSchema, New: func(cfg mediasource.Config, _ mediasource.Deps) (mediasource.Adapter, error) {
		f := sourcetest.NewFake(h.lib, h.caps)
		if cfg.Values.Secret("token") == "refused" {
			for _, op := range []string{"Items", "Root", "Changes", "Tags", "RootFolders", "Ping", "Rescan", "Poster"} {
				f.Fail(op, &mediasource.SourceError{Cause: mediasource.Unauthorized})
			}
		}
		return &counted{Adapter: f, n: &h.requests}, nil
	}}
}

func (h *fakeHarness) Serve(t testing.TB, lib *sourcetest.Library) mediasource.Config {
	h.lib = lib
	return mediasource.Config{Values: fakeValues(t, "")}
}

func (*fakeHarness) Refuse(t testing.TB) mediasource.Config {
	return mediasource.Config{Values: fakeValues(t, "refused")}
}

func (h *fakeHarness) Requests() int                       { return int(h.requests.Load()) }
func (h *fakeHarness) NativeID(n int) mediasource.NativeID { return h.id(n) }

// counted counts every adapter call but Capabilities.
type counted struct {
	mediasource.Adapter
	n *atomic.Int64
}

func (c *counted) Items(ctx context.Context, y func(mediasource.Root, []mediasource.Item) error, u mediasource.Unread) error {
	c.n.Add(1)
	return c.Adapter.Items(ctx, y, u)
}

func (c *counted) Root(ctx context.Context, r mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
	c.n.Add(1)
	return c.Adapter.Root(ctx, r)
}

func (c *counted) Changes(ctx context.Context, cur mediasource.Cursor) ([]mediasource.Change, mediasource.Cursor, error) {
	c.n.Add(1)
	return c.Adapter.Changes(ctx, cur)
}

func (c *counted) Tags(ctx context.Context) (map[mediasource.TagRef]string, error) {
	c.n.Add(1)
	return c.Adapter.Tags(ctx)
}

func (c *counted) RootFolders(ctx context.Context) ([]string, error) {
	c.n.Add(1)
	return c.Adapter.RootFolders(ctx)
}

func (c *counted) Ping(ctx context.Context) error {
	c.n.Add(1)
	return c.Adapter.Ping(ctx)
}

func (c *counted) Rescan(ctx context.Context, r mediasource.NativeRef) error {
	c.n.Add(1)
	return c.Adapter.Rescan(ctx, r)
}

func (c *counted) Poster(ctx context.Context, r mediasource.NativeRef) (io.ReadCloser, string, bool, error) {
	c.n.Add(1)
	return c.Adapter.Poster(ctx, r)
}

func decimal(n int) mediasource.NativeID { return mediasource.NativeID(fmt.Sprint(n)) }

func uuid(n int) mediasource.NativeID {
	return mediasource.NativeID(fmt.Sprintf("%08x-0000-4000-8000-%012x", n, n))
}

func TestContractOverFake(t *testing.T) {
	kinds := map[string][]media.Kind{"episode": {media.Episode}, "movie": {media.Movie}, "both": {media.Episode, media.Movie}}
	ids := map[string]func(int) mediasource.NativeID{"decimal": decimal, "uuid": uuid}
	for kn, k := range kinds {
		for idn, id := range ids {
			t.Run(kn+" "+idn, func(t *testing.T) {
				caps := mediasource.Capabilities{Kinds: k, Changes: true, Rescan: true, Tags: true, Posters: true}
				sourcetest.Contract(t, &fakeHarness{caps: caps, id: id})
			})
		}
	}
	t.Run("no optional capability", func(t *testing.T) {
		sourcetest.Contract(t, &fakeHarness{caps: mediasource.Capabilities{Kinds: []media.Kind{media.Movie}}, id: decimal})
	})
}

func TestFailRootOnlyThatRoot(t *testing.T) {
	lib := &sourcetest.Library{}
	for _, id := range []string{"1", "2", "3"} {
		ref := mediasource.NativeRef{ID: mediasource.NativeID(id), Kind: media.Movie}
		lib.Put(sourcetest.LibRoot{Root: mediasource.Root{Native: ref}, Items: []mediasource.Item{{Native: ref.ID}}})
	}
	f := sourcetest.NewFake(lib, mediasource.Capabilities{Kinds: []media.Kind{media.Movie}})
	bad := mediasource.NativeRef{ID: "2", Kind: media.Movie}
	f.FailRoot(bad, &mediasource.SourceError{Cause: mediasource.Unreachable})
	ctx := t.Context()
	if _, _, _, err := f.Root(ctx, bad); err == nil {
		t.Error("Root of the failed root = nil, want its error")
	}
	if _, _, ok, err := f.Root(ctx, mediasource.NativeRef{ID: "1", Kind: media.Movie}); !ok || err != nil {
		t.Errorf("Root of another root = %v %v, want it found", ok, err)
	}
	var yielded, unread []mediasource.NativeID
	err := f.Items(ctx, func(r mediasource.Root, _ []mediasource.Item) error {
		yielded = append(yielded, r.Native.ID)
		return nil
	}, func(ref mediasource.NativeRef, se *mediasource.SourceError) error {
		if se.Cause != mediasource.Unreachable {
			t.Errorf("unread cause %v, want the injected unreachable", se.Cause)
		}
		unread = append(unread, ref.ID)
		return nil
	})
	if err != nil || !slices.Equal(yielded, []mediasource.NativeID{"1", "3"}) || !slices.Equal(unread, []mediasource.NativeID{"2"}) {
		t.Errorf("Items = %v yielded %v unread %v, want 1,3 yielded and 2 unread", err, yielded, unread)
	}
	f.FailRoot(bad, nil)
	if _, _, ok, err := f.Root(ctx, bad); !ok || err != nil {
		t.Errorf("Root after clearing = %v %v, want found", ok, err)
	}
}

func TestFakeKindServesTheFake(t *testing.T) {
	f := sourcetest.NewFake(&sourcetest.Library{}, mediasource.Capabilities{Kinds: []media.Kind{media.Movie}})
	kind := sourcetest.FakeKind("fake-a", f)
	if kind.Type != "fake-a" || !reflect.DeepEqual(kind.Schema, sourcetest.FakeSchema) {
		t.Errorf("FakeKind = type %q, schema %+v; want fake-a over FakeSchema", kind.Type, kind.Schema)
	}
	for _, token := range []string{"one", "two"} {
		ad, err := kind.New(mediasource.Config{Type: "fake-a", Values: fakeValues(t, token)}, mediasource.Deps{})
		if err != nil || ad != mediasource.Adapter(f) {
			t.Errorf("New(token %s) = %v, %v; want the Fake itself", token, ad, err)
		}
	}
}

func TestFakeChangesExpired(t *testing.T) {
	f := sourcetest.NewFake(&sourcetest.Library{}, mediasource.Capabilities{Kinds: []media.Kind{media.Movie}, Changes: true})
	var cur mediasource.Cursor
	_ = cur.UnmarshalBinary([]byte{9, 9})
	_, _, err := f.Changes(t.Context(), cur)
	var se *mediasource.SourceError
	if !errors.As(err, &se) || se.Cause != mediasource.Expired {
		t.Errorf("Changes from an unreadable cursor = %v, want expired", err)
	}
}

func fakeValues(t testing.TB, token string) formschema.Values {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("token: "+token+"\n"), &doc); err != nil {
		t.Fatal(err)
	}
	vals, errs := sourcetest.FakeSchema.Decode(doc.Content[0])
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return vals
}
