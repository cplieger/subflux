package api

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gk_subflux_u28_captureSlog redirects the default slog logger to an
// in-memory buffer at DEBUG level for the duration of the test, restoring
// the previous default on cleanup. The guarded log calls under test
// (slog.Debug / slog.Error) emit through slog.Default(), so capturing it is
// the only observable that distinguishes the boolean guards being mutated.
func gk_subflux_u28_captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// --- arr_types.go:183  `if s != ""`  (CONDITIONALS_NEGATION) ---

// Test_gk_subflux_u28_HistoryEventType_unknownStringLogGuard exercises both
// branches of the `if s != ""` guard around the unknown-event DEBUG log.
func Test_gk_subflux_u28_HistoryEventType_unknownStringLogGuard(t *testing.T) {
	prev := loggedUnknownEvents
	loggedUnknownEvents = newLogOnce(256) // fresh dedup set so first() returns true
	t.Cleanup(func() { loggedUnknownEvents = prev })

	// Non-empty unknown event: original (`s != ""` true) logs once at DEBUG.
	// Negation (`s == ""`) would skip the log for a non-empty value.
	buf := gk_subflux_u28_captureSlog(t)
	var h HistoryEventType
	if err := h.UnmarshalJSON([]byte(`"gk_subflux_u28_unknownEvt"`)); err != nil {
		t.Fatalf("UnmarshalJSON(unknown string) error = %v, want nil", err)
	}
	if h != 0 {
		t.Errorf("HistoryEventType(unknown string) = %d, want 0", int(h))
	}
	if !strings.Contains(buf.String(), "unknown event type") {
		t.Errorf("non-empty unknown event: expected DEBUG log, got %q", buf.String())
	}

	// Empty event string: original (`s != ""` false) does NOT log. Negation
	// (`s == ""` true) would log the empty value.
	buf2 := gk_subflux_u28_captureSlog(t)
	var h2 HistoryEventType
	if err := h2.UnmarshalJSON([]byte(`""`)); err != nil {
		t.Fatalf("UnmarshalJSON(empty string) error = %v, want nil", err)
	}
	if buf2.Len() != 0 {
		t.Errorf("empty event string: expected no log, got %q", buf2.String())
	}
}

// --- lang_resolve.go:17  `if raw == ""`  (CONDITIONALS_NEGATION) ---

// Test_gk_subflux_u28_logUnknownLang_emptyGuard exercises both branches of
// the `if raw == ""` early-return guard in logUnknownLang.
func Test_gk_subflux_u28_logUnknownLang_emptyGuard(t *testing.T) {
	prev := loggedUnknownLangs
	loggedUnknownLangs = newLogOnce(256)
	t.Cleanup(func() { loggedUnknownLangs = prev })

	// Non-empty unmapped name: original (`raw == ""` false) proceeds and
	// logs. Negation (`raw != ""` true) would return early and log nothing.
	buf := gk_subflux_u28_captureSlog(t)
	logUnknownLang("gk_subflux_u28_unmappedName")
	if !strings.Contains(buf.String(), "unmapped name") {
		t.Errorf("non-empty name: expected DEBUG log, got %q", buf.String())
	}

	// Whitespace-only name trims to empty: original (`raw == ""` true)
	// returns early without logging. Negation would proceed and log.
	buf2 := gk_subflux_u28_captureSlog(t)
	logUnknownLang("   ")
	if buf2.Len() != 0 {
		t.Errorf("empty (trimmed) name: expected no log, got %q", buf2.String())
	}
}

// --- http.go:93  `if err := ...Encode(v); err != nil`  (CONDITIONALS_NEGATION) ---

