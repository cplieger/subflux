package mediarefresh_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediarefresh"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
)

// The sources a rig's store activates; a rig configures a subset.
const (
	srcA media.SourceID = "sonarr-a"
	srcB media.SourceID = "sonarr-b"
	srcC media.SourceID = "sonarr-c"
)

// The production tick, restated so a test reads in its own numbers.
const (
	debounce   = 5 * time.Second
	settle     = debounce + time.Second
	retryFirst = 30 * time.Second
)

var errDown = &mediasource.SourceError{Cause: mediasource.Unreachable, Err: errors.New("connection refused")}

// call is one Rescan a source answered.
type call struct {
	err  error
	at   time.Time
	ref  mediasource.NativeRef
	took time.Duration
}

// source is a rescanning source a test scripts: rescan answers each Rescan,
// given that ref's call count from 1, and ping each Ping; a nil script
// succeeds. It records every Rescan.
type source struct {
	*sourcetest.Fake
	rescan func(ctx context.Context, ref mediasource.NativeRef, n int) error
	ping   func() error
	calls  []call
	pings  int
	mu     sync.Mutex
}

func newSource() *source {
	caps := mediasource.Capabilities{Kinds: []media.Kind{media.Episode}, Rescan: true}
	return &source{Fake: sourcetest.NewFake(&sourcetest.Library{}, caps)}
}

func (s *source) script(rescan func(context.Context, mediasource.NativeRef, int) error, ping func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rescan, s.ping = rescan, ping
}

func (s *source) Rescan(ctx context.Context, ref mediasource.NativeRef) error {
	s.mu.Lock()
	f, n := s.rescan, 1
	for _, c := range s.calls {
		if c.ref == ref {
			n++
		}
	}
	s.mu.Unlock()
	start := time.Now()
	var err error
	if f != nil {
		err = f(ctx, ref, n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{ref: ref, at: start, took: time.Since(start), err: err})
	return err
}

func (s *source) Ping(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pings++
	if s.ping == nil {
		return nil
	}
	return s.ping()
}

// sent is every Rescan of ref.
func (s *source) sent(ref mediasource.NativeRef) []call {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []call
	for _, c := range s.calls {
		if c.ref == ref {
			out = append(out, c)
		}
	}
	return out
}

// succeeded counts ref's successful Rescans.
func (s *source) succeeded(ref mediasource.NativeRef) int {
	n := 0
	for _, c := range s.sent(ref) {
		if c.err == nil {
			n++
		}
	}
	return n
}

// refs are the roots s was asked to rescan, in call order.
func (s *source) refs() []mediasource.NativeRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]mediasource.NativeRef, len(s.calls))
	for i, c := range s.calls {
		out[i] = c.ref
	}
	return out
}

func (s *source) pinged() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pings
}

// downUntil fails every Rescan and Ping until up.
func downUntil(up time.Time) (func(context.Context, mediasource.NativeRef, int) error, func() error) {
	failing := func() error {
		if time.Now().Before(up) {
			return errDown
		}
		return nil
	}
	return func(context.Context, mediasource.NativeRef, int) error { return failing() }, failing
}

// faultyFeed is the store with failures a test injects: the next n Since
// calls, and the next n RootTargets calls per root. It records when each
// Since was called.
type faultyFeed struct {
	*statedb.Store
	targetFails map[media.RootKey]int
	sinceAt     []time.Time
	sinceFails  int
	mu          sync.Mutex
}

var errFeed = errors.New("injected feed failure")

func (f *faultyFeed) failSince(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sinceFails = n
}

func (f *faultyFeed) failTargets(root media.RootKey, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.targetFails[root] = n
}

func (f *faultyFeed) sinceTimes() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sinceAt)
}

func (f *faultyFeed) Since(ctx context.Context, epoch [16]byte, after uint64) (statedb.Delta, error) {
	f.mu.Lock()
	f.sinceAt = append(f.sinceAt, time.Now())
	fail := f.sinceFails > 0
	if fail {
		f.sinceFails--
	}
	f.mu.Unlock()
	if fail {
		return statedb.Delta{}, errFeed
	}
	return f.Store.Since(ctx, epoch, after)
}

func (f *faultyFeed) RootTargets(ctx context.Context, root media.RootKey) ([]statedb.RootTarget, error) {
	f.mu.Lock()
	fail := f.targetFails[root] > 0
	if fail {
		f.targetFails[root]--
	}
	f.mu.Unlock()
	if fail {
		return nil, errFeed
	}
	return f.Store.RootTargets(ctx, root)
}

