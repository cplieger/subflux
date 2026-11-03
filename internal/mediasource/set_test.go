package mediasource_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

func TestYieldErrorPassesThrough(t *testing.T) {
	sentinel := errors.New("store full")
	var l lib
	l.add(series("1"), episode("11", 1, 1))
	s := &script{caps: allCaps, items: func(ctx context.Context, yield func(mediasource.Root, []mediasource.Item) error, u mediasource.Unread) error {
		if err := l.walk(ctx, yield, u); err != nil {
			return fmt.Errorf("walk: %w", err)
		}
		return nil
	}}
	set, _ := oneSet(t, s, mediasource.Config{})
	t.Run("yield", func(t *testing.T) {
		err := set.Items(t.Context(), "src", func(mediasource.Root, []mediasource.Item) error { return sentinel }, nil)
		if err != sentinel { //nolint:errorlint // the sentinel itself, unwrapped, is the contract
			t.Errorf("Items = %v, want the yield's sentinel itself", err)
		}
	})
	t.Run("unread", func(t *testing.T) {
		var bad lib
		bad.add(series("1"), mediasource.Item{Native: "x/y"})
		s.items = func(ctx context.Context, yield func(mediasource.Root, []mediasource.Item) error, u mediasource.Unread) error {
			return bad.walk(ctx, yield, u)
		}
		err := set.Items(t.Context(), "src", (&walkLog{}).yield, func(mediasource.NativeRef, *mediasource.SourceError) error { return sentinel })
		if err != sentinel { //nolint:errorlint // the sentinel itself, unwrapped, is the contract
			t.Errorf("Items = %v, want the unread callback's sentinel itself", err)
		}
	})
}

func TestCancelledIsNotSourceError(t *testing.T) {
	s := &script{caps: allCaps, items: func(ctx context.Context, _ func(mediasource.Root, []mediasource.Item) error, _ mediasource.Unread) error {
		return &mediasource.SourceError{Cause: mediasource.Unreachable, Err: ctx.Err()}
	}}
	set, _ := oneSet(t, s, mediasource.Config{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := set.Items(ctx, "src", (&walkLog{}).yield, (&walkLog{}).report)
	var se *mediasource.SourceError
	if !errors.Is(err, context.Canceled) || errors.As(err, &se) {
		t.Errorf("Items on a cancelled context = %v, want context.Canceled and no *SourceError", err)
	}
}

func TestDeadlineIsUnreachable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &script{caps: allCaps, ping: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}}
		set, _ := oneSet(t, s, mediasource.Config{})
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		err := set.Ping(ctx, "src")
		if got := causeOf(t, err); got != mediasource.Unreachable {
			t.Errorf("Ping past its deadline: cause %v, want unreachable", got)
		}
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "did not answer in time") {
			t.Errorf("Ping past its deadline = %v, want a deadline the text names", err)
		}
	})
}

func TestAnswerAtDeadlineKept(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cases := []struct {
			name   string
			answer *mediasource.SourceError
			kept   bool
		}{
			{"outcome unknown", &mediasource.SourceError{Cause: mediasource.Unreachable, Err: fmt.Errorf("%w: HTTP 201", mediasource.ErrRescanOutcomeUnknown)}, true},
			{"unauthorized", &mediasource.SourceError{Cause: mediasource.Unauthorized, Err: errDown}, true},
			{"no cause", &mediasource.SourceError{Err: errDown}, false},
		}
		for _, c := range cases {
			s := &script{caps: allCaps, rescan: func(ctx context.Context, _ mediasource.NativeRef) error {
				<-ctx.Done()
				return c.answer
			}}
			set, _ := oneSet(t, s, mediasource.Config{})
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			err := set.Rescan(ctx, "src", series("1"))
			cancel()
			late := strings.Contains(err.Error(), "did not answer in time")
			switch {
			case c.kept && (causeOf(t, err) != c.answer.Cause || !errors.Is(err, c.answer.Err) || late):
				t.Errorf("%s answered at the deadline: Rescan = %v, want the source's answer", c.name, err)
			case !c.kept && (causeOf(t, err) != mediasource.Unreachable || !late):
				t.Errorf("%s at the deadline: Rescan = %v, want unreachable, did not answer in time", c.name, err)
			}
		}
	})
}

