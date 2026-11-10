//go:build corpus

package corpus

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
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
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
)

// TestMain lets this test binary stand in for ffmpeg and ffprobe.
func TestMain(m *testing.M) { ffmpegtest.Main(m) }

// The research's fit (reference-sync-research.md section 6): a result fits
// when at least fitBar of the truth-bearing cues land within fitTolMs.
const (
	fitTolMs = 600
	fitBar   = 0.9
	// noTruth marks a candidate event its source cannot place.
	noTruth = int64(-1)
	// maxWrongShare is AC19's bar on delivered-and-wrong at e2 >= 0.6
	// among a fitting set's same-cut cases.
	maxWrongShare = 0.03
)

// requireEnv is the variable name, failing t when it is unset: the gate
// never passes without its data.
func requireEnv(t *testing.T, name, what string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("%s must name %s", name, what)
	}
	return v
}

// caseClass is what a fitting set's case must show.
type caseClass uint8

const (
	// classSameCut: the candidate is for the video's cut; its truth places it.
	classSameCut caseClass = iota + 1
	// classCut: the candidate or the video lacks material (a cut, an ad
	// break, a recap); it is refused or delivered at a mapping placing it.
	classCut
	// classWrong: the candidate is for other content; it is never Verified.
	classWrong
)

var classNames = map[caseClass]string{classSameCut: "same_cut", classCut: "cut", classWrong: "wrong_content"}

// corpusCase is one alignment the gate runs: a candidate subtitle against a
// reference track's document, the candidate's language, and its truth.
type corpusCase struct {
	id, kind string
	ref      []byte
	cand     []byte
	// truth is, per candidate event, the video-time start it belongs at,
	// noTruth where the source cannot say; nil for wrong content.
	truth []int64
	tag   language.Tag
	class caseClass
	// video is the rig's video the case aligns against.
	video int
}

// audioRuns are the gate's two runs of a set, audio_fallback off then on.
var audioRuns = []bool{false, true}

func audioName(on bool) string {
	if on {
		return "audio_fallback_on"
	}
	return "audio_fallback_off"
}

// rig is the production aligner over one media root: the real store,
// writer, prober and status store, each video observed as one episode.
type rig struct {
	a     *syncsched.Aligner
	insts []statedb.InstanceID
}

type fixedRoots []string

func (r fixedRoots) MediaRoots(context.Context) []string { return r }

// newRig observes videos, paths under root, as one show's episodes in
// order.
func newRig(t *testing.T, root string, videos []string) *rig {
	t.Helper()
	policy := enPolicy(t)
	store, err := statedb.Open(filepath.Join(t.TempDir(), "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	if err := store.Activate(ctx, statedb.Activation{Sources: []media.SourceID{"sonarr"}, Languages: policy}); err != nil {
		t.Fatal(err)
	}
	r := &rig{insts: observe(t, store, root, videos)}
	st, err := status.New(ctx, statustest.NewMemPersister())
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.DiscardHandler)
	pol := func() language.Policy { return policy }
	w := mediawrite.New(mediawrite.Deps{Store: store, Roots: fixedRoots{root}, Alerts: st, Policy: pol, Logger: quiet})
	r.a = syncsched.New(syncsched.Deps{Writer: w, Prober: embedded.New(store), Status: st, Policy: pol, Logger: quiet})
	var ran sync.WaitGroup
	ran.Go(func() { _ = w.Run(ctx) })
	t.Cleanup(ran.Wait)
	return r
}

// rigs is one rig per worker over the same videos: each Aligner runs one
// alignment at a time.
func rigs(t *testing.T, root string, videos []string) []*rig {
	t.Helper()
	out := make([]*rig, workers())
	for i := range out {
		out[i] = newRig(t, root, videos)
	}
	return out
}

func workers() int { return runtime.GOMAXPROCS(0) }

