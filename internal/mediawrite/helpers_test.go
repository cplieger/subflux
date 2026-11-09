package mediawrite_test

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
)

// fakeAlerts keeps the alerts the writer holds active, as status.Store does
// for a retained kind.
type fakeAlerts struct {
	active   map[status.AlertSource]string
	kind     string // the retained kind; its sources outside retained are dropped
	retained []status.AlertSource
	raised   int
	mu       sync.Mutex
}

func ofKind(src status.AlertSource, kind string) bool {
	return kind != "" && (strings.HasPrefix(string(src), kind+":") || strings.HasPrefix(string(src), kind+"#"))
}

func (a *fakeAlerts) Reconcile(src status.AlertSource, active bool, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		a.active = map[status.AlertSource]string{}
	}
	if !active {
		delete(a.active, src)
		return
	}
	if ofKind(src, a.kind) && !slices.Contains(a.retained, src) {
		return
	}
	a.active[src] = text
	a.raised++
}

func (a *fakeAlerts) RetainAlerts(kind string, live []status.AlertSource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.kind, a.retained = kind, slices.Clone(live)
	for src := range a.active {
		if ofKind(src, kind) && !slices.Contains(live, src) {
			delete(a.active, src)
		}
	}
}

// text is src's active alert text; false when src is not active.
func (a *fakeAlerts) text(src status.AlertSource) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.active[src]
	return s, ok
}

func (a *fakeAlerts) activeCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.active)
}

// fakeRoots is a configured root set a test may change.
type fakeRoots struct {
	paths []string
	mu    sync.Mutex
}

func rootsOf(paths ...string) *fakeRoots { return &fakeRoots{paths: paths} }

func (r *fakeRoots) MediaRoots(context.Context) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.paths)
}

func (r *fakeRoots) set(paths ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = paths
}

const showTVDB = 121361

// openStore is a fresh store with one active source, sonarr, and English
// configured.
func openStore(t *testing.T) *statedb.Store {
	t.Helper()
	return openStoreAt(t, filepath.Join(t.TempDir(), "subflux.bolt"), "default:\n  - tag: en\n")
}

// openStoreAt opens the store at path, closed when the test ends, with
// sonarr active and policy, a languages section, in force.
func openStoreAt(t *testing.T, path, policy string) *statedb.Store {
	t.Helper()
	s, err := statedb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(policy), &doc); err != nil {
		t.Fatal(err)
	}
	p, errs := language.ParsePolicy(doc.Content[0])
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := s.Activate(t.Context(), statedb.Activation{Sources: []media.SourceID{"sonarr"}, Languages: p}); err != nil {
		t.Fatal(err)
	}
	return s
}

// seed observes one episode per video, numbered from 1 in order, and
// returns their instances.
func seed(t *testing.T, s *statedb.Store, videos ...string) []statedb.InstanceID {
	t.Helper()
	ids, _ := seedKeys(t, s, videos...)
	return ids
}

// seedKeys is seed returning each episode's key beside its instance.
func seedKeys(t *testing.T, s *statedb.Store, videos ...string) ([]statedb.InstanceID, []media.Key) {
	t.Helper()
	ctx := t.Context()
	p, err := s.BeginPass(ctx, "sonarr", statedb.PassScope{})
	if err != nil {
		t.Fatal(err)
	}
	observe(t, s, p, videos...)
	ids := make([]statedb.InstanceID, 0, len(videos))
	keys := make([]media.Key, 0, len(videos))
	for i := range videos {
		k := episodeKey(t, i+1)
		row, found, err := s.ItemRow(ctx, k)
		if err != nil || !found || row.Inst == 0 {
			t.Fatalf("ItemRow(%d) = %v, %v, %v", i+1, row.Inst, found, err)
		}
		ids = append(ids, row.Inst)
		keys = append(keys, k)
	}
	return ids, keys
}

// completePass runs a complete sonarr pass seeing the episodes at videos,
// numbered from 1, and nothing else.
func completePass(t *testing.T, s *statedb.Store, videos ...string) error {
	t.Helper()
	p, err := s.BeginPass(t.Context(), "sonarr", statedb.PassScope{})
	if err != nil {
		return err
	}
	if err := s.ObserveItems(t.Context(), "sonarr", p, showRoot, episodes(videos)); err != nil {
		return err
	}
	_, err = s.FinishPass(t.Context(), "sonarr", p, true, mediasource.Cursor{}, nil)
	return err
}

func episodeKey(t *testing.T, n int) media.Key {
	t.Helper()
	k, ok := media.Choose(media.Episode, showRoot.External, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: n})
	if !ok {
		t.Fatal("no key")
	}
	return k
}

// observe has pass p see the episodes at videos, numbered from 1.
func observe(t *testing.T, s *statedb.Store, p statedb.PassID, videos ...string) {
	t.Helper()
	if err := s.ObserveItems(t.Context(), "sonarr", p, showRoot, episodes(videos)); err != nil {
		t.Fatal(err)
	}
}

var showRoot = mediasource.Root{
	Native:   mediasource.NativeRef{ID: "73", Kind: media.Episode},
	Title:    "Show",
	External: media.ExternalIDs{TVDB: showTVDB},
}

