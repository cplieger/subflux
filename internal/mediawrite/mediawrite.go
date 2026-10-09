// Package mediawrite owns every subtitle write into the media tree and the
// process-wide record of which destination folders refuse writes.
//
// A write error marks its folder only when it reads as a folder fault and a
// probe of that folder (a real write of a hidden file, then a delete) fails
// too; any other refusal belongs to the one target. A marked folder blocks
// work under it until a probe or a write there succeeds, and while it is
// marked the condition is reported once at ERROR, as a persistent UI alert
// and as the media_root_unwritable gauge of its root.
package mediawrite

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/pathinside/v2"
	"github.com/cplieger/subflux/internal/effectqueue"
	"github.com/cplieger/subflux/internal/required"
)

const (
	defaultProbeTimeout    = 10 * time.Second
	defaultProbeTTL        = 60 * time.Second
	defaultRecheckInterval = 5 * time.Minute

	// maxFolderAlerts bounds the per-folder alerts so they cannot evict the
	// credential and startup alerts from the 100-entry AlertLog; folders past
	// it are reported by their root's aggregate alert.
	maxFolderAlerts = 16

	// unconfiguredRoot labels a folder a real write reached outside every
	// bound root.
	unconfiguredRoot = "unconfigured"

	folderAlertPrefix = "media-unwritable:"
	rootAlertPrefix   = "media-unwritable-root:"

	opWrite        = "write"
	opProbe        = "probe"
	opProbeCleanup = "probe cleanup"
)

// ErrUnwritable matches every *UnwritableError.
var ErrUnwritable = errors.New("media folder is not writable")

// UnwritableError reports a folder that refused a write or could not be
// proven writable. Op is "write", "probe" or "probe cleanup"; Err is the
// operating-system error or the probe timeout.
type UnwritableError struct {
	Err    error
	Folder string
	Op     string
}

func (e *UnwritableError) Error() string {
	return fmt.Sprintf("subtitles cannot be written in %s (%s): %v", e.Folder, e.Op, e.Err)
}

// Unwrap exposes ErrUnwritable and the underlying error.
func (e *UnwritableError) Unwrap() []error { return []error{ErrUnwritable, e.Err} }

// metrics is the gauge and counter surface the writer reports through.
type metrics interface {
	SetMediaRootUnwritable(root string, unwritable bool)
	DeleteMediaRoot(root string)
	IncSubtitleWriteError()
}

// alerts is the persistent-alert surface the writer raises into.
type alerts interface {
	RecordPersistent(source, msg string)
	DismissBySource(source string)
	HasUndismissed(source string) bool
}

// Config configures a Writer. Metrics and Alerts are required; every other
// zero field takes its documented default.
type Config struct {
	Metrics metrics
	Alerts  alerts
	// Write writes one file; nil is atomicfile.WriteFile capped at MaxBytes,
	// with no WithMode, so a subtitle gets the share's creation defaults.
	// Probes and subtitle writes both go through it.
	Write  func(ctx context.Context, path string, data []byte) error
	Remove func(path string) error                // nil: os.Remove
	Stat   func(path string) (fs.FileInfo, error) // nil: os.Stat
	Now    func() time.Time                       // nil: time.Now
	// MaxBytes caps every write; 0 means no cap.
	MaxBytes        int64
	ProbeTimeout    time.Duration // 0: 10s
	ProbeTTL        time.Duration // 0: 60s, how long a passing folder probe is reused
	RecheckInterval time.Duration // 0: 5m, the background recheck period
}

