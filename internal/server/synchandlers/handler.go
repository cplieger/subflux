// Package synchandlers provides HTTP handlers for subtitle sync operations
// (audio-based sync and manual offset adjustment).
package synchandlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/fsutil"
	"github.com/cplieger/subflux/internal/httputil"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/httphelpers"
)

// SyncStore documents the api.Store methods used by sync handlers.
type SyncStore interface {
	GetSyncOffset(ctx context.Context, path string) (int64, error)
	SetSyncOffset(ctx context.Context, path string, offsetMs int64) error
}

// Compile-time assertion: api.Store satisfies SyncStore.
var _ SyncStore = api.Store(nil)

// Deps holds all dependencies for the sync handler family.
type Deps struct {
	Store        SyncStore
	SubtitleProc api.SubtitleProcessor
	Activity     *activity.Log
	ValidatePath func(w http.ResponseWriter, r *http.Request, p, label string) bool
}

// Handler holds all dependencies for the sync handler family.
type Handler struct {
	store        SyncStore
	subtitleProc api.SubtitleProcessor
	activity     *activity.Log
	validatePath func(w http.ResponseWriter, r *http.Request, p, label string) bool
}

// New creates a Handler with the given dependencies.
func New(d Deps) *Handler {
	return &Handler{
		store:        d.Store,
		subtitleProc: d.SubtitleProc,
		activity:     d.Activity,
		validatePath: d.ValidatePath,
	}
}

// MaxSyncSubSize caps subtitle file reads for sync operations.
const MaxSyncSubSize = httputil.MaxDownloadBytes

// maxBodySize references the canonical constant from httphelpers.
const maxBodySize = httphelpers.MaxDefaultBodySize

// --- Request/Response types ---

type syncAudioRequest struct {
	SubtitlePath string `json:"subtitle_path"`
	VideoPath    string `json:"video_path"`
	DryRun       bool   `json:"dry_run,omitempty"`
}

type syncAudioResponse struct {
	Method     string  `json:"method"`
	OffsetMs   int64   `json:"offset_ms"`
	Confidence float64 `json:"confidence"`
	Applied    bool    `json:"applied"`
}

type syncOffsetRequest struct {
	SubtitlePath string `json:"subtitle_path"`
	OffsetMs     int64  `json:"offset_ms"`
}

// --- Handlers ---

// HandleSyncAudio handles POST /api/sync/audio.
func (h *Handler) HandleSyncAudio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !httphelpers.RequirePOST(w, r) {
		return
	}

	var req syncAudioRequest
	if !httphelpers.DecodeJSONBody(w, r, &req, maxBodySize) {
		return
	}
	if req.SubtitlePath == "" || req.VideoPath == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "subtitle_path and video_path required")
		return
	}

	if !h.validatePath(w, r, req.VideoPath, "video path") {
		return
	}
	if !h.validatePath(w, r, req.SubtitlePath, "subtitle path") {
		return
	}

	data, err := fsutil.ReadBounded(ctx, req.SubtitlePath, MaxSyncSubSize)
	if err != nil {
		slog.Warn("sync audio: read subtitle failed",
			"path", req.SubtitlePath, "error", err)
		api.BadRequestC(w, r, api.CodeSyncUnsupportedFormat, "failed to read subtitle")
		return
	}

	data = h.subtitleProc.NormalizeEncoding(data)

	actID := h.activity.Start("Audio Sync",
		filepath.Base(req.SubtitlePath), activity.SourceManual)
	defer h.activity.End(actID)

	slog.Info("audio sync requested",
		"subtitle", filepath.Base(req.SubtitlePath),
		"video", filepath.Base(req.VideoPath))

	result := h.subtitleProc.SyncFromAudio(ctx, data, req.VideoPath, req.SubtitlePath)

	resp := syncAudioResponse{
		OffsetMs:   result.Offset,
		Confidence: result.Confidence,
		Method:     result.Method,
		Applied:    result.Applied,
	}

	if !resp.Applied || req.DryRun {
		slog.Debug("audio sync not applied",
			"applied", resp.Applied,
			"dry_run", req.DryRun,
			"offset_ms", resp.OffsetMs,
			"confidence", resp.Confidence,
			"method", resp.Method,
			"path", filepath.Base(req.SubtitlePath))
	}

	if resp.Applied && result.Cues != nil && !req.DryRun {
		cumOffset, err := h.applySyncResult(ctx, req.SubtitlePath, result.Cues, result.Offset, result.Confidence)
		if err != nil {
			api.InternalErrorC(w, r, err, api.CodeInternalError, "path", req.SubtitlePath)
			return
		}
		resp.OffsetMs = cumOffset
	}

	slog.Info("audio sync completed",
		"subtitle", filepath.Base(req.SubtitlePath),
		"offset_ms", resp.OffsetMs,
		"confidence", resp.Confidence,
		"method", resp.Method,
		"applied", resp.Applied,
		"dry_run", req.DryRun)

	api.WriteJSON(w, resp)
}

