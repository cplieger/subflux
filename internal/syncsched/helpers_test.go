package syncsched_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
)

// rig is a library of one root holding one episode's video, the real
// store, writer, prober and status store over it, and an Aligner whose
// ffmpeg and ffprobe are this test binary.
type rig struct {
	store    *statedb.Store
	writer   *mediawrite.Writer
	status   *status.Store
	a        *syncsched.Aligner
	calls    func(ffmpegtest.Call) int
	stopRun  func()
	startRun func()
	root     string
	video    string
	inst     statedb.InstanceID
}

type fixedRoots []string

func (r fixedRoots) MediaRoots(context.Context) []string { return r }

// rigSetup is what a rigOption may change before the rig is built.
type rigSetup struct {
	persister  status.Persister
	roots      func(root string) []string
	deps       []func(*syncsched.Deps)
	empty      bool // the video's root holds nothing, so it is unavailable
	hostFFmpeg bool // the host's ffmpeg and ffprobe run, not this binary
	noRun      bool // the Aligner's Run waits for the test's startRun
}

// rigOption changes the rig before its Aligner is built.
type rigOption func(*rigSetup)

func withDeps(f func(*syncsched.Deps)) rigOption {
	return func(s *rigSetup) { s.deps = append(s.deps, f) }
}

func newRig(t *testing.T, opts ...rigOption) *rig {
	t.Helper()
	r := &rig{calls: ffmpegtest.Calls(t), root: t.TempDir()}
	r.video = filepath.Join(r.root, "Show", "Show S01E01.mkv")
	setup := rigSetup{roots: func(root string) []string { return []string{root} }, persister: statustest.NewMemPersister()}
	for _, o := range opts {
		o(&setup)
	}
	if !setup.hostFFmpeg {
		ffmpegtest.OnPath(t, "ffmpeg", "ffprobe")
	}
	if !setup.empty {
		if err := os.MkdirAll(filepath.Dir(r.video), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(r.video, []byte("video"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.store = openStore(t)
	r.inst = observe(t, r.store, r.video)
	st, err := status.New(t.Context(), setup.persister)
	if err != nil {
		t.Fatal(err)
	}
	r.status = st
	policy := func() language.Policy { return enPolicy(t) }
	r.writer = mediawrite.New(mediawrite.Deps{Store: r.store, Roots: fixedRoots(setup.roots(r.root)), Alerts: st, Policy: policy, Logger: quiet})
	d := syncsched.Deps{Writer: r.writer, Prober: embedded.New(r.store), Status: st, Policy: policy, Logger: quiet}
	for _, f := range setup.deps {
		f(&d)
	}
	r.a = syncsched.New(d)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.writer.Run(ctx)
	}()
	runCtx, stopRun := context.WithCancel(context.Background())
	ran := make(chan struct{})
	r.stopRun = sync.OnceFunc(func() {
		stopRun()
		<-ran
	})
	r.startRun = sync.OnceFunc(func() {
		go func() {
			defer close(ran)
			if err := r.a.Run(runCtx); err != nil {
				t.Errorf("Run = %v", err)
			}
		}()
	})
	if !setup.noRun {
		r.startRun()
	}
	t.Cleanup(func() {
		r.startRun()
		r.stopRun()
		cancel()
		<-done
	})
	return r
}

var quiet = slog.New(slog.DiscardHandler)

func openStore(t *testing.T) *statedb.Store {
	t.Helper()
	s, err := statedb.Open(filepath.Join(t.TempDir(), "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Activate(t.Context(), statedb.Activation{Sources: []media.SourceID{"sonarr"}, Languages: enPolicy(t)}); err != nil {
		t.Fatal(err)
	}
	return s
}

func enPolicy(t testing.TB) language.Policy {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("default:\n  - tag: en\n"), &doc); err != nil {
		t.Fatal(err)
	}
	p, errs := language.ParsePolicy(doc.Content[0])
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return p
}

// observe has a complete sonarr pass see one episode at video and returns
// its instance.
func observe(t *testing.T, s *statedb.Store, video string) statedb.InstanceID {
	t.Helper()
	ctx := t.Context()
	pass, err := s.BeginPass(ctx, "sonarr", statedb.PassScope{})
	if err != nil {
		t.Fatal(err)
	}
	root := mediasource.Root{Native: mediasource.NativeRef{ID: "73", Kind: media.Episode}, Title: "Show", External: media.ExternalIDs{TVDB: 121361}}
	item := mediasource.Item{
		Native: "e1", Title: "Episode", VideoPath: video, SceneName: filepath.Base(video),
		Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1},
	}
	if err := s.ObserveItems(ctx, "sonarr", pass, root, []mediasource.Item{item}); err != nil {
		t.Fatal(err)
	}
	key, ok := media.Choose(media.Episode, root.External, item.Numbering)
	if !ok {
		t.Fatal("no key")
	}
	row, found, err := s.ItemRow(ctx, key)
	if err != nil || !found || row.Inst == 0 {
		t.Fatalf("ItemRow = %v, %v, %v", row.Inst, found, err)
	}
	return row.Inst
}

func tag(t testing.TB, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("no language %q", s)
	}
	return tg
}

// track is one subtitle stream of the fake ffprobe's answer.
type track struct {
	codec, lang, title string
	index              int
	forced, hi         bool
}

// probeAnswer is the fake ffprobe's answer: a 25 fps video stream, an
// audio stream and tracks.
func probeAnswer(tracks ...track) string {
	streams := []string{
		`{"index":0,"codec_type":"video","codec_name":"h264","r_frame_rate":"25/1"}`,
		`{"index":1,"codec_type":"audio","codec_name":"aac"}`,
	}
	for _, tr := range tracks {
		streams = append(streams, fmt.Sprintf(`{"index":%d,"codec_type":"subtitle","codec_name":%q,"tags":{"language":%q,"title":%q},"disposition":{"forced":%d,"hearing_impaired":%d}}`,
			tr.index, tr.codec, tr.lang, tr.title, b2i(tr.forced), b2i(tr.hi)))
	}
	return `probe:{"streams":[` + strings.Join(streams, ",") + `]}`
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// japanese is the reference track most tests give the video: stream 2.
var japanese = track{codec: "subrip", lang: "jpn", index: 2}

// withReference has the fake ffprobe list tracks and extraction answer
// stream n with docs[n]; a stream with no doc fails.
func withReference(t *testing.T, docs map[int][]byte, tracks ...track) {
	t.Helper()
	dir := t.TempDir()
	for n, doc := range docs {
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(n)), doc, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ffmpegtest.UseFor(t, ffmpegtest.Probe, probeAnswer(tracks...))
	ffmpegtest.UseFor(t, ffmpegtest.Extract, "stream-file:"+dir)
}

// episode is n dialogue spans with lengths of 0.8 to 4 s and gaps of 0.3 to
// 6 s drawn from seed: no stretch repeats, so an aligner cannot lock onto a
// period.
func episode(seed uint64, n int) []subsync.Span {
	r := rand.New(rand.NewPCG(seed, 7))
	out := make([]subsync.Span, n)
	at := int64(5_000)
	for i := range out {
		l := 800 + r.Int64N(3_200)
		out[i] = subsync.Span{StartMs: at, EndMs: at + l}
		at += l + 300 + r.Int64N(5_700)
	}
	return out
}

func shifted(spans []subsync.Span, ms int64) []subsync.Span {
	out := make([]subsync.Span, len(spans))
	for i, s := range spans {
		out[i] = subsync.Span{StartMs: s.StartMs + ms, EndMs: s.EndMs + ms}
	}
	return out
}

func end(spans []subsync.Span) int64 { return spans[len(spans)-1].EndMs }

// srt renders spans as an SRT document, each cue one line of text.
func srt(spans []subsync.Span) []byte {
	var b strings.Builder
	for i, s := range spans {
		fmt.Fprintf(&b, "%d\n%s --> %s\nLine %d of the dialogue.\n\n", i+1, stamp(s.StartMs), stamp(s.EndMs), i+1)
	}
	return []byte(b.String())
}

func stamp(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d,%03d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60, ms%1000)
}

// owner is the user every test job runs for.
const owner int64 = 7

// subtitle writes payload beside the rig's video as name: an unmanaged
// file, with the expectation List would give it.
func (r *rig) subtitle(t *testing.T, name string, payload []byte) syncsched.Target {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(r.video), name), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := mediawrite.DecodeRef(r.inst, name)
	if err != nil {
		t.Fatal(err)
	}
	return syncsched.Target{File: ref, Expect: mediawrite.Expectation{SHA256: sha256.Sum256(payload)}}
}

