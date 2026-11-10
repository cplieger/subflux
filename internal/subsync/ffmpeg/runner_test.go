package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/timing"
)

func TestMain(m *testing.M) { ffmpegtest.Main(m) }

// fake is a Runner whose ffmpeg and ffprobe are this test binary, behaving
// as b.
func fake(t *testing.T, b string) Runner {
	t.Helper()
	ffmpegtest.Use(t, b)
	return Runner{FFmpeg: ffmpegtest.Path(), FFprobe: ffmpegtest.Path()}
}

func video(t *testing.T, content string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "video.mkv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// bounded is the test's own limit on a call that waits on a child.
func bounded(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestStderrFloodKeepsTail(t *testing.T) {
	r := fake(t, "stderr-flood")
	v := video(t, "")
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	_, err := r.ExtractSubtitle(bounded(t, time.Minute), v, 0, false)
	took := time.Since(start)
	runtime.ReadMemStats(&after)
	t.Logf("1 GiB of stderr drained in %v", took)
	if err == nil {
		t.Fatal("ExtractSubtitle succeeded on a child exiting 1")
	}
	if took > 30*time.Second {
		t.Errorf("call took %v, want under 30s", took)
	}
	want := make([]byte, maxStderrBytes)
	for i := range want {
		want[i] = ffmpegtest.FloodAt(ffmpegtest.FloodBytes - maxStderrBytes + int64(i))
	}
	msg := err.Error()
	if !strings.HasSuffix(msg, string(want)) {
		t.Errorf("error does not end with the stream's last %d bytes", maxStderrBytes)
	}
	if len(msg) > maxStderrBytes+100 {
		t.Errorf("error carries %d bytes, want at most the %d-byte tail and its label", len(msg), maxStderrBytes)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
		t.Errorf("the call allocated %d bytes, want under 8 MiB", grew)
	}
}

func TestOutputCapClosesAtOnce(t *testing.T) {
	r := fake(t, "stdout-flood")
	start := time.Now()
	_, err := r.ExtractSubtitle(bounded(t, time.Minute), video(t, ""), 0, false)
	if !errors.Is(err, ErrOutputCap) {
		t.Fatalf("error = %v, want ErrOutputCap", err)
	}
	if took := time.Since(start); took > waitDelay+time.Second {
		t.Errorf("ErrOutputCap after %v, want within %v", took, waitDelay+time.Second)
	}
}

func TestMissingBinaryUnavailable(t *testing.T) {
	v := video(t, "")
	for name, r := range map[string]Runner{
		"absolute path": {FFmpeg: filepath.Join(t.TempDir(), "ffmpeg"), FFprobe: filepath.Join(t.TempDir(), "ffprobe")},
		"PATH lookup":   {FFmpeg: "subflux-no-such-ffmpeg", FFprobe: "subflux-no-such-ffprobe"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.Probe(t.Context(), v); !errors.Is(err, ErrUnavailable) {
				t.Errorf("Probe error = %v, want ErrUnavailable", err)
			}
			if _, err := r.ExtractSubtitle(t.Context(), v, 0, false); !errors.Is(err, ErrUnavailable) {
				t.Errorf("ExtractSubtitle error = %v, want ErrUnavailable", err)
			}
			if err := r.PCM(t.Context(), v, 1, func([]int16) error { return nil }); !errors.Is(err, ErrUnavailable) {
				t.Errorf("PCM error = %v, want ErrUnavailable", err)
			}
			if _, err := r.Preview(t.Context(), v, 0); !errors.Is(err, ErrUnavailable) {
				t.Errorf("Preview error = %v, want ErrUnavailable", err)
			}
		})
	}
}

func TestVideoReadThroughFd3(t *testing.T) {
	r := fake(t, "echo-video")
	root := t.TempDir()
	path := filepath.Join(root, "video.mkv")
	if err := os.WriteFile(path, []byte("the opened video"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside the root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	got, err := r.ExtractSubtitle(bounded(t, time.Minute), f, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the opened video" {
		t.Errorf("child read %q, want the opened file's bytes", got)
	}
}

func TestPCMFrameLimitEndsNormally(t *testing.T) {
	r := fake(t, "pcm-forever")
	frames := 0
	start := time.Now()
	err := r.PCM(bounded(t, time.Minute), video(t, ""), 1000, func(f []int16) error {
		if len(f) != 80 || f[0] != int16(frames*80) {
			t.Fatalf("frame %d = %d samples from %d, want 80 from %d", frames, len(f), f[0], frames*80)
		}
		frames++
		return nil
	})
	if err != nil {
		t.Fatalf("PCM error = %v, want nil at maxFrames", err)
	}
	if frames != 1000 {
		t.Errorf("got %d frames, want 1000", frames)
	}
	if took := time.Since(start); took > waitDelay+time.Second {
		t.Errorf("PCM returned after %v, want within %v", took, waitDelay+time.Second)
	}
}

func TestPCMStreamEnd(t *testing.T) {
	r := fake(t, "pcm:8050")
	var got []int16
	err := r.PCM(bounded(t, time.Minute), video(t, ""), 1000, func(f []int16) error {
		got = append(got, f...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 8000 {
		t.Fatalf("got %d samples, want 8000: 100 whole frames, the trailing 50 dropped", len(got))
	}
	for i, s := range got {
		if s != int16(i) {
			t.Fatalf("sample %d = %d, want %d", i, s, i)
		}
	}
}

func TestPCMCallbackErrorStops(t *testing.T) {
	r := fake(t, "pcm-forever")
	stop := errors.New("enough")
	n := 0
	err := r.PCM(bounded(t, time.Minute), video(t, ""), 1000, func([]int16) error {
		n++
		if n == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || n != 3 {
		t.Errorf("PCM = %v after %d frames, want the callback's error after 3", err, n)
	}
}

func TestPreviewEndsAt128MiB(t *testing.T) {
	r := fake(t, "stdout-flood")
	rc, err := r.Preview(bounded(t, 2*time.Minute), video(t, ""), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	buf := make([]byte, 1<<20)
	var n int64
	var last time.Duration
	for {
		start := time.Now()
		m, err := rc.Read(buf)
		last = time.Since(start)
		n += int64(m)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Errorf("read error = %v, want io.EOF at the cap", err)
			}
			break
		}
	}
	if n != maxPreviewBytes {
		t.Errorf("read %d bytes, want %d", n, maxPreviewBytes)
	}
	if last > waitDelay+time.Second {
		t.Errorf("the ending read took %v, want within %v", last, waitDelay+time.Second)
	}
	if rc.(*preview).c.cmd.ProcessState == nil {
		t.Error("the child was not reaped when the stream ended")
	}
}

func TestPreviewCloseUnblocksRead(t *testing.T) {
	r := fake(t, "hang")
	rc, err := r.Preview(bounded(t, time.Minute), video(t, ""), 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := rc.Read(make([]byte, 1))
		done <- err
	}()
	if err := rc.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("Read returned no error after Close")
		}
	case <-time.After(waitDelay + time.Second):
		t.Fatal("Read still blocked after Close")
	}
}

func TestPreviewChildFailure(t *testing.T) {
	r := fake(t, "fail")
	rc, err := r.Preview(bounded(t, time.Minute), video(t, ""), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if _, err := io.ReadAll(rc); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("read error = %v, want the child's failure with its stderr", err)
	}
}

func TestFailureCarriesStderr(t *testing.T) {
	r := fake(t, "fail")
	_, err := r.Probe(bounded(t, time.Minute), video(t, ""))
	if err == nil || !strings.HasSuffix(err.Error(), ": boom") {
		t.Errorf("Probe error = %v, want the exit error ending in the stderr", err)
	}
}

func TestCallerContextEnds(t *testing.T) {
	r := fake(t, "hang")
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := r.Probe(ctx, video(t, ""))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Probe error = %v, want context.Canceled", err)
	}
	if took := time.Since(start); took > waitDelay+time.Second {
		t.Errorf("Probe returned after %v, want within %v", took, waitDelay+time.Second)
	}
}

// TestCallDeadline drives the call deadline through start, the one place
// every call sets it, with a deadline short enough to wait for.
func TestCallDeadline(t *testing.T) {
	ffmpegtest.Use(t, "hang")
	r := Runner{FFmpeg: ffmpegtest.Path()}
	ctx := bounded(t, time.Minute)
	t.Run("a call fails", func(t *testing.T) {
		c, err := start(ctx, "extract", r.ffmpeg(), 200*time.Millisecond, video(t, ""))
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.readAll(maxExtractBytes)
		if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			t.Errorf("readAll error = %v, want the call's own deadline", err)
		}
	})
	t.Run("a preview ends", func(t *testing.T) {
		c, err := start(ctx, "preview", r.ffmpeg(), 200*time.Millisecond, video(t, ""))
		if err != nil {
			t.Fatal(err)
		}
		p := &preview{c: c, r: io.LimitedReader{R: c.stdout, N: maxPreviewBytes}}
		defer p.Close()
		if _, err := io.ReadAll(p); err != nil {
			t.Errorf("read error = %v, want a clean end at previewTimeout", err)
		}
	})
}

func TestProbe(t *testing.T) {
	r := fake(t, `probe:{"streams":[{"index":2,"codec_type":"subtitle","codec_name":"ass","tags":{"language":"fre"}}]}`)
	got, err := r.Probe(bounded(t, time.Minute), video(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	want := []Stream{{Index: 2, Type: "subtitle", Codec: "ass", Language: "fre"}}
	if !slices.Equal(got, want) {
		t.Errorf("Probe = %+v, want %+v", got, want)
	}
}

func TestCallArguments(t *testing.T) {
	r := fake(t, "args")
	ctx := bounded(t, time.Minute)
	v := video(t, "")
	args := func(t *testing.T, out []byte, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(string(out), "\n")
	}
	has := func(t *testing.T, got []string, want ...string) {
		t.Helper()
		for i := range got {
			if slices.Equal(got[i:min(i+len(want), len(got))], want) {
				return
			}
		}
		t.Errorf("arguments %q lack %q", got, want)
	}
	t.Run("extract as SRT", func(t *testing.T) {
		out, err := r.ExtractSubtitle(ctx, v, 4, false)
		got := args(t, out, err)
		has(t, got, "-i", videoArg)
		has(t, got, "-map", "0:4")
		has(t, got, "-f", "srt", "pipe:1")
	})
	t.Run("extract ASS as a copy", func(t *testing.T) {
		out, err := r.ExtractSubtitle(ctx, v, 2, true)
		got := args(t, out, err)
		has(t, got, "-map", "0:2", "-c:s", "copy", "-f", "ass", "pipe:1")
	})
	t.Run("preview from 90s", func(t *testing.T) {
		rc, err := r.Preview(ctx, v, 90*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		out, err := io.ReadAll(rc)
		got := args(t, out, err)
		has(t, got, "-ss", "90.000", "-i", videoArg, "-t", seconds(timing.PreviewSpan))
		has(t, got, "-vf", "scale=-2:360")
		has(t, got, "-f", "mp4", "pipe:1")
	})
}

func TestCallerBugsRefused(t *testing.T) {
	r := fake(t, "args")
	v := video(t, "")
	if _, err := r.Probe(t.Context(), nil); err == nil {
		t.Error("Probe(nil video) succeeded")
	}
	if _, err := r.ExtractSubtitle(t.Context(), v, -1, false); err == nil {
		t.Error("ExtractSubtitle(index -1) succeeded")
	}
	if err := r.PCM(t.Context(), v, 0, func([]int16) error { return nil }); err == nil {
		t.Error("PCM(maxFrames 0) succeeded")
	}
	if _, err := r.Preview(t.Context(), v, -time.Second); err == nil {
		t.Error("Preview(from -1s) succeeded")
	}
}

func TestTailKeepsLastBytes(t *testing.T) {
	t.Parallel()
	for _, sizes := range [][]int{
		{10},
		{maxStderrBytes},
		{maxStderrBytes + 1},
		{3 * maxStderrBytes},
		{maxStderrBytes - 1, 1},
		{maxStderrBytes - 1, 2},
		{40 << 10, 40 << 10, 40 << 10},
		{1, maxStderrBytes, 7},
	} {
		var tl tail
		var all []byte
		next := byte(0)
		for _, n := range sizes {
			p := make([]byte, n)
			for i := range p {
				p[i] = next
				next++
			}
			if w, err := tl.Write(p); w != n || err != nil {
				t.Fatalf("Write(%d bytes) = %d, %v", n, w, err)
			}
			all = append(all, p...)
		}
		want := all[max(0, len(all)-maxStderrBytes):]
		if got := tl.bytes(); !bytes.Equal(got, want) {
			t.Errorf("writes %v: kept %d bytes, want the last %d in order", sizes, len(got), len(want))
		}
	}
}
