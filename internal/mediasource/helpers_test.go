package mediasource_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// script is an adapter whose every answer a test sets; a nil func answers
// the zero value. calls counts every method call.
type script struct {
	items   func(context.Context, func(mediasource.Root, []mediasource.Item) error, mediasource.Unread) error
	root    func(context.Context, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error)
	changes func(context.Context, mediasource.Cursor) ([]mediasource.Change, mediasource.Cursor, error)
	tags    func(context.Context) (map[mediasource.TagRef]string, error)
	folders func(context.Context) ([]string, error)
	ping    func(context.Context) error
	rescan  func(context.Context, mediasource.NativeRef) error
	poster  func(context.Context, mediasource.NativeRef) (io.ReadCloser, string, bool, error)
	caps    mediasource.Capabilities
	calls   atomic.Int64
}

var _ mediasource.Adapter = (*script)(nil)

func (s *script) Capabilities() mediasource.Capabilities { return s.caps }

func (s *script) Items(ctx context.Context, yield func(mediasource.Root, []mediasource.Item) error, unread mediasource.Unread) error {
	s.calls.Add(1)
	if s.items == nil {
		return nil
	}
	return s.items(ctx, yield, unread)
}

func (s *script) Root(ctx context.Context, ref mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
	s.calls.Add(1)
	if s.root == nil {
		return mediasource.Root{}, nil, false, nil
	}
	return s.root(ctx, ref)
}

func (s *script) Changes(ctx context.Context, cur mediasource.Cursor) ([]mediasource.Change, mediasource.Cursor, error) {
	s.calls.Add(1)
	if s.changes == nil {
		return nil, cur, nil
	}
	return s.changes(ctx, cur)
}

func (s *script) Tags(ctx context.Context) (map[mediasource.TagRef]string, error) {
	s.calls.Add(1)
	if s.tags == nil {
		return nil, nil
	}
	return s.tags(ctx)
}

func (s *script) RootFolders(ctx context.Context) ([]string, error) {
	s.calls.Add(1)
	if s.folders == nil {
		return nil, nil
	}
	return s.folders(ctx)
}

func (s *script) Ping(ctx context.Context) error {
	s.calls.Add(1)
	if s.ping == nil {
		return nil
	}
	return s.ping(ctx)
}

func (s *script) Rescan(ctx context.Context, ref mediasource.NativeRef) error {
	s.calls.Add(1)
	if s.rescan == nil {
		return nil
	}
	return s.rescan(ctx, ref)
}

func (s *script) Poster(ctx context.Context, ref mediasource.NativeRef) (io.ReadCloser, string, bool, error) {
	s.calls.Add(1)
	if s.poster == nil {
		return nil, "", false, nil
	}
	return s.poster(ctx, ref)
}

// allCaps lists both kinds and every capability.
var allCaps = mediasource.Capabilities{Kinds: []media.Kind{media.Episode, media.Movie}, Changes: true, Rescan: true, Tags: true, Posters: true}

func scriptKind(typ string, s *script) mediasource.AdapterKind {
	return mediasource.AdapterKind{
		Type:   typ,
		Schema: formschema.Schema{Fields: []formschema.Field{{Key: "url", Kind: formschema.String, Required: true}, {Key: "api_key", Kind: formschema.Secret, Required: true}}},
		New:    func(mediasource.Config, mediasource.Deps) (mediasource.Adapter, error) { return s, nil },
	}
}

// oneSet builds a Set of one script source "src" with cfg's mappings and
// labels, logging to the returned recorder.
func oneSet(t *testing.T, s *script, cfg mediasource.Config) (*mediasource.Set, *capture.Recorder) {
	t.Helper()
	log, rec := capture.New()
	cfg.ID, cfg.Type = "src", "script"
	set, err := mediasource.NewSet([]mediasource.AdapterKind{scriptKind("script", s)}, []mediasource.Config{cfg}, mediasource.Deps{Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	return set, rec
}

func series(id string) mediasource.NativeRef {
	return mediasource.NativeRef{ID: mediasource.NativeID(id), Kind: media.Episode}
}

func episode(id string, season, ep int) mediasource.Item {
	return mediasource.Item{Native: mediasource.NativeID(id), SeriesNumbering: media.Numbering{Scheme: media.TVDB, Season: season, Episode: ep}}
}

// lib is an ordered library a script walks, yielding each root in turn.
type lib []struct {
	root  mediasource.Root
	items []mediasource.Item
}

func (l lib) walk(_ context.Context, yield func(mediasource.Root, []mediasource.Item) error, _ mediasource.Unread) error {
	for _, r := range l {
		if err := yield(r.root, r.items); err != nil {
			return err
		}
	}
	return nil
}

func (l *lib) add(ref mediasource.NativeRef, items ...mediasource.Item) {
	*l = append(*l, struct {
		root  mediasource.Root
		items []mediasource.Item
	}{mediasource.Root{Native: ref, Title: "T " + string(ref.ID)}, items})
}

// walkLog records what Items yielded and reported unread.
type walkLog struct {
	yielded []mediasource.NativeID
	unread  []mediasource.NativeID
	causes  []mediasource.Cause
}

func (w *walkLog) yield(r mediasource.Root, _ []mediasource.Item) error {
	w.yielded = append(w.yielded, r.Native.ID)
	return nil
}

func (w *walkLog) report(ref mediasource.NativeRef, se *mediasource.SourceError) error {
	w.unread = append(w.unread, ref.ID)
	w.causes = append(w.causes, se.Cause)
	return nil
}

func causeOf(t *testing.T, err error) mediasource.Cause {
	t.Helper()
	var se *mediasource.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *SourceError", err)
	}
	return se.Cause
}

func joined(ids []mediasource.NativeID) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = string(id)
	}
	return strings.Join(s, ",")
}
