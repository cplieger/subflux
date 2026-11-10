//go:build ffmpeg

package syncsched_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/timing"
)

// TestPreviewTimestamps previews a generated two-minute video with the
// host's ffmpeg: the fragment starts at time 0 and runs from from to the
// video's end, and the RenderVTT track for the same from sits at source
// time minus from.
func TestPreviewTimestamps(t *testing.T) {
	r := newRig(t, func(s *rigSetup) { s.hostFFmpeg = true })
	gen := exec.CommandContext(t.Context(), "ffmpeg", "-nostdin", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=120:size=320x240:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=120",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-f", "matroska", r.video)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate the video: %v: %s", err, out)
	}
	cues, err := timing.ParseCues(srt([]subsync.Span{{StartMs: 30_000, EndMs: 32_000}, {StartMs: 95_000, EndMs: 97_500}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		want string
		from time.Duration
		left float64
	}{
		{"00:00:30.000 --> 00:00:32.000", 0, 120},
		{"00:00:05.000 --> 00:00:07.500", 90 * time.Second, 30},
	} {
		rc, _, ok, err := r.a.Preview(t.Context(), r.inst, tc.from)
		if err != nil || !ok {
			t.Fatalf("Preview from %v: %v, %v", tc.from, ok, err)
		}
		frag := filepath.Join(t.TempDir(), "preview.mp4")
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(frag, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if pts := probe(t, frag, "-select_streams", "v:0", "-show_entries", "packet=pts_time", "-read_intervals", "%+#1"); pts != 0 {
			t.Errorf("the preview from %v starts at %v s, want 0", tc.from, pts)
		}
		if d := probe(t, frag, "-show_entries", "format=duration"); d < tc.left-1 || d > tc.left+1 {
			t.Errorf("the preview from %v runs %v s, want about %v", tc.from, d, tc.left)
		}
		vtt, err := timing.RenderVTT(cues, timing.Identity{}, tc.from)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(vtt), tc.want) {
			t.Errorf("the track from %v:\n%s\nwant a cue %s", tc.from, vtt, tc.want)
		}
	}
}

// probe is the first number ffprobe prints for path under args.
func probe(t *testing.T, path string, args ...string) float64 {
	t.Helper()
	args = append(append([]string{"-v", "error"}, args...), "-of", "csv=p=0", path)
	out, err := exec.CommandContext(t.Context(), "ffprobe", args...).Output()
	if err != nil {
		t.Fatal(err)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), 64)
	if err != nil {
		t.Fatalf("ffprobe %v answered %q: %v", args, out, err)
	}
	return v
}