func episodes(videos []string) []mediasource.Item {
	items := make([]mediasource.Item, 0, len(videos))
	for i, v := range videos {
		items = append(items, mediasource.Item{
			Native: mediasource.NativeID("e" + string(rune('a'+i))), Title: "Episode", VideoPath: v, SceneName: filepath.Base(v),
			Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: i + 1},
		})
	}
	return items
}

var enTag = mustTag("en")

func mustTag(s string) language.Tag {
	tag, ok := language.Parse(s)
	if !ok {
		panic("no language " + s)
	}
	return tag
}

// srtOf is a one-cue SRT document saying text.
func srtOf(text string) string { return "1\n00:00:01,000 --> 00:00:02,000\n" + text + "\n" }

// landRecord writes content at path and stores it as a landed English
// delivery of inst, as Deliver would leave it.
func landRecord(t *testing.T, s *statedb.Store, inst statedb.InstanceID, key media.Key, path, content string) statedb.FileRecord {
	t.Helper()
	return landRecordAs(t, s, inst, key, path, content, enTag)
}

// landRecordAs is landRecord for a file of tag.
func landRecordAs(t *testing.T, s *statedb.Store, inst statedb.InstanceID, key media.Key, path, content string, tag language.Tag) statedb.FileRecord {
	t.Helper()
	writeFile(t, path, content)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	cues, err := timing.ParseCues([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	rec := statedb.FileRecord{
		Inst: inst, Name: filepath.Base(path), Tag: tag, Timing: timing.Identity{}, Locked: true,
		Identity:   statedb.Identity{Size: info.Size(), MtimeNs: info.ModTime().UnixNano(), SHA256: sha256.Sum256([]byte(content))},
		Provenance: &statedb.Provenance{Provider: "opensubtitles", Release: "Show.S01E01"},
	}
	d := statedb.PendingDeliver{Record: rec, Baseline: cues, Attempts: []statedb.Download{{
		At: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Target: tag, Variant: language.Standard, Items: []media.Key{key},
		Inst: inst, Run: statedb.RunID{1}, Attempt: 1, Delivered: true, Candidate: [32]byte{9},
	}}}
	if err := s.BeginDeliver(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if err := s.EndDeliver(t.Context(), inst, rec.Name, true); err != nil {
		t.Fatal(err)
	}
	recs, err := s.Files(t.Context(), inst)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Name == rec.Name {
			return r
		}
	}
	t.Fatalf("no record of %s after landing it", rec.Name)
	return statedb.FileRecord{}
}

// recordNames are the names of inst's stored records.
func recordNames(t *testing.T, s *statedb.Store, inst statedb.InstanceID) []string {
	t.Helper()
	recs, err := s.Files(t.Context(), inst)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range recs {
		names = append(names, r.Name)
	}
	return names
}

// writeFile creates path's directories and writes data at path.
func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newWriter is a writer over d with inject applied; nil fields take a
// fresh fake.
func newWriter(t *testing.T, d mediawrite.Deps, inject func(*mediawrite.Faults)) *mediawrite.Writer {
	t.Helper()
	if d.Alerts == nil {
		d.Alerts = &fakeAlerts{}
	}
	if d.Policy == nil {
		d.Policy = d.Store.Policy
	}
	w := mediawrite.New(d)
	if inject != nil {
		mediawrite.Inject(w, inject)
	}
	return w
}

// run runs w until the test ends and waits for every root's first probe.
func run(t *testing.T, w *mediawrite.Writer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	if err := mediawrite.Settled(t.Context(), w); err != nil {
		t.Fatal(err)
	}
}

func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// delivery is a valid Standard delivery of text to inst, tagged and
// targeting en, as attempt 1 of run 1.
func delivery(t *testing.T, inst statedb.InstanceID, text string) mediawrite.Delivery {
	t.Helper()
	cues, err := timing.ParseCues([]byte(srtOf(text)))
	if err != nil {
		t.Fatal(err)
	}
	return mediawrite.Delivery{
		Inst: inst, Tag: enTag, Target: enTag, Variant: language.Standard, Cues: cues, Timing: timing.Identity{},
		Provenance: statedb.Provenance{Provider: "opensubtitles", Release: "Show.S01E01"},
		Run:        statedb.RunID{1}, Attempt: 1, Candidate: [32]byte{7},
	}
}

// history is every download row naming k, newest first.
func history(t *testing.T, s *statedb.Store, k media.Key) []statedb.Download {
	t.Helper()
	rows, _, err := s.History(t.Context(), statedb.ItemHistory(k), "", 500)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// dirNames lists dir's entries by name.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// pendingCount is how many intents s holds.
func pendingCount(t *testing.T, s *statedb.Store) int {
	t.Helper()
	intents, err := s.PendingWrites(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return len(intents)
}

// logRecords is a slog handler keeping every record, for a writer's
// Logger.
type logRecords struct {
	recs []slog.Record
	mu   sync.Mutex
}

func (*logRecords) Enabled(context.Context, slog.Level) bool { return true }

func (l *logRecords) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recs = append(l.recs, r.Clone())
	return nil
}

func (l *logRecords) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecords) WithGroup(string) slog.Handler      { return l }

// messages are the messages logged at level.
func (l *logRecords) messages(level slog.Level) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, r := range l.recs {
		if r.Level == level {
			out = append(out, r.Message)
		}
	}
	return out
}
