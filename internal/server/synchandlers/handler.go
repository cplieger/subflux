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

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/httphelpers"
	"github.com/cplieger/subflux/internal/server/resolve"
)

// SyncStore documents the api.Store methods used by sync handlers.
type SyncStore interface {
	GetSyncOffset(ctx context.Context, path string) (int64, error)
	SetSyncOffset(ctx context.Context, path string, offsetMs int64) error
}

// Compile-time assertion: api.Store satisfies SyncStore.
var _ SyncStore = api.Store(nil)

// Deps holds all dependencies for the sync handler family. Resolve is the
// S7 typed-reference resolver: sync verbs address the subtitle by FileRef
// and the server resolves both the subtitle path (store row) and the video
// path (same media) — no client-supplied paths.
type Deps struct {
	Store        SyncStore
	SubtitleProc api.SubtitleProcessor
	Activity     *activity.Log
	Resolve      *resolve.Resolver
}

// Handler holds all dependencies for the sync handler family.
type Handler struct {
	store        SyncStore
	subtitleProc api.SubtitleProcessor
	activity     *activity.Log
	resolve      *resolve.Resolver
}

// New creates a Handler with the given dependencies.
func New(d Deps) *Handler {
	return &Handler{
		store:        d.Store,
		subtitleProc: d.SubtitleProc,
		activity:     d.Activity,
		resolve:      d.Resolve,
	}
}

// MaxSyncSubSize caps subtitle file reads for sync operations.
const MaxSyncSubSize = httputil.MaxDownloadBytes

// maxBodySize references the canonical constant from httphelpers.
const maxBodySize = httphelpers.MaxDefaultBodySize

// --- Request/Response types ---

// SyncAudioRequest is the typed body for POST /api/sync/audio: the FileRef
// of the subtitle to align (the server resolves the subtitle path from the
// store row and the video path from the same media) plus the dry-run flag.
type SyncAudioRequest struct {
	MediaType api.MediaType `json:"media_type"`
	MediaID   string        `json:"media_id"`
	Language  string        `json:"language"`
	Variant   string        `json:"variant,omitempty"`
	Source    string        `json:"source,omitempty"`
	Ordinal   int           `json:"ordinal,omitempty"`
	DryRun    bool          `json:"dry_run,omitempty"`
}

// SyncAudioResponse is the typed response for POST /api/sync/audio.
type SyncAudioResponse struct {
	Method     string  `json:"method"`
	OffsetMs   int64   `json:"offset_ms"`
	Confidence float64 `json:"confidence"`
	Applied    bool    `json:"applied"`
}

// SyncOffsetRequest is the typed body for POST /api/sync/offset: the FileRef
// of the subtitle plus the absolute cumulative offset to apply.
type SyncOffsetRequest struct {
	MediaType api.MediaType `json:"media_type"`
	MediaID   string        `json:"media_id"`
	Language  string        `json:"language"`
	Variant   string        `json:"variant,omitempty"`
	Source    string        `json:"source,omitempty"`
	Ordinal   int           `json:"ordinal,omitempty"`
	OffsetMs  int64         `json:"offset_ms"`
}

// fileRef converts the request's flat wire fields into a resolve.FileRef,
// applying the variant/source defaults.
func fileRef(mediaType api.MediaType, mediaID, language, variant, source string, ordinal int) *resolve.FileRef {
	if variant == "" {
		variant = string(api.VariantStandard)
	}
	if source == "" {
		source = string(api.SourceExternal)
	}
	return &resolve.FileRef{
		MediaType: mediaType,
		MediaID:   mediaID,
		Language:  language,
		Variant:   variant,
		Source:    source,
		Ordinal:   ordinal,
	}
}

// --- Handlers ---

// syncAudioPaths holds the server-resolved paths for one sync-audio request.
type syncAudioPaths struct {
	subtitle string
	video    string
}

