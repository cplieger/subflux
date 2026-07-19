package previewhandlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/resolve"
)

// previewTimeout is the maximum duration for a single preview stream.
const previewTimeout = 10 * time.Minute

// bufferedMaxDuration limits buffered (Safari) previews to 30 seconds.
const bufferedMaxDuration = 30

const displayGroupSeries = "series"

// HandlePreviewVideo handles
// GET /api/preview/video?media_type=...&media_id=...&season=...&episode=...&start=...&buffered=...
// The video is addressed by MediaRef (arr identity); the server resolves the
// arr-known video path — no client-supplied path exists on this verb.
func (h *Handler) HandlePreviewVideo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	ref, err := resolve.MediaRefFromQuery(r.URL.Query())
	if err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, err.Error())
		return
	}
	videoPath, err := h.deps.Resolve.VideoPath(r.Context(), ref)
	if err != nil {
		resolve.WriteError(w, r, err)
		return
	}

	startSec := 0.0
	if sv := r.URL.Query().Get("start"); sv != "" {
		if v, perr := strconv.ParseFloat(sv, 64); perr == nil && v >= 0 && v <= 86400 {
			startSec = v
		}
	}

	buffered := r.URL.Query().Get("buffered") == "true"

	slog.Debug("preview video request",
		"path", videoPath, "start", startSec, "buffered", buffered)

	ctx, cancel := context.WithTimeout(r.Context(), previewTimeout)
	defer cancel()

	rc := http.NewResponseController(w)
	if dlErr := rc.SetWriteDeadline(time.Now().Add(previewTimeout)); dlErr != nil {
		slog.Warn("preview write deadline extension failed, 60s server timeout applies", "error", dlErr)
	}

	if buffered {
		err = h.serveBuffered(ctx, w, r, videoPath, startSec)
	} else {
		err = h.streamVideo(ctx, w, videoPath, startSec)
	}
	if err != nil {
		mode := "streaming"
		if buffered {
			mode = "buffered"
		}
		slog.Warn(mode+" preview failed",
			"path", videoPath, "error", err)
		if !responseStarted(w) {
			api.InternalErrorC(w, r, err, api.CodePreviewUnavailable, "path", videoPath, "mode", mode)
			return
		}
	}
}

// streamVideo runs ffmpeg to transcode video to 360p H.264 + AAC as fMP4.
func (h *Handler) streamVideo(ctx context.Context, w http.ResponseWriter,
	path string, startSec float64,
) error {
	args := buildVideoArgs(path, startSec)
	return h.runFFmpegStream(ctx, w, args)
}

// serveBuffered runs ffmpeg to a temp file, then serves it with Content-Length.
func (h *Handler) serveBuffered(ctx context.Context, w http.ResponseWriter,
	r *http.Request, path string, startSec float64,
) error {
	if err := h.deps.FFmpegSem.Acquire(ctx, 1); err != nil {
		return fmt.Errorf("preview concurrency limit: %w", err)
	}
	defer h.deps.FFmpegSem.Release(1)

	tmp, err := os.CreateTemp("", "subflux-preview-*.mp4")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	args := buildBufferedArgs(path, startSec, tmpPath)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stderrBuf := getLimitedWriter()
	defer putLimitedWriter(stderrBuf)
	cmd.Stderr = stderrBuf

	if runErr := cmd.Run(); runErr != nil {
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg == "" {
			errMsg = runErr.Error()
		}
		return fmt.Errorf("ffmpeg: %s", errMsg)
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("open temp file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat temp file: %w", err)
	}
	if fi.Size() == 0 {
		return errors.New("ffmpeg produced no output")
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "preview.mp4", time.Now(), f)
	slog.Debug("buffered preview served", "path", path, "bytes", fi.Size())
	return nil
}

// runFFmpegStream executes ffmpeg and streams stdout to the HTTP response.
func (h *Handler) runFFmpegStream(ctx context.Context, w http.ResponseWriter,
	args []string,
) error {
	if err := h.deps.FFmpegSem.Acquire(ctx, 1); err != nil {
		return fmt.Errorf("preview concurrency limit: %w", err)
	}
	defer h.deps.FFmpegSem.Release(1)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderrBuf := getLimitedWriter()
	defer putLimitedWriter(stderrBuf)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	firstBuf := make([]byte, 32*1024)
	n, readErr := stdout.Read(firstBuf)

	if n == 0 && readErr != nil {
		if err := cmd.Wait(); err != nil {
			slog.Debug("ffmpeg wait error", "error", err)
		}
		return classifyFFmpegError(stderrBuf.String())
	}

	if readErr != nil {
		slog.Debug("ffmpeg initial read partial", "bytes", n, "error", readErr)
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")

	if _, writeErr := w.Write(firstBuf[:n]); writeErr != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			slog.Debug("kill ffmpeg failed", "error", killErr)
		}
		if waitErr := cmd.Wait(); waitErr != nil {
			slog.Debug("ffmpeg wait error", "error", waitErr)
		}
		return nil
	}

	if _, copyErr := io.Copy(w, stdout); copyErr != nil {
		slog.Debug("preview stream ended", "error", copyErr)
	}

	if waitErr := cmd.Wait(); waitErr != nil {
		slog.Debug("ffmpeg exit", "error", waitErr)
	}
	return nil
}