// Writer is the process-wide media write guard. It is safe for concurrent use.
//
// A bad folder's root is derived from the bound roots whenever it is read,
// so a Bind only swaps the roots and recomputes the alerts and gauges they
// imply. The logs, alerts and metric calls a state change decides go through
// queue, so they run after mu is released and in the order the state changed.
type Writer struct {
	validate func(ctx context.Context, path string) error
	bad      map[string]*badFolder
	probedOK map[string]time.Time
	probes   map[string]*probeCall // the probe whose work runs or whose outcome is being applied, per folder
	rootAggs map[string]bool       // roots whose aggregate alert this writer raised
	gauges   map[string]bool       // the last value queued per root series
	roots    []string              // deepest first
	queue    effectqueue.Queue
	cfg      Config
	mu       sync.Mutex
	warned   bool // the no-media_roots WARN was logged since the last Bind
}

type badFolder struct {
	since   time.Time
	err     error
	alerted bool // holds its own alert; the root aggregate counts it but is raised only while some folder has none
	vetted  bool
}

// faultReport is what a fault's log line, alert and error name: Op and Err
// are the failing operation, ProbeErr the confirming probe's own error.
type faultReport struct {
	Err      error
	ProbeErr error
	Op       string
}

// effects are the logs, alerts and metric calls a mutation decided under the
// mutex; they run after it is released because the alert hooks publish onto
// the SSE bus and a metric collaborator may block.
type effects []func()

// commitLocked queues fx behind every effect already queued and releases
// w.mu, then drains the queue.
func (w *Writer) commitLocked(fx effects) {
	w.queue.Add(fx...)
	w.mu.Unlock()
	w.queue.Drain()
}

// New builds a Writer; it fails only when Metrics or Alerts is missing, a
// nil pointer in either interface included.
func New(cfg Config) (*Writer, error) { //nolint:gocritic // hugeParam: the defaults are filled into New's own copy
	if required.Missing(cfg.Metrics) {
		return nil, errors.New("mediawrite: Config.Metrics is required")
	}
	if required.Missing(cfg.Alerts) {
		return nil, errors.New("mediawrite: Config.Alerts is required")
	}
	if cfg.Write == nil {
		maxBytes := cfg.MaxBytes
		cfg.Write = func(ctx context.Context, path string, data []byte) error {
			_, err := atomicfile.WriteFile(ctx, path, data, atomicfile.WithMaxBytes(maxBytes))
			return err
		}
	}
	if cfg.Remove == nil {
		cfg.Remove = os.Remove
	}
	if cfg.Stat == nil {
		cfg.Stat = os.Stat
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	cfg.ProbeTimeout = cmp.Or(cfg.ProbeTimeout, defaultProbeTimeout)
	cfg.ProbeTTL = cmp.Or(cfg.ProbeTTL, defaultProbeTTL)
	cfg.RecheckInterval = cmp.Or(cfg.RecheckInterval, defaultRecheckInterval)
	return &Writer{
		cfg:      cfg,
		bad:      map[string]*badFolder{},
		probedOK: map[string]time.Time{},
		probes:   map[string]*probeCall{},
		rootAggs: map[string]bool{},
		gauges:   map[string]bool{},
	}, nil
}

// Bind installs the configured media roots and the containment check that
// decides which requested folders may be probed. It runs on every
// activation: a new root's gauge starts at 0, a removed root loses its
// series, a bad folder resolves to the deepest new root containing it, and
// one no new root contains is dropped with its alert. It drops the passing
// probe cache; a probe already running finishes and settles against the
// state it finds. Before the first Bind every requested folder is refused.
func (w *Writer) Bind(roots []string, validate func(ctx context.Context, path string) error) {
	cleaned := deepestFirst(roots)
	var fx effects
	w.mu.Lock()
	old := w.roots
	w.roots = cleaned
	w.validate = validate
	w.warned = false
	clear(w.probedOK)
	for folder, b := range w.bad {
		switch {
		case w.rootOfLocked(folder) == "":
			delete(w.bad, folder)
			if b.alerted {
				fx = append(fx, w.dismissFn(folderAlertPrefix+folder))
			}
		case b.alerted && slices.Contains(old, folder) != slices.Contains(cleaned, folder):
			fx = append(fx, w.refreshFn(folder, b))
		}
	}
	aggRoots := map[string]bool{}
	for root := range w.rootAggs {
		aggRoots[root] = true
	}
	for folder, b := range w.bad {
		if !b.alerted {
			aggRoots[w.labelLocked(folder)] = true
		}
	}
	for root := range aggRoots {
		fx = append(fx, w.aggregateLocked(root, !w.rootAggs[root])...)
	}
	fx = append(fx, w.gaugesLocked()...)
	w.commitLocked(fx)
}

// deepestFirst puts a nested root before its parent: rootOfLocked takes the
// first root containing a path, so the other order resolves a nested root's
// folders to the parent.
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

// WriteFile writes a subtitle. A folder fault confirmed by a probe of the
// subtitle's own size returns an *UnwritableError and leaves the folder
// marked; every other failure is returned as is and marks nothing. A success
// clears the folder when it was marked.
func (w *Writer) WriteFile(ctx context.Context, path string, data []byte) error {
	folder := filepath.Dir(path)
	err := w.cfg.Write(ctx, path, data)
	if err == nil {
		w.clear(folder, false)
		return nil
	}
	if isContextErr(err) {
		return err
	}
	w.cfg.Metrics.IncSubtitleWriteError()
	if !folderFault(err, writeSource) {
		slog.Warn("subtitle not written", "path", path, "error", err)
		return err
	}

	size := max(int64(len(data)), int64(len(probeLine)))
	if w.cfg.MaxBytes > 0 {
		size = max(min(size, w.cfg.MaxBytes), int64(len(probeLine)))
	}
	report := &faultReport{Op: opWrite, Err: err}
	o, werr := w.probe(ctx, probeTarget{folder: folder, size: size, confirm: report})
	if werr != nil {
		return err
	}
	if o.kind != probeFault {
		slog.Warn("subtitle not written; its folder accepts other writes", "path", path, "error", err)
		return err
	}
	w.reraise(folder)
	return &UnwritableError{Folder: folder, Op: opWrite, Err: err}
}

// Blocked reports the deepest marked folder at or above path's folder, up to
// its root.
func (w *Writer) Blocked(path string) (folder string, blocked bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.bad) == 0 {
		return "", false
	}
	root := w.rootOfLocked(path)
	for f := filepath.Dir(path); ; f = filepath.Dir(f) {
		if _, ok := w.bad[f]; ok {
			return f, true
		}
		if f == root || filepath.Dir(f) == f {
			return "", false
		}
	}
}