// deliver has the writer deliver baseline as an English file at tm, with
// facts and decision as its provenance.
func (r *rig) deliver(t *testing.T, baseline []byte, tm timing.Timing, facts rank.Facts, d timing.Decision) syncsched.Target {
	t.Helper()
	cues, err := timing.ParseCues(baseline)
	if err != nil {
		t.Fatal(err)
	}
	en := tag(t, "en")
	rec, err := r.writer.Deliver(t.Context(), mediawrite.Delivery{
		Inst: r.inst, Tag: en, Target: en, Variant: language.Standard, Cues: cues, Timing: tm,
		Provenance: statedb.Provenance{
			Provider: "opensubtitles", Release: "Show.S01E01", Facts: facts,
			Timing: timing.Outcome{Decision: d, Verdict: timing.Trusted},
		},
		Run: statedb.RunID{1}, Attempt: 1, Candidate: [32]byte{7},
	})
	if err != nil {
		t.Fatal(err)
	}
	return syncsched.Target{File: mediawrite.RefOf(rec), Expect: mediawrite.Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256}}
}

// read is the file f names, as it is on disk.
func (r *rig) read(t *testing.T, f syncsched.Target) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(r.video), f.File.Name()))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// current is f as Current reads it now, and its expectation.
func (r *rig) current(t *testing.T, f syncsched.Target) (mediawrite.Timed, syncsched.Target) {
	t.Helper()
	inv, err := r.writer.List(t.Context(), r.inst, mediawrite.ListOptions{UnmanagedExpect: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, found := range inv.Files {
		if found.File == f.File {
			timed, err := r.writer.Current(t.Context(), found.File, found.Expect)
			if err != nil {
				t.Fatal(err)
			}
			return timed, syncsched.Target{File: found.File, Expect: found.Expect}
		}
	}
	t.Fatalf("%s is not listed", f.File.Name())
	return mediawrite.Timed{}, syncsched.Target{}
}

// propose starts owner's proposal for f by method and waits for its end.
func (r *rig) propose(t *testing.T, f syncsched.Target, method timing.Method) status.Job {
	t.Helper()
	job, err := r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: owner, File: f.File, Expect: f.Expect, Method: method})
	if err != nil {
		t.Fatal(err)
	}
	return r.wait(t, job.ID)
}

