package syncing

import (
	"testing"
	"time"

	"subflux/internal/api"
)

func FuzzPostProcess(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"), true, true, true, true, true)
	f.Add([]byte("1\n00:00:00,000 --> 00:00:01,000\n[music]\n\n"), false, false, false, false, false)
	f.Add([]byte(""), true, true, true, true, true)
	f.Add([]byte("not valid srt"), true, false, true, false, true)

	f.Fuzz(func(t *testing.T, data []byte, stripHI, stripTags, normUTF8, cleanWS, removeEmpty bool) {
		pp := api.PostProcessConfig{
			StripHI:          stripHI,
			StripTags:        stripTags,
			NormalizeUTF8:    normUTF8,
			CleanWhitespace:  cleanWS,
			RemoveEmpty:      removeEmpty,
			NormalizeEndings: true,
		}
		s := Syncer{}
		// Must not panic.
		result := s.PostProcess(data, pp)
		// Result length should not exceed input by a large factor.
		if len(result) > len(data)*10+1024 {
			t.Fatalf("PostProcess output unexpectedly large: %d vs input %d", len(result), len(data))
		}
	})
}

func FuzzParseSRTRoundtrip(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello world\n\n"))
	f.Add([]byte("1\n00:01:00,500 --> 00:01:03,200\nLine 1\nLine 2\n\n2\n00:02:00,000 --> 00:02:05,000\nSecond cue\n\n"))
	f.Add([]byte(""))

	p := NewSubtitleProcessor()

	f.Fuzz(func(t *testing.T, data []byte) {
		cues, err := p.ParseSRT(data)
		if err != nil {
			return
		}
		// WriteSRT must not panic on parsed cues.
		out, err := p.WriteSRT(cues)
		if err != nil {
			t.Fatalf("WriteSRT failed on parsed cues: %v", err)
		}
		if len(cues) > 0 && len(out) == 0 {
			t.Fatal("WriteSRT produced empty output from non-empty cues")
		}
	})
}

func FuzzShiftCues(f *testing.F) {
	f.Add(int64(1000), int64(2000), "hello", int64(500))
	f.Add(int64(0), int64(1000), "", int64(-500))
	f.Add(int64(60000), int64(62000), "text", int64(0))

	p := NewSubtitleProcessor()

	f.Fuzz(func(t *testing.T, startMs, endMs int64, text string, offsetMs int64) {
		if startMs < 0 || endMs < startMs || endMs > 360000000 {
			return
		}
		if offsetMs < -360000000 || offsetMs > 360000000 {
			return
		}
		cues := []api.SubtitleCue{
			{Start: time.Duration(startMs) * time.Millisecond, End: time.Duration(endMs) * time.Millisecond, Text: text},
		}
		offset := time.Duration(offsetMs) * time.Millisecond
		// Must not panic.
		shifted := p.ShiftCues(cues, offset)
		if len(shifted) != 1 {
			t.Fatalf("ShiftCues changed length: got %d want 1", len(shifted))
		}
	})
}
