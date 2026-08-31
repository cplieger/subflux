package syncing

import (
	"bytes"

	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/subtitleenc"
)

// SubtitleProcessor implements the SRT surface both handler families declare,
// using subsync, without an intermediate backend interface. Every operation
// (parse/write/shift/normalize) runs in-process; audio sync is a server-owned
// job and does not come through here.
// SubtitleProcessor is the SRT text surface both handler families declare:
// encoding normalization, parsing and serialization. Stateless, so the zero
// value is the only one anyone needs.
type SubtitleProcessor struct{}

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
	return subtitleenc.Normalize(data)
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
