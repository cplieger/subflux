package provider

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/epmarker"
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

	got, err := ExtractAndValidate(archived, epmarker.Any())
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

	got, err := ExtractAndValidate(want, epmarker.Any())
	if err != nil {
		t.Fatalf("ExtractAndValidate(raw) unexpected error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("ExtractAndValidate(raw) = %q, want %q", got, want)
	}
}

// TestExtractAndValidate_reportsTheArchivesOwnRefusal asserts a zip that opened
// and had nothing for this episode is reported as such. subtitlefile.Validate
// can only answer "detected zip archive" for those bytes, which reads as "this
// build cannot open archives" for a payload the extractor unpacked and walked
// member by member, and that misreading is what the message has to avoid.
func TestExtractAndValidate_reportsTheArchivesOwnRefusal(t *testing.T) {
	t.Parallel()
	archived := srtInZip(t, "Show.S03E02.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"))

	got, err := ExtractAndValidate(archived, epmarker.For(epmarker.Marker{Season: 3, Episode: 9}))
	if got != nil {
		t.Errorf("ExtractAndValidate(pack without S03E09) = %q, want nil", got)
	}
	if err == nil {
		t.Fatal("ExtractAndValidate(pack without S03E09) err = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "S03E09") {
		t.Errorf("err = %q, want the requested episode named", err)
	}
	if !strings.Contains(err.Error(), "S03E02.srt") {
		t.Errorf("err = %q, want the member it did hold named", err)
	}
	if strings.Contains(err.Error(), "binary archive data") {
		t.Errorf("err = %q, want the extractor's refusal rather than Validate's "+
			"container verdict", err)
	}
}

// TestExtractAndValidate_unreadableContainerStillDescribesTheBytes asserts the
// ErrNotArchive fallback survives: when nothing here can open the payload, the
// raw bytes are the whole story and Validate is what names them.
func TestExtractAndValidate_unreadableContainerStillDescribesTheBytes(t *testing.T) {
	t.Parallel()
	// 7z magic: a real container, and one this package deliberately does not
	// unpack, so Validate's format name is the most useful thing anyone can say.
	sevenZip := append([]byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}, bytes.Repeat([]byte{0x00, 0x81}, 300)...)

	got, err := ExtractAndValidate(sevenZip, epmarker.For(epmarker.Marker{Season: 1, Episode: 1}))
	if got != nil {
		t.Errorf("ExtractAndValidate(7z) = %d bytes, want nil", len(got))
	}
	if err == nil {
		t.Fatal("ExtractAndValidate(7z) err = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "7z") {
		t.Errorf("err = %q, want the detected container format named", err)
	}
}
