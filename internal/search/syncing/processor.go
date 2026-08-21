package syncing

import (
	"bytes"
	"context"
	"io"

	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subsync"
)

// SubtitleProcessor implements the SRT surface synchandlers declares, using subsync,
// without an intermediate backend interface. The lightweight operations
// (parse/write/shift/normalize) always run in-process; the heavy audio sync
// routes through the configured SyncExec.
type SubtitleProcessor struct {
	exec SyncExec
}

// NewSubtitleProcessorWithExec creates a SubtitleProcessor whose audio sync
// runs through the given executor (server mode: the syncworker client). The
// zero SubtitleProcessor runs audio sync in-process.
func NewSubtitleProcessorWithExec(exec SyncExec) SubtitleProcessor {
	return SubtitleProcessor{exec: exec}
}

// Compile-time check.

// apiCuesFromSubsync converts []subsync.Cue to []subflux.SubtitleCue via
// explicit field-by-field copy.
func apiCuesFromSubsync(cues []subsync.Cue) []subflux.SubtitleCue {
	if len(cues) == 0 {
		return nil
	}
	out := make([]subflux.SubtitleCue, len(cues))
	for i, c := range cues {
		out[i] = subflux.SubtitleCue{
			Start: c.Start,
			End:   c.End,
			Text:  c.Text,
		}
	}
	return out
}

// subsyncCuesFromAPI converts []subflux.SubtitleCue to []subsync.Cue via
// explicit field-by-field copy.
func subsyncCuesFromAPI(cues []subflux.SubtitleCue) []subsync.Cue {
	if len(cues) == 0 {
		return nil
	}
	out := make([]subsync.Cue, len(cues))
	for i, c := range cues {
		out[i] = subsync.Cue{
			Start: c.Start,
			End:   c.End,
			Text:  c.Text,
		}
	}
	return out
}

// NormalizeEncoding converts subtitle data to UTF-8.
func (SubtitleProcessor) NormalizeEncoding(data []byte) []byte {
	return subsync.NormalizeEncoding(data)
}

// ParseSRT parses SRT subtitle data into cues.
func (SubtitleProcessor) ParseSRT(data []byte) ([]subflux.SubtitleCue, error) {
	cues, err := subsync.ParseSRT(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return apiCuesFromSubsync(cues), nil
}

// WriteSRT serializes cues to SRT format.
func (SubtitleProcessor) WriteSRT(cues []subflux.SubtitleCue) ([]byte, error) {
	var buf bytes.Buffer
	if err := subsync.WriteSRT(&buf, subsyncCuesFromAPI(cues)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SyncFromAudio runs audio-based sync on subtitle data.
func (p SubtitleProcessor) SyncFromAudio(ctx context.Context, data []byte, videoPath, subtitlePath string) subflux.AudioSyncResult {
	exec := p.exec
	if exec == nil {
		exec = InProcessExec{}
	}
	result := exec.Audio(ctx, data, videoPath, subtitlePath)
	return subflux.AudioSyncResult{
		Method:     string(result.Method),
		Cues:       apiCuesFromSubsync(result.Cues),
		Offset:     result.Offset,
		Confidence: float64(result.Confidence),
		Applied:    result.Applied() && result.ShouldApply(),
	}
}

// Ensure io is used (ParseSRT uses bytes.NewReader which implements io.Reader).
var _ io.Reader = (*bytes.Reader)(nil)