// logs is a concurrency-safe slog sink.
type logs struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// warns are the WARN lines logged so far.
func (l *logs) warns() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for line := range strings.Lines(l.buf.String()) {
		if strings.Contains(line, "level=WARN") {
			out = append(out, line)
		}
	}
	return out
}

// rig is a Refresher running over a real store and Set, inside a synctest
// bubble.
type rig struct {
	t       *testing.T
	db      *statedb.Store
	feed    *faultyFeed
	r       *mediarefresh.Refresher
	logs    *logs
	serving map[media.SourceID]*source
	eps     map[epID]episode
	policy  language.Policy
	mu      sync.Mutex
}

// epID names one episode a source lists.
type epID struct {
	src    media.SourceID
	tvdb   int
	number int
}

// episode is a listed episode's key and instance.
type episode struct {
	key  media.Key
	inst statedb.InstanceID
}

// newRig runs a Refresher over a fresh store activating srcA to srcC and,
// when ids are given, a Set of those sources, waiting for every sink to
// reach the head.
func newRig(t *testing.T, ids ...media.SourceID) *rig {
	t.Helper()
	db, err := statedb.Open(filepath.Join(t.TempDir(), "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := &rig{
		t: t, db: db, logs: &logs{}, serving: map[media.SourceID]*source{}, eps: map[epID]episode{},
		feed: &faultyFeed{Store: db, targetFails: map[media.RootKey]int{}},
	}
	r.activate("en")
	r.r = mediarefresh.New(mediarefresh.Deps{Store: r.feed, Logger: slog.New(slog.NewTextHandler(r.logs, nil))})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	})
	if len(ids) > 0 {
		for _, id := range ids {
			r.serving[id] = newSource()
		}
		r.reload(ids...)
	}
	synctest.Wait()
	return r
}

// activate activates srcA to srcC with the default languages tags.
func (r *rig) activate(tags ...string) {
	r.t.Helper()
	doc := "default:\n"
	for _, tag := range tags {
		doc += "  - tag: " + tag + "\n"
	}
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		r.t.Fatal(err)
	}
	p, errs := language.ParsePolicy(n.Content[0])
	if len(errs) > 0 {
		r.t.Fatal(errs)
	}
	r.policy = p
	if err := r.db.Activate(r.t.Context(), statedb.Activation{Sources: []media.SourceID{srcA, srcB, srcC}, Languages: p}); err != nil {
		r.t.Fatal(err)
	}
}

// src is the source the latest Set build served for id.
func (r *rig) src(id media.SourceID) *source {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.serving[id]
}

// replace makes the next Set build serve a new source for id, returning it.
func (r *rig) replace(id media.SourceID) *source {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := newSource()
	r.serving[id] = s
	return s
}

// reload hands the Refresher a Set rebuilt over ids, each served by its
// current source; an id with none gets a new one.
func (r *rig) reload(ids ...media.SourceID) {
	r.t.Helper()
	kind := mediasource.AdapterKind{Type: "fake", Schema: sourcetest.FakeSchema, New: func(c mediasource.Config, _ mediasource.Deps) (mediasource.Adapter, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.serving[c.ID] == nil {
			r.serving[c.ID] = newSource()
		}
		return r.serving[c.ID], nil
	}}
	cfgs := make([]mediasource.Config, len(ids))
	for i, id := range ids {
		cfgs[i] = mediasource.Config{ID: id, Type: "fake"}
	}
	set, err := mediasource.NewSet([]mediasource.AdapterKind{kind}, cfgs, mediasource.Deps{Logger: slog.New(slog.DiscardHandler)}, sourcetest.OwnPlaces{})
	if err != nil {
		r.t.Fatal(err)
	}
	r.r.ReloadSources(set)
}

// show is one series a source lists: its root id, TVDB id and episode
// numbers in season 1.
type show struct {
	native   string
	tvdb     int
	episodes []int
}

// ref is the root a source rescans for sh.
func (sh show) ref() mediasource.NativeRef {
	return mediasource.NativeRef{ID: mediasource.NativeID(sh.native), Kind: media.Episode}
}

// list has a complete pass of id see exactly shows, each episode's video at
// a path of its own.
func (r *rig) list(id media.SourceID, shows ...show) {
	r.t.Helper()
	r.listAt(id, func(sh show, n int) string { return fmt.Sprintf("/tv/%s/%d/E%02d.mkv", id, sh.tvdb, n) }, shows...)
}

