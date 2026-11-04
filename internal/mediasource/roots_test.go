package mediasource_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediasource"
	"go.yaml.in/yaml/v3"
)

var errDown = errors.New("connection refused")

func failingFolders(context.Context) ([]string, error) {
	return nil, &mediasource.SourceError{Cause: mediasource.Unreachable, Err: errDown}
}

func folders(f ...string) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) { return f, nil }
}

// arrValues decodes an entry's url and api_key by scriptKind's schema.
func arrValues(t *testing.T, url, key string) formschema.Values {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("url: "+url+"\napi_key: "+key+"\n"), &doc); err != nil {
		t.Fatal(err)
	}
	vals, errs := scriptKind("script", nil).Schema.Decode(doc.Content[0])
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return vals
}

// rebuild builds a Set of src (script s) and other (a script with no
// capabilities beyond listing), as app does after a Save.
func rebuild(t *testing.T, s *script, src mediasource.Config) *mediasource.Set {
	t.Helper()
	src.ID, src.Type = "src", "script"
	other := mediasource.Config{ID: "other", Type: "other"}
	kinds := []mediasource.AdapterKind{scriptKind("script", s), scriptKind("other", &script{caps: allCaps})}
	log, _ := capture.New()
	set, err := mediasource.NewSet(kinds, []mediasource.Config{src, other}, mediasource.Deps{Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestMediaRootsInheritedAcrossRebuild(t *testing.T) {
	cfg := mediasource.Config{Values: arrValues(t, "http://sonarr:8989", "k1")}
	prev := rebuild(t, &script{caps: allCaps, folders: folders("/tv")}, cfg)
	if got := prev.MediaRoots(t.Context()); !slices.Equal(got, []string{"/tv"}) {
		t.Fatalf("first Set roots %q, want [/tv]", got)
	}
	next := rebuild(t, &script{caps: allCaps, folders: failingFolders}, cfg)
	next.Inherit(prev)
	if got := next.MediaRoots(t.Context()); !slices.Equal(got, []string{"/tv"}) {
		t.Errorf("rebuilt Set roots %q with RootFolders failing, want the inherited [/tv]", got)
	}
	next.Inherit(nil)
}

func TestMediaRootsCoalescedFill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		release := make(chan struct{})
		s := &script{caps: allCaps, folders: func(ctx context.Context) ([]string, error) {
			calls.Add(1)
			select {
			case <-release:
				return []string{"/tv"}, nil
			case <-ctx.Done():
				return nil, &mediasource.SourceError{Cause: mediasource.Unreachable, Err: ctx.Err()}
			}
		}}
		set, _ := oneSet(t, s, mediasource.Config{})
		first, cancel := context.WithCancel(t.Context())
		firstDone := make(chan []string)
		go func() { firstDone <- set.MediaRoots(first) }()
		synctest.Wait()
		var wg sync.WaitGroup
		got := make([][]string, 19)
		for i := range got {
			wg.Go(func() { got[i] = set.MediaRoots(t.Context()) })
		}
		synctest.Wait()
		cancel()
		if r := <-firstDone; len(r) != 0 {
			t.Errorf("cancelled first caller got %q, want nothing", r)
		}
		close(release)
		wg.Wait()
		for i, r := range got {
			if !slices.Equal(r, []string{"/tv"}) {
				t.Errorf("caller %d got %q, want [/tv]", i+1, r)
			}
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("%d RootFolders calls, want one fill for every caller", n)
		}
	})
}

