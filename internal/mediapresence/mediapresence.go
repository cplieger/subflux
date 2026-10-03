// Package mediapresence decides whether a media file is gone or only
// unreachable. A file is gone only when the filesystem says it does not exist
// and the bound media root holding it is present, readable and not empty: an
// unmounted share usually leaves an empty mountpoint, so an empty root reads as
// unreachable rather than as every file under it deleted. Any other failure,
// including a filesystem call that outlives the timeout, is a fault of the
// root; while one is recorded it is reported once at ERROR, as a persistent
// alert and as the media_root_unavailable gauge of that root.
package mediapresence

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cplieger/pathinside/v2"
	"github.com/cplieger/subflux/internal/effectqueue"
	"github.com/cplieger/subflux/internal/required"
)

const (
	defaultTimeout         = 10 * time.Second
	defaultRecheckInterval = 5 * time.Minute

	alertPrefix = "media-unavailable:"
)

// ErrUnavailable matches every *UnavailableError.
var ErrUnavailable = errors.New("media root unavailable")

// ErrOutsideRoots reports a path no bound media root contains, whose absence
// therefore proves nothing.
var ErrOutsideRoots = errors.New("path is outside every media root")

var (
	errTimeout = errors.New("filesystem call timed out")
	errEmpty   = errors.New("media root is empty; the share may not be mounted")
	errNotDir  = errors.New("media root is not a directory")
)

// UnavailableError reports that Path could not be judged because Root, the
// bound root holding it, is faulted; Err is the failure that decided it.
type UnavailableError struct {
	Err  error
	Root string
	Path string
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("media root %s is unavailable (checking %s): %v", e.Root, e.Path, e.Err)
}

// Unwrap exposes ErrUnavailable and the underlying error.
func (e *UnavailableError) Unwrap() []error { return []error{ErrUnavailable, e.Err} }

// Metrics is the gauge surface the checker reports through.
type Metrics interface {
	SetMediaRootUnavailable(root string, unavailable bool)
	DeleteMediaRootUnavailable(root string)
}

// Alerts is the persistent-alert surface the checker raises into.
type Alerts interface {
	RecordPersistent(source, msg string)
	DismissBySource(source string)
}

// Config configures a Checker. Metrics and Alerts are required; every other
// zero field takes its documented default.
type Config struct {
	Metrics Metrics
	Alerts  Alerts
	Stat    func(path string) (fs.FileInfo, error) // nil: os.Stat
	// HasEntries reports whether a directory holds at least one entry; nil
	// reads one name.
	HasEntries      func(dir string) (bool, error)
	Timeout         time.Duration // 0: 10s, per filesystem call
	RecheckInterval time.Duration // 0: 5m, the background recheck period
}

// Checker is the process-wide judge of media file absence. It is safe for
// concurrent use.
type Checker struct {
	faults   map[string]*fault
	inflight map[string]*op // the one filesystem call outstanding per root
	cfg      Config
	roots    []string // deepest first
	queue    effectqueue.Queue
	mu       sync.Mutex
}

// fault is a root's recorded failure and the path whose check found it, which
// is what a recheck re-runs. A timed-out call faults the path that started it,
// whichever caller was waiting on it.
type fault struct {
	err  error
	path string
}

// check is one filesystem call made while judging path.
type check struct {
	fn   func() error
	path string
	// notExistAnswers marks the stat of path itself: its not-exist leads on to
	// the root check rather than deciding anything.
	notExistAnswers bool
}

// op is one check in flight; err is valid once done is closed.
type op struct {
	deadline time.Time
	err      error
	done     chan struct{}
	check
}

// timeoutError is a call that outlived the timeout; path is the check that
// started it.
type timeoutError struct {
	path  string
	after time.Duration
}

func (e *timeoutError) Error() string { return fmt.Sprintf("%v after %s", errTimeout, e.after) }

func (e *timeoutError) Unwrap() error { return errTimeout }

// New builds a Checker; it fails only when Metrics or Alerts is missing, a
// nil pointer in either interface included.
func New(cfg Config) (*Checker, error) {
	if required.Missing(cfg.Metrics) {
		return nil, errors.New("mediapresence: Config.Metrics is required")
	}
	if required.Missing(cfg.Alerts) {
		return nil, errors.New("mediapresence: Config.Alerts is required")
	}
	if cfg.Stat == nil {
		cfg.Stat = os.Stat
	}
	if cfg.HasEntries == nil {
		cfg.HasEntries = hasEntries
	}
	cfg.Timeout = cmp.Or(cfg.Timeout, defaultTimeout)
	cfg.RecheckInterval = cmp.Or(cfg.RecheckInterval, defaultRecheckInterval)
	return &Checker{cfg: cfg, faults: map[string]*fault{}, inflight: map[string]*op{}}, nil
}