// HandlePreviewPoster proxies poster images from Sonarr/Radarr.
func (h *Handler) HandlePreviewPoster(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	mediaType := r.URL.Query().Get("type")
	idStr := r.URL.Query().Get("id")
	if mediaType == "" || idStr == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "type and id required")
		return
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid id")
		return
	}

	slog.Debug("preview poster request",
		"type", mediaType, "id", id, "style", r.URL.Query().Get("style"))

	ls := h.deps.StateFunc()
	arrURL, apiKey, ok := resolveArrConfig(ls, mediaType)
	if !ok {
		if mediaType != "movie" && mediaType != displayGroupSeries {
			api.BadRequestC(w, r, api.CodeBadRequest, "type must be movie or series")
		} else {
			api.BadRequestC(w, r, api.CodeBadRequest, mediaType+" arr not configured")
		}
		return
	}

	posterURL := posterCoverURL(arrURL, id, r.URL.Query().Get("style"))

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, posterURL, http.NoBody)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "build request", "url", posterURL)
		return
	}
	req.Header.Set("X-Api-Key", apiKey)

	resp, err := h.deps.PosterClient.Do(req)
	if err != nil {
		slog.Debug("poster fetch failed", "url", posterURL, "error", err)
		api.BadGatewayC(w, r, api.CodeBadGateway, "poster fetch failed")
		return
	}
	defer resp.Body.Close()

	writePosterResponse(w, r, resp, posterURL)
}

// writePosterResponse relays an upstream poster response to the client. A
// non-200 upstream status becomes a 404, and a missing or non-image upstream
// content type defaults to image/jpeg.
func writePosterResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, posterURL string) {
	if resp.StatusCode != http.StatusOK {
		slog.Debug("poster upstream error", "url", posterURL, "status", resp.StatusCode)
		if _, drainErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); drainErr != nil {
			slog.Debug("failed to drain poster response", "error", drainErr)
		}
		api.NotFoundC(w, r, api.CodeNotFound, "poster not found")
		return
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if _, copyErr := io.Copy(w, io.LimitReader(resp.Body, 2<<20)); copyErr != nil {
		slog.Debug("poster copy error", "error", copyErr)
	}
}

// resolveArrConfig returns the arr base URL and API key for the given media type.
func resolveArrConfig(ls *LiveState, mediaType string) (arrURL, apiKey string, ok bool) {
	switch mediaType {
	case "movie":
		if !ls.HasRadarr {
			return "", "", false
		}
		return ls.RadarrConfig.URL, ls.RadarrConfig.APIKey, true
	case displayGroupSeries:
		if !ls.HasSonarr {
			return "", "", false
		}
		return ls.SonarrConfig.URL, ls.SonarrConfig.APIKey, true
	default:
		return "", "", false
	}
}

// HandlePreviewStart handles GET /api/preview/start?media_type=...&media_id=...&language=...
// (FileRef query parameters). Analyzes subtitle cue density and returns the
// best starting timestamp. The subtitle is addressed by FileRef and resolved
// from the store; no client-supplied path.
func (h *Handler) HandlePreviewStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	ref, err := resolve.FileRefFromQuery(r.URL.Query())
	if err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, err.Error())
		return
	}
	subPath, err := h.deps.Resolve.SubtitlePath(r.Context(), ref)
	if err != nil {
		resolve.WriteError(w, r, err)
		return
	}

	_, cues, parseErr := h.readAndParseSRT(subPath)
	if parseErr != nil || len(cues) == 0 {
		slog.Warn("preview start: read/parse subtitle failed",
			"path", subPath, "error", parseErr, "cues", len(cues))
		api.BadRequestC(w, r, api.CodeBadRequest, "failed to parse subtitle")
		return
	}

	startMs := findDialogueDenseStart(cues)
	startSec := float64(startMs) / 1000.0

	mins := int(startSec) / 60
	secs := int(startSec) % 60
	desc := fmt.Sprintf("%d:%02d — dialogue-dense section", mins, secs)

	api.WriteJSON(w, PreviewStartResponse{
		StartSeconds: startSec,
		Description:  desc,
	})
}