// listAt is list with each episode's video at path(show, number).
func (r *rig) listAt(id media.SourceID, path func(show, int) string, shows ...show) {
	r.t.Helper()
	ctx := r.t.Context()
	p, err := r.db.BeginPass(ctx, id, statedb.PassScope{})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, sh := range shows {
		root := mediasource.Root{Native: sh.ref(), Title: "Show " + sh.native, External: media.ExternalIDs{TVDB: uint64(sh.tvdb)}}
		items := make([]mediasource.Item, 0, len(sh.episodes))
		for _, n := range sh.episodes {
			v := path(sh, n)
			items = append(items, mediasource.Item{
				Native: mediasource.NativeID(fmt.Sprintf("e%d-%d", sh.tvdb, n)), Title: "Episode", VideoPath: v, SceneName: filepath.Base(v),
				Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: n},
			})
		}
		if err := r.db.ObserveItems(ctx, id, p, root, items); err != nil {
			r.t.Fatal(err)
		}
	}
	if _, err := r.db.FinishPass(ctx, id, p, true, mediasource.Cursor{}, nil); err != nil {
		r.t.Fatal(err)
	}
	for _, sh := range shows {
		for _, n := range sh.episodes {
			k, ok := media.Choose(media.Episode, media.ExternalIDs{TVDB: uint64(sh.tvdb)}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: n})
			if !ok {
				r.t.Fatalf("no key for %d E%d", sh.tvdb, n)
			}
			row, found, err := r.db.ItemRow(ctx, k)
			if err != nil || !found || row.Inst == 0 {
				r.t.Fatalf("ItemRow(%s) = %v, %v, %v", k, row.Inst, found, err)
			}
			r.eps[epID{src: id, tvdb: sh.tvdb, number: n}] = episode{key: k, inst: row.Inst}
		}
	}
}

func (r *rig) ep(id media.SourceID, tvdb, number int) episode {
	r.t.Helper()
	e, ok := r.eps[epID{src: id, tvdb: tvdb, number: number}]
	if !ok {
		r.t.Fatalf("%s lists no %d E%d", id, tvdb, number)
	}
	return e
}

// deliver lands an English subtitle for e, as mediawrite's Deliver
// records one, and returns its record.
func (r *rig) deliver(e episode) statedb.FileRecord {
	r.t.Helper()
	const doc = "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	cues, err := timing.ParseCues([]byte(doc))
	if err != nil {
		r.t.Fatal(err)
	}
	tag, _ := language.Parse("en")
	name := fmt.Sprintf("%d.en.srt", e.inst)
	rec := statedb.FileRecord{
		Inst: e.inst, Name: name, Tag: tag, Timing: timing.Identity{},
		Identity:   statedb.Identity{Size: int64(len(doc)), MtimeNs: 1, SHA256: sha256.Sum256([]byte(name))},
		Provenance: &statedb.Provenance{Provider: "opensubtitles", Release: "Show.S01E01"},
	}
	d := statedb.PendingDeliver{Record: rec, Baseline: cues, Attempts: []statedb.Download{{
		At: time.Now(), Target: tag, Variant: language.Standard, Items: []media.Key{e.key},
		Inst: e.inst, Run: statedb.RunID{1}, Attempt: 1, Delivered: true, Candidate: [32]byte{9},
	}}}
	if err := r.db.BeginDeliver(r.t.Context(), d); err != nil {
		r.t.Fatal(err)
	}
	if err := r.db.EndDeliver(r.t.Context(), e.inst, name, true); err != nil {
		r.t.Fatal(err)
	}
	return rec
}

// retime lands a timing save of rec.
func (r *rig) retime(rec statedb.FileRecord) {
	r.t.Helper()
	rec.Timing = timing.Shift{Ms: 500}
	if err := r.db.BeginRetime(r.t.Context(), rec, nil); err != nil {
		r.t.Fatal(err)
	}
	if err := r.db.EndRetime(r.t.Context(), rec.Inst, rec.Name, true); err != nil {
		r.t.Fatal(err)
	}
}

// remove lands the removal of rec's file, as mediawrite's Delete and its
// reconcile of a file gone outside subflux both record it.
func (r *rig) remove(rec statedb.FileRecord) {
	r.t.Helper()
	if err := r.db.BeginDelete(r.t.Context(), rec.Inst, rec.Name, rec.Identity.SHA256); err != nil {
		r.t.Fatal(err)
	}
	if err := r.db.EndDelete(r.t.Context(), rec.Inst, rec.Name, true); err != nil {
		r.t.Fatal(err)
	}
}

// after advances the bubble's clock by d and waits for every sink to
// block.
func after(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}