// decodeSyncAudioRequest decodes and gates a sync-audio request: POST only,
// JSON body, FileRef resolving to a stored subtitle whose media has a known
// video path, and the ASS/SSA apply refusal. ok=false means the response has
// already been written.
//
// The ASS/SSA gate exists because the writeback path serializes cues as SRT
// dialogue only, which would silently destroy styling, signs, and karaoke
// and leave SRT content under an .ass name. Dry-run is still allowed so the
// computed offset can be inspected. Lift the gate only when a
// format-preserving ASS writer exists. The gate runs on the RESOLVED path.
func (h *Handler) decodeSyncAudioRequest(w http.ResponseWriter, r *http.Request) (req SyncAudioRequest, paths syncAudioPaths, ok bool) {
	if !httphelpers.RequirePOST(w, r) {
		return req, paths, false
	}
	if !httphelpers.DecodeJSONBody(w, r, &req, maxBodySize) {
		return req, paths, false
	}
	ref := fileRef(req.MediaType, req.MediaID, req.Language, req.Variant, req.Source, req.Ordinal)
	if err := ref.Validate(); err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, err.Error())
		return req, paths, false
	}
	subPath, err := h.resolve.SubtitlePath(r.Context(), ref)
	if err != nil {
		resolve.WriteError(w, r, err)
		return req, paths, false
	}
	videoPath, err := h.resolve.VideoPathForFile(r.Context(), ref)
	if err != nil {
		resolve.WriteError(w, r, err)
		return req, paths, false
	}
	if !req.DryRun && isASSSubtitlePath(subPath) {
		api.BadRequestC(w, r, api.CodeSyncUnsupportedFormat,
			"audio sync cannot be applied to ASS/SSA subtitles (writeback is SRT-only and would discard styling); use dry_run to inspect the offset")
		return req, paths, false
	}
	return req, syncAudioPaths{subtitle: subPath, video: videoPath}, true
}

// HandleSyncAudio handles POST /api/sync/audio.
func (h *Handler) HandleSyncAudio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, paths, ok := h.decodeSyncAudioRequest(w, r)
	if !ok {
		return
	}

	data, err := atomicfile.ReadBounded(ctx, paths.subtitle, MaxSyncSubSize)
	if err != nil {
		slog.Warn("sync audio: read subtitle failed",
			"path", paths.subtitle, "error", err)
		api.BadRequestC(w, r, api.CodeSyncUnsupportedFormat, "failed to read subtitle")
		return
	}

	data = h.subtitleProc.NormalizeEncoding(data)

	actID := h.activity.Start("Audio Sync",
		filepath.Base(paths.subtitle), activity.SourceManual)
	defer h.activity.End(actID)

	slog.Info("audio sync requested",
		"subtitle", filepath.Base(paths.subtitle),
		"video", filepath.Base(paths.video))

	result := h.subtitleProc.SyncFromAudio(ctx, data, paths.video, paths.subtitle)

	resp := SyncAudioResponse{
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
			"path", filepath.Base(paths.subtitle))
	}

	if resp.Applied && result.Cues != nil && !req.DryRun {
		cumOffset, err := h.applySyncResult(ctx, paths.subtitle, result.Cues, result.Offset, result.Confidence)
		if err != nil {
			api.InternalErrorC(w, r, err, api.CodeInternalError, "path", paths.subtitle)
			return
		}
		resp.OffsetMs = cumOffset
	}

	slog.Info("audio sync completed",
		"subtitle", filepath.Base(paths.subtitle),
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

	var req SyncOffsetRequest
	if !httphelpers.DecodeJSONBody(w, r, &req, maxBodySize) {
		return
	}
	ref := fileRef(req.MediaType, req.MediaID, req.Language, req.Variant, req.Source, req.Ordinal)
	if err := ref.Validate(); err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, err.Error())
		return
	}
	subtitlePath, err := h.resolve.SubtitlePath(ctx, ref)
	if err != nil {
		resolve.WriteError(w, r, err)
		return
	}

	slog.Info("manual offset requested",
		"subtitle", filepath.Base(subtitlePath),
		"offset_ms", req.OffsetMs)

	currentOffset, err := h.store.GetSyncOffset(ctx, subtitlePath)
	if err != nil {
		slog.Debug("sync offset: no previous offset, treating as zero",
			"path", subtitlePath, "error", err)
		currentOffset = 0
	}
	delta := req.OffsetMs - currentOffset

	_, cues, parseErr := h.readAndParseSRT(subtitlePath)
	if parseErr != nil || len(cues) == 0 {
		slog.Debug("sync offset: read/parse failed",
			"path", subtitlePath, "error", parseErr, "cues", len(cues))
		api.BadRequestC(w, r, api.CodeSyncUnsupportedFormat, "failed to parse subtitle")
		return
	}

	if delta != 0 {
		offset := time.Duration(delta) * time.Millisecond
		// ShiftAndFilterCues (not the bare ShiftCues clamp) so a large
		// negative offset DROPS cues pushed entirely before time zero instead
		// of writing them as 00:00:00,000 --> 00:00:00,000 flashes — matching
		// what the preview path already shows the user.
		cues = ShiftAndFilterCues(cues, offset)
	}

	srtData, err := h.subtitleProc.WriteSRT(cues)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "write SRT")
		return
	}

	// WithMaxBytes mirrors the read bound above: this handler refuses to
	// persist a subtitle its own ReadBounded(MaxSyncSubSize) path would
	// refuse to load on the next request.
	if _, err := atomicfile.WriteFile(ctx, subtitlePath, srtData,
		atomicfile.WithMaxBytes(MaxSyncSubSize)); err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "save", "path", subtitlePath)
		return
	}

	slog.Info("manual offset applied",
		"offset_ms", req.OffsetMs,
		"delta_ms", delta,
		"path", filepath.Base(subtitlePath))

	if err := h.store.SetSyncOffset(ctx, subtitlePath, req.OffsetMs); err != nil {
		slog.Error("sync offset: file saved but DB offset update failed",
			"path", filepath.Base(subtitlePath),
			"offset_ms", req.OffsetMs,
			"error", err)
		api.JSONErrorWithCode(w, r, http.StatusInternalServerError, api.CodeInternalError,
			"offset applied but tracking failed; re-open sync dialog to verify")
		return
	}

	api.WriteJSON(w, map[string]int64{"applied_offset_ms": req.OffsetMs})
}

