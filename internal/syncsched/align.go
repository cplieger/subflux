package syncsched

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
	"github.com/cplieger/subflux/internal/subsync/vad"
	"github.com/cplieger/subflux/internal/timing"
)

// Aligned is Align's result: the parsed candidate, the timing to deliver it
// at and the pass's outcome.
type Aligned struct {
	Timing  timing.Timing
	Cues    timing.Cues
	Outcome timing.Outcome
}

// Align is search's call; it writes nothing and waits for the slot in the
// automatic class. A payload no parser reads is timing.ErrFormatUnsupported,
// and Skip is Identity and Trusted at once. Otherwise the reference runs up
// to subsync.MaxModel, then, under ReferenceThenAudio, audio when the
// reference neither verified nor refused, after a reference failure too
// unless ffmpeg is missing or the video cannot be opened. Another cut returns
// ErrDifferentCut alone, a failure with no verified result Identity and
// Unverified beside ErrSyncFailed, and an ended context its error.
func (a *Aligner) Align(ctx context.Context, inst statedb.InstanceID, payload []byte, tag language.Tag, d timing.Decision) (Aligned, error) {
	cues, err := timing.ParseCues(payload)
	if err != nil {
		return Aligned{}, err
	}
	switch d.Action {
	case timing.Skip:
		return Aligned{Cues: cues, Timing: timing.Identity{}, Outcome: timing.Outcome{Decision: d, Verdict: timing.Trusted}}, nil
	case timing.ActionReference, timing.ReferenceThenAudio, timing.RigidReference:
	default:
		return Aligned{}, fmt.Errorf("syncsched: Align with action %d", d.Action)
	}
	p := pass{
		inst: inst, cues: cues, tag: tag, origin: originSearch,
		reference: true, audio: d.Action == timing.ReferenceThenAudio, rigid: d.Action == timing.RigidReference,
	}
	r, err := a.runPass(ctx, classAutomatic, &p)
	if err != nil {
		return Aligned{}, err
	}
	out := Aligned{Cues: cues, Timing: r.timing, Outcome: r.outcome(d)}
	if r.failure != nil {
		return out, fmt.Errorf("%w: %w", ErrSyncFailed, r.failure)
	}
	return out, nil
}

// pass is one alignment's inputs: which stages it may settle, rigid, which
// caps the reference at Linear, and forced, which skips Settle's coverage
// grounds.
type pass struct {
	// started, when set, runs once the pass holds the slot.
	started   func()
	origin    origin
	tag       language.Tag
	cues      timing.Cues
	inst      statedb.InstanceID
	reference bool // settle the reference fit
	audio     bool // run audio when the reference did not verify
	rigid     bool
	forced    bool
	// below is what a fit under its gate settles to: search delivers
	// Identity, a job offers the fit itself.
	below subsync.Fallback
}

// result is a pass that refused nothing: the fit settled last (none when
// none ran), its settled timing and verdict, and a stage failure no
// verified fit outran.
type result struct {
	timing  timing.Timing
	failure error
	fit     subsync.Result
	verdict timing.Verdict
}

func (r *result) outcome(d timing.Decision) timing.Outcome {
	return timing.Outcome{Confidence: r.fit.Confidence, OffsetMs: timing.OffsetMs(r.timing), Decision: d, Method: r.fit.Method, Verdict: r.verdict}
}

// runPass runs p in the slot under class c. A panic becomes a failure with
// an Identity, Unverified result.
func (a *Aligner) runPass(ctx context.Context, c class, p *pass) (r result, err error) {
	release, err := a.slot.acquire(ctx, c)
	if err != nil {
		return result{}, err
	}
	defer release()
	if p.started != nil {
		p.started()
	}
	start := time.Now()
	defer func() {
		if v := recover(); v != nil {
			a.log.Error("syncsched: alignment panicked", "instance", p.inst, "panic", v, "stack", string(debug.Stack()))
			a.m.failures.Inc(string(stagePanic))
			r, err = unverified(fmt.Errorf("alignment panicked: %v", v)), nil
		}
		a.m.ran(p.origin, &r, err, time.Since(start))
		a.logPass(p, &r, err)
	}()
	return a.align(ctx, p)
}

func (a *Aligner) logPass(p *pass, r *result, err error) {
	switch {
	case errors.Is(err, ErrDifferentCut):
		a.log.Info("syncsched: the subtitle is for a different cut of the video", "instance", p.inst, "origin", p.origin, "method", methodLabels[r.fit.Method])
	case err == nil && r.failure != nil:
		a.log.Warn("syncsched: sync failed", "instance", p.inst, "origin", p.origin, "error", r.failure)
	}
}