// Bind installs the configured media roots. It runs on every activation: a new
// root's gauge starts at 0, and a removed root loses its fault, alert and
// series. Before the first Bind every path is outside the roots.
func (c *Checker) Bind(roots []string) {
	cleaned := deepestFirst(roots)
	c.mu.Lock()
	var fx []func()
	for _, r := range c.roots {
		if slices.Contains(cleaned, r) {
			continue
		}
		if _, ok := c.faults[r]; ok {
			delete(c.faults, r)
			fx = append(fx, c.dismissFn(r))
		}
		fx = append(fx, func() { c.cfg.Metrics.DeleteMediaRootUnavailable(r) })
	}
	for _, r := range cleaned {
		if !slices.Contains(c.roots, r) {
			fx = append(fx, func() { c.cfg.Metrics.SetMediaRootUnavailable(r, false) })
		}
	}
	c.roots = cleaned
	c.commitLocked(fx)
}

// Gone reports whether path definitely no longer exists. A false with a nil
// error means it exists. A non-nil error means nothing can be concluded and
// nothing may be deleted on its strength: ErrOutsideRoots, an
// *UnavailableError, or ctx's error when ctx ended first. While a root is
// faulted every path under it but the one that faulted it is unavailable, so
// only that path's check can clear the fault.
func (c *Checker) Gone(ctx context.Context, path string) (bool, error) {
	path = filepath.Clean(path)
	c.mu.Lock()
	root := c.rootOfLocked(path)
	held := c.heldLocked(root, path)
	c.mu.Unlock()
	switch {
	case root == "":
		return false, ErrOutsideRoots
	case held != nil:
		return false, held
	}
	gone, err := c.judge(ctx, root, path)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, c.fail(root, path, err)
	}
	c.clear(root, path)
	return gone, nil
}

// heldLocked returns the *UnavailableError for path when root is faulted by a
// different path.
func (c *Checker) heldLocked(root, path string) error {
	if f, ok := c.faults[root]; ok && f.path != path {
		return &UnavailableError{Root: root, Path: path, Err: f.err}
	}
	return nil
}

// Unavailable reports the faulted root holding path, if any.
func (c *Checker) Unavailable(path string) (root string, unavailable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	root = c.rootOfLocked(filepath.Clean(path))
	_, unavailable = c.faults[root]
	return root, unavailable
}

// Run rechecks every faulted root each RecheckInterval by judging the path
// that faulted it again, so a remounted share clears without waiting for that
// path's next caller. It blocks until ctx is done and returns ctx's error.
func (c *Checker) Run(ctx context.Context) error {
	timer := time.NewTimer(c.cfg.RecheckInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		c.recheck(ctx)
		timer.Reset(c.cfg.RecheckInterval)
	}
}

func (c *Checker) recheck(ctx context.Context) {
	c.mu.Lock()
	pending := map[string]string{}
	for root, f := range c.faults {
		pending[root] = f.path
	}
	c.mu.Unlock()
	for root, path := range pending {
		if _, err := c.judge(ctx, root, path); err == nil {
			c.clear(root, path)
		} else if ctx.Err() != nil {
			return
		}
	}
}

// judge stats path and, when it does not exist, its root. An error is a
// fault, or ctx's error.
func (c *Checker) judge(ctx context.Context, root, path string) (bool, error) {
	err := c.call(ctx, root, check{path: path, notExistAnswers: true, fn: func() error {
		_, err := c.cfg.Stat(path)
		return err
	}})
	switch {
	case err == nil:
		return false, nil
	case !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	if err := c.call(ctx, root, check{path: path, fn: func() error { return c.rootReadable(root) }}); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Checker) rootReadable(root string) error {
	fi, err := c.cfg.Stat(root)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errNotDir
	}
	ok, err := c.cfg.HasEntries(root)
	if err != nil {
		return err
	}
	if !ok {
		return errEmpty
	}
	return nil
}

// call runs chk in root's single in-flight slot: a root has at most one
// filesystem call outstanding, and a caller finding one waits for it until that
// call's deadline, then shares its answer when it faulted the root or starts
// its own, so a stuck share costs one goroutine however many callers ask or
// give up.
func (c *Checker) call(ctx context.Context, root string, chk check) error {
	for {
		c.mu.Lock()
		o, waiting := c.inflight[root]
		if !waiting {
			o = &op{check: chk, deadline: time.Now().Add(c.cfg.Timeout), done: make(chan struct{})}
			c.inflight[root] = o
			go c.run(root, o)
		}
		c.mu.Unlock()
		timer := time.NewTimer(time.Until(o.deadline))
		select {
		case <-o.done:
			timer.Stop()
			if !waiting || o.faulted() {
				return o.err
			}
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			return &timeoutError{path: o.path, after: c.cfg.Timeout}
		}
	}
}

