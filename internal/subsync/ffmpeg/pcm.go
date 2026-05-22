package ffmpeg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"sync"

	"context"
	"time"
)

// PCMSampleRate is the sample rate used for PCM audio extraction.
const PCMSampleRate = 8000

// ExtractSegmentPCM extracts raw PCM audio from a time range.
// Returns int16 samples at PCMSampleRate. durationMs=0 means no limit.
func ExtractSegmentPCM(ctx context.Context, path string, startMs, durationMs int64) ([]int16, error) {
	return ExtractPCMWithFilter(ctx, path, startMs, durationMs, "aresample=async=1")
}

// ExtractPCMWithFilter extracts PCM audio with a custom ffmpeg audio filter.
func ExtractPCMWithFilter(ctx context.Context, path string, startMs, durationMs int64, af string) ([]int16, error) {
	maxSamples := 100_000_000 // ~3.5 hours at 8 kHz
	if durationMs > 0 {
		maxSamples = int(durationMs) * PCMSampleRate / 1000
	}

	pcm, err := extractFFmpegPCMFiltered(ctx, path, startMs, durationMs, maxSamples, af)
	if err != nil {
		return nil, err
	}
	if len(pcm) == 0 {
		return nil, errors.New("no audio samples")
	}
	return pcm, nil
}

// extractFFmpegPCMFiltered extracts PCM audio with a custom audio filter chain.
func extractFFmpegPCMFiltered(ctx context.Context, path string, startMs, durationMs int64, maxSamples int, af string) ([]int16, error) {
	if !Available() {
		return nil, errors.New("ffmpeg not available")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	var args []string
	if startMs > 0 {
		args = append(args, "-ss", strconv.FormatFloat(float64(startMs)/1000, 'f', 3, 64))
	}
	args = append(args, "-i", "file:"+path)
	if durationMs > 0 {
		args = append(args, "-t", strconv.FormatFloat(float64(durationMs)/1000, 'f', 3, 64))
	}
	args = append(args,
		"-vn",
		"-f", "s16le",
		"-ac", "1",
		"-acodec", "pcm_s16le",
		"-af", af,
		"-ar", strconv.Itoa(PCMSampleRate),
		"-loglevel", "error", "pipe:1",
	)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Cancel = func() error { return cmd.Process.Kill() }
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg start: %w", err)
	}

	samples := readPCMSamples(stdout, maxSamples)

	// Close stdout pipe before waiting to prevent deadlock.
	stdout.Close()

	if waitErr := cmd.Wait(); waitErr != nil {
		slog.Debug("ffmpeg wait", "error", waitErr)
	}

	slog.Debug("ffmpeg PCM extracted",
		"path", path, "samples", len(samples),
		"duration_s", float64(len(samples))/float64(PCMSampleRate))

	if len(samples) == 0 {
		return nil, errors.New("ffmpeg produced no audio")
	}
	return samples, nil
}

// pcmBufPool reuses read buffers for readPCMSamples to reduce allocations.
var pcmBufPool = sync.Pool{
	New: func() any { b := make([]byte, 32768); return &b },
}

// readPCMSamples reads int16 little-endian PCM samples from r up to maxSamples.
func readPCMSamples(r io.Reader, maxSamples int) []int16 {
	samples := make([]int16, 0, maxSamples)
	bufp, _ := pcmBufPool.Get().(*[]byte)
	buf := *bufp
	defer pcmBufPool.Put(bufp)
	for len(samples) < maxSamples {
		n, readErr := r.Read(buf)
		for i := 0; i+1 < n; i += 2 {
			raw := binary.LittleEndian.Uint16(buf[i : i+2])
			samples = append(samples, int16(raw)) //nolint:gosec // G115: PCM sample reinterpretation
			if len(samples) >= maxSamples {
				break
			}
		}
		if readErr != nil {
			break
		}
	}
	return samples
}
