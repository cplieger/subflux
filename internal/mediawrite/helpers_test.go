package mediawrite

import (
	"bufio"
	"context"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cplieger/pathinside/v2"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/server/activity"
)

// harness is a Writer over a t.TempDir() root with switchable filesystem
// seams that count their calls, the real alert log and the real metrics.
type harness struct {
	w       *Writer
	alerts  *activity.AlertLog
	metrics *obs.Metrics
	logs    *logCapture

	writeFn    func(ctx context.Context, path string, data []byte) error
	removeFn   func(path string) error
	statFn     func(path string) (fs.FileInfo, error)
	validateFn func(ctx context.Context, path string) error

	stats     map[string]int
	validates map[string]int
	root      string
	writes    []writeCall
	removes   []string
	mu        sync.Mutex
}

type writeCall struct {
	path string
	size int
}

type harnessOpt func(*Config)

func newHarness(t *testing.T, opts ...harnessOpt) *harness {
	t.Helper()
	h := &harness{
		root:      t.TempDir(),
		alerts:    activity.NewAlertLog(100),
		metrics:   obs.New(),
		logs:      captureLogs(t),
		stats:     map[string]int{},
		validates: map[string]int{},
	}
	cfg := Config{
		Metrics:  h.metrics,
		Alerts:   h.alerts,
		MaxBytes: 1 << 20,
		Write: func(ctx context.Context, path string, data []byte) error {
			h.mu.Lock()
			h.writes = append(h.writes, writeCall{path: path, size: len(data)})
			fn := h.writeFn
			h.mu.Unlock()
			if fn == nil {
				return nil
			}
			return fn(ctx, path, data)
		},
		Remove: func(path string) error {
			h.mu.Lock()
			h.removes = append(h.removes, path)
			fn := h.removeFn
			h.mu.Unlock()
			if fn == nil {
				return nil
			}
			return fn(path)
		},
		Stat: func(path string) (fs.FileInfo, error) {
			h.mu.Lock()
			h.stats[path]++
			fn := h.statFn
			h.mu.Unlock()
			if fn == nil {
				return os.Stat(path)
			}
			return fn(path)
		},
	}
	for _, o := range opts {
		o(&cfg)
	}
	w, err := New(cfg)
	if err != nil {
		t.Fatalf("Setup: New: %v", err)
	}
	h.w = w
	h.w.Bind([]string{h.root}, h.validate)
	return h
}

// validate is the default containment fake: lexical containment in a bound
// root, with the switchable override.
func (h *harness) validate(ctx context.Context, path string) error {
	h.mu.Lock()
	h.validates[path]++
	fn := h.validateFn
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, path)
	}
	if !pathinside.Root(h.root).Contains(path) {
		return fs.ErrPermission
	}
	return nil
}

func (h *harness) setWrite(fn func(ctx context.Context, path string, data []byte) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writeFn = fn
}

func (h *harness) setRemove(fn func(path string) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeFn = fn
}

func (h *harness) setStat(fn func(path string) (fs.FileInfo, error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statFn = fn
}

func (h *harness) setValidate(fn func(ctx context.Context, path string) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.validateFn = fn
}

// dir creates a folder under the root and returns its path.
func (h *harness) dir(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join(h.root, rel)
	if err := os.MkdirAll(p, 0o750); err != nil {
		t.Fatalf("Setup: mkdir %s: %v", p, err)
	}
	return p
}

// probeWrites counts probe writes into folder ("" for every folder).
func (h *harness) probeWrites(folder string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.writes {
		if isProbeName(c.path) && (folder == "" || filepath.Dir(c.path) == folder) {
			n++
		}
	}
	return n
}

func (h *harness) writesSnapshot() []writeCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]writeCall(nil), h.writes...)
}

func (h *harness) writeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.writes)
}

func (h *harness) removeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.removes)
}

func (h *harness) probeSizes() []int {
	h.mu.Lock()
	defer h.mu.Unlock()
	var sizes []int
	for _, c := range h.writes {
		if isProbeName(c.path) {
			sizes = append(sizes, c.size)
		}
	}
	return sizes
}

func (h *harness) statCalls(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stats[path]
}

func (h *harness) validateCalls(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.validates[path]
}

func (h *harness) totalValidates() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.validates {
		n += c
	}
	return n
}

func isProbeName(p string) bool { return strings.HasPrefix(filepath.Base(p), probePrefix) }

// alertSources lists the undismissed persistent alert sources.
func (h *harness) alertSources() []string {
	var out []string
	for _, a := range h.alerts.VisibleAlerts() {
		if a.Kind == activity.AlertPersistent {
			out = append(out, a.Source)
		}
	}
	return out
}

func (h *harness) alertMessage(source string) string {
	for _, a := range h.alerts.VisibleAlerts() {
		if a.Source == source {
			return a.Message
		}
	}
	return ""
}

// series reads one sample from the metrics exposition; ok is false when the
// series is absent.
func (h *harness) series(t *testing.T, line string) (v float64, ok bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.metrics.Handler()(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		name, val, found := strings.Cut(sc.Text(), " ")
		if found && name == line {
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				t.Fatalf("parse %q: %v", sc.Text(), err)
			}
			return f, true
		}
	}
	return 0, false
}

func (h *harness) gauge(t *testing.T, root string) float64 {
	t.Helper()
	v, ok := h.series(t, `subflux_media_root_unwritable{root="`+root+`"}`)
	if !ok {
		t.Fatalf("media_root_unwritable{root=%q} is absent", root)
	}
	return v
}

func (h *harness) writeErrors(t *testing.T) float64 {
	t.Helper()
	v, _ := h.series(t, "subflux_subtitle_write_errors_total")
	return v
}

func erofs(path string) error { return &fs.PathError{Op: "open", Path: path, Err: syscall.EROFS} }

// failUnder fails every write into folder or below it with err(path).
func failUnder(folder string, err func(string) error) func(context.Context, string, []byte) error {
	return func(_ context.Context, path string, _ []byte) error {
		if pathinside.Root(folder).Contains(path) {
			return err(path)
		}
		return nil
	}
}

// logCapture records every slog record for the test; tests using it run
// serially because slog's default logger is process-global.
type logCapture struct {
	records []logRecord
	mu      sync.Mutex
}

type logRecord struct {
	attrs map[string]string
	msg   string
	level slog.Level
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(c))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

func (*logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	rec := logRecord{level: r.Level, msg: r.Message, attrs: map[string]string{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.String()
		return true
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, rec)
	return nil
}

func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }

func (c *logCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}

func (c *logCapture) at(level slog.Level) []logRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []logRecord
	for _, r := range c.records {
		if r.level == level {
			out = append(out, r)
		}
	}
	return out
}

func (c *logCapture) withMsg(msg string) []logRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []logRecord
	for _, r := range c.records {
		if r.msg == msg {
			out = append(out, r)
		}
	}
	return out
}

// clock is an injectable Now for the TTL tests.
type clock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
