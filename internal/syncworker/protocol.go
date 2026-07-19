// Package syncworker isolates subtitle-sync computation in a supervised
// one-shot child process (P13). Sync is the engine's most memory-intensive
// work (PCM decode + alignment inside a 1 GiB no-swap container); running it
// in a same-binary `subflux sync-worker` child means a memory blowup is
// killed by the kernel OOM killer as the LARGEST-badness process — the fat
// child — while the server keeps serving. One job per process, versioned
// JSON over stdin/stdout, kill-on-cancel, concurrency-one admission.
//
// The protocol carries the raw inputs of the two heavy strategies
// (reference-based and audio-based sync) and returns a wire copy of
// subsync.SyncResult. Both sides convert to/from the real subsync types, so
// the parent applies the SAME Applied()/ShouldApply() logic it always did —
// no duplicated decision rules on the wire.
package syncworker

import (
	"time"

	"github.com/cplieger/subflux/internal/subsync"
)

// ProtocolVersion guards the parent/child JSON contract. Parent and child
// are the same binary, so a mismatch can only mean the executable was
// replaced while a request was in flight (upgrade race); the child answers
// with an error response rather than guessing.
const ProtocolVersion = 1

// Op names for Request.Op.
const (
	// OpReference aligns subtitle data against an embedded reference track
	// (syncing.SyncAgainstReference).
	OpReference = "reference"
	// OpAudio aligns subtitle data against the audio track
	// (syncing.SyncFromAudio).
	OpAudio = "audio"
)

// Request is the parent->child job description, one per process invocation.
type Request struct {
	Version       int     `json:"version"`
	Op            string  `json:"op"`
	Data          []byte  `json:"data"` // subtitle bytes (base64 via encoding/json)
	VideoPath     string  `json:"video_path"`
	SubtitlePath  string  `json:"subtitle_path,omitempty"`  // audio op: ASS detection hint
	Lang          string  `json:"lang,omitempty"`           // reference op: exclude language
	MinConfidence float64 `json:"min_confidence,omitempty"` // reference op: apply threshold
}

// Response is the child->parent result envelope. Error is set for protocol
// or infrastructure failures; strategy-level "no sync found" is NOT an error
// (it travels as a MethodNone result, exactly like the in-process calls).
type Response struct {
	Version int            `json:"version"`
	Error   string         `json:"error,omitempty"`
	Result  WireSyncResult `json:"result"`
}

// WireSyncResult is the JSON projection of subsync.SyncResult.
type WireSyncResult struct {
	Method     string    `json:"method"`
	Cues       []WireCue `json:"cues,omitempty"`
	OffsetMs   int64     `json:"offset_ms"`
	Confidence float64   `json:"confidence"`
	Rate       float64   `json:"rate"`
}

// WireCue is one subtitle cue on the wire (durations in nanoseconds).
type WireCue struct {
	Text    string `json:"t"`
	StartNs int64  `json:"s"`
	EndNs   int64  `json:"e"`
}

// wireFromResult projects a subsync.SyncResult onto the wire.
func wireFromResult(r *subsync.SyncResult) WireSyncResult {
	out := WireSyncResult{
		Method:     string(r.Method),
		OffsetMs:   r.Offset,
		Confidence: float64(r.Confidence),
		Rate:       r.Rate,
	}
	if len(r.Cues) > 0 {
		out.Cues = make([]WireCue, len(r.Cues))
		for i, c := range r.Cues {
			out.Cues[i] = WireCue{Text: c.Text, StartNs: int64(c.Start), EndNs: int64(c.End)}
		}
	}
	return out
}

// resultFromWire reconstructs the subsync.SyncResult the parent's existing
// Applied()/ShouldApply() logic operates on.
func resultFromWire(w WireSyncResult) subsync.SyncResult {
	r := subsync.SyncResult{
		Method:     subsync.SyncMethod(w.Method),
		Offset:     w.OffsetMs,
		Confidence: subsync.Confidence(w.Confidence),
		Rate:       w.Rate,
	}
	if len(w.Cues) > 0 {
		r.Cues = make([]subsync.Cue, len(w.Cues))
		for i, c := range w.Cues {
			r.Cues[i] = subsync.Cue{Text: c.Text, Start: time.Duration(c.StartNs), End: time.Duration(c.EndNs)}
		}
	}
	return r
}