func TestItemsRootViolationReportsUnread(t *testing.T) {
	walkOf := func(l lib) *script {
		return &script{caps: allCaps, items: l.walk}
	}
	t.Run("one bad root is unread, the rest yielded", func(t *testing.T) {
		var l lib
		l.add(series("A"), episode("1", 1, 1))
		l.add(series("B"), mediasource.Item{Native: "2", SeriesNumbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}, VideoPath: "/tv/b\n.mkv"})
		l.add(series("C"), episode("3", 1, 1))
		set, _ := oneSet(t, walkOf(l), mediasource.Config{})
		var w walkLog
		if err := set.Items(t.Context(), "src", w.yield, w.report); err != nil {
			t.Fatalf("Items = %v, want the walk to go on past B", err)
		}
		if got := joined(w.yielded); got != "A,C" {
			t.Errorf("yielded %s, want A,C", got)
		}
		if got := joined(w.unread); got != "B" || w.causes[0] != mediasource.Protocol {
			t.Errorf("unread %s %v, want B protocol", got, w.causes)
		}
	})
	t.Run("a root yielded twice fails the call", func(t *testing.T) {
		var l lib
		l.add(series("A"))
		l.add(series("A"))
		set, _ := oneSet(t, walkOf(l), mediasource.Config{})
		var w walkLog
		err := set.Items(t.Context(), "src", w.yield, w.report)
		if causeOf(t, err) != mediasource.Protocol || joined(w.yielded) != "A" {
			t.Errorf("Items = %v after yielding %s, want protocol after A alone", err, joined(w.yielded))
		}
	})
	t.Run("the third consecutive unread root fails the call", func(t *testing.T) {
		var l lib
		l.add(series("A"))
		for _, id := range []string{"B", "C", "D"} {
			l.add(series(id), mediasource.Item{Native: "bad/id"})
		}
		l.add(series("E"))
		set, _ := oneSet(t, walkOf(l), mediasource.Config{})
		var w walkLog
		err := set.Items(t.Context(), "src", w.yield, w.report)
		if causeOf(t, err) != mediasource.Protocol || !strings.Contains(err.Error(), "root D") {
			t.Errorf("Items = %v, want the third unread root D's error", err)
		}
		if joined(w.unread) != "B,C" || joined(w.yielded) != "A" {
			t.Errorf("unread %s yielded %s, want B,C and A", joined(w.unread), joined(w.yielded))
		}
	})
	t.Run("the 33rd unread root fails the call", func(t *testing.T) {
		var l lib
		for i := range 33 {
			l.add(series(fmt.Sprintf("bad%d", i)), mediasource.Item{Native: "bad/id"})
			l.add(series(fmt.Sprintf("ok%d", i)))
		}
		set, _ := oneSet(t, walkOf(l), mediasource.Config{})
		var w walkLog
		err := set.Items(t.Context(), "src", w.yield, w.report)
		if causeOf(t, err) != mediasource.Protocol || !strings.Contains(err.Error(), "root bad32") {
			t.Errorf("Items = %v, want the 33rd unread root's error", err)
		}
		if len(w.unread) != 32 || len(w.yielded) != 32 {
			t.Errorf("unread %d yielded %d, want 32 and 32", len(w.unread), len(w.yielded))
		}
	})
	t.Run("the adapter's own reports count", func(t *testing.T) {
		s := &script{caps: allCaps, items: func(_ context.Context, yield func(mediasource.Root, []mediasource.Item) error, unread mediasource.Unread) error {
			for _, id := range []string{"A", "B", "C"} {
				if err := unread(series(id), &mediasource.SourceError{Cause: mediasource.Unreachable, Err: errors.New("episodes: 500")}); err != nil {
					return err
				}
			}
			return yield(mediasource.Root{Native: series("D")}, nil)
		}}
		set, _ := oneSet(t, s, mediasource.Config{})
		var w walkLog
		err := set.Items(t.Context(), "src", w.yield, w.report)
		if causeOf(t, err) != mediasource.Unreachable || joined(w.unread) != "A,B" || len(w.yielded) != 0 {
			t.Errorf("Items = %v, unread %s, yielded %s: want C's unreachable after A,B", err, joined(w.unread), joined(w.yielded))
		}
		var se *mediasource.SourceError
		if errors.As(err, &se); se.Source != "src" || se.Op != "Items" {
			t.Errorf("error names %q %q, want the Set's source and op", se.Source, se.Op)
		}
	})
}

