package provider

import (
	"archive/zip"
	"bytes"
	"testing"
)

// srtInZip packs one subtitle file into a zip archive, the shape most providers
// hand back for a download.
func srtInZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, err := w.Create(name)
	if err != nil {
		t.Fatalf("zip.Create(%q): %v", name, err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("zip.Write(%q): %v", name, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// TestExtractAndValidate_returnsTheArchivedSubtitle asserts an archived payload
// yields the subtitle from INSIDE the archive: the raw fallback is for bare
// subtitle bodies, and returning the container bytes would hand a binary blob to
// the writer.
func TestExtractAndValidate_returnsTheArchivedSubtitle(t *testing.T) {
	t.Parallel()
	want := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	archived := srtInZip(t, "movie.srt", want)

	got, err := ExtractAndValidate(archived, 0, 0)
	if err != nil {
		t.Fatalf("ExtractAndValidate(zip) unexpected error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("ExtractAndValidate(zip) = %q, want the archived subtitle %q", got, want)
	}
}

// TestExtractAndValidate_fallsBackToTheRawBody asserts a bare subtitle body (no
// archive) passes through unchanged.
func TestExtractAndValidate_fallsBackToTheRawBody(t *testing.T) {
	t.Parallel()
	want := []byte("1\n00:00:01,000 --> 00:00:02,000\nBare\n")

	got, err := ExtractAndValidate(want, 0, 0)
	if err != nil {
		t.Fatalf("ExtractAndValidate(raw) unexpected error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("ExtractAndValidate(raw) = %q, want %q", got, want)
	}
}
