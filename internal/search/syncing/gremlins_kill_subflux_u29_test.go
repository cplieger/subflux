package syncing

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// TestGk_subflux_u29_ParseSRT_validReturnsCues kills processor.go:67
// CONDITIONALS_NEGATION on "if err != nil" inside ParseSRT. A valid SRT
// parses with err == nil and yields the cues; the mutated guard
// "if err == nil { return nil, err }" returns (nil, nil) on success, dropping
// the parsed cues entirely.
func TestGk_subflux_u29_ParseSRT_validReturnsCues(t *testing.T) {
	const srt = "1\n00:00:01,000 --> 00:00:02,000\nHello world\n\n"
	cues, err := SubtitleProcessor{}.ParseSRT([]byte(srt))
	if err != nil {
		t.Fatalf("ParseSRT(valid) error = %v, want nil", err)
	}
	if len(cues) != 1 {
		t.Fatalf("ParseSRT(valid) returned %d cues, want 1", len(cues))
	}
	if cues[0].Text != "Hello world" {
		t.Errorf("ParseSRT(valid) cue text = %q, want %q", cues[0].Text, "Hello world")
	}
}

// TestGk_subflux_u29_PostProcess_stripHIAppliesCueLevel kills syncer.go:85:10
// and :85:30 (the "err != nil || len(cues) == 0" early-return guard) and
// :97:47 (the WriteSRT "err != nil" guard).
//
// With a valid non-empty SRT, the byte-level pass (PostProcessBytes) does NOT
// strip HI — only the cue-level PostProcess does. Each mutated guard
// short-circuits to "return data" (the un-cue-processed bytes), leaving the
// "[music]" annotation intact:
//
//   - 85:10 "err == nil"      → true on success → early return (HI intact)
//   - 85:30 "len(cues) != 0"  → true on a non-empty SRT → early return
//   - 97:47 "err == nil"      → true (WriteSRT to a buffer never fails) →
//     returns the pre-cue-level data before "data = buf.Bytes()"
func TestGk_subflux_u29_PostProcess_stripHIAppliesCueLevel(t *testing.T) {
	const in = "1\n00:00:01,000 --> 00:00:02,000\n[music]Hello\n\n"
	const want = "1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"
	got := Syncer{}.PostProcess([]byte(in), api.PostProcessConfig{StripHI: true})
	if string(got) != want {
		t.Errorf("PostProcess(stripHI) = %q, want %q", got, want)
	}
}