// PreviewStartResponse is the typed response for HandlePreviewStart.
type PreviewStartResponse struct {
	Description  string  `json:"description"`
	StartSeconds float64 `json:"start_seconds"`
}

// HandlePreviewSubtitle handles GET /api/preview/subtitle?media_type=...&...&start=...&shift=...
// (FileRef query parameters plus start/shift). Returns the subtitle content
// as WebVTT for the video preview track. The subtitle is addressed by
// FileRef and resolved from the store; no client-supplied path.
func (h *Handler) HandlePreviewSubtitle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	ref, err := resolve.FileRefFromQuery(r.URL.Query())
	if err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, err.Error())
		return
	}
	subPath, err := h.deps.Resolve.SubtitlePath(r.Context(), ref)
	if err != nil {
		resolve.WriteError(w, r, err)
		return
	}

	var startMs int64
	if o := r.URL.Query().Get("start"); o != "" {
		sec, perr := strconv.ParseFloat(o, 64)
		if perr == nil {
			startMs = int64(sec * 1000)
		} else {
			slog.Debug("preview subtitle: invalid start param", "value", o, "error", perr)
		}
	}

	var shiftMs int64
	if o := r.URL.Query().Get("shift"); o != "" {
		ms, perr := strconv.ParseInt(o, 10, 64)
		if perr == nil {
			shiftMs = ms
		} else {
			slog.Debug("preview subtitle: invalid shift param", "value", o, "error", perr)
		}
	}

	data, err := h.deps.ReadBounded(r.Context(), subPath, maxSyncSubSize)
	if err != nil {
		slog.Debug("preview subtitle: read failed",
			"path", subPath, "error", err)
		api.BadRequestC(w, r, api.CodeBadRequest, "failed to read subtitle")
		return
	}

	data = h.deps.SubtitleProc.NormalizeEncoding(data)
	cues, err := h.deps.SubtitleProc.ParseSRT(data)
	if err != nil {
		slog.Debug("preview subtitle: parse failed",
			"path", subPath, "error", err)
		api.BadRequestC(w, r, api.CodeBadRequest, "failed to parse subtitle")
		return
	}

	totalShift := -time.Duration(startMs)*time.Millisecond +
		time.Duration(shiftMs)*time.Millisecond
	cues = shiftAndFilterCues(cues, totalShift)

	w.Header().Set("Content-Type", "text/vtt")
	if _, err := fmt.Fprint(w, srtToWebVTT(cues)); err != nil {
		slog.Debug("write VTT response failed", "error", err)
	}
}

// maxSyncSubSize caps subtitle file reads for preview operations.
const maxSyncSubSize int64 = 10 << 20 // 10 MB

// readAndParseSRT reads a subtitle file, normalizes encoding, and parses SRT.
func (h *Handler) readAndParseSRT(path string) ([]byte, []api.SubtitleCue, error) {
	data, err := h.deps.ReadBounded(context.Background(), path, maxSyncSubSize)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read subtitle: %w", err)
	}
	data = h.deps.SubtitleProc.NormalizeEncoding(data)
	cues, err := h.deps.SubtitleProc.ParseSRT(data)
	if err != nil {
		return data, nil, fmt.Errorf("failed to parse subtitle: %w", err)
	}
	return data, cues, nil
}

// shiftAndFilterCues applies a timing shift and removes cues ending before zero.
func shiftAndFilterCues(cues []api.SubtitleCue, totalShift time.Duration) []api.SubtitleCue {
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

// findDialogueDenseStart finds the timestamp (ms) of the densest 60-second window.
func findDialogueDenseStart(cues []api.SubtitleCue) int64 {
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
	return max(bestStart-leadInMs, 0)
}

// srtToWebVTT converts parsed SRT cues to WebVTT format string.
func srtToWebVTT(cues []api.SubtitleCue) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for i, c := range cues {
		fmt.Fprintf(&b, "%d\n", i+1)
		fmt.Fprintf(&b, "%s --> %s\n",
			msToVTT(c.Start.Milliseconds()),
			msToVTT(c.End.Milliseconds()))
		b.WriteString(c.Text)
		b.WriteString("\n\n")
	}
	return b.String()
}

func msToVTT(ms int64) string {
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
