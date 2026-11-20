package plex

import (
	"context"
	"fmt"
	"time"

	"github.com/cplieger/plexapi/v3"
)

// merge reads, per section, the items Plex updated at or after the
// section's high-water, with their media parts, into the index. A merge
// under deltaReuse old is reused. Plex keeps an item's addedAt when its file
// is upgraded and moves its updatedAt, so only an updatedAt read finds the
// new path. Each section commits on its own: a failed walk leaves that
// section's high-water, and so its items, for the next merge. A build that
// succeeds during the merge drops the rest of it, stamping no merge.
func (a *adapter) merge(ctx context.Context) error {
	select {
	case a.merging <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-a.merging }()
	if a.idx.mergedWithin(deltaReuse) {
		return nil
	}
	sections, hw, gen := a.idx.snapshot()
	for _, s := range sections {
		var items []plexapi.Item
		top := hw[s.key]
		for it, err := range a.c.UpdatedSince(ctx, s.key, s.metadataType, hw[s.key], pageSize, nil) {
			if err != nil {
				return fmt.Errorf("plex: reading section %s's items updated since %d: %w", s.key, hw[s.key], err)
			}
			items = append(items, it)
			top = max(top, it.UpdatedAt)
		}
		if !a.idx.commit(s.key, items, top, gen) {
			return nil
		}
	}
	a.idx.mergeDone(gen)
	return nil
}

func (ix *index) mergedWithin(d time.Duration) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return !ix.merged.IsZero() && time.Since(ix.merged) < d
}

// commit applies one section's merged items, less any key Plex answered
// not-found for, then moves its high-water to top. It drops them and answers
// false when the index is no longer the snapshot's generation gen: a newer
// build read every item updated after its start, and its seed, a minute
// before that start, makes the next merge re-read anything the walk raced.
// It also answers false after Shutdown.
func (ix *index) commit(sec plexapi.RatingKey, items []plexapi.Item, top int64, gen uint64) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.stopped || ix.gen != gen {
		return false
	}
	for i := range items {
		if _, gone := ix.gone[items[i].RatingKey]; !gone {
			ix.cur.put(&items[i])
		}
	}
	ix.highWater[sec] = max(ix.highWater[sec], top)
	return true
}

// mergeDone stamps a merge of generation gen as the one deltaReuse reuses,
// unless a build succeeded since its snapshot.
func (ix *index) mergeDone(gen uint64) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.gen == gen {
		ix.merged = time.Now()
	}
}
