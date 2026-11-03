package mediasource_test

import (
	"context"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/mediasource"
)

func tagged(id string, tags ...mediasource.TagRef) mediasource.Item {
	it := episode(id, 1, 1)
	it.Tags = tags
	return it
}

func tagsOf(m map[mediasource.TagRef]string) func(context.Context) (map[mediasource.TagRef]string, error) {
	return func(context.Context) (map[mediasource.TagRef]string, error) { return m, nil }
}

func TestTagReadFailureFailsClosed(t *testing.T) {
	var l lib
	l.add(series("1"), tagged("11", "7"))
	s := &script{caps: allCaps, items: l.walk, tags: func(context.Context) (map[mediasource.TagRef]string, error) {
		return nil, &mediasource.SourceError{Cause: mediasource.Unreachable, Err: errDown}
	}, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
		return l[0].root, l[0].items, true, nil
	}}
	set, _ := oneSet(t, s, mediasource.Config{ExcludeTags: []string{"no-subflux"}})
	var w walkLog
	if err := set.Items(t.Context(), "src", w.yield, w.report); causeOf(t, err) != mediasource.Unreachable || len(w.yielded) != 0 {
		t.Errorf("Items = %v, yielded %s; want the tag read's error and nothing yielded", err, joined(w.yielded))
	}
	if _, items, found, err := set.Root(t.Context(), "src", series("1")); causeOf(t, err) != mediasource.Unreachable || found || items != nil {
		t.Errorf("Root = %v %v %v, want the tag read's error and no item", found, items, err)
	}
}

func TestExcludedItemMarked(t *testing.T) {
	var l lib
	l.add(series("1"), tagged("11", "7"), tagged("12", "8"), tagged("13"))
	s := &script{caps: allCaps, items: l.walk, tags: tagsOf(map[mediasource.TagRef]string{"7": " No-Subflux ", "8": "anime"})}
	set, _ := oneSet(t, s, mediasource.Config{ExcludeTags: []string{"no-subflux"}})
	var got []bool
	err := set.Items(t.Context(), "src", func(_ mediasource.Root, items []mediasource.Item) error {
		for _, it := range items {
			got = append(got, it.Excluded)
		}
		return nil
	}, (&walkLog{}).report)
	if err != nil || len(got) != 3 || !got[0] || got[1] || got[2] {
		t.Errorf("Items = %v, excluded %v; want only the item tagged no-subflux", err, got)
	}
}

func TestExcludedTagPastTheCut(t *testing.T) {
	tags := make([]mediasource.TagRef, 0, 65)
	for i := range 64 {
		tags = append(tags, mediasource.TagRef(strconv.Itoa(100+i)))
	}
	it := tagged("11", append(tags, "7")...)
	var l lib
	l.add(series("1"), it)
	s := &script{
		caps: allCaps, items: l.walk, tags: tagsOf(map[mediasource.TagRef]string{"7": "no-subflux"}),
		root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
			return l[0].root, l[0].items, true, nil
		},
	}
	set, _ := oneSet(t, s, mediasource.Config{ExcludeTags: []string{"no-subflux"}})
	t.Run("items", func(t *testing.T) {
		var got []mediasource.Item
		err := set.Items(t.Context(), "src", func(_ mediasource.Root, items []mediasource.Item) error {
			got = items
			return nil
		}, (&walkLog{}).report)
		if err != nil || len(got) != 1 || !got[0].Excluded || len(got[0].Tags) != 64 {
			t.Errorf("Items = %v, %v; want the item excluded by its 65th tag and its tags cut to 64", err, got)
		}
	})
	t.Run("root", func(t *testing.T) {
		_, items, found, err := set.Root(t.Context(), "src", series("1"))
		if err != nil || !found || len(items) != 1 || !items[0].Excluded || len(items[0].Tags) != 64 {
			t.Errorf("Root = %v %v %v; want the item excluded by its 65th tag and its tags cut to 64", found, items, err)
		}
	})
}

