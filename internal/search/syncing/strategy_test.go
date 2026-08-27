package syncing_test

import (
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/subsync"
)

// lateSRT and refSRT are the same five cues one second apart, which is enough
// material for the aligner to find a constant offset.
const (
	lateSRT = "1\r\n00:00:02,000 --> 00:00:03,000\r\nFirst\r\n\r\n" +
		"2\r\n00:00:12,000 --> 00:00:13,000\r\nSecond\r\n\r\n" +
		"3\r\n00:00:22,000 --> 00:00:23,000\r\nThird\r\n\r\n" +
		"4\r\n00:00:32,000 --> 00:00:33,000\r\nFourth\r\n\r\n" +
		"5\r\n00:00:42,000 --> 00:00:43,000\r\nFifth\r\n\r\n"

	refSRT = "1\r\n00:00:01,000 --> 00:00:02,000\r\nFirst\r\n\r\n" +
		"2\r\n00:00:11,000 --> 00:00:12,000\r\nSecond\r\n\r\n" +
		"3\r\n00:00:21,000 --> 00:00:22,000\r\nThird\r\n\r\n" +
		"4\r\n00:00:31,000 --> 00:00:32,000\r\nFourth\r\n\r\n" +
		"5\r\n00:00:41,000 --> 00:00:42,000\r\nFifth\r\n\r\n"
)

// TestSyncFromCues_parses_a_UTF16_subtitle pins that the reference path decodes
// for parsing. A UTF-16 subtitle encodes its own STRUCTURE too, so the digits,
// the "-->" and the line breaks are all NUL-interleaved and ParseSRT finds no
// cues in the raw bytes. The failure is silent in the worst way: the result is
// MethodNone, which every caller reads as "nothing needed correcting" rather
// than "I could not read this", so the subtitle is saved unsynced with no
// warning anywhere. The audio path decoded already; this is the other one.
func TestSyncFromCues_parses_a_UTF16_subtitle(t *testing.T) {
	t.Parallel()

	utf16 := []byte{0xFF, 0xFE}
	for _, r := range lateSRT {
		utf16 = append(utf16, byte(r), 0)
	}

	refCues, err := subsync.ParseSRT(strings.NewReader(refSRT))
	if err != nil {
		t.Fatalf("ParseSRT(reference) error = %v, want nil", err)
	}

	got := syncing.SyncFromCues(t.Context(), utf16, refCues, "")
	if got.Method == subsync.MethodNone {
		t.Fatalf("SyncFromCues(UTF-16 subtitle) = MethodNone, want a real alignment: "+
			"the same cues parse once decoded (%d reference cues)", len(refCues))
	}
	if len(got.Cues) == 0 {
		t.Errorf("SyncFromCues(UTF-16 subtitle) returned no cues, want the parsed set")
	}
}

// TestSyncFromCues_matches_the_decoded_result verifies the decode is the only
// difference: the same subtitle in UTF-8 and in UTF-16 must align identically.
// Stated as an oracle rather than a captured offset, so it keeps holding if the
// aligner's answer ever changes.
func TestSyncFromCues_matches_the_decoded_result(t *testing.T) {
	t.Parallel()

	utf16 := []byte{0xFF, 0xFE}
	for _, r := range lateSRT {
		utf16 = append(utf16, byte(r), 0)
	}

	refCues, err := subsync.ParseSRT(strings.NewReader(refSRT))
	if err != nil {
		t.Fatalf("ParseSRT(reference) error = %v, want nil", err)
	}

	wantResult := syncing.SyncFromCues(t.Context(), []byte(lateSRT), refCues, "")
	gotResult := syncing.SyncFromCues(t.Context(), utf16, refCues, "")

	if gotResult.Offset != wantResult.Offset {
		t.Errorf("SyncFromCues(UTF-16) offset = %d, want %d (the UTF-8 answer for the same cues)",
			gotResult.Offset, wantResult.Offset)
	}
	if gotResult.Method != wantResult.Method {
		t.Errorf("SyncFromCues(UTF-16) method = %v, want %v", gotResult.Method, wantResult.Method)
	}
	if len(gotResult.Cues) != len(wantResult.Cues) {
		t.Errorf("SyncFromCues(UTF-16) returned %d cues, want %d",
			len(gotResult.Cues), len(wantResult.Cues))
	}
}
