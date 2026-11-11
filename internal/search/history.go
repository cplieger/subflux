package search

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/cplieger/go/keyenc"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/statedb"
)

// Bounds of the attempt memory.
const (
	// attemptMemory is how long an attempt keeps its candidate from being
	// fetched again automatically, unless remembered says it keeps nothing.
	attemptMemory = 30 * 24 * time.Hour
	// historyPage is how many download rows each History read takes.
	historyPage = 100
)

// attemptKey tells one candidate from another with the same release name:
// the SHA-256 of the keyenc join of its provider and its Ref's token.
func attemptKey(id provider.ProviderID, ref provider.Ref) [32]byte {
	return sha256.Sum256([]byte(keyenc.Join(string(id), ref.Token())))
}

// packWhyNot is the WhyNot of each attempt spelling refusing a pack.
var packWhyNot = map[string]WhyNot{
	RefusalDifferentEpisode: DifferentEpisode, RefusalEpisodeCount: EpisodeCount, RefusalOrderUndetermined: OrderUndetermined,
}

// itemHistory is what one item's download log says to a run.
type itemHistory struct {
	// tried holds the attempt keys of the last attemptMemory's
	// remembered rows.
	tried map[[32]byte]bool
	// delivered is when each pair was last delivered, within the horizon
	// the log was read to.
	delivered map[pair]time.Time
	// packRefused is, by attempt key, the newest of the last
	// attemptMemory's automatic rows refusing a pack (8.11), as a WhyNot.
	packRefused map[[32]byte]WhyNot
}

// readHistory reads k's log newest first, a page at a time, until a row is
// older than horizon before now.
func (e *Engine) readHistory(ctx context.Context, k media.Key, now time.Time, horizon time.Duration) (itemHistory, error) {
	h := itemHistory{tried: map[[32]byte]bool{}, delivered: map[pair]time.Time{}, packRefused: map[[32]byte]WhyNot{}}
	var cursor statedb.ListCursor
	for {
		rows, next, err := e.deps.Store.History(ctx, statedb.ItemHistory(k), cursor, historyPage)
		if err != nil {
			return itemHistory{}, fmt.Errorf("search: read %s's history: %w", k, err)
		}
		for i := range rows {
			if !h.add(&rows[i], now.Sub(rows[i].At), horizon) {
				return h, nil
			}
		}
		if next == "" {
			return h, nil
		}
		cursor = next
	}
}

// add notes one row of the given age; false once the row is past horizon,
// where the read stops.
func (h *itemHistory) add(d *statedb.Download, age, horizon time.Duration) bool {
	if age > horizon {
		return false
	}
	if age <= attemptMemory && remembered(d.Refusal) {
		h.tried[d.Candidate] = true
	}
	if why, ok := packWhyNot[d.Refusal]; ok && age <= attemptMemory && !d.Provenance.Manual {
		if _, newer := h.packRefused[d.Candidate]; !newer {
			h.packRefused[d.Candidate] = why
		}
	}
	if p := (pair{target: d.Target, variant: d.Variant}); d.Delivered && h.delivered[p].IsZero() {
		h.delivered[p] = d.At
	}
	return true
}