// TestAnswerTable has one subtest per row of the Set's answer check.
func TestAnswerTable(t *testing.T) {
	ok := episode("1", 1, 1)
	perRoot := map[string]mediasource.Item{
		"item id of 65 bytes":    {Native: mediasource.NativeID(strings.Repeat("a", 65)), SeriesNumbering: ok.SeriesNumbering},
		"item id with a slash":   {Native: "a/b", SeriesNumbering: ok.SeriesNumbering},
		"relative video path":    {Native: "1", SeriesNumbering: ok.SeriesNumbering, VideoPath: "tv/a.mkv"},
		"unclean video path":     {Native: "1", SeriesNumbering: ok.SeriesNumbering, VideoPath: "/tv/../a.mkv"},
		"unmapped under a to":    {Native: "1", SeriesNumbering: ok.SeriesNumbering, VideoPath: "/media/x.mkv"},
		"season past 65535":      {Native: "1", SeriesNumbering: media.Numbering{Scheme: media.TVDB, Season: 65536, Episode: 1}},
		"episode numbering none": {Native: "1", SeriesNumbering: media.Numbering{Scheme: media.None, Season: 1, Episode: 1}},
		"air date not a date":    {Native: "1", SeriesNumbering: ok.SeriesNumbering, AirDate: "2024-13-01"},
	}
	mapped := mediasource.Config{PathMappings: []mediasource.PathMapping{{From: "/arr", To: "/media"}}}
	for name, bad := range perRoot {
		t.Run("per root: "+name, func(t *testing.T) {
			var l lib
			l.add(series("A"), bad)
			l.add(series("B"), ok)
			s := &script{caps: allCaps, items: l.walk, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
				return l[0].root, l[0].items, true, nil
			}}
			set, _ := oneSet(t, s, mapped)
			var w walkLog
			if err := set.Items(t.Context(), "src", w.yield, w.report); err != nil {
				t.Fatalf("Items = %v, want the walk to go on", err)
			}
			if joined(w.unread) != "A" || joined(w.yielded) != "B" || w.causes[0] != mediasource.Protocol {
				t.Errorf("unread %s %v yielded %s, want A protocol and B", joined(w.unread), w.causes, joined(w.yielded))
			}
			_, _, found, err := set.Root(t.Context(), "src", series("A"))
			if found || causeOf(t, err) != mediasource.Protocol {
				t.Errorf("Root = %v %v, want a failed call with protocol", found, err)
			}
		})
	}
	t.Run("per root: duplicate item id", func(t *testing.T) {
		var l lib
		l.add(series("A"), ok, ok)
		l.add(series("B"), ok)
		set, _ := oneSet(t, &script{caps: allCaps, items: l.walk}, mediasource.Config{})
		var w walkLog
		if err := set.Items(t.Context(), "src", w.yield, w.report); err != nil || joined(w.unread) != "A" || joined(w.yielded) != "B" {
			t.Errorf("Items = %v unread %s yielded %s, want A unread and B", err, joined(w.unread), joined(w.yielded))
		}
	})
	t.Run("per root: a movie with a numbering", func(t *testing.T) {
		var l lib
		l = append(l, struct {
			root  mediasource.Root
			items []mediasource.Item
		}{mediasource.Root{Native: mediasource.NativeRef{ID: "9", Kind: media.Movie}}, []mediasource.Item{episode("9", 1, 1)}})
		set, _ := oneSet(t, &script{caps: allCaps, items: l.walk}, mediasource.Config{})
		var w walkLog
		if err := set.Items(t.Context(), "src", w.yield, w.report); err != nil || joined(w.unread) != "9" {
			t.Errorf("Items = %v unread %s, want 9 unread", err, joined(w.unread))
		}
	})

	identity := map[string]mediasource.NativeRef{
		"root kind outside Kinds": {ID: "1", Kind: media.Movie},
		"root id of 65 bytes":     series(strings.Repeat("a", 65)),
		"root id with a slash":    series("a/b"),
		"zero root kind":          {ID: "1"},
	}
	for name, ref := range identity {
		t.Run("identity: "+name, func(t *testing.T) {
			var l lib
			l.add(series("A"))
			l.add(ref)
			l.add(series("C"))
			caps := allCaps
			caps.Kinds = []media.Kind{media.Episode}
			set, _ := oneSet(t, &script{caps: caps, items: l.walk}, mediasource.Config{})
			var w walkLog
			err := set.Items(t.Context(), "src", w.yield, w.report)
			if causeOf(t, err) != mediasource.Protocol || joined(w.yielded) != "A" || len(w.unread) != 0 {
				t.Errorf("Items = %v yielded %s unread %s, want a failed call after A", err, joined(w.yielded), joined(w.unread))
			}
		})
	}
	t.Run("identity: an adapter unread report with a bad id", func(t *testing.T) {
		s := &script{caps: allCaps, items: func(_ context.Context, _ func(mediasource.Root, []mediasource.Item) error, unread mediasource.Unread) error {
			return unread(series("a/b"), &mediasource.SourceError{Cause: mediasource.Unreachable})
		}}
		set, _ := oneSet(t, s, mediasource.Config{})
		var w walkLog
		if err := set.Items(t.Context(), "src", w.yield, w.report); causeOf(t, err) != mediasource.Protocol || len(w.unread) != 0 {
			t.Errorf("Items = %v unread %s, want protocol and nothing reported", err, joined(w.unread))
		}
	})
	t.Run("identity: Root(A) answering B", func(t *testing.T) {
		s := &script{caps: allCaps, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
			return mediasource.Root{Native: series("B")}, nil, true, nil
		}}
		set, _ := oneSet(t, s, mediasource.Config{})
		if _, _, found, err := set.Root(t.Context(), "src", series("A")); found || causeOf(t, err) != mediasource.Protocol {
			t.Errorf("Root = %v %v, want protocol", found, err)
		}
	})
	t.Run("identity: a false Root carrying items", func(t *testing.T) {
		s := &script{caps: allCaps, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
			return mediasource.Root{}, []mediasource.Item{ok}, false, nil
		}}
		set, _ := oneSet(t, s, mediasource.Config{})
		if _, _, found, err := set.Root(t.Context(), "src", series("A")); found || causeOf(t, err) != mediasource.Protocol {
			t.Errorf("Root = %v %v, want protocol", found, err)
		}
	})
	t.Run("absent: a false Root with nothing", func(t *testing.T) {
		set, _ := oneSet(t, &script{caps: allCaps}, mediasource.Config{})
		if _, _, found, err := set.Root(t.Context(), "src", series("A")); found || err != nil {
			t.Errorf("Root = %v %v, want absent with no error", found, err)
		}
	})
	t.Run("identity: a bad root ref passed in", func(t *testing.T) {
		s := &script{caps: allCaps}
		set, _ := oneSet(t, s, mediasource.Config{})
		if _, _, _, err := set.Root(t.Context(), "src", series("../x")); causeOf(t, err) != mediasource.Protocol || s.calls.Load() != 0 {
			t.Errorf("Root = %v after %d adapter calls, want protocol and none", err, s.calls.Load())
		}
	})
	changes := map[string][]mediasource.Change{
		"change root of a foreign kind": {{Root: mediasource.NativeRef{ID: "1", Kind: media.Movie}}},
		"change root named twice":       {{Root: series("1")}, {Root: series("1")}},
		"change root id with a slash":   {{Root: series("a/b")}},
	}
	for name, ch := range changes {
		t.Run("identity: "+name, func(t *testing.T) {
			caps := allCaps
			caps.Kinds = []media.Kind{media.Episode}
			s := &script{caps: caps, changes: func(context.Context, mediasource.Cursor) ([]mediasource.Change, mediasource.Cursor, error) {
				return ch, mediasource.Cursor{}, nil
			}}
			set, _ := oneSet(t, s, mediasource.Config{})
			if got, _, err := set.Changes(t.Context(), "src", mediasource.Cursor{}); causeOf(t, err) != mediasource.Protocol || got != nil {
				t.Errorf("Changes = %v %v, want protocol and no changes", got, err)
			}
		})
	}
	t.Run("link: kept when an absolute http URL, else dropped", func(t *testing.T) {
		for in, want := range map[string]string{
			"https://sonarr.example.com/series/x": "https://sonarr.example.com/series/x",
			"ftp://sonarr.example.com/x":          "",
			"/series/x":                           "",
			"https://a.example/\nx":               "",
		} {
			s := &script{caps: allCaps, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
				return mediasource.Root{Native: series("A"), Link: in}, nil, true, nil
			}}
			set, _ := oneSet(t, s, mediasource.Config{})
			r, _, _, err := set.Root(t.Context(), "src", series("A"))
			if err != nil || r.Link != want {
				t.Errorf("Link %q = %q %v, want %q", in, r.Link, err, want)
			}
		}
	})
}

