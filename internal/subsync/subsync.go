// Package subsync provides subtitle timing synchronization.
//
// It supports multiple sync strategies:
//   - Constant-offset alignment (alass algorithm port)
//   - Framerate correction (known ratios + golden-section search)
//   - Split-aware DP alignment (handles commercial breaks, different cuts)
//   - Audio-based sync (energy VAD + FFT cross-correlation, no reference needed)
//   - Encoding normalization (UTF-16, Windows-1252 → UTF-8)
//
// Basic usage:
//
//	result, err := subsync.SyncFile(ctx, "video.mkv", "subtitle.srt", nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	os.WriteFile("subtitle.synced.srt", result.Data, 0o644)
//
// With a reference subtitle:
//
//	result, err := subsync.SyncFile(ctx, "video.mkv", "subtitle.srt", &subsync.Options{
//	    Reference: "reference.en.srt",
//	})
package subsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/fsutil"
)

// Options configures a sync operation.
type Options struct {
	// PostProcess configures subtitle post-processing (HI removal, tag stripping, etc.).
	// Nil means no post-processing.
	PostProcess *PostProcessOptions

	// Framerate enables framerate correction detection.
	// nil = default (true), non-nil = explicit value.
	Framerate *bool

	// Splits enables split-aware alignment.
	// nil = default (true), non-nil = explicit value.
	Splits *bool

	// Reference is the path to a correctly-timed reference subtitle.
	Reference string

	// SplitPenalty controls split detection sensitivity (0 = default 1000ms).
	SplitPenalty float64

	// MinConfidence is the minimum confidence to accept a sync result (default: 0.5).
	MinConfidence float64

	// Audio enables audio-based sync (default: false).
	Audio bool
}

// Result holds the output of a sync operation.
type Result struct {
	// Method describes which strategy produced the result.
	// One of: MethodOffset, MethodFramerate, MethodSplit, MethodAudio, MethodCrosslang, MethodNone.
	Method SyncMethod

	// Data is the synchronized subtitle file content (UTF-8, SRT format).
	Data []byte

	// OffsetMs is the constant offset applied (milliseconds).
	OffsetMs int64

	// Confidence is the quality score of the sync (0.0 to 1.0).
	Confidence Confidence

	// Rate is the framerate ratio applied (1.0 = no change).
	Rate float64

	// Applied is true if the sync changed timing and confidence was sufficient.
	Applied bool
}

// SyncFile synchronizes a subtitle file against a video file.
// The video path is used for audio-based sync when no reference is available.
// Pass nil for opts to use defaults.
func SyncFile(ctx context.Context, videoPath, subtitlePath string, opts *Options) (Result, error) {
	if opts == nil {
		opts = &Options{}
	}

	// Read and normalize the subtitle.
	subData, err := readAndNormalize(ctx, subtitlePath)
	if err != nil {
		return Result{}, fmt.Errorf("read subtitle %s: %w", filepath.Base(subtitlePath), err)
	}

	incCues, err := ParseSRT(bytes.NewReader(subData))
	if err != nil {
		return Result{}, fmt.Errorf("parse subtitle %s: %w", filepath.Base(subtitlePath), err)
	}
	if len(incCues) == 0 {
		return Result{Data: subData, Method: MethodNone}, nil
	}

	// Load reference if provided.
	var refCues []Cue
	if opts.Reference != "" {
		refData, refErr := readAndNormalize(ctx, opts.Reference)
		if refErr != nil {
			return Result{}, fmt.Errorf("read reference %s: %w", filepath.Base(opts.Reference), refErr)
		}
		refCues, err = ParseSRT(bytes.NewReader(refData))
		if err != nil {
			return Result{}, fmt.Errorf("parse reference %s: %w", filepath.Base(opts.Reference), err)
		}
		slog.Debug("sync file: reference loaded",
			"reference", filepath.Base(opts.Reference),
			"cues", len(refCues))
	}

	return syncAndBuild(ctx, videoPath, opts, refCues, incCues, filepath.Base(subtitlePath))
}

// syncAndBuild runs the sync engine, applies post-processing, and builds the Result.
// If label is non-empty, a debug log is emitted with sync results.
func syncAndBuild(ctx context.Context, videoPath string, opts *Options, refCues, incCues []Cue, label string) (Result, error) {
	syncOpts := SyncOptions{
		VideoPath:       videoPath,
		SplitPenalty:    opts.SplitPenalty,
		EnableFramerate: opts.Framerate == nil || *opts.Framerate,
		EnableSplits:    opts.Splits == nil || *opts.Splits,
		EnableAudio:     opts.Audio && videoPath != "",
		MinConfidence:   Confidence(opts.MinConfidence),
	}

	sr := SyncWithOptions(ctx, refCues, incCues, &syncOpts)

	if label != "" {
		slog.Debug("sync complete",
			"subtitle", label,
			"method", sr.Method,
			"confidence", float64(sr.Confidence),
			"offset_ms", sr.Offset,
			"cues", len(incCues))
	}

	outCues := sr.Cues
	if opts.PostProcess != nil {
		outCues = PostProcess(outCues, *opts.PostProcess)
	}

	var buf bytes.Buffer
	if err := WriteSRT(&buf, outCues); err != nil {
		return Result{}, fmt.Errorf("write synced subtitle: %w", err)
	}

	outData := buf.Bytes()
	if opts.PostProcess != nil {
		outData = PostProcessBytes(outData, *opts.PostProcess)
	}

	return Result{
		Data:       outData,
		OffsetMs:   sr.Offset,
		Rate:       sr.Rate,
		Confidence: sr.Confidence,
		Method:     sr.Method,
		Applied:    sr.Applied() && sr.ShouldApply(),
	}, nil
}

// readAndNormalize reads a subtitle file, validates its size, and normalizes
// the encoding to UTF-8. Uses a two-layer size check: os.Stat for early
// rejection (avoids reading large files at all), then io.LimitReader as
// defense-in-depth against TOCTOU races where the file grows between stat
// and read.
func readAndNormalize(ctx context.Context, path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if err2 := ctx.Err(); err2 != nil {
		return nil, err2
	}

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if err2 := ctx.Err(); err2 != nil {
		return nil, err2
	}
	if info.IsDir() {
		return nil, errors.New("path is a directory, not a file")
	}
	if info.Size() > api.MaxSafeFileBytes {
		return nil, fmt.Errorf("%w: %d bytes (max %d)", fsutil.ErrFileTooLarge, info.Size(), api.MaxSafeFileBytes)
	}

	data, err := io.ReadAll(io.LimitReader(f, api.MaxSafeFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > api.MaxSafeFileBytes {
		return nil, fmt.Errorf("%w: read %d bytes (max %d)", fsutil.ErrFileTooLarge, len(data), api.MaxSafeFileBytes)
	}
	return NormalizeEncoding(data), nil
}

// OutputPath generates the output file path for a synced subtitle.
// For "movie.fr.srt" it returns "movie.fr.synced.srt".
func OutputPath(inputPath string) string {
	ext := filepath.Ext(inputPath)
	base := strings.TrimSuffix(inputPath, ext)
	return base + ".synced" + ext
}