func unverified(failure error) result {
	return result{timing: timing.Identity{}, verdict: timing.Unverified, failure: failure}
}

// align is one pass in the slot. Its error is the context's or
// ErrDifferentCut; every other failure is the result's.
func (a *Aligner) align(ctx context.Context, p *pass) (result, error) {
	video, err := a.writer.OpenVideo(ctx, p.inst)
	if err != nil {
		return a.failed(ctx, stageOpen, err)
	}
	defer video.Close()
	inc := subsync.DialogueSpans(p.cues)
	ref, err := a.reference(ctx, p, video)
	if ctx.Err() != nil {
		return result{}, ctx.Err()
	}
	r := unverified(err)
	audio := p.audio && !errors.Is(err, ffmpeg.ErrUnavailable)
	fit, guarded, err := fitReference(ctx, p, ref, inc)
	if err != nil {
		return result{}, err
	}
	if fit != nil && p.reference {
		if r, err = settle(fit, p, nil); err != nil || r.verdict == timing.Verified {
			return r, err
		}
	}
	if !audio {
		return r, nil
	}
	llr, err := a.speech(ctx, video)
	switch {
	case ctx.Err() != nil:
		return result{}, ctx.Err()
	case err != nil:
		a.m.failures.Inc(string(stagePCM))
		r.failure = errors.Join(r.failure, err)
		return r, nil
	case len(llr) == 0:
		return r, nil
	}
	heard, err := subsync.AlignAudio(ctx, subsync.Speech{LLR: llr}, guarded, inc, int64(len(llr))*frameMs)
	switch {
	case err != nil:
		return result{}, err
	case heard.Method == timing.MethodNone:
		return r, nil
	}
	return settle(&heard, p, r.failure)
}

// fitReference fits inc to ref, nil when there is no ref. guarded is ref
// when it passed the reference guard, so an audio fit is measured against it.
func fitReference(ctx context.Context, p *pass, ref, inc []subsync.Span) (_ *subsync.Result, guarded []subsync.Span, _ error) {
	if ref == nil {
		return nil, nil, nil
	}
	upTo := subsync.MaxModel
	if p.rigid {
		upTo = min(upTo, subsync.ModelLinear)
	}
	fit, err := subsync.AlignReference(ctx, ref, inc, upTo)
	if err != nil {
		return nil, nil, err
	}
	if fit.Measured {
		guarded = ref
	}
	return &fit, guarded, nil
}

// frameMs is one LLR's span of audio: a vad.FrameSamples frame at the
// runner's 8 kHz.
const frameMs = vad.FrameSamples / 8

// settle is fit's verdict under p. A failure stays the result's unless the
// fit verified.
func settle(fit *subsync.Result, p *pass, failure error) (result, error) {
	tm, v, err := subsync.Settle(fit, p.forced, p.below)
	if err != nil {
		return result{fit: *fit}, err
	}
	r := result{timing: tm, verdict: v, fit: *fit}
	if v != timing.Verified {
		r.failure = failure
	}
	return r, nil
}

// failed is a pass that stopped at s before any fit ran.
func (a *Aligner) failed(ctx context.Context, s stage, err error) (result, error) {
	if ctx.Err() != nil {
		return result{}, ctx.Err()
	}
	a.m.failures.Inc(string(s))
	return unverified(err), nil
}

// reference is the dialogue of the video's reference track, nil when it
// has none. A probe or extraction failure is counted and returned.
func (a *Aligner) reference(ctx context.Context, p *pass, video *os.File) ([]subsync.Span, error) {
	probe, err := a.prober.Probe(ctx, p.inst, video)
	if err != nil {
		a.countFailure(ctx, stageProbe)
		return nil, err
	}
	track, ok := chooseReference(probe.Tracks, p.tag, a.policy())
	if !ok {
		return nil, nil
	}
	spans, err := a.referenceSpans(ctx, p.inst, probe.Stamp, &track, video)
	if err != nil {
		a.countFailure(ctx, stageExtract)
		return nil, err
	}
	return spans, nil
}

func (a *Aligner) countFailure(ctx context.Context, s stage) {
	if ctx.Err() == nil {
		a.m.failures.Inc(string(s))
	}
}

// speech is one vad.Detector LLR per frame of video's first audio stream,
// at most subsync.MaxAudioFrames.
func (a *Aligner) speech(ctx context.Context, video *os.File) ([]float64, error) {
	d := vad.NewDetector()
	var llr []float64
	err := a.run.PCM(ctx, video, subsync.MaxAudioFrames, func(frame []int16) error {
		llr = append(llr, d.LLR(frame))
		return nil
	})
	return llr, err
}