func TestPathRule(t *testing.T) {
	bad := map[string]string{
		"LF":           "/tv/a\nb.mkv",
		"ESC":          "/tv/a\x1bb.mkv",
		"bidi control": "/tv/a\u202eb.mkv",
		"invalid UTF8": "/tv/a\xffb.mkv",
		"over 4096":    "/" + strings.Repeat("a", 4096),
	}
	for name, p := range bad {
		t.Run("video path: "+name, func(t *testing.T) {
			s := &script{caps: allCaps, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
				it := episode("1", 1, 1)
				it.VideoPath = p
				return mediasource.Root{Native: series("A")}, []mediasource.Item{it}, true, nil
			}}
			set, _ := oneSet(t, s, mediasource.Config{})
			if _, _, _, err := set.Root(t.Context(), "src", series("A")); causeOf(t, err) != mediasource.Protocol {
				t.Errorf("Root = %v, want protocol", err)
			}
		})
		t.Run("root folder: "+name, func(t *testing.T) {
			s := &script{caps: allCaps, folders: func(context.Context) ([]string, error) { return []string{"/tv", p, "tv/"}, nil }}
			set, rec := oneSet(t, s, mediasource.Config{})
			got, err := set.RootFolders(t.Context(), "src")
			if err != nil || len(got) != 1 || got[0] != "/tv" {
				t.Errorf("RootFolders = %q %v, want [/tv]", got, err)
			}
			if rec.Count("root folders dropped") != 1 {
				t.Errorf("WARNs %q, want one for the dropped folders", rec.Messages())
			}
		})
	}
	t.Run("a mapped path that grows past 4096 is refused", func(t *testing.T) {
		s := &script{caps: allCaps, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
			it := episode("1", 1, 1)
			it.VideoPath = "/a/" + strings.Repeat("b", 4090)
			return mediasource.Root{Native: series("A")}, []mediasource.Item{it}, true, nil
		}}
		set, _ := oneSet(t, s, mediasource.Config{PathMappings: []mediasource.PathMapping{{From: "/a", To: "/media/long"}}})
		if _, _, _, err := set.Root(t.Context(), "src", series("A")); causeOf(t, err) != mediasource.Protocol {
			t.Errorf("Root = %v, want protocol", err)
		}
	})
}