// RecheckInterval is how often Run re-probes the marked folders.
func (w *Writer) RecheckInterval() time.Duration {
	return w.cfg.RecheckInterval
}

func (w *Writer) rootOfLocked(path string) string {
	for _, r := range w.roots {
		if pathinside.Root(r).Contains(path) {
			return r
		}
	}
	return ""
}

// labelLocked is the root series a folder reports under.
func (w *Writer) labelLocked(folder string) string { return cmpRoot(w.rootOfLocked(folder)) }

func cmpRoot(root string) string {
	if root == "" {
		return unconfiguredRoot
	}
	return root
}

// fail records a folder fault against the current roots. Only a real write
// marks a folder outside every root. A write-op report is not counted,
// because WriteFile counted the failed write. A repeat fault on a marked
// folder updates its error and alert text unless quiet is set. It reports
// whether the folder is marked.
func (w *Writer) fail(folder string, rep faultReport, vetted, quiet bool) (marked bool) {
	countProbe := rep.Op != opWrite
	var fx effects
	w.mu.Lock()
	root := w.rootOfLocked(folder)
	if root == "" && rep.Op != opWrite {
		w.mu.Unlock()
		return false
	}
	label := cmpRoot(root)
	delete(w.probedOK, folder)
	b, already := w.bad[folder]
	switch {
	case already && quiet:
	case already:
		b.err = rep.Err
		b.vetted = b.vetted || vetted
		fx = append(fx, w.refreshFn(folder, b))
		if countProbe {
			fx = append(fx, w.cfg.Metrics.IncSubtitleWriteError)
		}
	default:
		b = &badFolder{err: rep.Err, since: w.cfg.Now(), vetted: vetted}
		b.alerted = w.alertedCountLocked() < maxFolderAlerts
		w.bad[folder] = b
		fx = append(fx, func() { logFault(folder, label, rep) })
		if countProbe {
			fx = append(fx, w.cfg.Metrics.IncSubtitleWriteError)
		}
		if b.alerted {
			msg := folderMessage(folder, rep.Err, w.isRootLocked(folder))
			fx = append(fx, func() { w.cfg.Alerts.RecordPersistent(folderAlertPrefix+folder, msg) })
		}
		fx = append(fx, w.aggregateLocked(label, !b.alerted)...)
		fx = append(fx, w.gaugesLocked()...)
	}
	w.commitLocked(fx)
	return true
}

