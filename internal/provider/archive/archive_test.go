package archive

import (
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
		{"empty data", []byte{}, false},
		{"binary data", []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x80, 0x81, 0x82, 0x83}, false},
		{"signature after 4KB", append(make([]byte, 4097), []byte(" --> ")...), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeSubtitle(tt.data); got != tt.want {
				t.Errorf("LooksLikeSubtitle() = %v, want %v", got, tt.want)
			}
		})
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

func TestMatchesMultiEpisodeRange(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		episode int
		want    bool
	}{
		{"in range", "Show.S01E01-E05.srt", 3, true},
		{"out of range", "Show.S01E01-E05.srt", 6, false},
		{"start of range", "Show.S01E01-E05.srt", 1, true},
		{"end of range", "Show.S01E01-E05.srt", 5, true},
		{"not a range", "Show.S01E05.srt", 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchesMultiEpisodeRange(tt.file, tt.episode); got != tt.want {
				t.Errorf("MatchesMultiEpisodeRange(%q, %d) = %v, want %v", tt.file, tt.episode, got, tt.want)
			}
		})
	}
}
