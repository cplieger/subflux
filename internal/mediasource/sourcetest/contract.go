package sourcetest

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Harness adapts one source kind to the suite.
type Harness interface {
	Kind() mediasource.AdapterKind
	// Serve starts a fake upstream over lib for the test's lifetime,
	// answering from lib's state at each request, and returns an entry
	// pointing at it. Serve and Refuse return Values decoded by Kind's
	// Schema, as ParseConfigs would.
	Serve(t testing.TB, lib *Library) mediasource.Config
	// Refuse returns an entry whose upstream refuses the credential.
	Refuse(t testing.TB) mediasource.Config
	// Requests counts the requests the upstream has answered.
	Requests() int
	// NativeID is the n-th id, n from 1, in the upstream's id scheme.
	NativeID(n int) mediasource.NativeID
}

const contractID media.SourceID = "contract"

// secretKey names a field that must be a Secret.
var secretKey = regexp.MustCompile(`(key|token|password|secret)$`)

// Contract runs the suite through a Set over h's kind: the schema, New
// making no request, Root agreeing with Items, an unknown root answering
// false, the yield's error coming back unchanged, UnreadRootContinuesWalk,
// Changes from the head naming each mutated root once with its cursor's
// bytes round-tripping, every capable method answering, a refused credential
// answering Unauthorized, and Unsupported for each false capability.
func Contract(t *testing.T, h Harness) {
	t.Helper()
	kind := h.Kind()
	t.Run("schema", func(t *testing.T) { checkSchema(t, kind.Schema) })
	lib := &Library{}
	cfg := h.Serve(t, lib)
	cfg.ID, cfg.Type = contractID, kind.Type
	t.Run("New makes no request", func(t *testing.T) {
		before := h.Requests()
		if _, err := kind.New(cfg, mediasource.Deps{}); err != nil {
			t.Fatalf("New = %v", err)
		}
		if n := h.Requests() - before; n != 0 {
			t.Errorf("New sent %d requests, want none", n)
		}
	})
	set := contractSet(t, kind, &cfg)
	caps, _ := set.Capabilities(contractID)
	fill(lib, h, caps.Kinds)
	c := &contract{h: h, lib: lib, set: set, caps: caps}
	t.Run("Root agrees with Items", c.rootAgrees)
	t.Run("unknown root answers false", c.unknownRoot)
	t.Run("yield error comes back unchanged", c.yieldError)
	t.Run("UnreadRootContinuesWalk", c.unreadRootContinuesWalk)
	if caps.Changes {
		t.Run("Changes from the head", c.changesFromHead)
	}
	t.Run("capable methods answer", func(t *testing.T) { c.capableMethods(t, kind, &cfg) })
	t.Run("refused credential", func(t *testing.T) {
		refused := h.Refuse(t)
		refused.ID, refused.Type = contractID, kind.Type
		err := contractSet(t, kind, &refused).Ping(t.Context(), contractID)
		if cause(err) != mediasource.Unauthorized {
			t.Errorf("Ping with a refused credential = %v, want unauthorized", err)
		}
	})
	t.Run("a false capability is unsupported", c.unsupported)
}

type contract struct {
	h    Harness
	lib  *Library
	set  *mediasource.Set
	caps mediasource.Capabilities
}

func checkSchema(t *testing.T, s formschema.Schema) {
	seen := map[string]bool{}
	for _, f := range s.Fields {
		if f.Key == "" || seen[f.Key] {
			t.Errorf("schema key %q is empty or repeated", f.Key)
		}
		seen[f.Key] = true
		if secretKey.MatchString(f.Key) && f.Kind != formschema.Secret {
			t.Errorf("schema key %q names a credential but is not a Secret", f.Key)
		}
	}
}

