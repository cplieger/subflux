// Package ffmpeg is subflux's one ffmpeg and ffprobe runner. Every call has a
// deadline and a stdout cap, keeps only a bounded tail of stderr, kills its
// child when the call ends early, and hands the child the video as an open
// file, never as a path.
package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/timing"
)

var (
	// ErrUnavailable means the ffmpeg or ffprobe binary is not there.
	ErrUnavailable = errors.New("ffmpeg: not available")
	// ErrOutputCap means the child wrote past its call's cap and was stopped there.
	ErrOutputCap = errors.New("ffmpeg: output over its cap")
)

// Each call's deadline and stdout cap, and the PCM sample rate.
const (
	probeTimeout   = 2 * time.Minute
	extractTimeout = 10 * time.Minute
	pcmTimeout     = 15 * time.Minute
	previewTimeout = 15 * time.Minute
	// waitDelay bounds how long a call waits for a killed child's pipes.
	waitDelay     = 5 * time.Second
	maxProbeBytes = 10 << 20
	// maxExtractBytes is timing.ParseCues' document limit: anything larger
	// would be refused after the read anyway.
	maxExtractBytes = 16 << 20
	maxPreviewBytes = 128 << 20
	sampleRate      = 8000
)

const (
	frameSamples = sampleRate / 100 // one 10 ms frame
	maxPCMFrames = 500_000
)

// videoArg names the video for the child. It is ExtraFiles[0], so fd 3, and
// opening the magic link reopens the file the caller opened: replacing the
// video's path after the open changes nothing the child reads.
const videoArg = "file:/proc/self/fd/3"

var (
	errHalted   = errors.New("ffmpeg: stopped by the runner")
	errTimedOut = fmt.Errorf("ffmpeg: call deadline passed: %w", context.DeadlineExceeded)
)

// Runner runs ffmpeg and ffprobe. Its zero value runs them from PATH; FFmpeg
// and FFprobe name other binaries.
type Runner struct{ FFmpeg, FFprobe string }

func (r Runner) ffmpeg() string {
	if r.FFmpeg != "" {
		return r.FFmpeg
	}
	return "ffmpeg"
}

func (r Runner) ffprobe() string {
	if r.FFprobe != "" {
		return r.FFprobe
	}
	return "ffprobe"
}

// ExtractSubtitle returns stream index of video as an ASS document when
// keepASS is set (the stream is copied, so it must be ASS or SSA), else
// converted to SRT; over maxExtractBytes is ErrOutputCap.
func (r Runner) ExtractSubtitle(ctx context.Context, video *os.File, index int, keepASS bool) ([]byte, error) {
	if index < 0 {
		return nil, fmt.Errorf("ffmpeg extract: stream index %d below 0", index)
	}
	format := []string{"-f", subrip}
	if keepASS {
		format = []string{"-c:s", "copy", "-f", ass}
	}
	args := append([]string{"-nostdin", "-v", "error", "-i", videoArg, "-map", "0:" + strconv.Itoa(index)}, format...)
	c, err := start(ctx, "extract", r.ffmpeg(), extractTimeout, video, append(args, "pipe:1")...)
	if err != nil {
		return nil, err
	}
	return c.readAll(maxExtractBytes)
}