// parallel runs f(w, i) for every i in [0, n) on workers() workers, w the
// worker's number, and fails t with every error f returns.
func parallel(t *testing.T, n int, f func(w, i int) error) {
	t.Helper()
	next := make(chan int)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for w := range workers() {
		wg.Go(func() {
			for i := range next {
				errs[i] = f(w, i)
			}
		})
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

// enPolicy is a policy whose default is English: the gate's stand-in tracks
// are untagged, so the policy picks no reference by language.
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

// observe has one complete sonarr pass see videos as episodes 1 to n of one
// show and returns their instances.
func observe(t *testing.T, s *statedb.Store, root string, videos []string) []statedb.InstanceID {
	t.Helper()
	ctx := t.Context()
	pass, err := s.BeginPass(ctx, "sonarr", statedb.PassScope{})
	if err != nil {
		t.Fatal(err)
	}
	show := mediasource.Root{Native: mediasource.NativeRef{ID: "1", Kind: media.Episode}, Title: "Corpus", External: media.ExternalIDs{TVDB: 1}}
	items := make([]mediasource.Item, len(videos))
	for i, v := range videos {
		items[i] = mediasource.Item{
			Native: mediasource.NativeID("e" + strconv.Itoa(i+1)), Title: v, VideoPath: filepath.Join(root, v), SceneName: filepath.Base(v),
			Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: i + 1},
		}
	}
	if err := s.ObserveItems(ctx, "sonarr", pass, show, items); err != nil {
		t.Fatal(err)
	}
	insts := make([]statedb.InstanceID, len(items))
	for i, item := range items {
		key, ok := media.Choose(media.Episode, show.External, item.Numbering)
		if !ok {
			t.Fatalf("no key for episode %d", i+1)
		}
		row, found, err := s.ItemRow(ctx, key)
		if err != nil || !found || row.Inst == 0 {
			t.Fatalf("ItemRow(%s) = %v, %v, %v", item.Title, row.Inst, found, err)
		}
		insts[i] = row.Inst
	}
	return insts
}

// standIn is the stand-in ffmpeg's library: n placeholder videos, each
// holding the path of its own directory, whose files the stand-in answers
// (ffmpegtest's from-video): the reference document every video's one
// untagged SRT track extracts to, and speech over its dialogue.
type standIn struct {
	root   string
	videos []string
	dirs   []string
	speech sync.Map // reference SHA-256 to its speech file, a *speechFile
}

type speechFile struct {
	once sync.Once
	path string
	err  error
}

func newStandIn(t *testing.T, n int) *standIn {
	t.Helper()
	ffmpegtest.OnPath(t, "ffmpeg", "ffprobe")
	ffmpegtest.UseFor(t, ffmpegtest.Probe, `probe:{"streams":[`+
		`{"index":0,"codec_type":"video","codec_name":"h264","r_frame_rate":"24000/1001"},`+
		`{"index":1,"codec_type":"audio","codec_name":"aac"},`+
		`{"index":2,"codec_type":"subtitle","codec_name":"subrip"}]}`)
	ffmpegtest.UseFor(t, ffmpegtest.Extract, "from-video:reference")
	ffmpegtest.UseFor(t, ffmpegtest.PCM, "from-video:speech.pcm")
	s := &standIn{root: t.TempDir(), videos: make([]string, n), dirs: make([]string, n)}
	if err := os.Mkdir(filepath.Join(s.root, "v"), 0o755); err != nil {
		t.Fatal(err)
	}
	media := t.TempDir()
	for i := range n {
		s.videos[i] = filepath.Join("v", strconv.Itoa(i)+".mkv")
		s.dirs[i] = filepath.Join(media, strconv.Itoa(i))
		if err := os.Mkdir(s.dirs[i], 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.root, s.videos[i]), []byte(s.dirs[i]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// stage makes video i answer c's reference and, with audio, speech over its
// dialogue for 30 s past it; without, PCM fails.
func (s *standIn) stage(i int, c *corpusCase, audio bool) error {
	dir := s.dirs[i]
	if err := os.WriteFile(filepath.Join(dir, "reference"), c.ref, 0o600); err != nil {
		return err
	}
	pcm := filepath.Join(dir, "speech.pcm")
	if err := os.Remove(pcm); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !audio {
		return nil
	}
	key := digest(c.ref)
	v, _ := s.speech.LoadOrStore(key, &speechFile{path: filepath.Join(filepath.Dir(dir), "speech-"+key+".pcm")})
	f := v.(*speechFile)
	f.once.Do(func() {
		spans, err := parseSpans(c.ref)
		if err != nil {
			f.err = err
			return
		}
		f.err = ffmpegtest.WriteSpeech(f.path, spans, min(spans[len(spans)-1].EndMs+30_000, subsync.MaxAudioFrames*10))
	})
	if f.err != nil {
		return f.err
	}
	return os.Symlink(f.path, pcm)
}

// pcmCalls is how many PCM calls video i has answered.
func (s *standIn) pcmCalls(i int) (int, error) {
	return ffmpegtest.FromVideoCalls(s.dirs[i], ffmpegtest.PCM)
}

// outcome is one production Align of a case.
type outcome struct {
	aligned syncsched.Aligned
	// refusal is "" for a delivery and otherwise names why nothing was:
	// different_cut, or the error's text for a case that could not run.
	refusal string
	// failure is ErrSyncFailed's cause: an Identity, Unverified delivery.
	failure string
	pcm     int // the PCM calls the run made; 0 under the host's ffmpeg
}

// delivered reports whether search would deliver the candidate: every
// answer but ErrDifferentCut, ErrSyncFailed's Identity included.
func (o *outcome) delivered() bool { return o.refusal == "" }

// align runs c through r's aligner at video i under d. Every error but
// ErrDifferentCut and ErrSyncFailed is the run's.
func (r *rig) align(ctx context.Context, i int, c *corpusCase, d timing.Decision) (outcome, error) {
	aligned, err := r.a.Align(ctx, r.insts[i], c.cand, c.tag, d)
	o := outcome{aligned: aligned}
	switch {
	case errors.Is(err, syncsched.ErrDifferentCut):
		o.refusal = "different_cut"
	case errors.Is(err, syncsched.ErrSyncFailed):
		o.failure = err.Error()
	case err != nil:
		return o, fmt.Errorf("%s: Align: %w", c.id, err)
	}
	return o, nil
}

// alignStandIn stages c at video i of s, aligns it and counts its PCM calls.
func (r *rig) alignStandIn(ctx context.Context, s *standIn, i int, c *corpusCase, d timing.Decision, audio bool) (outcome, error) {
	if err := s.stage(i, c, audio); err != nil {
		return outcome{}, err
	}
	before, err := s.pcmCalls(i)
	if err != nil {
		return outcome{}, err
	}
	o, err := r.align(ctx, i, c, d)
	if err != nil {
		return o, err
	}
	after, err := s.pcmCalls(i)
	o.pcm = after - before
	return o, err
}

// parseSpans is the sorted dialogue of a subtitle document.
func parseSpans(doc []byte) ([]subsync.Span, error) {
	c, err := timing.ParseCues(doc)
	if err != nil {
		return nil, err
	}
	s := subsync.DialogueSpans(c)
	slices.SortStableFunc(s, func(a, b subsync.Span) int { return int(a.StartMs - b.StartMs) })
	if len(s) == 0 {
		return nil, errors.New("a document with no dialogue")
	}
	return s, nil
}

// parseStarts is every event's start in a subtitle document, in order.
func parseStarts(doc []byte) ([]int64, error) {
	c, err := timing.ParseCues(doc)
	if err != nil {
		return nil, err
	}
	ev := c.Events()
	out := make([]int64, len(ev))
	for i, e := range ev {
		out[i] = e.StartMs
	}
	return out, nil
}

// modelFit is AlignReference's answer for a case at one ceiling, before and
// after Settle.
type modelFit struct {
	fit     subsync.Result
	settled string // verified, unverified or different_cut
	share   float64
	bearing int // the truth-bearing events share is over
}

// diagnosis is what the thresholds the gate fixes read of one case: its
// fits at ModelLinear and ModelPiecewise, and its truth share at Identity,
// whose shape both fits carry as IdentityShape.
type diagnosis struct {
	lin, pw modelFit
	idShare float64
}

// diagnose fits c at ModelLinear and ModelPiecewise.
func diagnose(ctx context.Context, c *corpusCase) (diagnosis, error) {
	ref, err := parseSpans(c.ref)
	if err != nil {
		return diagnosis{}, fmt.Errorf("%s: reference: %w", c.id, err)
	}
	inc, err := timing.ParseCues(c.cand)
	if err != nil {
		return diagnosis{}, fmt.Errorf("%s: candidate: %w", c.id, err)
	}
	starts, _ := parseStarts(c.cand)
	var d diagnosis
	for _, m := range []struct {
		to    *modelFit
		model subsync.Model
	}{{&d.lin, subsync.ModelLinear}, {&d.pw, subsync.ModelPiecewise}} {
		fit, err := subsync.AlignReference(ctx, ref, subsync.DialogueSpans(inc), m.model)
		if err != nil {
			return diagnosis{}, fmt.Errorf("%s: AlignReference: %w", c.id, err)
		}
		mf := modelFit{fit: fit, settled: "different_cut"}
		switch _, v, err := subsync.Settle(&fit, false, subsync.AtIdentity); {
		case err != nil && !errors.Is(err, subsync.ErrDifferentCut):
			return diagnosis{}, fmt.Errorf("%s: Settle: %w", c.id, err)
		case err == nil && v == timing.Verified:
			mf.settled = "verified"
		case err == nil:
			mf.settled = "unverified"
		}
		mf.share, mf.bearing = fitShare(fit.Timing, starts, c.truth)
		*m.to = mf
	}
	d.idShare, _ = fitShare(timing.Identity{}, starts, c.truth)
	return d, nil
}

// diagnoseAll diagnoses every case.
func diagnoseAll(t *testing.T, cases []corpusCase) []diagnosis {
	t.Helper()
	out := make([]diagnosis, len(cases))
	parallel(t, len(cases), func(_, i int) (err error) {
		out[i], err = diagnose(t.Context(), &cases[i])
		return err
	})
	return out
}

// placedAt is where t places an event starting at ms, false when t drops
// it: timing.Piecewise's documented mapping, ms × Scale plus the offset of
// the segment holding ms.
func placedAt(t timing.Timing, ms int64) (int64, bool) {
	switch t := t.(type) {
	case timing.Identity:
		return ms, true
	case timing.Shift:
		return ms + t.Ms, true
	case timing.Linear:
		return int64(math.Round(float64(ms)*t.Scale)) + t.OffsetMs, true
	case timing.Piecewise:
		for _, s := range t.Segments {
			if ms >= s.FromMs && (s.ToMs == 0 || ms < s.ToMs) {
				return int64(math.Round(float64(ms)*t.Scale)) + s.OffsetMs, !s.Drop
			}
		}
	}
	return 0, false
}

// fitShare is the share of truth-bearing events t places within fitTolMs of
// their truth, a dropped one counting as misplaced, and how many bear one;
// 0, 0 with no truth or no timing.
func fitShare(t timing.Timing, starts, truth []int64) (share float64, bearing int) {
	if t == nil || truth == nil {
		return 0, 0
	}
	placed := 0
	for i, want := range truth {
		if want == noTruth {
			continue
		}
		bearing++
		if at, ok := placedAt(t, starts[i]); ok && abs(at-want) <= fitTolMs {
			placed++
		}
	}
	if bearing == 0 {
		return 0, 0
	}
	return float64(placed) / float64(bearing), bearing
}

func abs(v int64) int64 { return max(v, -v) }

// caseRun is one case's record in a run.
type caseRun struct {
	c        *corpusCase
	action   timing.Action
	out      outcome
	share    float64 // the delivered timing's fit; 0 with no truth or no delivery
	bearing  int
	diag     diagnosis
	extra    string // the set's own column
	expected string
}

// verifiedWrong reports a delivery Verified that does not fit its truth.
func (r *caseRun) verifiedWrong() bool {
	return r.out.delivered() && r.out.aligned.Outcome.Verdict == timing.Verified && r.bearing > 0 && r.share < fitBar
}

// scored fills r's delivered share.
func (r *caseRun) scored() error {
	if !r.out.delivered() {
		return nil
	}
	starts, err := parseStarts(r.c.cand)
	r.share, r.bearing = fitShare(r.out.aligned.Timing, starts, r.c.truth)
	return err
}

// runFitting aligns every case of a fitting set, diagnosed in diag, as an
// unflagged pair through rs, one rig per worker; s is nil under the host's
// ffmpeg.
func runFitting(t *testing.T, rs []*rig, s *standIn, cases []corpusCase, diag []diagnosis, audio bool) []caseRun {
	t.Helper()
	unflagged, _ := decisions(audio)
	runs := make([]caseRun, len(cases))
	parallel(t, len(cases), func(w, i int) (err error) {
		c := &cases[i]
		run := caseRun{c: c, action: unflagged.Action, expected: classNames[c.class], diag: diag[i]}
		if s != nil {
			run.out, err = rs[w].alignStandIn(t.Context(), s, c.video, c, unflagged, audio)
		} else {
			run.out, err = rs[w].align(t.Context(), c.video, c, unflagged)
		}
		if err == nil {
			err = run.scored()
		}
		runs[i] = run
		return err
	})
	return runs
}

// checkFitting asserts AC19's bars common to every fitting set on one run:
// no wrong-content result Verified, at most maxWrongShare of the same-cut
// cases with truth Verified and not fitting, and no cut case delivered at a
// mapping that misplaces it while a usable reference could judge it; with
// none usable, nothing can see the cut (G4-23).
func checkFitting(t *testing.T, runs []caseRun) {
	t.Helper()
	sameCut, wrong := 0, 0
	for i := range runs {
		r := &runs[i]
		o := r.out.aligned.Outcome
		switch r.c.class {
		case classWrong:
			if r.out.delivered() && o.Verdict == timing.Verified {
				t.Errorf("%s (%s): wrong content Verified at %.3f", r.c.id, r.c.kind, o.Confidence)
			}
		case classSameCut:
			if r.c.truth == nil {
				continue
			}
			sameCut++
			if r.verifiedWrong() {
				wrong++
			}
		case classCut:
			if r.out.delivered() && r.diag.pw.fit.Measured && r.bearing > 0 && r.share < fitBar {
				t.Errorf("%s (%s): a cut delivered %s at %#v, placing %.2f of its truth", r.c.id, r.c.kind, text(o.Verdict), r.out.aligned.Timing, r.share)
			}
		}
	}
	if sameCut > 0 && float64(wrong) > maxWrongShare*float64(sameCut) {
		t.Errorf("%d of %d same-cut cases Verified and not fitting, over %.0f%%", wrong, sameCut, 100*maxWrongShare)
	}
	t.Logf("%d same-cut cases, %d Verified and not fitting", sameCut, wrong)
}

// tsvHeader is every run's columns; extra is the set's own.
const tsvHeader = "case\tkind\texpected\taction\trefusal\tverdict\tmethod\tconfidence\toffset_ms\tfailure\tpcm_calls\tfit\tbearing\tmeasured" +
	"\tlin_settled\tlin_conf\tlin_fit\tlin_edge_speech_ms\tlin_inserted_ms\tlin_edge_fit" +
	"\tpw_settled\tpw_conf\tpw_fit\tpw_edge_speech_ms\tpw_inserted_ms\tpw_edge_fit" +
	"\tid_fit\tid_edge_speech_ms\tid_edge_fit\textra"

// writeRuns writes one TSV per run, <dir>/<set>-<audio run>-<UTC time>.tsv.
func writeRuns(t *testing.T, dir, set string, audio bool, runs []caseRun) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s-%s.tsv", set, audioName(audio), time.Now().UTC().Format("20060102T150405Z")))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, tsvHeader)
	for i := range runs {
		fmt.Fprintln(w, strings.Join(runs[i].cols(), "\t"))
	}
	if err := errors.Join(w.Flush(), f.Close()); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}

func (r *caseRun) cols() []string {
	o := r.out.aligned.Outcome
	verdict, method := "none", "none"
	if r.out.delivered() {
		verdict, method = text(o.Verdict), text(o.Method)
	}
	cols := []string{
		r.c.id, r.c.kind, r.expected, text(r.action), or(r.out.refusal, "none"), verdict, method,
		ff(o.Confidence), strconv.FormatInt(o.OffsetMs, 10), or(strings.ReplaceAll(r.out.failure, "\t", " "), "none"),
		strconv.Itoa(r.out.pcm), ff(r.share), strconv.Itoa(r.bearing), strconv.FormatBool(r.diag.pw.fit.Measured),
	}
	for _, m := range []*modelFit{&r.diag.lin, &r.diag.pw} {
		s := m.fit.Shape
		cols = append(cols, m.settled, ff(m.fit.Confidence), ff(m.share),
			strconv.FormatInt(s.EdgeSpeechMs, 10), strconv.FormatInt(s.InsertedMs, 10), ff(s.EdgeFit))
	}
	id := r.diag.pw.fit.IdentityShape
	cols = append(cols, ff(r.diag.idShare), strconv.FormatInt(id.EdgeSpeechMs, 10), ff(id.EdgeFit))
	return append(cols, or(r.extra, "none"))
}

// text is v's wire name.
func text(v interface{ MarshalText() ([]byte, error) }) string {
	b, err := v.MarshalText()
	if err != nil {
		return "none"
	}
	return string(b)
}

func ff(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