// faulted reports whether the finished call failed for its whole root. Only
// the not-exist of a path's own stat is that path's alone.
func (o *op) faulted() bool {
	return o.err != nil && (!o.notExistAnswers || !errors.Is(o.err, fs.ErrNotExist))
}

func (c *Checker) run(root string, o *op) {
	o.err = o.fn()
	c.mu.Lock()
	delete(c.inflight, root)
	c.commitLocked(c.settleLocked(root, o))
	close(o.done)
}

// settleLocked records a finished call's answer whether or not its caller
// still waits: a failure faults root, and success clears a fault its timeout
// recorded.
func (c *Checker) settleLocked(root string, o *op) []func() {
	switch {
	case o.faulted():
		return c.faultLocked(root, o.path, o.err)
	case o.err != nil:
		return nil
	}
	if f, ok := c.faults[root]; ok && f.path == o.path && errors.Is(f.err, errTimeout) {
		return c.clearLocked(root, o.path)
	}
	return nil
}

// fail records err against root and returns the error the caller reports.
func (c *Checker) fail(root, path string, err error) error {
	owner := path
	if te, ok := errors.AsType[*timeoutError](err); ok {
		owner = te.path
	}
	c.mu.Lock()
	c.commitLocked(c.faultLocked(root, owner, err))
	return &UnavailableError{Root: root, Path: path, Err: err}
}

// faultLocked records err as owner's fault of root. A root already faulted
// keeps its alert and its owner; only the owner's failures replace the cause,
// and a timeout never replaces the failure its call ended with.
func (c *Checker) faultLocked(root, owner string, err error) []func() {
	if !slices.Contains(c.roots, root) {
		return nil
	}
	if f, ok := c.faults[root]; ok {
		if f.path == owner && (!errors.Is(err, errTimeout) || errors.Is(f.err, errTimeout)) {
			f.err = err
		}
		return nil
	}
	c.faults[root] = &fault{err: err, path: owner}
	msg := fmt.Sprintf("Media root %s cannot be read (%s: %v). Nothing under it is treated as deleted, "+
		"and new imports there wait, until it can be read again. Check that the share is mounted.",
		root, owner, err)
	return []func(){
		func() {
			slog.Error("media root unavailable; no state under it is deleted",
				"root", root, "path", owner, "error", err)
		},
		func() { c.cfg.Alerts.RecordPersistent(alertPrefix+root, msg) },
		func() { c.cfg.Metrics.SetMediaRootUnavailable(root, true) },
	}
}

func (c *Checker) clear(root, path string) {
	c.mu.Lock()
	c.commitLocked(c.clearLocked(root, path))
}

// clearLocked drops root's fault when path is the one that recorded it, since a
// different path under the same root succeeding says nothing about that one.
func (c *Checker) clearLocked(root, path string) []func() {
	if f, ok := c.faults[root]; !ok || f.path != path {
		return nil
	}
	delete(c.faults, root)
	return []func(){
		func() { slog.Info("media root readable again", "root", root) },
		c.dismissFn(root),
		func() { c.cfg.Metrics.SetMediaRootUnavailable(root, false) },
	}
}

func (c *Checker) dismissFn(root string) func() {
	return func() { c.cfg.Alerts.DismissBySource(alertPrefix + root) }
}

// commitLocked queues fx in mutation order and releases c.mu before running
// them, since the alert hooks publish onto the event bus.
func (c *Checker) commitLocked(fx []func()) {
	c.queue.Add(fx...)
	c.mu.Unlock()
	c.queue.Drain()
}

func (c *Checker) rootOfLocked(path string) string {
	for _, r := range c.roots {
		if pathinside.Root(r).Contains(path) {
			return r
		}
	}
	return ""
}

// deepestFirst puts a nested root before its parent, so a path resolves to the
// deepest root containing it.
func deepestFirst(roots []string) []string {
	cleaned := make([]string, 0, len(roots))
	for _, r := range roots {
		if r != "" {
			cleaned = append(cleaned, filepath.Clean(r))
		}
	}
	slices.Sort(cleaned)
	cleaned = slices.Compact(cleaned)
	slices.SortStableFunc(cleaned, func(a, b string) int {
		return cmp.Compare(strings.Count(b, string(filepath.Separator)), strings.Count(a, string(filepath.Separator)))
	})
	return cleaned
}

func hasEntries(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	return len(names) > 0, err
}
