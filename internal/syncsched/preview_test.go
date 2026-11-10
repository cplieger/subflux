package syncsched_test

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

func TestPreviewBounds(t *testing.T) {
	r := newRig(t)
	ffmpegtest.UseFor(t, ffmpegtest.Preview, "hang")
	for _, from := range []time.Duration{-time.Millisecond, 24*time.Hour + time.Millisecond} {
		if _, _, _, err := r.a.Preview(t.Context(), r.inst, from); !errors.Is(err, syncsched.ErrPreviewRange) {
			t.Errorf("a preview from %v: %v, want ErrPreviewRange", from, err)
		}
	}
	var open []io.ReadCloser
	for _, from := range []time.Duration{0, 24 * time.Hour} {
		rc, ctype, ok, err := r.a.Preview(t.Context(), r.inst, from)
		if err != nil || !ok || ctype != "video/mp4" {
			t.Fatalf("a preview from %v: %q, %v, %v; want a video/mp4 stream", from, ctype, ok, err)
		}
		open = append(open, rc)
	}
	if _, _, _, err := r.a.Preview(t.Context(), r.inst, 0); !errors.Is(err, syncsched.ErrPreviewCapacity) {
		t.Errorf("a third preview: %v, want ErrPreviewCapacity", err)
	}
	if err := open[0].Close(); err != nil {
		t.Fatal(err)
	}
	if err := open[0].Close(); err != nil {
		t.Fatal(err)
	}
	rc, _, _, err := r.a.Preview(t.Context(), r.inst, 0)
	if err != nil {
		t.Fatalf("a preview after one closed: %v", err)
	}
	if _, _, _, err := r.a.Preview(t.Context(), r.inst, 0); !errors.Is(err, syncsched.ErrPreviewCapacity) {
		t.Errorf("a second close freed a second place: %v, want ErrPreviewCapacity", err)
	}
	for _, c := range []io.ReadCloser{rc, open[1]} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPreviewStreams(t *testing.T) {
	r := newRig(t)
	ffmpegtest.UseFor(t, ffmpegtest.Preview, "echo-video")
	rc, _, ok, err := r.a.Preview(t.Context(), r.inst, 90*time.Second)
	if err != nil || !ok {
		t.Fatalf("Preview = %v, %v", ok, err)
	}
	b, err := io.ReadAll(rc)
	if err != nil || string(b) != "video" {
		t.Errorf("the stream read %q, %v; want the video ffmpeg was handed", b, err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if ok, err := previewOf(t, r, r.inst+1000); ok || err != nil {
		t.Errorf("an unknown instance: %v, %v; want false and no error", ok, err)
	}
	ffmpegtest.OnPath(t)
	if _, err := previewOf(t, r, r.inst); err == nil {
		t.Error("a preview with no ffmpeg: nil error")
	}
	ffmpegtest.OnPath(t, "ffmpeg")
	for range syncsched.MaxPreviews {
		rc, _, ok, err := r.a.Preview(t.Context(), r.inst, 0)
		if err != nil || !ok {
			t.Fatalf("a failed start kept its place: %v, %v", ok, err)
		}
		t.Cleanup(func() { _ = rc.Close() })
	}
}

// previewOf opens and closes one preview of inst.
func previewOf(t *testing.T, r *rig, inst statedb.InstanceID) (bool, error) {
	t.Helper()
	rc, _, ok, err := r.a.Preview(t.Context(), inst, 0)
	if rc != nil {
		_ = rc.Close()
	}
	return ok, err
}

func TestPreviewErrorsAreInCodes(t *testing.T) {
	outside := newRig(t, func(s *rigSetup) {
		s.roots = func(string) []string { return []string{filepath.Join(t.TempDir(), "elsewhere")} }
	})
	unavailable := newRig(t, func(s *rigSetup) { s.empty = true })
	for name, tc := range map[string]struct {
		r    *rig
		want error
	}{
		"root unavailable": {unavailable, mediawrite.ErrRootUnavailable},
		"outside root":     {outside, mediawrite.ErrOutsideRoot},
	} {
		ok, err := previewOf(t, tc.r, tc.r.inst)
		if !errors.Is(err, tc.want) || ok {
			t.Errorf("%s: %v, %v; want %v", name, ok, err, tc.want)
		}
		if !coded(err) {
			t.Errorf("%s: %v matches no entry of syncsched.Codes", name, err)
		}
	}
}

// coded reports whether err matches an entry of syncsched.Codes.
func coded(err error) bool {
	_, ok := codeOf(err)
	return ok
}

func codeOf(err error) (string, bool) {
	for _, c := range syncsched.Codes {
		if errors.Is(err, c.Err) {
			return c.Code, true
		}
	}
	return "", false
}

// TestPreviewAndApplyRenderBaseline has a managed file's record hold a
// wrong shift: the proposal is against the baseline, so the preview track
// before and after the apply is the same, and the file shifts once.
func TestPreviewAndApplyRenderBaseline(t *testing.T) {
	video := episode(50, 150)
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	f := r.deliver(t, srt(shifted(video, -1_500)), timing.Shift{Ms: 500}, rank.Facts{}, byReference)
	job := r.propose(t, f, timing.MethodReference)
	p, ok := r.a.Proposal(owner, job.ID)
	if !ok || p.Timing != (timing.Shift{Ms: 1_500}) {
		t.Fatalf("Proposal = %#v, %v; want Shift{1500} against the baseline", p.Timing, ok)
	}
	track := func() []byte {
		t.Helper()
		cur, _ := r.current(t, f)
		vtt, err := timing.RenderVTT(cur.Cues, p.Timing, 0)
		if err != nil {
			t.Fatal(err)
		}
		return vtt
	}
	before := track()
	if _, err := r.writer.Retime(t.Context(), p.File, p.Expect, p.Timing, &p.Outcome); err != nil {
		t.Fatal(err)
	}
	if stale := track(); !bytes.Equal(stale, before) {
		t.Errorf("the stale preview after the apply differs:\n%s\nwant\n%s", stale, before)
	}
	onDisk, err := timing.ParseCues(r.read(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if first := onDisk.Events()[0]; first.StartMs != video[0].StartMs {
		t.Errorf("the applied file's first cue starts at %d ms, want the video's %d", first.StartMs, video[0].StartMs)
	}
	if _, err := r.writer.Retime(t.Context(), p.File, p.Expect, p.Timing, &p.Outcome); !errors.Is(err, mediawrite.ErrExpectation) {
		t.Errorf("applying the proposal twice: %v, want ErrExpectation", err)
	}
}