// clear drops a marked folder that proved writable. stampOK records a
// passing probe for the Folders TTL.
func (w *Writer) clear(folder string, stampOK bool) {
	w.mu.Lock()
	if stampOK {
		w.probedOK[folder] = w.cfg.Now()
	}
	fx, root, ok := w.dropLocked(folder)
	if ok {
		fx = append(effects{func() { slog.Info("media folder writable again", "folder", folder, "root", root) }}, fx...)
	}
	w.commitLocked(fx)
}

// forget drops a marked folder that no longer exists or is no longer one
// subflux may probe; nothing became writable, so it logs at DEBUG.
// unvettedOnly keeps a folder a containment check or a write already vetted.
func (w *Writer) forget(folder string, unvettedOnly bool) {
	w.mu.Lock()
	if b, ok := w.bad[folder]; !ok || (unvettedOnly && b.vetted) {
		w.mu.Unlock()
		return
	}
	fx, root, _ := w.dropLocked(folder)
	fx = append(effects{func() { slog.Debug("media folder gone; dropping its write state", "folder", folder, "root", root) }}, fx...)
	w.commitLocked(fx)
}

func (w *Writer) dropLocked(folder string) (fx effects, root string, ok bool) {
	b, ok := w.bad[folder]
	if !ok {
		return nil, "", false
	}
	delete(w.bad, folder)
	root = w.labelLocked(folder)
	if b.alerted {
		fx = append(fx, w.dismissFn(folderAlertPrefix+folder))
	}
	fx = append(fx, w.aggregateLocked(root, false)...)
	fx = append(fx, w.gaugesLocked()...)
	return fx, root, true
}

// reraise re-creates a folder's dismissed alert (or its root aggregate's);
// an undismissed one was already refreshed by the settlement.
func (w *Writer) reraise(folder string) {
	w.mu.Lock()
	var fx effects
	if b, ok := w.bad[folder]; ok {
		fx = append(fx, w.recreateFn(folder, b))
	}
	w.commitLocked(fx)
}

func (w *Writer) refreshFn(folder string, b *badFolder) func() {
	source, msg := w.alertForLocked(folder, b)
	return func() {
		if w.cfg.Alerts.HasUndismissed(source) {
			w.cfg.Alerts.RecordPersistent(source, msg)
		}
	}
}

func (w *Writer) recreateFn(folder string, b *badFolder) func() {
	source, msg := w.alertForLocked(folder, b)
	return func() {
		if !w.cfg.Alerts.HasUndismissed(source) {
			w.cfg.Alerts.RecordPersistent(source, msg)
		}
	}
}

func (w *Writer) alertForLocked(folder string, b *badFolder) (source, msg string) {
	if b.alerted {
		return folderAlertPrefix + folder, folderMessage(folder, b.err, w.isRootLocked(folder))
	}
	root := w.labelLocked(folder)
	return rootAlertPrefix + root, w.aggregateMessageLocked(root)
}