func TestInheritAcrossKeyOnlyEdit(t *testing.T) {
	cfg := mediasource.Config{Values: arrValues(t, "http://sonarr:8989", "k1"), ExcludeTags: []string{"no-subflux"}}
	up := &script{caps: allCaps, folders: folders("/tv"), tags: func(context.Context) (map[mediasource.TagRef]string, error) {
		return map[mediasource.TagRef]string{"1": "no-subflux"}, nil
	}}
	prev := rebuild(t, up, cfg)
	if err := prev.Items(t.Context(), "src", (&walkLog{}).yield, (&walkLog{}).report); err != nil {
		t.Fatal(err)
	}
	if got := prev.MediaRoots(t.Context()); !slices.Equal(got, []string{"/tv"}) {
		t.Fatalf("roots %q, want [/tv]", got)
	}
	down := func() *script {
		return &script{caps: allCaps, folders: failingFolders, tags: func(context.Context) (map[mediasource.TagRef]string, error) {
			return nil, &mediasource.SourceError{Cause: mediasource.Unreachable, Err: errDown}
		}, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
			return mediasource.Root{Native: series("1")}, nil, true, nil
		}}
	}
	t.Run("an api_key edit keeps both caches", func(t *testing.T) {
		rekeyed := cfg
		rekeyed.Values = arrValues(t, "http://sonarr:8989", "k2")
		next := rebuild(t, down(), rekeyed)
		next.Inherit(prev)
		if got := next.MediaRoots(t.Context()); !slices.Equal(got, []string{"/tv"}) {
			t.Errorf("roots %q, want the inherited [/tv]", got)
		}
		if _, _, _, err := next.Root(t.Context(), "src", series("1")); err != nil {
			t.Errorf("Root = %v, want the inherited tag cache to answer", err)
		}
	})
	t.Run("a url edit drops both", func(t *testing.T) {
		moved := cfg
		moved.Values = arrValues(t, "http://sonarr-2:8989", "k1")
		next := rebuild(t, down(), moved)
		next.Inherit(prev)
		if got := next.MediaRoots(t.Context()); len(got) != 0 {
			t.Errorf("roots %q, want none: another url may name another instance", got)
		}
		if _, _, _, err := next.Root(t.Context(), "src", series("1")); err == nil {
			t.Error("Root = nil, want the failed tag read: the tag cache is not inherited across a url edit")
		}
	})
}

func TestMediaRootsFromMappingsAndFolders(t *testing.T) {
	kinds := []mediasource.AdapterKind{
		scriptKind("mapped", &script{caps: allCaps, folders: folders("/ignored")}),
		scriptKind("unmapped", &script{caps: allCaps, folders: folders("/tv", "/m1")}),
	}
	cfgs := []mediasource.Config{
		{ID: "a", Type: "mapped", PathMappings: []mediasource.PathMapping{{From: "/x", To: "/m2"}, {From: "/y", To: "/m1"}}},
		{ID: "b", Type: "unmapped"},
	}
	set, err := mediasource.NewSet(kinds, cfgs, mediasource.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if got := set.MediaRoots(t.Context()); !slices.Equal(got, []string{"/m1", "/m2", "/tv"}) {
		t.Errorf("MediaRoots = %q, want [/m1 /m2 /tv]", got)
	}
}

func TestMediaRootsAfterFailedRefresh(t *testing.T) {
	s := &script{caps: allCaps, folders: folders("/tv")}
	set, rec := oneSet(t, s, mediasource.Config{})
	if got := set.MediaRoots(t.Context()); !slices.Equal(got, []string{"/tv"}) {
		t.Fatalf("roots %q, want [/tv]", got)
	}
	s.folders = failingFolders
	if _, err := set.RootFolders(t.Context(), "src"); err == nil {
		t.Fatal("probe answered nil, want its failure")
	}
	if got := set.MediaRoots(t.Context()); !slices.Equal(got, []string{"/tv"}) {
		t.Errorf("roots after a failed probe %q, want the last answer [/tv]", got)
	}
	s.folders = folders("/tv2")
	if _, err := set.RootFolders(t.Context(), "src"); err != nil {
		t.Fatal(err)
	}
	if got := set.MediaRoots(t.Context()); !slices.Equal(got, []string{"/tv2"}) {
		t.Errorf("roots after a good probe %q, want [/tv2]", got)
	}
	if rec.Len() != 0 {
		t.Errorf("logged %q, want nothing", rec.Messages())
	}
}

func TestMediaRootsFailedFillRootless(t *testing.T) {
	s := &script{caps: allCaps, folders: failingFolders}
	set, rec := oneSet(t, s, mediasource.Config{})
	for range 2 {
		if got := set.MediaRoots(t.Context()); len(got) != 0 {
			t.Errorf("roots %q after a failed fill, want none", got)
		}
	}
	if n := s.calls.Load(); n != 1 {
		t.Errorf("%d RootFolders calls, want the one fill per Set", n)
	}
	if rec.Count("root folders unread") != 1 {
		t.Errorf("WARNs %q, want one for the failed fill", rec.Messages())
	}
}
