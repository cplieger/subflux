package archive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// zipEntry is a single file to include in a test zip archive.
type zipEntry struct {
	name    string
	content []byte
}

// makeZip creates a zip archive with the given files in order.
func makeZip(t *testing.T, files ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		fw, err := w.Create(f.name)
		if err != nil {
			t.Fatalf("zip.Create(%q): %v", f.name, err)
		}
		if _, err := fw.Write(f.content); err != nil {
			t.Fatalf("zip.Write(%q): %v", f.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

func TestExtractFromZip(t *testing.T) {
	t.Parallel()

	srt := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	ass := []byte("[Script Info]\nTitle: Test\n")
	ssa := []byte("[Script Info]\nTitle: SSA\n")
	sub := []byte("{0}{100}Hello\n")

	tests := []struct {
		name string
		data []byte
		want []byte
	}{
		// Supported subtitle extensions.
		{"extracts srt", makeZip(t, zipEntry{"subtitle.srt", srt}), srt},
		{"extracts ass", makeZip(t, zipEntry{"subtitle.ass", ass}), ass},
		{"extracts ssa", makeZip(t, zipEntry{"subtitle.ssa", ssa}), ssa},
		{"extracts sub", makeZip(t, zipEntry{"subtitle.sub", sub}), sub},
		{"case insensitive extension", makeZip(t, zipEntry{"subtitle.SRT", srt}), srt},

		// Subtitle in subdirectory.
		{"extracts from subdirectory", makeZip(t, zipEntry{"subs/subtitle.srt", srt}), srt},

		// First subtitle wins when multiple are present.
		{"returns first subtitle", makeZip(t,
			zipEntry{"first.srt", srt},
			zipEntry{"second.ass", ass},
		), srt},

		// Filtering behavior.
		{"skips non-subtitle files", makeZip(t,
			zipEntry{"readme.txt", []byte("not a subtitle")},
			zipEntry{"subtitle.srt", srt},
		), srt},
		{"skips hidden files", makeZip(t,
			zipEntry{".hidden.srt", []byte("hidden subtitle")},
			zipEntry{"visible.srt", srt},
		), srt},
		{"only hidden subtitles returns nil", makeZip(t,
			zipEntry{".hidden.srt", []byte("hidden subtitle")},
		), nil},
		{"extracts vtt subtitle", makeZip(t,
			zipEntry{"subtitle.vtt", []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n")},
		), []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n")},

		// Nil return cases.
		{"no subtitles returns nil", makeZip(t,
			zipEntry{"readme.txt", []byte("no subtitles here")},
		), nil},
		{"invalid zip returns nil", []byte("not a zip file"), nil},
		{"nil data returns nil", nil, nil},
		{"empty data returns nil", []byte{}, nil},
		{"empty zip returns nil", makeZip(t), nil},
		{"empty subtitle content returns nil", makeZip(t,
			zipEntry{"empty.srt", []byte{}},
		), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractFromZip(tt.data, 0, 0)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ExtractFromZip(%d bytes) = %q, want %q",
					len(tt.data), got, tt.want)
			}
		})
	}
}

// TestextractFromZip_rejects_zip_bomb verifies that entries with a declared
// uncompressed size exceeding 50x the compressed size are skipped.
func TestExtractFromZip_rejects_zip_bomb(t *testing.T) {
	t.Parallel()

	// Build a valid zip with a small subtitle, then patch the uncompressed
	// size in the central directory to trigger the zip bomb guard (ratio > 50).
	// Go's zip.NewReader reads sizes from the central directory, so only
	// that header needs patching.
	content := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	data := makeZip(t, zipEntry{"subtitle.srt", content})

	// Central directory entry stores uncompressed size at offset 24
	// (4 bytes, little-endian) from its signature (0x02014b50).
	fakeUncompressed := uint32(len(content)) * 100 // 100x > 50x threshold
	centralIdx := bytes.Index(data, []byte("PK\x01\x02"))
	if centralIdx < 0 {
		t.Fatal("central directory header not found")
	}
	binary.LittleEndian.PutUint32(data[centralIdx+24:centralIdx+28], fakeUncompressed)

	got := ExtractFromZip(data, 0, 0)
	if got != nil {
		t.Errorf("ExtractFromZip() = %q, want nil (zip bomb rejected)", got)
	}
}

// TestextractFromZip_rejects_zero_compressed verifies that entries with
// zero compressed size but non-zero uncompressed size are rejected.
func TestExtractFromZip_rejects_zero_compressed(t *testing.T) {
	t.Parallel()

	content := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	data := makeZip(t, zipEntry{"subtitle.srt", content})

	centralIdx := bytes.Index(data, []byte("PK\x01\x02"))
	if centralIdx < 0 {
		t.Fatal("central directory header not found")
	}
	// Set compressed size to 0 (offset 20) while keeping uncompressed > 0.
	binary.LittleEndian.PutUint32(data[centralIdx+20:centralIdx+24], 0)

	got := ExtractFromZip(data, 0, 0)
	if got != nil {
		t.Errorf("ExtractFromZip() = %q, want nil (zero compressed rejected)", got)
	}
}

// TestextractFromZip_rejects_oversized verifies that subtitle content
// exceeding MaxExtractSize is rejected rather than silently truncated.
func TestExtractFromZip_rejects_oversized(t *testing.T) {
	t.Parallel()

	// Create content one byte over the 5 MB limit.
	// Use Store method (no compression) to avoid triggering the zip bomb
	// ratio guard, which rejects high compression ratios.
	content := make([]byte, MaxExtractSize+1)
	for i := range content {
		content[i] = byte(i)
	}
	data := makeZipStored(t, zipEntry{"subtitle.srt", content})

	got := ExtractFromZip(data, 0, 0)
	if got != nil {
		t.Errorf("ExtractFromZip() returned %d bytes, want nil (oversized rejected)", len(got))
	}
}

// TestextractFromZip_accepts_at_limit verifies that subtitle content
// exactly at MaxExtractSize is accepted.
func TestExtractFromZip_accepts_at_limit(t *testing.T) {
	t.Parallel()

	content := make([]byte, MaxExtractSize)
	for i := range content {
		content[i] = byte(i)
	}
	data := makeZipStored(t, zipEntry{"subtitle.srt", content})

	got := ExtractFromZip(data, 0, 0)
	if !bytes.Equal(got, content) {
		t.Errorf("ExtractFromZip() returned %d bytes, want %d (at-limit accepted)",
			len(got), len(content))
	}
}

// PBT: extractFromZip round-trips; creating a zip with a single .srt file
// and extracting it returns the original content.
func TestExtractFromZip_roundtrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Generate non-empty subtitle content (1-500 bytes of printable ASCII).
		content := []byte(rapid.StringMatching(`[a-zA-Z0-9 \n]{1,500}`).Draw(t, "content"))

		data := makeZipForPBT(t, zipEntry{"subtitle.srt", content})

		got := ExtractFromZip(data, 0, 0)

		if !bytes.Equal(got, content) {
			t.Errorf("extractFromZip round-trip failed: got %d bytes, want %d bytes",
				len(got), len(content))
		}
	})
}

