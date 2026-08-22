package search

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// boundLogPath fixtures. The property that matters is narrow: the cut must
// never land inside a multi-byte rune, because the result goes straight into a
// slog attribute and from there into Loki, where a partial rune is invalid
// UTF-8. The tail-keeping and "..." marker semantics are deliberate (a media
// path's filename is its identifying end) and are pinned here too, so a future
// "just use the shared head-keeping cut" edit fails instead of silently
// changing which end of the path an operator sees.

// threeByteRune is a 3-byte UTF-8 rune (U+65E5). Three bytes is the width that
// makes every straddling offset reachable with a byte-length sweep.
const threeByteRune = "日"

func TestBoundLogPathUnderCapUnchanged(t *testing.T) {
	for _, p := range []string{
		"",
		"/media/tv/Show/S01E01.mkv",
		strings.Repeat("a", maxLogPathLen-1),
		strings.Repeat("a", maxLogPathLen),
		// Multi-byte content under the cap is returned verbatim: the cap is on
		// bytes, and nothing is cut, so nothing can be split.
		strings.Repeat(threeByteRune, maxLogPathLen/3),
	} {
		if got := boundLogPath(p); got != p {
			t.Errorf("boundLogPath(%d bytes) truncated an under-cap path:\n got %q\nwant %q",
				len(p), got, p)
		}
	}
}

func TestBoundLogPathKeepsTailWithMarker(t *testing.T) {
	// An all-ASCII over-cap path: the cut is exactly at len-maxLogPathLen and
	// the kept tail is exactly the cap.
	head := strings.Repeat("a", 100)
	tail := strings.Repeat("b", maxLogPathLen)
	got := boundLogPath(head + tail)
	if want := "..." + tail; got != want {
		t.Errorf("boundLogPath dropped the tail-keeping semantics:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, head) {
		t.Error("boundLogPath kept the head; the filename end is the informative one")
	}
}

// TestBoundLogPathNeverSplitsARune sweeps the ASCII tail length across every
// offset at which the 3-byte rune straddles, sits exactly on, or clears the
// 256-byte cut, so the case the old byte-slice implementation got wrong (cut
// index inside the rune) is covered along with its neighbours.
func TestBoundLogPathNeverSplitsARune(t *testing.T) {
	const head = 300 // any length that pushes the input over the cap

	for tailLen := maxLogPathLen - 8; tailLen <= maxLogPathLen+8; tailLen++ {
		p := strings.Repeat("a", head) + threeByteRune + strings.Repeat("b", tailLen)
		got := boundLogPath(p)

		if !utf8.ValidString(got) {
			t.Errorf("tailLen=%d: boundLogPath emitted invalid UTF-8: %q", tailLen, got)
		}
		kept, ok := strings.CutPrefix(got, "...")
		if !ok {
			t.Fatalf("tailLen=%d: over-cap path lost its truncation marker: %q", tailLen, got)
		}
		if len(kept) > maxLogPathLen {
			t.Errorf("tailLen=%d: kept %d bytes, over the %d-byte cap",
				tailLen, len(kept), maxLogPathLen)
		}
		if !strings.HasSuffix(p, kept) {
			t.Errorf("tailLen=%d: kept text is not a suffix of the input: %q", tailLen, kept)
		}
		// The whole point: a rune is kept whole or dropped whole, never cut.
		if strings.Contains(kept, threeByteRune[1:]) && !strings.Contains(kept, threeByteRune) {
			t.Errorf("tailLen=%d: kept a partial rune: %q", tailLen, kept)
		}
	}
}

// A path whose whole over-cap tail is continuation bytes offers the forward
// walk no rune start to stop on, so the walk ends at the end of the string and
// the marker is all that is left. Such a path is already invalid UTF-8 on disk
// — bounding it for a log attribute must terminate, not read past it.
func TestBoundLogPathTailOfContinuationBytesKeepsNothing(t *testing.T) {
	t.Parallel()
	p := strings.Repeat("\x80", maxLogPathLen+44)
	if got, want := boundLogPath(p), "..."; got != want {
		t.Errorf("boundLogPath(%d continuation bytes) = %q, want %q", len(p), got, want)
	}
}

// TestBoundLogPathCutOnRuneStartKeepsTheRune pins the exact-boundary case: when
// len-maxLogPathLen already lands on the rune's first byte, the forward walk
// must not advance and the rune survives.
func TestBoundLogPathCutOnRuneStartKeepsTheRune(t *testing.T) {
	tail := strings.Repeat("b", maxLogPathLen-len(threeByteRune))
	p := strings.Repeat("a", 300) + threeByteRune + tail
	want := "..." + threeByteRune + tail
	if got := boundLogPath(p); got != want {
		t.Errorf("cut landing on a rune start dropped the rune:\n got %q\nwant %q", got, want)
	}
}
