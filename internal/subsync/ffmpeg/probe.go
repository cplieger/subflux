package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"time"
)

// maxProbeOutputBytes is the maximum size for ffprobe JSON output (10 MB).
const maxProbeOutputBytes = 10 << 20

// Track holds metadata for a single stream from ffprobe output.
type Track struct {
	CodecName       string
	CodecType       string
	Language        string // extracted from tags
	Title           string // extracted from tags
	RFrameRate      string // raw r_frame_rate from ffprobe (e.g. "24000/1001")
	Index           int
	Forced          bool // from disposition
	HearingImpaired bool // from disposition
}

// probeOutput is the JSON structure returned by ffprobe -show_streams.
type probeOutput struct {
	Streams []probeStream `json:"streams"`
}

type probeStream struct {
	Tags        map[string]string `json:"tags"`
	Disposition map[string]int    `json:"disposition"`
	CodecName   string            `json:"codec_name"`
	CodecType   string            `json:"codec_type"`
	RFrameRate  string            `json:"r_frame_rate"`
	Index       int               `json:"index"`
}

// ProbeStreams runs ffprobe and returns parsed stream metadata.
// Filters by codec_type if filterType is non-empty (e.g. "subtitle", "audio").
func ProbeStreams(ctx context.Context, path, filterType string) ([]Track, error) {
	if !ProbeAvailable() {
		return nil, errors.New("ffprobe not available")
	}

	args := []string{
		"-v", "error",
		"-show_streams",
		"-print_format", "json",
	}
	if filterType != "" {
		args = append(args, "-select_streams", shortStreamType(filterType))
	}
	args = append(args, "file:"+path)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w: %s", path, err, stderr.String())
	}

	return ParseProbeOutput(stdout.Bytes())
}

// ProbeDuration returns the duration of a media file in milliseconds.
func ProbeDuration(ctx context.Context, path string) (int64, error) {
	if !ProbeAvailable() {
		return 0, errors.New("ffprobe not available")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe", //nolint:gosec // G204: args from validated config
		"-v", "error",
		"-show_entries", "format=duration",
		"-print_format", "json",
		"file:"+path,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("ffprobe duration %s: %w: %s", path, err, stderr.String())
	}

	var out struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return 0, err
	}

	if out.Format.Duration == "" {
		slog.Debug("ffprobe returned empty duration", "path", path)
		return 0, fmt.Errorf("ffprobe returned no duration for %s", path)
	}

	dur, err := strconv.ParseFloat(out.Format.Duration, 64)
	if err != nil {
		return 0, err
	}

	ms := int64(dur * float64(time.Second/time.Millisecond))
	slog.Debug("ffprobe duration", "path", path, "duration_ms", ms)

	return ms, nil
}

// ProbeVideoFPS returns the video stream's frame rate, or 0 if unavailable.
func ProbeVideoFPS(ctx context.Context, path string) float64 {
	streams, err := ProbeStreams(ctx, path, "video")
	if err != nil {
		slog.Debug("ffprobe video FPS failed", "path", path, "error", err)
		return 0
	}
	if len(streams) == 0 {
		slog.Debug("ffprobe video FPS: no video streams", "path", path)
		return 0
	}
	return parseFrameRate(streams[0].RFrameRate)
}

// ParseProbeOutput parses raw ffprobe JSON output into Track structs.
func ParseProbeOutput(data []byte) ([]Track, error) {
	if len(data) > maxProbeOutputBytes {
		return nil, fmt.Errorf("ffprobe output too large: %d bytes", len(data))
	}

	var out probeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("ffprobe parse: %w", err)
	}

	tracks := make([]Track, 0, len(out.Streams))
	for _, s := range out.Streams {
		t := Track{
			Index:      s.Index,
			CodecName:  s.CodecName,
			CodecType:  s.CodecType,
			RFrameRate: s.RFrameRate,
		}

		if lang, ok := s.Tags["language"]; ok {
			t.Language = lang
		} else if lang, ok := s.Tags["LANGUAGE"]; ok {
			t.Language = lang
		}

		if title, ok := s.Tags["title"]; ok {
			t.Title = title
		} else if title, ok := s.Tags["TITLE"]; ok {
			t.Title = title
		}

		if s.Disposition != nil {
			t.Forced = s.Disposition["forced"] == 1
			t.HearingImpaired = s.Disposition["hearing_impaired"] == 1
		}

		tracks = append(tracks, t)
	}

	slog.Debug("parsed ffprobe output", "count", len(tracks))
	return tracks, nil
}
