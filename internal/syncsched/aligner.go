// Package syncsched aligns subtitles to their videos inside the server:
// search's Align, and the sync jobs a person starts with their previews. One
// slot runs one alignment at a time; ffmpeg still parses each video in its
// own process.
package syncsched

import (
	"cmp"
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/cplieger/go/keylock"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
)

// Deps is what New needs; every field but Logger is required. Policy reads
// the language policy in force, which picks the reference track.
type Deps struct {
	Writer *mediawrite.Writer
	Prober *embedded.Prober
	Status *status.Store
	Policy func() language.Policy
	Logger *slog.Logger
}

// Aligner runs every alignment through its one slot. Construct it with New.
type Aligner struct {
	m       *metrics
	spawn   chan func(context.Context)
	prober  *embedded.Prober
	status  *status.Store
	policy  func() language.Policy
	log     *slog.Logger
	writer  *mediawrite.Writer
	spans   *spanCache
	starts  *keylock.Set[startKey]
	slot    *slot
	stopped chan struct{}
	run     ffmpeg.Runner
	ledger  ledger // guarded by mu
	mu      sync.Mutex
}

// New returns an Aligner over d. It panics on a missing required dependency.
func New(d Deps) *Aligner {
	if d.Writer == nil || d.Prober == nil || d.Status == nil || d.Policy == nil {
		panic("syncsched: New needs Writer, Prober, Status and Policy")
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	m := newMetrics()
	return &Aligner{
		writer:  d.Writer,
		prober:  d.Prober,
		status:  d.Status,
		policy:  d.Policy,
		log:     log,
		m:       m,
		slot:    newSlot(m),
		spans:   newSpanCache(),
		starts:  keylock.New(startKey{}.compare),
		spawn:   make(chan func(context.Context)),
		stopped: make(chan struct{}),
		ledger:  newLedger(),
	}
}

// Run owns every sync job's goroutine until ctx ends, then waits for each
// job to end; a job the end cut short ends Interrupted, and one started
// after it ends Interrupted at once. Call it once.
func (a *Aligner) Run(ctx context.Context) error {
	var jobs sync.WaitGroup
	for {
		select {
		case job := <-a.spawn:
			jobs.Go(func() { job(ctx) })
		case <-ctx.Done():
			close(a.stopped)
			jobs.Wait()
			return nil
		}
	}
}

// startKey serializes the starts of one owner's proposals for one file and
// method, so a repeated request finds the first one's job.
type startKey struct {
	file   mediawrite.FileRef
	owner  int64
	method uint8
}

func (startKey) compare(a, b startKey) int {
	return cmp.Or(
		cmp.Compare(a.owner, b.owner),
		cmp.Compare(a.file.Inst(), b.file.Inst()),
		strings.Compare(a.file.Name(), b.file.Name()),
		cmp.Compare(a.method, b.method),
	)
}