// resync starts owner's batch over files by method and waits for its end.
func (r *rig) resync(t *testing.T, method timing.Method, files ...syncsched.Target) status.Job {
	t.Helper()
	job, err := r.a.Resync(t.Context(), syncsched.ResyncRequest{Owner: owner, Files: files, Method: method})
	if err != nil {
		t.Fatal(err)
	}
	return r.wait(t, job.ID)
}

// wait is job id once it is Done, its cause one of syncsched.Causes.
func (r *rig) wait(t *testing.T, id status.JobID) status.Job {
	t.Helper()
	deadline := time.After(2 * time.Minute)
	for {
		changed := r.status.Changed()
		j, ok, err := r.status.Get(t.Context(), id)
		if err != nil || !ok {
			t.Fatalf("job %s: %v, %v", id, ok, err)
		}
		if j.State == status.Done {
			if j.Cause != "" && !slices.Contains(syncsched.Causes, j.Cause) {
				t.Errorf("job %s ended with cause %q, outside syncsched.Causes", id, j.Cause)
			}
			return j
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("job %s never ended: %+v", id, j)
		}
	}
}

// ended reports whether j ended with outcome and cause.
func ended(j *status.Job, o status.Outcome, cause string) bool {
	return j.State == status.Done && j.Outcome == o && j.Cause == cause
}
