package syncsched

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"sync"

	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/timing"
)

// chooseReference picks the stored text track to align against: one whose
// tag is zero or grades farther than SameLanguage from tag, since a track in
// the subtitle's own language may be the same text, preferring non-forced,
// then SRT, then not HI, then the lowest index.
func chooseReference(tracks []statedb.EmbeddedTrack, tag language.Tag, p language.Policy) (statedb.EmbeddedTrack, bool) {
	var best statedb.EmbeddedTrack
	found := false
	for i := range tracks {
		t := &tracks[i]
		if !slices.Contains(embedded.TextCodecs, t.Codec) || p.Grade(tag, t.Tag).Closeness <= language.SameLanguage {
			continue
		}
		if !found || preference(t, &best) < 0 {
			best, found = *t, true
		}
	}
	return best, found
}

func preference(a, b *statedb.EmbeddedTrack) int {
	return cmp.Or(
		boolOrder(a.Flags.Forced, b.Flags.Forced),
		boolOrder(a.Codec != "srt", b.Codec != "srt"),
		boolOrder(a.Flags.HI, b.Flags.HI),
		cmp.Compare(a.Index, b.Index),
	)
}

// boolOrder sorts false first.
func boolOrder(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// referenceSpans is the dialogue of video's track, extracted once per
// instance, stamp and stream while the span cache holds it.
func (a *Aligner) referenceSpans(ctx context.Context, inst statedb.InstanceID, stamp statedb.VideoStamp, track *statedb.EmbeddedTrack, video *os.File) ([]subsync.Span, error) {
	key := spanKey{inst: inst, stamp: stamp, stream: track.Index}
	if spans, ok := a.spans.get(key); ok {
		return spans, nil
	}
	// ASS and SSA are copied as they are, so the style classifier sees
	// their styles; every other text codec converts to SRT.
	doc, err := a.run.ExtractSubtitle(ctx, video, track.Index, track.Codec == "ass" || track.Codec == "ssa")
	if err != nil {
		return nil, err
	}
	cues, err := timing.ParseCues(doc)
	if err != nil {
		return nil, fmt.Errorf("syncsched: reference stream %d: %w", track.Index, err)
	}
	spans := subsync.DialogueSpans(cues)
	a.spans.put(key, spans)
	return spans, nil
}

type spanKey struct {
	stamp  statedb.VideoStamp
	inst   statedb.InstanceID
	stream int
}

type spanEntry struct {
	spans []subsync.Span
	key   spanKey
}

// spanCache is a least-recently-used cache of reference spans; a caller
// never modifies the spans it gets.
type spanCache struct {
	entries []spanEntry // least recently used first; guarded by mu
	mu      sync.Mutex
}

func newSpanCache() *spanCache { return &spanCache{entries: make([]spanEntry, 0, referenceCacheSize)} }

func (c *spanCache) get(k spanKey) ([]subsync.Span, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := slices.IndexFunc(c.entries, func(e spanEntry) bool { return e.key == k })
	if i < 0 {
		return nil, false
	}
	e := c.entries[i]
	c.entries = append(slices.Delete(c.entries, i, i+1), e)
	return e.spans, true
}

func (c *spanCache) put(k spanKey, spans []subsync.Span) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := slices.IndexFunc(c.entries, func(e spanEntry) bool { return e.key == k }); i >= 0 {
		c.entries = slices.Delete(c.entries, i, i+1)
	}
	if len(c.entries) == referenceCacheSize {
		c.entries = slices.Delete(c.entries, 0, 1)
	}
	c.entries = append(c.entries, spanEntry{key: k, spans: spans})
}