// --- Helpers ---

// isASSSubtitlePath reports whether the path names an ASS/SSA subtitle, the
// formats the SRT-only writeback must never overwrite (see HandleSyncAudio).
func isASSSubtitlePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ass", ".ssa":
		return true
	default:
		return false
	}
}

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
	data, err := atomicfile.ReadBounded(context.Background(), path, MaxSyncSubSize)
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

	// WithMaxBytes mirrors the read bound: readAndParseSRT and the sync-audio
	// read cap at MaxSyncSubSize, so the staged write must refuse to cross it.
	pf, err := atomicfile.NewPendingFile(ctx, path, atomicfile.WithMaxBytes(MaxSyncSubSize))
	if err != nil {
		return 0, fmt.Errorf("save (prepare): %w", err)
	}
	defer func() { _ = pf.Cleanup() }()
	if _, err := pf.Write(srtData); err != nil {
		return 0, fmt.Errorf("save (write): %w", err)
	}

	prevOffset, offsetErr := h.store.GetSyncOffset(ctx, path)
	if offsetErr != nil {
		slog.Debug("no previous sync offset, starting from zero", "path", path)
	}
	cumulativeOffset := prevOffset + audioOffset

	// Commit the file BEFORE recording the offset (same order as the manual
	// offset handler): if the commit fails the DB still holds the old offset,
	// so the next delta is computed against what is actually on disk. The
	// reverse order could persist an offset for a write that never landed and
	// silently mis-shift the next sync.
	if _, err := pf.Commit(ctx); err != nil {
		return 0, fmt.Errorf("save (commit): %w", err)
	}

	if err := h.store.SetSyncOffset(ctx, path, cumulativeOffset); err != nil {
		slog.Error("audio sync: file saved but DB offset update failed",
			"path", filepath.Base(path),
			"cumulative_offset_ms", cumulativeOffset,
			"error", err)
		return 0, fmt.Errorf("offset applied but tracking failed (re-open the sync dialog to verify): %w", err)
	}

	slog.Info("audio sync applied",
		"offset_ms", audioOffset,
		"cumulative_offset_ms", cumulativeOffset,
		"confidence", confidence,
		"path", filepath.Base(path))

	return cumulativeOffset, nil
}
