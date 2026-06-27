package api

import (
	"strings"
	"testing"
)

// TestLogUnknownLang_logs_only_nonempty_name pins the empty-input guard in
// logUnknownLang: a non-empty unmapped name is reported once at DEBUG, while a
// name that trims to empty is dropped silently. This must not be t.Parallel
// because captureSlog swaps the process-wide default logger.
func TestLogUnknownLang_logs_only_nonempty_name(t *testing.T) {
	prev := loggedUnknownLangs
	loggedUnknownLangs = newLogOnce(256) // fresh dedup set so first() returns true
	t.Cleanup(func() { loggedUnknownLangs = prev })

	// Non-empty unmapped name: proceeds past the guard and logs once.
	buf := captureSlog(t)
	logUnknownLang("klingon")
	if !strings.Contains(buf.String(), "unmapped name") {
		t.Errorf("non-empty name: expected DEBUG log, got %q", buf.String())
	}

	// Whitespace-only name trims to empty: the guard returns early, no log.
	buf2 := captureSlog(t)
	logUnknownLang("   ")
	if buf2.Len() != 0 {
		t.Errorf("empty (trimmed) name: expected no log, got %q", buf2.String())
	}
}
