package archive

import (
	"bytes"
	"testing"
)

func TestLooksLikeSubtitle(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"valid SRT", []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), true},
		{"valid ASS", []byte("[Script Info]\nTitle: Test\n"), true},
		{"valid VTT", []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nHi\n"), true},
		{"UTF-8 BOM prefix", []byte{0xEF, 0xBB, 0xBF, '1', '\n', '0', '0', ':', '0', '0', ':', '0', '1', ',', '0', '0', '0', ' ', '-', '-', '>', ' ', '0', '0', ':', '0', '0', ':', '0', '2', ',', '0', '0', '0', '\n'}, true},
		{"BOM only with no content", []byte{0xEF, 0xBB, 0xBF}, false},
		{"empty data", []byte{}, false},
		{"binary data", []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x80, 0x81, 0x82, 0x83}, false},
		{"signature after 4KB", append(make([]byte, 4097), []byte(" --> ")...), false},
		// One non-text byte in ten keeps the ratio at the inclusive limit
		// (nonText*10 == len), which must still be accepted; the guard rejects
		// only strictly-higher ratios.
		{"non-text ratio exactly at threshold accepted", append([]byte(" --> abcd"), 0x00), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeSubtitle(tt.data); got != tt.want {
				t.Errorf("LooksLikeSubtitle() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLooksLikeSubtitle_signature_within_4kb_of_large_input(t *testing.T) {
	t.Parallel()
	// Signature at byte 100 (within the 4KB probe window) must be found even
	// though the input is much larger than 4KB.
	data := make([]byte, 5000)
	for i := range data {
		data[i] = 'A'
	}
	copy(data[100:], []byte(" --> "))

	if !LooksLikeSubtitle(data) {
		t.Error("LooksLikeSubtitle(large input, signature within 4KB) = false, want true")
	}
}

func TestLooksLikeSubtitle_signature_beyond_4kb_returns_false(t *testing.T) {
	t.Parallel()
	// Signature placed beyond the 4KB probe window must not be found.
	data := make([]byte, 5000)
	for i := range data {
		data[i] = 'A'
	}
	copy(data[4500:], []byte(" --> "))

	if LooksLikeSubtitle(data) {
		t.Error("LooksLikeSubtitle(signature beyond 4KB) = true, want false")
	}
}

func TestLooksLikeSubtitle_high_non_text_ratio_returns_false(t *testing.T) {
	t.Parallel()
	// More than 10% non-text bytes, even with a subtitle signature present:
	// the non-text check rejects it before signature matching.
	data := bytes.Repeat([]byte{0x01}, 50)
	data = append(data, []byte(" --> ")...)
	data = append(data, bytes.Repeat([]byte{0x01}, 50)...)

	if LooksLikeSubtitle(data) {
		t.Error("LooksLikeSubtitle(high non-text ratio with signature) = true, want false")
	}
}

func TestHasArchiveSignature(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"ZIP magic", []byte{'P', 'K', 3, 4, 0, 0}, true},
		{"RAR magic", []byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x00}, true},
		{"plain text", []byte("hello world"), false},
		{"short data", []byte{0x50, 0x4B}, false},
		// Magic-length boundaries: exactly the magic length must qualify.
		{"ZIP magic exactly 4 bytes", []byte{'P', 'K', 3, 4}, true},
		{"ZIP three bytes too short", []byte{'P', 'K', 3}, false},
		{"ZIP wrong fourth byte", []byte{'P', 'K', 3, 5}, false},
		{"RAR magic exactly 6 bytes", []byte{'R', 'a', 'r', '!', 0x1a, 0x07}, true},
		{"RAR five bytes too short", []byte{'R', 'a', 'r', '!', 0x1a}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasArchiveSignature(tt.data); got != tt.want {
				t.Errorf("HasArchiveSignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool // true = non-nil result
	}{
		{"raw SRT passthrough", []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), true},
		{"unrecognized binary", []byte{0x00, 0x01, 0x02, 0x03, 0x80, 0x81, 0x82, 0x83, 0x84, 0x85}, false},
		{"empty data", []byte{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Extract(tt.data, 1, 1)
			if (got != nil) != tt.want {
				t.Errorf("Extract() nil=%v, want nil=%v", got == nil, !tt.want)
			}
		})
	}
}

// TestExtract_default_branch_zip exercises the unknown-magic branch of Extract:
// a one-byte prefix makes HasArchiveSignature false (so neither the ZIP nor RAR
// fast path is taken), yet Go's archive/zip still reads the prefixed archive, so
// the default branch's ZIP probe must succeed and return the subtitle.
func TestExtract_default_branch_zip(t *testing.T) {
	t.Parallel()
	content := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	z := makeZip(t, zipEntry{name: "sub.srt", content: content})
	prefixed := append([]byte{'X'}, z...)

	if HasArchiveSignature(prefixed) {
		t.Fatalf("prefixed zip unexpectedly has an archive signature (test setup)")
	}
	got := Extract(prefixed, 0, 0)
	if !bytes.Equal(got, content) {
		t.Fatalf("Extract(prefixed zip) = %q, want %q", got, content)
	}
}

// TestExtract_default_branch_rar exercises the unknown-magic branch for RAR: a
// byte prefix makes HasArchiveSignature false, and if rardecode still reads the
// prefixed RAR the default branch's RAR probe must return the subtitle.
func TestExtract_default_branch_rar(t *testing.T) {
	t.Parallel()
	rar := loadRARFixture(t)
	prefixed := append([]byte("ZZZZ"), rar...)

	if HasArchiveSignature(prefixed) {
		t.Fatalf("prefixed rar unexpectedly has an archive signature (test setup)")
	}
	got := Extract(prefixed, 0, 0)
	if got == nil {
		t.Fatalf("Extract(prefixed rar) = nil, want extracted subtitle")
	}
}