func TestAdapterExcludedOverwritten(t *testing.T) {
	it := tagged("11")
	it.Excluded = true
	var l lib
	l.add(series("1"), it)
	cases := map[string]mediasource.Config{"with labels": {ExcludeTags: []string{"no-subflux"}}, "with none": {}}
	for name, cfg := range cases {
		s := &script{caps: allCaps, items: l.walk, tags: tagsOf(nil)}
		set, _ := oneSet(t, s, cfg)
		_ = set.Items(t.Context(), "src", func(_ mediasource.Root, items []mediasource.Item) error {
			if items[0].Excluded {
				t.Errorf("%s: an untagged item the adapter marked excluded is still excluded", name)
			}
			return nil
		}, nil)
	}
}

func TestUnmatchedLabelWarnsOncePerPass(t *testing.T) {
	var l lib
	l.add(series("1"), tagged("11"))
	s := &script{
		caps: allCaps, items: l.walk, tags: tagsOf(map[mediasource.TagRef]string{"1": "anime"}),
		root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
			return l[0].root, l[0].items, true, nil
		},
	}
	set, rec := oneSet(t, s, mediasource.Config{ExcludeTags: []string{"no-subflux", "Keep-Out"}})
	for range 2 {
		if err := set.Items(t.Context(), "src", (&walkLog{}).yield, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := set.Root(t.Context(), "src", series("1")); err != nil {
		t.Fatal(err)
	}
	if n := rec.Count("name no tag"); n != 2 {
		t.Errorf("%d unmatched-label WARNs over two passes and a Root, want 2", n)
	}
	if v, _ := rec.AttrValue("name no tag", "labels"); v != "[keep-out]" {
		t.Errorf("WARN names %s, want [keep-out] alone: the default label is silent", v)
	}

	quiet, rec := oneSet(t, &script{caps: allCaps, items: l.walk, tags: tagsOf(nil)}, mediasource.Config{ExcludeTags: []string{"no-subflux"}})
	if err := quiet.Items(t.Context(), "src", (&walkLog{}).yield, nil); err != nil || rec.Len() != 0 {
		t.Errorf("default label matching no tag: %v, logged %q; want nothing", err, rec.Messages())
	}
}

func TestRootReusesTagsWithinFiveMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tagReads int
		s := &script{caps: allCaps, tags: func(context.Context) (map[mediasource.TagRef]string, error) {
			tagReads++
			return nil, nil
		}, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
			return mediasource.Root{Native: series("1")}, nil, true, nil
		}}
		set, _ := oneSet(t, s, mediasource.Config{ExcludeTags: []string{"no-subflux"}})
		root := func() {
			if _, _, _, err := set.Root(t.Context(), "src", series("1")); err != nil {
				t.Fatal(err)
			}
		}
		root()
		time.Sleep(5*time.Minute - time.Second)
		root()
		if tagReads != 1 {
			t.Errorf("%d tag reads within 5 minutes, want 1", tagReads)
		}
		time.Sleep(time.Second)
		root()
		if tagReads != 2 {
			t.Errorf("%d tag reads at 5 minutes, want 2", tagReads)
		}
		if err := set.Items(t.Context(), "src", nil, nil); err != nil || tagReads != 3 {
			t.Errorf("Items = %v after %d tag reads, want a fresh read", err, tagReads)
		}
	})
}

func TestNoTagsCapabilityExcludesNothing(t *testing.T) {
	caps := allCaps
	caps.Tags = false
	var l lib
	l.add(series("1"), tagged("11", "7"))
	for labels, warns := range map[string]int{"no-subflux": 0, "keep-out": 1} {
		s := &script{caps: caps, items: l.walk}
		set, rec := oneSet(t, s, mediasource.Config{ExcludeTags: []string{labels}})
		if n := rec.Count("cannot read tags"); n != warns {
			t.Errorf("labels [%s]: %d NewSet WARNs, want %d", labels, n, warns)
		}
		err := set.Items(t.Context(), "src", func(_ mediasource.Root, items []mediasource.Item) error {
			if items[0].Excluded {
				t.Errorf("labels [%s]: an item excluded by a source without tags", labels)
			}
			return nil
		}, nil)
		if err != nil || s.calls.Load() != 1 {
			t.Errorf("labels [%s]: Items = %v after %d adapter calls, want one Items call and no Tags", labels, err, s.calls.Load())
		}
	}
}
