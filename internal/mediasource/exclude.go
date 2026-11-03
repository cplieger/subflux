package mediasource

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
)

// defaultExcludeTag applies when an entry has no exclude_tags key.
const defaultExcludeTag = "no-subflux"

// tagCache is a source's resolved exclusion set: refreshed by every Items
// call and by a Root call once older than tagCacheMaxAge.
type tagCache struct {
	at       time.Time
	excluded map[TagRef]struct{}
	mu       sync.Mutex
	ok       bool
}

func (c *tagCache) recent(now time.Time) (map[TagRef]struct{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ok || now.Sub(c.at) >= tagCacheMaxAge {
		return nil, false
	}
	return c.excluded, true
}

func (c *tagCache) store(at time.Time, excluded map[TagRef]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at, c.excluded, c.ok = at, excluded, true
}

func (c *tagCache) inherit(prev *tagCache) {
	prev.mu.Lock()
	at, excluded, ok := prev.at, prev.excluded, prev.ok
	prev.mu.Unlock()
	if ok {
		c.store(at, excluded)
	}
}

// normLabel is arrapi's tag-label rule: trimmed and lower-cased.
func normLabel(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func labelSet(labels []string) map[string]struct{} {
	set := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		set[normLabel(l)] = struct{}{}
	}
	return set
}

func hasConfiguredLabel(labels map[string]struct{}) bool {
	for l := range labels {
		if l != defaultExcludeTag {
			return true
		}
	}
	return false
}

// exclusions resolves the source's exclude labels to the tag ids they name,
// before the call reads any item: fresh for a whole pass, else reusing an
// answer under tagCacheMaxAge old. A failed tag read fails the call, so no
// excluded item is ever reported unmarked. A source without Tags, or an
// entry listing no labels, excludes nothing.
func (s *Set) exclusions(ctx context.Context, a *answer, fresh bool) (map[TagRef]struct{}, error) {
	src := a.src
	if len(src.labels) == 0 || !src.caps.Tags {
		return nil, nil
	}
	if !fresh {
		if excluded, ok := src.tags.recent(time.Now()); ok {
			return excluded, nil
		}
	}
	tags, err := src.adapter.Tags(ctx)
	if err != nil {
		return nil, fail(ctx, a, err)
	}
	excluded := map[TagRef]struct{}{}
	matched := map[string]bool{}
	for ref, label := range tags {
		l := normLabel(label)
		if _, ok := src.labels[l]; ok {
			excluded[ref] = struct{}{}
			matched[l] = true
		}
	}
	if fresh {
		s.warnUnmatched(src, matched)
	}
	src.tags.store(time.Now(), excluded)
	return excluded, nil
}

// warnUnmatched logs, once per whole pass, the configured labels that name
// no tag; the default label is silent, since most sources never define it.
func (s *Set) warnUnmatched(src *source, matched map[string]bool) {
	var missing []string
	for l := range src.labels {
		if l != defaultExcludeTag && !matched[l] {
			missing = append(missing, l)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		s.log.Warn("source exclude_tags name no tag, so they exclude nothing", "source", src.id, "labels", missing)
	}
}