func (w *Writer) dismissFn(source string) func() {
	return func() { w.cfg.Alerts.DismissBySource(source) }
}

// aggregateLocked keeps the root's aggregate alert in step with its bad
// folders: raised (or re-created, when raise is set) while one has no alert
// of its own, otherwise refreshed only while undismissed, and dismissed once
// none remains.
func (w *Writer) aggregateLocked(root string, raise bool) effects {
	source := rootAlertPrefix + root
	if msg := w.aggregateMessageLocked(root); msg != "" {
		w.rootAggs[root] = true
		return effects{func() {
			if raise || w.cfg.Alerts.HasUndismissed(source) {
				w.cfg.Alerts.RecordPersistent(source, msg)
			}
		}}
	}
	if !w.rootAggs[root] {
		return nil
	}
	delete(w.rootAggs, root)
	return effects{w.dismissFn(source)}
}

// aggregateMessageLocked is the root's aggregate alert text, "" when every
// bad folder under it holds its own alert.
func (w *Writer) aggregateMessageLocked(root string) string {
	var n int
	var first string
	var firstBad *badFolder
	for folder, b := range w.bad {
		if w.labelLocked(folder) != root {
			continue
		}
		n++
		if b.alerted {
			continue
		}
		if firstBad == nil || b.since.Before(firstBad.since) || (b.since.Equal(firstBad.since) && folder < first) {
			first, firstBad = folder, b
		}
	}
	if firstBad == nil {
		return ""
	}
	return fmt.Sprintf("%d folders under %s cannot be written. The first was %s, which failed with %v. %s",
		n, root, first, firstBad.err, remedy(false))
}

// gaugesLocked queues the media_root_unwritable changes the roots and bad
// folders imply: a root reads 1 while a bad folder resolves to it, a removed
// root loses its series, and the unconfigured series, once exported, stays.
func (w *Writer) gaugesLocked() effects {
	want := make(map[string]bool, len(w.roots)+1)
	for _, r := range w.roots {
		want[r] = false
	}
	if _, ok := w.gauges[unconfiguredRoot]; ok {
		want[unconfiguredRoot] = false
	}
	for folder := range w.bad {
		want[w.labelLocked(folder)] = true
	}
	var fx effects
	for root, unwritable := range want {
		if cur, ok := w.gauges[root]; ok && cur == unwritable {
			continue
		}
		w.gauges[root] = unwritable
		fx = append(fx, func() { w.cfg.Metrics.SetMediaRootUnwritable(root, unwritable) })
	}
	for root := range w.gauges {
		if _, ok := want[root]; !ok {
			delete(w.gauges, root)
			fx = append(fx, func() { w.cfg.Metrics.DeleteMediaRoot(root) })
		}
	}
	return fx
}

func (w *Writer) alertedCountLocked() int {
	var n int
	for _, b := range w.bad {
		if b.alerted {
			n++
		}
	}
	return n
}

func (w *Writer) isRootLocked(folder string) bool { return slices.Contains(w.roots, folder) }

func folderMessage(folder string, err error, isRoot bool) string {
	return fmt.Sprintf("Subtitles cannot be written in %s: %v. %s", folder, err, remedy(isRoot))
}

func remedy(isRoot bool) string {
	s := "Scans and downloads for files there are paused until a write test there succeeds. " +
		"Check that the share is mounted read-write, has free space, and that subflux's user " +
		"may create and delete files in it"
	if isRoot {
		return s + ", or remove it from media_roots."
	}
	return s + "."
}

func logFault(folder, root string, rep faultReport) {
	args := []any{"folder", folder, "root", root, "op", rep.Op, "error", rep.Err}
	if rep.ProbeErr != nil {
		args = append(args, "probe_error", rep.ProbeErr)
	}
	slog.Error("media folder not writable; subtitle work paused", args...)
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