// PCM decodes video's first audio stream to 8 kHz mono and calls frame with
// each 80-sample (10 ms) frame in order; the slice is reused, so frame must
// not keep it. It returns frame's first error, or nil once the stream ends or
// maxFrames frames (at most 500,000) were passed, the child then killed. A
// trailing part frame is dropped.
func (r Runner) PCM(ctx context.Context, video *os.File, maxFrames int, frame func([]int16) error) error {
	if maxFrames < 1 {
		return fmt.Errorf("ffmpeg pcm: maxFrames %d below 1", maxFrames)
	}
	c, err := start(ctx, "pcm", r.ffmpeg(), pcmTimeout, video,
		"-nostdin", "-v", "error", "-i", videoArg, "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-af", "aresample=async=1", "-ac", "1", "-ar", strconv.Itoa(sampleRate),
		"-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
	if err != nil {
		return err
	}
	in := bufio.NewReaderSize(c.stdout, 32<<10)
	raw := make([]byte, 2*frameSamples)
	samples := make([]int16, frameSamples)
	for range min(maxFrames, maxPCMFrames) {
		if _, err := io.ReadFull(in, raw); err != nil {
			return c.endPCM(err)
		}
		for i := range samples {
			samples[i] = int16(binary.LittleEndian.Uint16(raw[2*i:])) //nolint:gosec // G115: s16le reinterpretation
		}
		if err := frame(samples); err != nil {
			c.halt()
			_ = c.wait()
			return err
		}
	}
	c.halt()
	return c.wait()
}

// Preview streams a fragmented MP4 of video from from: 360p H.264 ultrafast
// with AAC stereo, at most timing.PreviewSpan long, its timestamps starting
// at 0. The stream ends at maxPreviewBytes or previewTimeout; a read returns
// the child's failure, if any, in place of io.EOF. Close kills the child and
// is safe while a Read is blocked.
func (r Runner) Preview(ctx context.Context, video *os.File, from time.Duration) (io.ReadCloser, error) {
	if from < 0 {
		return nil, fmt.Errorf("ffmpeg preview: from %v below 0", from)
	}
	c, err := start(ctx, "preview", r.ffmpeg(), previewTimeout, video,
		"-nostdin", "-v", "error", "-ss", seconds(from), "-i", videoArg, "-t", seconds(timing.PreviewSpan),
		"-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30", "-vf", "scale=-2:360",
		"-pix_fmt", "yuv420p", "-profile:v", "baseline", "-level", "3.0",
		"-c:a", "aac", "-ac", "2", "-b:a", "64k",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")
	if err != nil {
		return nil, err
	}
	return &preview{c: c, r: io.LimitedReader{R: c.stdout, N: maxPreviewBytes}}, nil
}

func seconds(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }

type preview struct {
	c       *child
	err     error // sticky, owned by Read
	waitErr error
	r       io.LimitedReader
	reaped  sync.Once
}

func (p *preview) Read(b []byte) (int, error) {
	if p.err != nil {
		return 0, p.err
	}
	n, err := p.r.Read(b)
	if err == nil {
		return n, nil
	}
	if p.r.N == 0 {
		p.c.halt()
	}
	switch werr := p.reap(); {
	case werr != nil && !errors.Is(werr, errTimedOut):
		p.err = werr
	case errors.Is(err, io.EOF):
		p.err = io.EOF
	default:
		p.err = fmt.Errorf("ffmpeg preview: read: %w", err)
	}
	return n, p.err
}

func (p *preview) Close() error {
	p.c.halt()
	_ = p.reap()
	return nil
}

// reap waits for the child once, whichever of Read and Close gets there first.
func (p *preview) reap() error {
	p.reaped.Do(func() { p.waitErr = p.c.wait() })
	return p.waitErr
}

// child is one running call: its process, the stdout it reads and the tail of
// its stderr.
type child struct {
	parent context.Context
	ctx    context.Context
	stop   context.CancelCauseFunc
	expire context.CancelFunc
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr *tail
	what   string
}

func start(ctx context.Context, what, bin string, timeout time.Duration, video *os.File, args ...string) (*child, error) {
	if video == nil {
		return nil, fmt.Errorf("ffmpeg %s: no video", what)
	}
	tctx, expire := context.WithTimeoutCause(ctx, timeout, errTimedOut)
	cctx, stop := context.WithCancelCause(tctx)
	c := &child{parent: ctx, ctx: cctx, stop: stop, expire: expire, stderr: new(tail), what: what}
	c.cmd = exec.CommandContext(cctx, bin, args...)
	c.cmd.WaitDelay = waitDelay
	c.cmd.ExtraFiles = []*os.File{video}
	c.cmd.Stderr = c.stderr
	out, err := c.cmd.StdoutPipe()
	if err == nil {
		err = c.cmd.Start()
	}
	if err != nil {
		c.release()
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, fmt.Errorf("ffmpeg %s: start: %w", what, err)
	}
	c.stdout = out
	return c, nil
}

// halt kills the child; wait then reports its end as clean.
func (c *child) halt() { c.stop(errHalted) }

func (c *child) release() {
	c.stop(nil)
	c.expire()
}

// wait reaps the child and says why it ended: nil when it exited 0 or the
// runner halted it, the caller's context error when that context ended first,
// else the exit or deadline error with the stderr tail.
func (c *child) wait() error {
	err := c.cmd.Wait()
	cause := context.Cause(c.ctx)
	c.release()
	switch {
	case err == nil, errors.Is(cause, errHalted):
		return nil
	case c.parent.Err() != nil:
		return fmt.Errorf("ffmpeg %s: %w", c.what, context.Cause(c.parent))
	case errors.Is(cause, errTimedOut):
		return c.fail(errTimedOut)
	}
	return c.fail(err)
}

func (c *child) fail(err error) error {
	if t := bytes.TrimSpace(c.stderr.bytes()); len(t) > 0 {
		return fmt.Errorf("ffmpeg %s: %w: %s", c.what, err, t)
	}
	return fmt.Errorf("ffmpeg %s: %w", c.what, err)
}

// readAll reads the child's whole stdout, refusing it past limit at once:
// the child is killed there rather than left writing until its deadline.
func (c *child) readAll(limit int) ([]byte, error) {
	out, err := io.ReadAll(io.LimitReader(c.stdout, int64(limit)+1))
	if len(out) > limit {
		c.halt()
		_ = c.wait()
		return nil, c.fail(ErrOutputCap)
	}
	if werr := c.wait(); werr != nil {
		return nil, werr
	}
	if err != nil {
		return nil, fmt.Errorf("ffmpeg %s: read: %w", c.what, err)
	}
	return out, nil
}

// endPCM reaps a child whose stream ended: a whole or part frame short at the
// end is the stream's clean end unless the child failed.
func (c *child) endPCM(readErr error) error {
	if err := c.wait(); err != nil {
		return err
	}
	if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil
	}
	return fmt.Errorf("ffmpeg pcm: read: %w", readErr)
}
