package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"time"
)

// ExtractRawSubtitle runs ffmpeg to extract a subtitle stream by index,
// converting to SRT format. Returns raw bytes (no parsing).
func ExtractRawSubtitle(ctx context.Context, videoPath string, streamIndex int) ([]byte, error) {
	if !Available() {
		return nil, errors.New("ffmpeg not available")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", "file:"+videoPath,
		"-map", fmt.Sprintf("0:%d", streamIndex),
		"-f", CodecSRT,
		"-loglevel", "error",
		"pipe:1",
	)

	pipe, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		return nil, fmt.Errorf("stdout pipe: %w", pipeErr)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if startErr := cmd.Start(); startErr != nil {
		return nil, fmt.Errorf("ffmpeg start: %w", startErr)
	}

	limited := io.LimitReader(pipe, MaxExtractBytes)
	raw, readErr := io.ReadAll(limited)

	if len(raw) >= MaxExtractBytes {
		slog.Warn("subtitle extraction truncated at size limit",
			"stream", streamIndex, "path", videoPath, "max_bytes", MaxExtractBytes)
	}

	if waitErr := cmd.Wait(); waitErr != nil {
		if readErr != nil {
			slog.Debug("ffmpeg read error also occurred", "read_error", readErr)
		}
		return nil, fmt.Errorf("ffmpeg extract stream %d: %w: %s",
			streamIndex, waitErr, stderr.String())
	}
	if readErr != nil {
		return nil, fmt.Errorf("read ffmpeg output: %w", readErr)
	}

	return raw, nil
}