func TestCountCuts(t *testing.T) {
	alt := make([]string, 33)
	for i := range alt {
		alt[i] = fmt.Sprintf("Alt %d", i)
	}
	tags := make([]mediasource.TagRef, 65)
	for i := range tags {
		tags[i] = mediasource.TagRef(fmt.Sprint(i + 1))
	}
	tags = append(tags[:1], append([]mediasource.TagRef{"bad/tag"}, tags[1:]...)...)
	it := episode("1", 1, 1)
	it.Title = "line one\nline two" + strings.Repeat("x", 600)
	it.SceneName = "scene\x1bname"
	it.Tags = tags
	s := &script{caps: allCaps, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
		return mediasource.Root{Native: series("A"), Title: "Show\r\nTitle", AltTitles: alt, Order: mediasource.Order{Type: strings.Repeat("o", 70)}}, []mediasource.Item{it}, true, nil
	}}
	set, rec := oneSet(t, s, mediasource.Config{})
	r, items, _, err := set.Root(t.Context(), "src", series("A"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.AltTitles) != 32 || r.AltTitles[31] != "Alt 31" {
		t.Errorf("alt titles cut to %d ending %q, want the first 32", len(r.AltTitles), r.AltTitles[len(r.AltTitles)-1])
	}
	if strings.ContainsAny(r.Title, "\r\n") || strings.ContainsAny(items[0].Title, "\n") || strings.Contains(items[0].SceneName, "\x1b") {
		t.Errorf("titles %q %q %q, want one safe line each", r.Title, items[0].Title, items[0].SceneName)
	}
	if len(items[0].Title) > 512 || !strings.HasSuffix(items[0].Title, "…") {
		t.Errorf("item title of %d bytes, want at most 512 ending in the cut marker", len(items[0].Title))
	}
	if len(r.Order.Type) > 64 {
		t.Errorf("order type of %d bytes, want at most 64", len(r.Order.Type))
	}
	if got := items[0].Tags; len(got) != 64 || got[1] != "2" {
		t.Errorf("tags %d with second %q, want 64 valid tags, the bad one dropped", len(got), got[1])
	}
	if n := rec.Count("source answer cut"); n != 1 {
		t.Errorf("%d cut WARNs, want one per call", n)
	}
}