// makeZipForPBT creates a zip archive for use in rapid property tests.
// Uses rapid.T for test context.
func makeZipForPBT(t *rapid.T, files ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		fw, err := w.Create(f.name)
		if err != nil {
			t.Fatalf("zip.Create(%q): %v", f.name, err)
		}
		if _, err := fw.Write(f.content); err != nil {
			t.Fatalf("zip.Write(%q): %v", f.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// makeZipStored creates a zip archive using Store method (no compression).
// This avoids triggering the zip bomb ratio guard for large test content.
func makeZipStored(t *testing.T, files ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		hdr := &zip.FileHeader{Name: f.name, Method: zip.Store}
		fw, err := w.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", f.name, err)
		}
		if _, err := fw.Write(f.content); err != nil {
			t.Fatalf("zip.Write(%q): %v", f.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// --- Episode-aware extraction ---

func TestExtractFromZip_episode_matching(t *testing.T) {
	t.Parallel()

	e01 := []byte("episode 1 subtitle")
	e02 := []byte("episode 2 subtitle")
	e03 := []byte("episode 3 subtitle")

	data := makeZip(t,
		zipEntry{"Show S01E01.srt", e01},
		zipEntry{"Show S01E02.srt", e02},
		zipEntry{"Show S01E03.srt", e03},
	)

	t.Run("extracts matching episode", func(t *testing.T) {
		t.Parallel()
		got := ExtractFromZip(data, 1, 2)
		if !bytes.Equal(got, e02) {
			t.Errorf("ExtractFromZip(S01E02) = %q, want %q", got, e02)
		}
	})

	t.Run("extracts first episode", func(t *testing.T) {
		t.Parallel()
		got := ExtractFromZip(data, 1, 1)
		if !bytes.Equal(got, e01) {
			t.Errorf("ExtractFromZip(S01E01) = %q, want %q", got, e01)
		}
	})

	t.Run("extracts last episode", func(t *testing.T) {
		t.Parallel()
		got := ExtractFromZip(data, 1, 3)
		if !bytes.Equal(got, e03) {
			t.Errorf("ExtractFromZip(S01E03) = %q, want %q", got, e03)
		}
	})

	t.Run("no match returns nil", func(t *testing.T) {
		t.Parallel()
		got := ExtractFromZip(data, 1, 99)
		if got != nil {
			t.Errorf("ExtractFromZip(S01E99) = %q, want nil (no fallback)", got)
		}
	})

	t.Run("zero episode returns first", func(t *testing.T) {
		t.Parallel()
		got := ExtractFromZip(data, 0, 0)
		if !bytes.Equal(got, e01) {
			t.Errorf("ExtractFromZip(0,0) = %q, want %q", got, e01)
		}
	})

	t.Run("wrong season returns nil", func(t *testing.T) {
		t.Parallel()
		got := ExtractFromZip(data, 2, 1)
		if got != nil {
			t.Errorf("ExtractFromZip(S02E01) = %q, want nil (no fallback)", got)
		}
	})

	t.Run("season only falls back to first", func(t *testing.T) {
		t.Parallel()
		got := ExtractFromZip(data, 1, 0)
		if !bytes.Equal(got, e01) {
			t.Errorf("ExtractFromZip(1,0) = %q, want %q (fallback to first)", got, e01)
		}
	})

	t.Run("episode only falls back to first", func(t *testing.T) {
		t.Parallel()
		got := ExtractFromZip(data, 0, 1)
		if !bytes.Equal(got, e01) {
			t.Errorf("ExtractFromZip(0,1) = %q, want %q (fallback to first)", got, e01)
		}
	})
}

func TestMatchesEpisode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		season  int
		episode int
		want    bool
	}{
		{"standard match", "Show S01E05.srt", 1, 5, true},
		{"case insensitive", "show s02e10.srt", 2, 10, true},
		{"nested path", "subs/Show S01E03.srt", 1, 3, true},
		{"wrong episode", "Show S01E05.srt", 1, 6, false},
		{"wrong season", "Show S02E05.srt", 1, 5, false},
		{"no pattern", "subtitle.srt", 1, 1, false},
		{"multi-ep E01E02 match first", "Show S01E01E02.srt", 1, 1, true},
		{"multi-ep E01E02 match second", "Show S01E01E02.srt", 1, 2, true},
		{"multi-ep E01E02 no match", "Show S01E01E02.srt", 1, 3, false},
		{"multi-ep E01-E02 match", "Show S01E01-E02.srt", 1, 2, true},
		{"multi-ep E03-E04 match", "Show S01E03-E04.srt", 1, 3, true},
		{"multi-ep E01-02 match", "Show S01E01-02.srt", 1, 2, true},
		{"single ep not false multi-ep", "Show S01E05.srt", 1, 3, false},

		// Dot separator in multi-episode ranges.
		{"multi-ep E01.E02 dot separator", "Show S01E01.E02.srt", 1, 2, true},
		{"multi-ep E01.02 dot no E prefix", "Show S01E01.02.srt", 1, 2, true},

		// Triple-episode: multiEpRe only matches first pair.
		{"triple-ep first", "Show S01E01E02E03.srt", 1, 1, true},
		{"triple-ep second", "Show S01E01E02E03.srt", 1, 2, true},
		{"triple-ep third not matched", "Show S01E01E02E03.srt", 1, 3, false},

		// False positive guards (ep2 > 999, span > 50).
		{"year in title not false positive", "Show.1923.S01E01.1923.REPACK.srt", 1, 1923, false},
		{"resolution not false positive", "Show.S01E05.720p.srt", 1, 720, false},
		{"rejects ep2 over 999", "Show.S01E01.E1000.srt", 1, 500, false},
		{"rejects span over 50", "Show.S01E01-E99.srt", 1, 50, false},
		{"accepts span exactly 50", "Show.S01E01-E51.srt", 1, 25, true},

		// Multi-digit seasons and high episode numbers.
		{"multi-digit season", "Show S10E05.srt", 10, 5, true},
		{"high episode number", "Show S01E100.srt", 1, 100, true},
		{"three-digit season and episode", "Show S100E200.srt", 100, 200, true},

		// Multiple S##E## patterns in one filename.
		{"multiple SxxExx matches second", "Show S01E01 - S01E02.srt", 1, 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MatchesEpisode(tt.path, tt.season, tt.episode)
			if got != tt.want {
				t.Errorf("MatchesEpisode(%q, %d, %d) = %v, want %v",
					tt.path, tt.season, tt.episode, got, tt.want)
			}
		})
	}
}

// TestExtractFromZip_accepts_exactly_max_entries verifies the inclusive
// central-directory cap: a zip with exactly maxZipEntries valid entries must
// still be processed (the guard rejects only len > maxZipEntries).
func TestExtractFromZip_accepts_exactly_max_entries(t *testing.T) {
	t.Parallel()
	first := []byte("first subtitle\n")
	entries := make([]zipEntry, maxZipEntries)
	entries[0] = zipEntry{name: "sub_0000.srt", content: first}
	for i := 1; i < maxZipEntries; i++ {
		entries[i] = zipEntry{name: fmt.Sprintf("sub_%04d.srt", i), content: []byte("x\n")}
	}
	data := makeZip(t, entries...)

	got := ExtractFromZip(data, 0, 0)
	if !bytes.Equal(got, first) {
		t.Fatalf("ExtractFromZip(%d-entry zip) = %q, want %q "+
			"(exactly maxZipEntries entries must be accepted)", maxZipEntries, got, first)
	}
}

// TestIsValidSubtitleEntry_size_guards covers the decompression-bomb size
// guards: a zero compressed size with positive uncompressed is rejected, a
// compression ratio above 50 is rejected, while the inclusive ratio boundary
// (exactly 50) and an empty entry (both sizes zero, which must not divide by
// zero) are accepted.
func TestIsValidSubtitleEntry_size_guards(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		comp, uncomp uint64
		want         bool
	}{
		{"zero compressed with positive uncompressed rejected", 0, 10, false},
		{"both sizes zero accepted", 0, 0, true},
		{"ratio exactly 50 accepted", 1, 50, true},
		{"ratio over 50 rejected", 1, 1000, false},
		{"normal entry accepted", 100, 200, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &zip.File{FileHeader: zip.FileHeader{
				Name:               "a.srt",
				CompressedSize64:   tc.comp,
				UncompressedSize64: tc.uncomp,
			}}
			if got := IsValidSubtitleEntry(f); got != tc.want {
				t.Errorf("IsValidSubtitleEntry(comp=%d, uncomp=%d) = %v, want %v",
					tc.comp, tc.uncomp, got, tc.want)
			}
		})
	}
}

func TestMatchesMultiEpisodeRange(t *testing.T) {
	t.Parallel()
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

		// Every range in the name is scanned, not just the first: episode 6
		// lies only in the second range [5,8].
		{"episode in second of two ranges", "Show.E01E02.and.E05E08.srt", 6, true},
		{"episode in first of two ranges", "Show.E01E02.and.E05E08.srt", 1, true},

		// ep2 == 999 is the inclusive top of the accepted range (the year/range
		// guard rejects only ep2 > 999); ep2 == 1000 exceeds the cap.
		{"ep2 exactly 999 accepted", "E950E999", 975, true},
		{"ep2 1000 exceeds cap rejected", "E950E1000", 975, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchesMultiEpisodeRange(tt.file, tt.episode); got != tt.want {
				t.Errorf("MatchesMultiEpisodeRange(%q, %d) = %v, want %v",
					tt.file, tt.episode, got, tt.want)
			}
		})
	}
}
