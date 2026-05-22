package subsync

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"subflux/internal/api"
)

func TestSyncFile_nil_opts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subPath := filepath.Join(dir, "sub.srt")
	writeSRTFile(t, subPath, testCues(10, 0, 3*time.Second))

	// nil opts should use defaults (audio disabled because no video path).
	result, err := SyncFile(context.Background(), "", subPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Data) == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestSyncFile_nonexistent_subtitle(t *testing.T) {
	t.Parallel()
	_, err := SyncFile(context.Background(), "video.mkv", "/nonexistent/sub.srt", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent subtitle")
	}
}

func TestSyncFile_nonexistent_reference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subPath := filepath.Join(dir, "sub.srt")
	writeSRTFile(t, subPath, testCues(10, 0, 3*time.Second))

	_, err := SyncFile(context.Background(), "video.mkv", subPath, &Options{
		Reference: "/nonexistent/ref.srt",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent reference")
	}
}

func TestSyncFile_empty_subtitle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subPath := filepath.Join(dir, "empty.srt")
	if err := os.WriteFile(subPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := SyncFile(context.Background(), "", subPath, &Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Method != MethodNone {
		t.Fatalf("expected method 'none', got %q", result.Method)
	}
}

func TestSyncFile_with_reference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	refCues := testCues(20, 0, 5*time.Second)
	incCues := testCues(20, 2*time.Second, 5*time.Second)

	refPath := filepath.Join(dir, "ref.srt")
	subPath := filepath.Join(dir, "sub.srt")
	writeSRTFile(t, refPath, refCues)
	writeSRTFile(t, subPath, incCues)

	result, err := SyncFile(context.Background(), "", subPath, &Options{
		Reference: refPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Data) == 0 {
		t.Fatal("expected non-empty output")
	}
	if result.Method == MethodNone {
		t.Fatal("expected a sync method to be applied")
	}
}

func TestSyncReader_basic(t *testing.T) {
	t.Parallel()
	refCues := testCues(20, 0, 5*time.Second)
	incCues := testCues(20, 2*time.Second, 5*time.Second)

	var refBuf, subBuf bytes.Buffer
	WriteSRT(&refBuf, refCues)
	WriteSRT(&subBuf, incCues)

	result, err := syncReader(context.Background(), "nonexistent.mkv", &subBuf, &refBuf, &Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Data) == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestSyncReader_no_reference(t *testing.T) {
	t.Parallel()
	cues := testCues(10, 0, 3*time.Second)
	var buf bytes.Buffer
	WriteSRT(&buf, cues)

	result, err := syncReader(context.Background(), "", &buf, nil, &Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Data) == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestOutputPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want string
	}{
		{"movie.fr.srt", "movie.fr.synced.srt"},
		{"sub.srt", "sub.synced.srt"},
		{"/path/to/movie.en.srt", "/path/to/movie.en.synced.srt"},
		{"subtitle", "subtitle.synced"},
		{"", ".synced"},
	}
	for _, tt := range tests {
		if got := OutputPath(tt.input); got != tt.want {
			t.Errorf("OutputPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSyncFile_encoding_normalization(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a UTF-16 LE BOM subtitle.
	srt := "1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"
	utf16le := encodeUTF16LE(srt)
	subPath := filepath.Join(dir, "sub.srt")
	if err := os.WriteFile(subPath, utf16le, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := SyncFile(context.Background(), "", subPath, &Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(result.Data), "Hello") {
		t.Fatal("expected 'Hello' in output after encoding normalization")
	}
}

func TestSyncFile_rejects_oversized_subtitle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subPath := filepath.Join(dir, "huge.srt")

	// Create a file just over the size limit.
	f, err := os.Create(subPath)
	if err != nil {
		t.Fatal(err)
	}
	// Write api.MaxSafeFileBytes + 1 bytes.
	if err := f.Truncate(api.MaxSafeFileBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	_, err = SyncFile(context.Background(), "", subPath, &Options{})
	if err == nil {
		t.Fatal("expected error for oversized subtitle")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected 'too large' error, got: %v", err)
	}
}

func TestSyncFile_rejects_oversized_reference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	subPath := filepath.Join(dir, "sub.srt")
	writeSRTFile(t, subPath, testCues(5, 0, 2*time.Second))

	refPath := filepath.Join(dir, "huge_ref.srt")
	f, err := os.Create(refPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(api.MaxSafeFileBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	_, err = SyncFile(context.Background(), "", subPath, &Options{
		Reference: refPath,
	})
	if err == nil {
		t.Fatal("expected error for oversized reference")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected 'too large' error, got: %v", err)
	}
}

func TestSyncReader_rejects_oversized_subtitle(t *testing.T) {
	t.Parallel()
	// Create a reader that produces more than api.MaxSafeFileBytes bytes.
	huge := make([]byte, api.MaxSafeFileBytes+1)
	_, err := syncReader(context.Background(), "", bytes.NewReader(huge), nil, &Options{})
	if err == nil {
		t.Fatal("expected error for oversized subtitle reader")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected 'too large' error, got: %v", err)
	}
}

func TestSyncReader_nil_opts(t *testing.T) {
	t.Parallel()
	cues := testCues(10, 0, 3*time.Second)
	var buf bytes.Buffer
	WriteSRT(&buf, cues)

	result, err := syncReader(context.Background(), "", &buf, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Data) == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestSyncReader_subtitle_read_error(t *testing.T) {
	t.Parallel()
	r := &errReaderImmediate{err: errors.New("read failed")}
	_, err := syncReader(context.Background(), "", r, nil, &Options{})
	if err == nil {
		t.Fatal("expected error for failing subtitle reader")
	}
	if !strings.Contains(err.Error(), "read subtitle") {
		t.Fatalf("expected 'read subtitle' error, got: %v", err)
	}
}

func TestSyncReader_reference_read_error(t *testing.T) {
	t.Parallel()
	cues := testCues(10, 0, 3*time.Second)
	var subBuf bytes.Buffer
	WriteSRT(&subBuf, cues)

	r := &errReaderImmediate{err: errors.New("ref read failed")}
	_, err := syncReader(context.Background(), "", &subBuf, r, &Options{})
	if err == nil {
		t.Fatal("expected error for failing reference reader")
	}
	if !strings.Contains(err.Error(), "read reference") {
		t.Fatalf("expected 'read reference' error, got: %v", err)
	}
}

func TestSyncReader_rejects_oversized_reference(t *testing.T) {
	t.Parallel()
	// Valid subtitle, oversized reference.
	cues := testCues(10, 0, 3*time.Second)
	var subBuf bytes.Buffer
	WriteSRT(&subBuf, cues)

	huge := make([]byte, api.MaxSafeFileBytes+1)
	_, err := syncReader(context.Background(), "", &subBuf, bytes.NewReader(huge), &Options{})
	if err == nil {
		t.Fatal("expected error for oversized reference reader")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected 'too large' error, got: %v", err)
	}
}

func TestSyncReader_with_postprocess(t *testing.T) {
	t.Parallel()
	refCues := testCues(20, 0, 5*time.Second)
	incCues := testCues(20, 2*time.Second, 5*time.Second)

	var refBuf, subBuf bytes.Buffer
	WriteSRT(&refBuf, refCues)
	WriteSRT(&subBuf, incCues)

	pp := allPostProcess()
	result, err := syncReader(context.Background(), "", &subBuf, &refBuf, &Options{
		PostProcess: &pp,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Data) == 0 {
		t.Fatal("expected non-empty output with post-processing")
	}
}

func TestSyncReader_empty_subtitle(t *testing.T) {
	t.Parallel()
	result, err := syncReader(context.Background(), "", strings.NewReader(""), nil, &Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Method != MethodNone {
		t.Fatalf("expected method 'none', got %q", result.Method)
	}
}

func TestSyncFile_with_postprocess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	refCues := testCues(20, 0, 5*time.Second)
	incCues := testCues(20, 2*time.Second, 5*time.Second)

	refPath := filepath.Join(dir, "ref.srt")
	subPath := filepath.Join(dir, "sub.srt")
	writeSRTFile(t, refPath, refCues)
	writeSRTFile(t, subPath, incCues)

	pp := allPostProcess()
	result, err := SyncFile(context.Background(), "", subPath, &Options{
		Reference:   refPath,
		PostProcess: &pp,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Data) == 0 {
		t.Fatal("expected non-empty output with post-processing")
	}
}

// --- Helpers ---

func testCues(n int, offset, gap time.Duration) []Cue {
	cues := make([]Cue, n)
	for i := range n {
		start := offset + time.Duration(i)*gap
		cues[i] = Cue{
			Start: start,
			End:   start + 2*time.Second,
			Text:  "test line",
		}
	}
	return cues
}

func writeSRTFile(t *testing.T, path string, cues []Cue) {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteSRT(&buf, cues); err != nil {
		t.Fatalf("write SRT: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func encodeUTF16LE(s string) []byte {
	// BOM + UTF-16 LE encoding of ASCII text.
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFE}) // BOM
	for _, r := range s {
		buf.WriteByte(byte(r))
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

// errReaderImmediate returns an error immediately on Read.
type errReaderImmediate struct {
	err error
}

func (r *errReaderImmediate) Read([]byte) (int, error) {
	return 0, r.err
}