// HandleSyncOffset handles POST /api/sync/offset.
func (h *Handler) HandleSyncOffset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !httphelpers.RequirePOST(w, r) {
		return
	}

	var req syncOffsetRequest
	if !httphelpers.DecodeJSONBody(w, r, &req, maxBodySize) {
		return
	}
	if req.SubtitlePath == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "subtitle_path required")
		return
	}

	if !h.validatePath(w, r, req.SubtitlePath, "subtitle path") {
		return
	}

	slog.Info("manual offset requested",
		"subtitle", filepath.Base(req.SubtitlePath),
		"offset_ms", req.OffsetMs)

	currentOffset, err := h.store.GetSyncOffset(ctx, req.SubtitlePath)
	if err != nil {
		slog.Debug("sync offset: no previous offset, treating as zero",
			"path", req.SubtitlePath, "error", err)
		currentOffset = 0
	}
	delta := req.OffsetMs - currentOffset

	_, cues, parseErr := h.readAndParseSRT(req.SubtitlePath)
	if parseErr != nil || len(cues) == 0 {
		slog.Debug("sync offset: read/parse failed",
			"path", req.SubtitlePath, "error", parseErr, "cues", len(cues))
		api.BadRequestC(w, r, api.CodeSyncUnsupportedFormat, "failed to parse subtitle")
		return
	}

	if delta != 0 {
		offset := time.Duration(delta) * time.Millisecond
		cues = h.subtitleProc.ShiftCues(cues, offset)
	}

	srtData, err := h.subtitleProc.WriteSRT(cues)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "write SRT")
		return
	}

	if err := fsutil.AtomicWriteFile(ctx, req.SubtitlePath, srtData); err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "save", "path", req.SubtitlePath)
		return
	}

	slog.Info("manual offset applied",
		"offset_ms", req.OffsetMs,
		"delta_ms", delta,
		"path", filepath.Base(req.SubtitlePath))

	if err := h.store.SetSyncOffset(ctx, req.SubtitlePath, req.OffsetMs); err != nil {
		slog.Error("sync offset: file saved but DB offset update failed",
			"path", filepath.Base(req.SubtitlePath),
			"offset_ms", req.OffsetMs,
			"error", err)
		api.JSONErrorWithCode(w, r, http.StatusInternalServerError, api.CodeInternalError,
			"offset applied but tracking failed; re-open sync dialog to verify")
		return
	}

	api.WriteJSON(w, map[string]int64{"applied_offset_ms": req.OffsetMs})
}

// --- Helpers ---

// ShiftAndFilterCues applies a timing shift to all cues and removes cues
// that end before time zero. Cue start times are clamped to zero.
func ShiftAndFilterCues(cues []api.SubtitleCue, totalShift time.Duration) []api.SubtitleCue {
	if totalShift == 0 {
		return cues
	}
	var filtered []api.SubtitleCue
	for _, c := range cues {
		newEnd := c.End + totalShift
		if newEnd <= 0 {
			continue
		}
		newStart := max(c.Start+totalShift, 0)
		filtered = append(filtered, api.SubtitleCue{
			Start: newStart, End: newEnd, Text: c.Text,
		})
	}
	return filtered
}