func TestAudioLangsCut(t *testing.T) {
	en, ok := language.Parse("en")
	if !ok {
		t.Fatal("en does not parse")
	}
	it := episode("1", 1, 1)
	it.AudioLangs = []language.Tag{en, {}}
	for range 16 {
		it.AudioLangs = append(it.AudioLangs, en)
	}
	s := &script{caps: allCaps, root: func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
		return mediasource.Root{Native: series("A")}, []mediasource.Item{it}, true, nil
	}}
	set, rec := oneSet(t, s, mediasource.Config{})
	_, items, _, err := set.Root(t.Context(), "src", series("A"))
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].AudioLangs; len(got) != 16 || got[1] != en {
		t.Errorf("%d audio languages, want 16 with the zero tag dropped", len(got))
	}
	if rec.Count("source answer cut") != 1 {
		t.Errorf("WARNs %q, want one cut", rec.Messages())
	}
}

func TestUnsupportedAndUnknownNeverCallAdapter(t *testing.T) {
	s := &script{caps: mediasource.Capabilities{Kinds: []media.Kind{media.Episode}}}
	set, _ := oneSet(t, s, mediasource.Config{})
	ctx := t.Context()
	calls := map[string]func(id media.SourceID) error{
		"Changes": func(id media.SourceID) error { _, _, err := set.Changes(ctx, id, mediasource.Cursor{}); return err },
		"Rescan":  func(id media.SourceID) error { return set.Rescan(ctx, id, series("1")) },
		"Poster":  func(id media.SourceID) error { _, _, _, err := set.Poster(ctx, id, series("1")); return err },
	}
	for name, call := range calls {
		if got := causeOf(t, call("src")); got != mediasource.Unsupported {
			t.Errorf("%s without the capability: %v, want unsupported", name, got)
		}
	}
	unknown := map[string]func(id media.SourceID) error{
		"Items":       func(id media.SourceID) error { return set.Items(ctx, id, nil, nil) },
		"Root":        func(id media.SourceID) error { _, _, _, err := set.Root(ctx, id, series("1")); return err },
		"Ping":        func(id media.SourceID) error { return set.Ping(ctx, id) },
		"RootFolders": func(id media.SourceID) error { _, err := set.RootFolders(ctx, id); return err },
		"LocalPath":   func(id media.SourceID) error { _, err := set.LocalPath(id, "/tv"); return err },
	}
	for name, call := range unknown {
		if got := causeOf(t, call("other")); got != mediasource.UnknownSource {
			t.Errorf("%s of an unknown source: %v, want unknown_source", name, got)
		}
	}
	if n := s.calls.Load(); n != 0 {
		t.Errorf("adapter called %d times, want none", n)
	}
	if _, ok := set.Capabilities("other"); ok {
		t.Error("Capabilities of an unknown source answered true")
	}
}