func contractSet(t *testing.T, kind mediasource.AdapterKind, cfg *mediasource.Config) *mediasource.Set {
	t.Helper()
	set, err := mediasource.NewSet([]mediasource.AdapterKind{kind}, []mediasource.Config{*cfg}, mediasource.Deps{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("NewSet = %v", err)
	}
	return set
}

// fill puts three roots of each kind: an episode root with two numbered
// items, a movie root with one item.
func fill(lib *Library, h Harness, kinds []media.Kind) {
	n := 0
	next := func() mediasource.NativeID {
		n++
		return h.NativeID(n)
	}
	lib.SetTags(map[mediasource.TagRef]string{mediasource.TagRef(next()): "anime"})
	lib.SetFolders([]string{"/library"})
	for _, k := range kinds {
		for range 3 {
			ref := mediasource.NativeRef{ID: next(), Kind: k}
			r := LibRoot{Root: mediasource.Root{Native: ref, Title: "Title " + string(ref.ID)}}
			if k == media.Movie {
				r.Items = []mediasource.Item{{Native: ref.ID, VideoPath: "/library/" + string(ref.ID) + ".mkv"}}
			} else {
				for e := 1; e <= 2; e++ {
					id := next()
					r.Items = append(r.Items, mediasource.Item{
						Native:          id,
						SeriesNumbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: e},
						VideoPath:       fmt.Sprintf("/library/%s/s01e%02d.mkv", ref.ID, e),
					})
				}
			}
			lib.Put(r)
		}
	}
}

type walked struct {
	roots  map[mediasource.NativeRef]*walkedRoot
	order  []mediasource.NativeRef
	unread []mediasource.NativeRef
}

type walkedRoot struct {
	items []mediasource.Item
	root  mediasource.Root
}

func (c *contract) walk(t *testing.T) walked {
	t.Helper()
	w := walked{roots: map[mediasource.NativeRef]*walkedRoot{}}
	err := c.set.Items(t.Context(), contractID, func(r mediasource.Root, items []mediasource.Item) error {
		w.roots[r.Native] = &walkedRoot{items: items, root: r}
		w.order = append(w.order, r.Native)
		return nil
	}, func(ref mediasource.NativeRef, _ *mediasource.SourceError) error {
		w.unread = append(w.unread, ref)
		return nil
	})
	if err != nil {
		t.Fatalf("Items = %v", err)
	}
	return w
}

func (c *contract) listed() []mediasource.NativeRef {
	roots, _, _, _ := c.lib.Snapshot()
	refs := make([]mediasource.NativeRef, len(roots))
	for i := range roots {
		refs[i] = roots[i].Root.Native
	}
	return refs
}

func (c *contract) rootAgrees(t *testing.T) {
	w := c.walk(t)
	if !sameRefs(w.order, c.listed()) {
		t.Fatalf("Items yielded %v, want every listed root %v", w.order, c.listed())
	}
	for ref, got := range w.roots {
		r, items, ok, err := c.set.Root(t.Context(), contractID, ref)
		if err != nil || !ok {
			t.Errorf("Root(%v) = %v %v, want the root Items yielded", ref, ok, err)
			continue
		}
		if !reflect.DeepEqual(r, got.root) || !reflect.DeepEqual(items, got.items) {
			t.Errorf("Root(%v) = %+v %+v, Items yielded %+v %+v", ref, r, items, got.root, got.items)
		}
	}
}

func (c *contract) unknownRoot(t *testing.T) {
	ref := mediasource.NativeRef{ID: c.h.NativeID(9999), Kind: c.caps.Kinds[0]}
	if _, _, ok, err := c.set.Root(t.Context(), contractID, ref); ok || err != nil {
		t.Errorf("Root of an unknown root = %v %v, want false and no error", ok, err)
	}
}

var errContractSentinel = errors.New("contract: the caller's own error")

func (c *contract) yieldError(t *testing.T) {
	err := c.set.Items(t.Context(), contractID, func(mediasource.Root, []mediasource.Item) error { return errContractSentinel }, nil)
	if err != errContractSentinel { //nolint:errorlint // the sentinel itself, unwrapped, is the contract
		t.Errorf("Items = %v, want the yield's error itself", err)
	}
}

func (c *contract) unreadRootContinuesWalk(t *testing.T) {
	roots, _, _, _ := c.lib.Snapshot()
	broken := roots[1]
	broken.ReadFails = true
	c.lib.Put(broken)
	defer func() {
		broken.ReadFails = false
		c.lib.Put(broken)
	}()
	w := c.walk(t)
	want := slices.DeleteFunc(c.listed(), func(r mediasource.NativeRef) bool { return r == broken.Root.Native })
	if !slices.Equal(w.unread, []mediasource.NativeRef{broken.Root.Native}) || !sameRefs(w.order, want) {
		t.Errorf("Items reported %v unread and yielded %v, want %v unread and %v yielded", w.unread, w.order, broken.Root.Native, want)
	}
}