// Test_gk_subflux_u28_WriteJSONStatus_encodeFailureLogGuard exercises both
// branches of the encode-error guard. An unencodable value (a channel) makes
// Encode return an error; an ordinary map encodes cleanly.
func Test_gk_subflux_u28_WriteJSONStatus_encodeFailureLogGuard(t *testing.T) {
	// Encode failure: original (`err != nil` true) logs the failure.
	// Negation (`err == nil`) would not log on failure.
	buf := gk_subflux_u28_captureSlog(t)
	WriteJSONStatus(httptest.NewRecorder(), http.StatusOK, make(chan int))
	if !strings.Contains(buf.String(), "writeJSON encode failed") {
		t.Errorf("encode failure: expected DEBUG log, got %q", buf.String())
	}

	// Encode success: original (`err != nil` false) does NOT log. Negation
	// (`err == nil` true) would log on every successful encode.
	buf2 := gk_subflux_u28_captureSlog(t)
	WriteJSONStatus(httptest.NewRecorder(), http.StatusOK, map[string]int{"k": 1})
	if strings.Contains(buf2.String(), "writeJSON encode failed") {
		t.Errorf("encode success: expected no log, got %q", buf2.String())
	}
}

// --- http_errors.go:75  `if err != nil`  (CONDITIONALS_NEGATION) ---

// Test_gk_subflux_u28_InternalErrorC_logGuard exercises both branches of the
// `if err != nil` guard around the ERROR-level log in InternalErrorC.
func Test_gk_subflux_u28_InternalErrorC_logGuard(t *testing.T) {
	// Non-nil error: original (`err != nil` true) logs the cause at ERROR.
	// Negation (`err == nil`) would not log when err is non-nil.
	buf := gk_subflux_u28_captureSlog(t)
	InternalErrorC(httptest.NewRecorder(), nil, errors.New("gk_subflux_u28_boom"), "gk_subflux_u28_code")
	if !strings.Contains(buf.String(), "gk_subflux_u28_boom") {
		t.Errorf("non-nil error: expected ERROR log with cause, got %q", buf.String())
	}

	// Nil error: original (`err != nil` false) does NOT log. Negation
	// (`err == nil` true) would log on a nil error.
	buf2 := gk_subflux_u28_captureSlog(t)
	InternalErrorC(httptest.NewRecorder(), nil, nil, "gk_subflux_u28_code")
	if buf2.Len() != 0 {
		t.Errorf("nil error: expected no log, got %q", buf2.String())
	}
}

// --- request_id.go:52  `if len(s) < 1 || len(s) > 64`  (CONDITIONALS_BOUNDARY x2) ---

// Test_gk_subflux_u28_validRequestID_lengthBoundaries pins the exact length
// limits of validRequestID.
// Kills 52:12 BOUNDARY (`len(s) < 1` -> `<= 1`: a 1-char id must stay valid)
// and 52:26 BOUNDARY (`len(s) > 64` -> `>= 64`: a 64-char id must stay valid).
func Test_gk_subflux_u28_validRequestID_lengthBoundaries(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"single_char_valid", "a", true},
		{"len_64_valid", strings.Repeat("a", 64), true},
		{"empty_invalid", "", false},
		{"len_65_invalid", strings.Repeat("a", 65), false},
	}
	for _, tc := range tests {
		if got := validRequestID(tc.in); got != tc.want {
			t.Errorf("validRequestID(%s, len=%d) = %v, want %v", tc.name, len(tc.in), got, tc.want)
		}
	}
}

// --- subtitle_validate.go:50  `if len(check) > 512`  (CONDITIONALS_BOUNDARY) ---

// Test_gk_subflux_u28_ValidateSubtitleData_512boundary exercises the
// truncation guard at subtitle_validate.go:50. NOTE: the 50:16 mutant
// (`>` -> `>=`) is EQUIVALENT — at len == 512, check[:512] is a no-op, so
// `check` (and therefore CountNonTextBytes(check) and len(check)) is
// identical under both operators. This test documents correct behaviour; it
// cannot and does not claim to kill 50:16.
func Test_gk_subflux_u28_ValidateSubtitleData_512boundary(t *testing.T) {
	// 512 bytes of clean printable text -> not binary -> nil error.
	clean := bytes.Repeat([]byte("A"), 512)
	if err := ValidateSubtitleData(clean); err != nil {
		t.Errorf("ValidateSubtitleData(512 clean bytes) = %v, want nil", err)
	}
	// A RAR magic prefix is rejected as binary regardless of length.
	if err := ValidateSubtitleData([]byte("Rar!\x1a\x07\x00rest")); !errors.Is(err, ErrBinaryData) {
		t.Errorf("ValidateSubtitleData(rar magic) = %v, want ErrBinaryData", err)
	}
}