func TestNewSetRefusesFailingNew(t *testing.T) {
	failing := mediasource.AdapterKind{Type: "bad", New: func(mediasource.Config, mediasource.Deps) (mediasource.Adapter, error) {
		return nil, errors.New("url must be absolute")
	}}
	good := scriptKind("script", &script{caps: allCaps})
	cases := map[string][]mediasource.Config{
		"failing New":  {{ID: "a", Type: "bad"}},
		"unknown type": {{ID: "a", Type: "plex"}},
		"duplicate id": {{ID: "a", Type: "script"}, {ID: "a", Type: "script"}},
	}
	for name, cfgs := range cases {
		if set, err := mediasource.NewSet([]mediasource.AdapterKind{failing, good}, cfgs, mediasource.Deps{}); err == nil || set != nil {
			t.Errorf("NewSet with %s = %v, %v; want an error", name, set, err)
		}
	}
	set, err := mediasource.NewSet([]mediasource.AdapterKind{good}, []mediasource.Config{{ID: "b", Type: "script"}, {ID: "a", Type: "script"}}, mediasource.Deps{})
	if err != nil || joined(sourceIDs(set.Sources())) != "b,a" {
		t.Errorf("NewSet = %v, sources %v; want b,a in entry order", err, set.Sources())
	}
}

func TestPosterAnswerChecked(t *testing.T) {
	s := &script{caps: allCaps, poster: func(context.Context, mediasource.NativeRef) (io.ReadCloser, string, bool, error) {
		return nil, "image/jpeg", true, nil
	}}
	set, _ := oneSet(t, s, mediasource.Config{})
	if _, _, _, err := set.Poster(t.Context(), "src", series("1")); causeOf(t, err) != mediasource.Protocol {
		t.Errorf("Poster found with no body = %v, want protocol", err)
	}
	s.poster = func(context.Context, mediasource.NativeRef) (io.ReadCloser, string, bool, error) {
		return io.NopCloser(strings.NewReader("jpg")), "image/jpeg\r\nX: y", true, nil
	}
	body, ctype, ok, err := set.Poster(t.Context(), "src", series("1"))
	if err != nil || !ok || strings.ContainsAny(ctype, "\r\n") {
		t.Errorf("Poster = %q %v %v, want a one-line content type", ctype, ok, err)
	}
	if body != nil {
		_ = body.Close()
	}
}

func sourceIDs(ids []media.SourceID) []mediasource.NativeID {
	out := make([]mediasource.NativeID, len(ids))
	for i, id := range ids {
		out[i] = mediasource.NativeID(id)
	}
	return out
}
