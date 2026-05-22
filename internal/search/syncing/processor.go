package syncing

import (
	"bytes"
	"context"
	"io"
	"time"

	"subflux/internal/api"
	"subflux/internal/subsync"
)

// SubtitleProcessor implements api.SubtitleProcessor directly using subsync,
// without an intermediate backend interface.
type SubtitleProcessor struct{}

// NewSubtitleProcessor creates a SubtitleProcessor.
func NewSubtitleProcessor() SubtitleProcessor {
	return SubtitleProcessor{}
}

// Compile-time check.
var _ api.SubtitleProcessor = SubtitleProcessor{}

// apiCuesFromSubsync converts []subsync.Cue to []api.SubtitleCue via
// explicit field-by-field copy.
func apiCuesFromSubsync(cues []subsync.Cue) []api.SubtitleCue {
	if len(cues) == 0 {
		return nil
	}
	out := make([]api.SubtitleCue, len(cues))
	for i, c := range cues {
		out[i] = api.SubtitleCue{
			Start: c.Start,
			End:   c.End,
			Text:  c.Text,
		}
	}
	return out
}

// subsyncCuesFromAPI converts []api.SubtitleCue to []subsync.Cue via
// explicit field-by-field copy.
func subsyncCuesFromAPI(cues []api.SubtitleCue) []subsync.Cue {
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
func (SubtitleProcessor) ParseSRT(data []byte) ([]api.SubtitleCue, error) {
	cues, err := subsync.ParseSRT(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return apiCuesFromSubsync(cues), nil
}

// WriteSRT serializes cues to SRT format.
func (SubtitleProcessor) WriteSRT(cues []api.SubtitleCue) ([]byte, error) {
	var buf bytes.Buffer
	if err := subsync.WriteSRT(&buf, subsyncCuesFromAPI(cues)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ShiftCues applies a timing offset to all cues.
func (SubtitleProcessor) ShiftCues(cues []api.SubtitleCue, offset time.Duration) []api.SubtitleCue {
	shifted := subsync.ShiftCues(subsyncCuesFromAPI(cues), offset)
	return apiCuesFromSubsync(shifted)
}

// SyncFromAudio runs audio-based sync on subtitle data.
func (SubtitleProcessor) SyncFromAudio(ctx context.Context, data []byte, videoPath, subtitlePath string) api.AudioSyncResult {
	result := SyncFromAudio(ctx, data, videoPath, subtitlePath)
	return api.AudioSyncResult{
		Method:     string(result.Method),
		Cues:       apiCuesFromSubsync(result.Cues),
		Offset:     result.Offset,
		Confidence: float64(result.Confidence),
		Applied:    result.Applied() && result.ShouldApply(),
	}
}

// Ensure io is used (ParseSRT uses bytes.NewReader which implements io.Reader).
var _ io.Reader = (*bytes.Reader)(nil)
