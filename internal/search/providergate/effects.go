package providergate

import (
	"context"
	"log/slog"

	"github.com/cplieger/subflux/internal/subflux"
)

// effects is what a mutation decided under g.mu: the records to persist, its
// metric calls, then its logs and its events.
type effects struct {
	writes  []subflux.ProviderID
	metrics []func()
	after   []func()
	events  []Event
}

func (fx *effects) metric(fn func()) { fx.metrics = append(fx.metrics, fn) }

func (fx *effects) persist(id subflux.ProviderID) { fx.writes = append(fx.writes, id) }

func (fx *effects) do(fn func()) { fx.after = append(fx.after, fn) }

func (fx *effects) event(e Event) { fx.events = append(fx.events, e) } //nolint:gocritic // hugeParam: the event is queued by value

// commit is one mutation's effects, held in mutation order until flush has
// written its records.
type commit struct {
	fx       *effects
	onChange func(Event)
	written  chan struct{} // closed once flush has applied fx; nil when fx writes nothing
}

// commitLocked appends fx behind every earlier mutation, releases g.mu, waits
// until fx's records are written and then drains the effect queue. A
// write never waits on an effect, so an earlier event callback cannot delay
// it, and each effect still follows the writes of its own mutation and every
// earlier one. Writes run detached from ctx's cancellation.
func (g *Gate) commitLocked(ctx context.Context, fx *effects) {
	c := &commit{fx: fx, onChange: g.onChange}
	if len(fx.writes) > 0 {
		c.written = make(chan struct{})
	}
	g.commits = append(g.commits, c)
	g.mu.Unlock()
	g.flush(context.WithoutCancel(ctx))
	if c.written != nil {
		<-c.written
	}
	g.queue.Drain()
}

// flush takes the pending commits in mutation order, writes each one's
// records and then queues its metric calls, logs and events. A flush that
// finds another in progress returns at once and leaves its commit to that
// one, which blocks on store I/O only, never on an effect. A write that
// panics hands the commits after it to a new flush.
func (g *Gate) flush(ctx context.Context) {
	g.mu.Lock()
	if g.flushing {
		g.mu.Unlock()
		return
	}
	g.flushing = true
	ended := false
	defer func() {
		if ended {
			return
		}
		g.mu.Lock()
		g.flushing = false
		pending := len(g.commits) > 0
		g.mu.Unlock()
		if pending {
			go func() {
				g.flush(ctx)
				g.queue.Drain()
			}()
		}
	}()
	for {
		if len(g.commits) == 0 {
			g.commits = nil
			g.flushing = false
			ended = true
			g.mu.Unlock()
			return
		}
		c := g.commits[0]
		g.commits = g.commits[1:]
		g.mu.Unlock()
		g.apply(ctx, c)
		g.mu.Lock()
	}
}

// apply writes c's records, queues its metric calls, logs and events, then
// releases c's caller, panic included, so a waiting caller never blocks on a
// write that will not finish.
func (g *Gate) apply(ctx context.Context, c *commit) {
	if c.written != nil {
		defer close(c.written)
	}
	for _, id := range c.fx.writes {
		g.persist(ctx, id)
	}
	g.queue.Add(c.fx.metrics...)
	g.queue.Add(c.fx.after...)
	if c.onChange != nil {
		for _, e := range c.fx.events {
			g.queue.Add(func() { c.onChange(e) })
		}
	}
}

// persist writes id's CURRENT in-memory record, or deletes the key when the
// record is gone, so a slow earlier write can never land over a later state.
// It runs only from flush, one write at a time. A record still lacking a
// fingerprint is hashed here, outside g.mu.
func (g *Gate) persist(ctx context.Context, id subflux.ProviderID) {
	if g.store == nil {
		return
	}
	for {
		snap, canonical, ok := g.snapshot(id)
		if !ok {
			if err := g.store.DeleteProviderAuthRecord(ctx, id); err != nil {
				slog.Warn("provider credential record delete failed", "provider", id, "error", err)
			}
			return
		}
		if snap.Fingerprint != "" {
			if err := g.store.PutProviderAuthRecord(ctx, &snap); err != nil {
				slog.Warn("provider credential record write failed", "provider", id, "error", err)
			}
			return
		}
		if canonical == nil {
			return
		}
		g.setFingerprint(id, canonical, g.hasher.Hash(*canonical))
	}
}

// snapshot copies id's record. canonical is nil for a record whose settings
// this process never held, which therefore cannot be fingerprinted again.
func (g *Gate) snapshot(id subflux.ProviderID) (rec subflux.ProviderAuthRecord, canonical *string, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r := g.records[id]
	if r == nil {
		return subflux.ProviderAuthRecord{}, nil, false
	}
	rec = r.rec
	rec.FailedOps = append([]string(nil), r.rec.FailedOps...)
	return rec, r.canonical, true
}

// setFingerprint stores fp on id's record when the record still carries the
// canonical form fp was derived from.
func (g *Gate) setFingerprint(id subflux.ProviderID, canonical *string, fp string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r := g.records[id]; r != nil && r.canonical == canonical {
		r.rec.Fingerprint = fp
	}
}