// FindDialogueDenseStart finds the timestamp (ms) of the densest 60-second
// dialogue window in the subtitle.
func FindDialogueDenseStart(cues []api.SubtitleCue) int64 {
	if len(cues) == 0 {
		return 0
	}

	const windowMs int64 = 60_000
	const leadInMs int64 = 10_000

	var bestStart int64
	var bestChars int

	for i, anchor := range cues {
		anchorMs := anchor.Start.Milliseconds()
		windowEnd := anchorMs + windowMs
		chars := 0
		for j := i; j < len(cues) && cues[j].Start.Milliseconds() < windowEnd; j++ {
			chars += len(strings.TrimSpace(cues[j].Text))
		}
		if chars > bestChars {
			bestChars = chars
			bestStart = anchorMs
		}
	}

	start := max(bestStart-leadInMs, 0)
	return start
}

// SrtToWebVTT converts parsed SRT cues to WebVTT format string.
func SrtToWebVTT(cues []api.SubtitleCue) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for i, c := range cues {
		fmt.Fprintf(&b, "%d\n", i+1)
		fmt.Fprintf(&b, "%s --> %s\n",
			MsToVTT(c.Start.Milliseconds()),
			MsToVTT(c.End.Milliseconds()))
		b.WriteString(c.Text)
		b.WriteString("\n\n")
	}
	return b.String()
}

// MsToVTT formats milliseconds as VTT timestamp (HH:MM:SS.mmm).
func MsToVTT(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3_600_000
	ms %= 3_600_000
	m := ms / 60_000
	ms %= 60_000
	sec := ms / 1000
	frac := ms % 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, sec, frac)
}

// readAndParseSRT reads a subtitle file, normalizes encoding, and parses SRT.
func (h *Handler) readAndParseSRT(path string) ([]byte, []api.SubtitleCue, error) {
	data, err := fsutil.ReadBounded(context.Background(), path, MaxSyncSubSize)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read subtitle: %w", err)
	}
	data = h.subtitleProc.NormalizeEncoding(data)
	cues, err := h.subtitleProc.ParseSRT(data)
	if err != nil {
		return data, nil, fmt.Errorf("failed to parse subtitle: %w", err)
	}
	return data, cues, nil
}

// applySyncResult writes the synced subtitle to disk and records the
// cumulative offset in the DB.
func (h *Handler) applySyncResult(ctx context.Context, path string, cues []api.SubtitleCue, audioOffset int64, confidence float64) (int64, error) {
	srtData, err := h.subtitleProc.WriteSRT(cues)
	if err != nil {
		return 0, fmt.Errorf("write SRT: %w", err)
	}

	tmpPath, cleanup, err := fsutil.PrepareAtomicWrite(ctx, path, srtData)
	if err != nil {
		return 0, fmt.Errorf("save (prepare): %w", err)
	}

	prevOffset, offsetErr := h.store.GetSyncOffset(ctx, path)
	if offsetErr != nil {
		slog.Debug("no previous sync offset, starting from zero", "path", path)
	}
	cumulativeOffset := prevOffset + audioOffset

	if err := h.store.SetSyncOffset(ctx, path, cumulativeOffset); err != nil {
		cleanup()
		slog.Error("audio sync: DB offset update failed, temp file removed",
			"path", filepath.Base(path),
			"cumulative_offset_ms", cumulativeOffset,
			"error", err)
		return 0, fmt.Errorf("offset tracking failed: %w", err)
	}

	if err := fsutil.CommitAtomicWrite(tmpPath, path); err != nil {
		return 0, fmt.Errorf("save (commit): %w", err)
	}

	slog.Info("audio sync applied",
		"offset_ms", audioOffset,
		"cumulative_offset_ms", cumulativeOffset,
		"confidence", confidence,
		"path", filepath.Base(path))

	return cumulativeOffset, nil
}