func (c *contract) changesFromHead(t *testing.T) {
	ctx := t.Context()
	changes, head, err := c.set.Changes(ctx, contractID, mediasource.Cursor{})
	if err != nil || len(changes) != 0 {
		t.Fatalf("Changes from the zero cursor = %v %v, want none and the head", changes, err)
	}
	roots, _, _, _ := c.lib.Snapshot()
	changed := roots[0]
	changed.Root.Title += " (renamed)"
	added := roots[2]
	added.Root.Native.ID = c.h.NativeID(5000)
	removed := roots[len(roots)-1].Root.Native
	c.lib.Put(changed)
	c.lib.Put(changed)
	c.lib.Put(added)
	c.lib.Remove(removed)
	want := []mediasource.NativeRef{changed.Root.Native, added.Root.Native, removed}

	raw, err := head.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var reread mediasource.Cursor
	if err := reread.UnmarshalBinary(raw); err != nil {
		t.Fatal(err)
	}
	for name, cur := range map[string]mediasource.Cursor{"head": head, "head's bytes": reread} {
		got, _, err := c.set.Changes(ctx, contractID, cur)
		refs := make([]mediasource.NativeRef, len(got))
		for i, ch := range got {
			refs[i] = ch.Root
		}
		if err != nil || !sameRefs(refs, want) || len(refs) != len(want) {
			t.Errorf("Changes from the %s = %v %v, want each of %v once", name, refs, err, want)
		}
	}
	c.lib.Put(roots[len(roots)-1])
}

func (c *contract) capableMethods(t *testing.T, kind mediasource.AdapterKind, cfg *mediasource.Config) {
	ctx := t.Context()
	_, tags, folders, _ := c.lib.Snapshot()
	if c.caps.Tags {
		c.adapterTags(t, kind, cfg, tags)
	}
	if got, err := c.set.RootFolders(ctx, contractID); err != nil || !slices.Equal(got, folders) {
		t.Errorf("RootFolders = %v %v, want %v", got, err, folders)
	}
	if err := c.set.Ping(ctx, contractID); err != nil {
		t.Errorf("Ping = %v, want nil", err)
	}
	ref := c.listed()[0]
	if c.caps.Rescan {
		if err := c.set.Rescan(ctx, contractID, ref); err != nil {
			t.Errorf("Rescan = %v, want nil", err)
		}
	}
	if c.caps.Posters {
		c.poster(t, ref)
	}
}

func (*contract) adapterTags(t *testing.T, kind mediasource.AdapterKind, cfg *mediasource.Config, tags map[mediasource.TagRef]string) {
	ad, err := kind.New(*cfg, mediasource.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ad.Tags(t.Context()); err != nil || !maps.Equal(got, tags) {
		t.Errorf("Tags = %v %v, want %v", got, err, tags)
	}
}

func (c *contract) poster(t *testing.T, ref mediasource.NativeRef) {
	body, _, _, err := c.set.Poster(t.Context(), contractID, ref)
	if err != nil {
		t.Errorf("Poster = %v, want an answer", err)
	}
	if body != nil {
		_, _ = io.Copy(io.Discard, body)
		_ = body.Close()
	}
}

func (c *contract) unsupported(t *testing.T) {
	ctx := t.Context()
	ref := c.listed()[0]
	calls := map[string]struct {
		call func() error
		has  bool
	}{
		"Changes": {func() error { _, _, err := c.set.Changes(ctx, contractID, mediasource.Cursor{}); return err }, c.caps.Changes},
		"Rescan":  {func() error { return c.set.Rescan(ctx, contractID, ref) }, c.caps.Rescan},
		"Poster":  {func() error { _, _, _, err := c.set.Poster(ctx, contractID, ref); return err }, c.caps.Posters},
	}
	for name, m := range calls {
		if !m.has && cause(m.call()) != mediasource.Unsupported {
			t.Errorf("%s without the capability did not answer unsupported", name)
		}
	}
}

func cause(err error) mediasource.Cause {
	var se *mediasource.SourceError
	if !errors.As(err, &se) {
		return 0
	}
	return se.Cause
}

func sameRefs(a, b []mediasource.NativeRef) bool {
	return len(a) == len(b) && !slices.ContainsFunc(a, func(r mediasource.NativeRef) bool { return !slices.Contains(b, r) })
}
