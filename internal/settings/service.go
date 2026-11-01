package settings

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

var (
	// ErrActivation is Save's answer when activate failed: the file is
	// restored and the previous settings stay live.
	ErrActivation = errors.New("settings: the saved settings could not be activated")
	// ErrClosed is Save's answer once Shutdown has begun.
	ErrClosed = errors.New("settings: shutting down")
	// ErrConflict is Save's answer to a base other than the live revision.
	ErrConflict = errors.New("settings: saved from an older revision")
)

// SaveCheck is app's save-time check: Save runs it after the parse and before
// the write, under the Save's context, and Load never does. A non-empty
// answer refuses the Save as a parse error does, writing nothing. From a
// Failed or Unconfigured snapshot prev is the zero Active.
type SaveCheck func(ctx context.Context, prev, next Active) []formschema.FieldError

// Service holds the live snapshot and saves the file.
type Service struct {
	activate func(context.Context, Active) error
	check    SaveCheck
	cur      atomic.Pointer[snapshot]
	// stop cancels every Save's write-and-activate context; only Shutdown
	// fires it.
	stop    context.Context
	cancel  context.CancelFunc
	changed chan struct{}
	// slot is the save lock, a channel so a wait honours its context.
	slot    chan struct{}
	closing chan struct{}
	drained chan struct{}
	path    string
	// seen is the file as this service last read or wrote it; slot guards it.
	seen      fileState
	r         Registries
	closeOnce sync.Once
	drainOnce sync.Once
	diverged  atomic.Bool
}

// NewService serves s, which Load(path, r) answered. activate makes a parsed
// configuration live, nil when nothing needs to; check may be nil.
func NewService(path string, r Registries, s Snapshot, activate func(context.Context, Active) error, check SaveCheck) *Service {
	stop, cancel := context.WithCancel(context.Background())
	svc := &Service{
		activate: activate,
		check:    check,
		stop:     stop,
		cancel:   cancel,
		changed:  make(chan struct{}, 1),
		slot:     make(chan struct{}, 1),
		closing:  make(chan struct{}),
		drained:  make(chan struct{}),
		path:     path,
		r:        r,
	}
	if s.p == nil {
		s = rootFailure("settings: no snapshot was loaded", fileState{})
	}
	svc.seen = s.p.file
	svc.cur.Store(s.p)
	return svc
}

// Snapshot answers the live snapshot.
func (s *Service) Snapshot() Snapshot { return Snapshot{s.cur.Load()} }

// Changed is signalled after every commit and every Diverged change: capacity
// 1, coalescing, read by app's change loop alone.
func (s *Service) Changed() <-chan struct{} { return s.changed }

// Diverged reports a failed restore after a failed activation: the file holds
// settings that are not live. A committed Save clears it.
func (s *Service) Diverged() bool { return s.diverged.Load() }

// Merge fills each unchanged scalar of entry, an unsaved element of
// section's list, from the saved element with its id, then calls parse once
// with a private copy in which every ${SUBFLUX_...} is expanded, so a
// connection test parses and uses the saved secret server-side. parse must
// not be nil. The expanded copy lives only for that call: Merge returns no
// document, writes nothing and logs nothing. A field error names an
// unchanged path with no saved value, a refused shape or an unresolved
// variable, never a value; parse's own errors are returned as it gave them.
func (s *Service) Merge(section string, entry *yaml.Node, parse func(expanded *yaml.Node) []formschema.FieldError) []formschema.FieldError {
	if entry == nil {
		return []formschema.FieldError{{Path: section, Message: msgEnterEntry}}
	}
	if errs := walkShapes(entry, section, bodyShape); len(errs) > 0 {
		return errs
	}
	stored := mappingValue(s.cur.Load().doc, section)
	var se *yaml.Node
	if holdsUnchanged(entry) {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{entry}}
		var ok bool
		if se, ok = elementByID(entry, seq, stored); !ok {
			return []formschema.FieldError{{Path: section, Message: msgEnterEntry}}
		}
	}
	merged, errs := mergeNode(entry, se, section)
	if len(errs) > 0 {
		return errs
	}
	out, err := reread(merged)
	if err != nil {
		return []formschema.FieldError{{Path: section, Message: msgUnreadable}}
	}
	if errs := walkShapes(out, section, fileShape); len(errs) > 0 {
		return errs
	}
	if errs := expand(out, section); len(errs) > 0 {
		return errs
	}
	return parse(out)
}

// Shutdown refuses every later Save with ErrClosed and waits for the Save in
// progress. When ctx ends first it cancels that Save's write-and-activate
// context, which restores the file, and returns ctx's error without waiting
// for it; app closes the store only after Shutdown returns nil.
func (s *Service) Shutdown(ctx context.Context) error {
	s.closeOnce.Do(func() { close(s.closing) })
	// select picks at random among ready cases, so an idle service would
	// answer an expired ctx's error half the time without this first try.
	if s.tryDrain() {
		return nil
	}
	select {
	case <-s.drained:
	case s.slot <- struct{}{}:
		s.drainOnce.Do(func() { close(s.drained) })
	case <-ctx.Done():
		s.cancel()
		return ctx.Err()
	}
	return nil
}

func (s *Service) tryDrain() bool {
	select {
	case <-s.drained:
	case s.slot <- struct{}{}:
		s.drainOnce.Do(func() { close(s.drained) })
	default:
		return false
	}
	return true
}

// lock takes the save lock, or answers ErrClosed once Shutdown began, or
// ctx's error.
func (s *Service) lock(ctx context.Context) error {
	select {
	case s.slot <- struct{}{}:
	case <-s.closing:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	// Shutdown may begin with the slot free, and select picks either ready case.
	select {
	case <-s.closing:
		<-s.slot
		return ErrClosed
	default:
		return nil
	}
}

func (s *Service) unlock() { <-s.slot }

func (s *Service) signal() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}
