package mediasource

import (
	"context"
	"reflect"
	"slices"
	"sync"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/media"
)

// rootCache is an unmapped source's last successful RootFolders answer.
// fill is closed when the Set's one first fill ends, and cancel ends it;
// both nil before it starts.
type rootCache struct {
	fill   chan struct{}
	cancel context.CancelFunc
	last   []string
	mu     sync.Mutex
	ok     bool
}

func (c *rootCache) store(folders []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last, c.ok = slices.Clone(folders), true
}

func (c *rootCache) load() ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.last), c.ok
}

// MediaRoots answers, deduped and sorted, each source's mapping targets, or
// for an unmapped source its last successful root folders. An empty cache
// is filled once per Set on a context no caller cancels, bounded by
// rootFoldersWait and ended by Close; each caller waits for it at most
// until its own deadline. A closed Set starts no fill.
func (s *Set) MediaRoots(ctx context.Context) []string {
	var out []string
	var waits []*source
	var fills []chan struct{}
	for _, id := range s.ids {
		src := s.byID[id]
		if len(src.cfg.PathMappings) > 0 {
			for _, pm := range src.cfg.PathMappings {
				out = append(out, pm.To)
			}
			continue
		}
		if last, ok := src.roots.load(); ok {
			out = append(out, last...)
			continue
		}
		if fill := s.startFill(ctx, src); fill != nil {
			waits, fills = append(waits, src), append(fills, fill)
		}
	}
	for i, src := range waits {
		select {
		case <-fills[i]:
		case <-ctx.Done():
			return sortedSet(out)
		}
		last, _ := src.roots.load()
		out = append(out, last...)
	}
	return sortedSet(out)
}

// startFill starts src's one fill unless it started already, and answers
// the channel closed at its end; nil when the Set closed before any fill.
func (s *Set) startFill(ctx context.Context, src *source) chan struct{} {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	src.roots.mu.Lock()
	defer src.roots.mu.Unlock()
	if src.roots.fill != nil || s.closed {
		return src.roots.fill
	}
	done := make(chan struct{})
	fillCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rootFoldersWait)
	src.roots.fill, src.roots.cancel = done, cancel
	s.fills.Go(func() {
		defer close(done)
		defer cancel()
		if _, err := s.RootFolders(fillCtx, src.id); err != nil && !s.isClosed() {
			s.log.Warn("source root folders unread; its roots are unknown until a probe reads them", "source", src.id, "err", err)
		}
	})
	return done
}

// Close ends the root-folder fills the Set runs and returns once each has
// returned; after it MediaRoots answers only what the Set already holds.
func (s *Set) Close() {
	s.lifeMu.Lock()
	s.closed = true
	for _, src := range s.byID {
		src.roots.mu.Lock()
		if src.roots.cancel != nil {
			src.roots.cancel()
		}
		src.roots.mu.Unlock()
	}
	s.lifeMu.Unlock()
	s.fills.Wait()
}

func (s *Set) isClosed() bool {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	return s.closed
}

func sortedSet(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}

// Inherit copies prev's root-folder and tag caches into each entry whose
// Config equals prev's entry with that ID outside the fields its kind's
// schema marks Secret, so an edit elsewhere, or of a key alone, keeps a
// source's roots while it is down. App calls it before publishing a rebuilt
// Set; a nil prev is a no-op.
func (s *Set) Inherit(prev *Set) {
	if prev == nil {
		return
	}
	for id, src := range s.byID {
		old, ok := prev.byID[id]
		if !ok || compareEntry(src.schema, &old.cfg, &src.cfg) == EntryChanged {
			continue
		}
		if last, ok := old.roots.load(); ok {
			src.roots.store(last)
		}
		src.tags.inherit(&old.tags)
	}
}

// valuesDiffer reports whether a and b differ in a field of schema that
// counts selects, a field set in one and absent in the other included.
func valuesDiffer(schema formschema.Schema, a, b formschema.Values, counts func(formschema.Field) bool) bool {
	for _, f := range schema.Fields {
		if !counts(f) {
			continue
		}
		va, inA := a[f.Key]
		vb, inB := b[f.Key]
		if inA != inB || !reflect.DeepEqual(va, vb) {
			return true
		}
	}
	return false
}

// LocalPath maps a folder a source reports by the rule a video path maps
// by, an unmatched path passing through. A path breaking the path rule, or
// one aliasing a mapped path, is Protocol; an unknown source UnknownSource.
func (s *Set) LocalPath(src media.SourceID, remote string) (string, error) {
	a, err := s.call(src, "LocalPath", nil)
	if err != nil {
		return "", err
	}
	local, err := a.src.m.Map(remote)
	if err != nil {
		return "", a.refuse("folder %q %w", quote(remote), err)
	}
	return local, nil
}
