package archive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/epmarker"
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

func TestZipExtract(t *testing.T) {
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
			got, _ := zipExtract(tt.data, epmarker.Any())
			if !bytes.Equal(got, tt.want) {
				t.Errorf("zipExtract(%d bytes) = %q, want %q",
					len(tt.data), got, tt.want)
			}
		})
	}
}

// TestExtractFromZip_rejects_zip_bomb verifies that entries with a declared
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

	got, _ := zipExtract(data, epmarker.Any())
	if got != nil {
		t.Errorf("zipExtract() = %q, want nil (zip bomb rejected)", got)
	}
}

// TestExtractFromZip_rejects_zero_compressed verifies that entries with
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

	got, _ := zipExtract(data, epmarker.Any())
	if got != nil {
		t.Errorf("zipExtract() = %q, want nil (zero compressed rejected)", got)
	}
}

// TestExtractFromZip_rejects_oversized verifies that subtitle content
// exceeding maxExtractSize is rejected rather than silently truncated.
func TestExtractFromZip_rejects_oversized(t *testing.T) {
	t.Parallel()

	// Create content one byte over the 5 MB limit.
	// Use Store method (no compression) to avoid triggering the zip bomb
	// ratio guard, which rejects high compression ratios.
	content := make([]byte, maxExtractSize+1)
	for i := range content {
		content[i] = byte(i)
	}
	data := makeZipStored(t, zipEntry{"subtitle.srt", content})

	got, _ := zipExtract(data, epmarker.Any())
	if got != nil {
		t.Errorf("zipExtract() returned %d bytes, want nil (oversized rejected)", len(got))
	}
}

// TestExtractFromZip_accepts_at_limit verifies that subtitle content
// exactly at maxExtractSize is accepted.
func TestExtractFromZip_accepts_at_limit(t *testing.T) {
	t.Parallel()

	content := make([]byte, maxExtractSize)
	for i := range content {
		content[i] = byte(i)
	}
	data := makeZipStored(t, zipEntry{"subtitle.srt", content})

	got, _ := zipExtract(data, epmarker.Any())
	if !bytes.Equal(got, content) {
		t.Errorf("zipExtract() returned %d bytes, want %d (at-limit accepted)",
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

		got, _ := zipExtract(data, epmarker.Any())

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
		got, _ := zipExtract(data, epmarker.For(epmarker.Marker{Season: 1, Episode: 2}))
		if !bytes.Equal(got, e02) {
			t.Errorf("zipExtract(S01E02) = %q, want %q", got, e02)
		}
	})

	t.Run("extracts first episode", func(t *testing.T) {
		t.Parallel()
		got, _ := zipExtract(data, epmarker.For(epmarker.Marker{Season: 1, Episode: 1}))
		if !bytes.Equal(got, e01) {
			t.Errorf("zipExtract(S01E01) = %q, want %q", got, e01)
		}
	})

	t.Run("extracts last episode", func(t *testing.T) {
		t.Parallel()
		got, _ := zipExtract(data, epmarker.For(epmarker.Marker{Season: 1, Episode: 3}))
		if !bytes.Equal(got, e03) {
			t.Errorf("zipExtract(S01E03) = %q, want %q", got, e03)
		}
	})

	t.Run("no match returns nil", func(t *testing.T) {
		t.Parallel()
		got, _ := zipExtract(data, epmarker.For(epmarker.Marker{Season: 1, Episode: 99}))
		if got != nil {
			t.Errorf("zipExtract(S01E99) = %q, want nil (no fallback)", got)
		}
	})

	t.Run("zero episode returns first", func(t *testing.T) {
		t.Parallel()
		got, _ := zipExtract(data, epmarker.Any())
		if !bytes.Equal(got, e01) {
			t.Errorf("zipExtract(0,0) = %q, want %q", got, e01)
		}
	})

	t.Run("wrong season returns nil", func(t *testing.T) {
		t.Parallel()
		got, _ := zipExtract(data, epmarker.For(epmarker.Marker{Season: 2, Episode: 1}))
		if got != nil {
			t.Errorf("zipExtract(S02E01) = %q, want nil (no fallback)", got)
		}
	})

	t.Run("season only falls back to first", func(t *testing.T) {
		t.Parallel()
		got, _ := zipExtract(data, epmarker.Any())
		if !bytes.Equal(got, e01) {
			t.Errorf("zipExtract(1,0) = %q, want %q (fallback to first)", got, e01)
		}
	})

	t.Run("episode only falls back to first", func(t *testing.T) {
		t.Parallel()
		got, _ := zipExtract(data, epmarker.Any())
		if !bytes.Equal(got, e01) {
			t.Errorf("zipExtract(0,1) = %q, want %q (fallback to first)", got, e01)
		}
	})
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

	got, _ := zipExtract(data, epmarker.Any())
	if !bytes.Equal(got, first) {
		t.Fatalf("zipExtract(%d-entry zip) = %q, want %q "+
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
			f := &zip.File{
				Name:               "a.srt",
				CompressedSize64:   tc.comp,
				UncompressedSize64: tc.uncomp,
			}
			if got := gateZipEntry(f); got != tc.want {
				t.Errorf("gateZipEntry(comp=%d, uncomp=%d) = %v, want %v",
					tc.comp, tc.uncomp, got, tc.want)
			}
		})
	}
}

// makeZipUnreadableMethod builds a zip whose members are stored under a
// compression method the READER has no decompressor for. That is not a
// contrived shape: Go's archive/zip implements Store and Deflate only, so any
// pack an uploader compressed with LZMA (method 14) or PPMd reads its central
// directory perfectly and then fails at Open.
func makeZipUnreadableMethod(t *testing.T, names ...string) []byte {
	t.Helper()
	const lzma = 14
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// The bytes are written through verbatim; only the recorded method matters,
	// because the reader rejects the entry before it decompresses anything.
	w.RegisterCompressor(lzma, func(out io.Writer) (io.WriteCloser, error) {
		return nopWriteCloser{out}, nil
	})
	for _, name := range names {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: lzma})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := fw.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")); err != nil {
			t.Fatalf("zip.Write(%q): %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// TestExtractFromZip_matched_member_that_cannot_be_read_says_so pins the
// distinction the old nil return could not express: the pack IS the right one
// and its episode IS present, so reporting "no member for S01E08" would send
// the reader looking for a numbering problem that does not exist.
func TestExtractFromZip_matched_member_that_cannot_be_read_says_so(t *testing.T) {
	t.Parallel()
	data := makeZipUnreadableMethod(t, "Black.Sails.S01E08.BDRip.x264-DEMAND.srt")

	got, err := zipExtract(data, epmarker.For(epmarker.Marker{Season: 1, Episode: 8}))
	if got != nil {
		t.Errorf("zipExtract(unreadable member) = %q, want nil", got)
	}
	if err == nil {
		t.Fatal("zipExtract(unreadable member) err = nil, want a read failure")
	}
	if !strings.Contains(err.Error(), "failed to read") {
		t.Errorf("zipExtract(unreadable member) err = %q, want it to report a read failure", err)
	}
	if !errors.Is(err, zip.ErrAlgorithm) {
		t.Errorf("zipExtract(unreadable member) err = %v, want it to wrap zip.ErrAlgorithm", err)
	}
	if errors.Is(err, ErrNotArchive) {
		t.Errorf("zipExtract(unreadable member) err = %v, want NOT ErrNotArchive: "+
			"the zip opened, so the caller must not fall back to describing the raw bytes", err)
	}
}

// TestExtractFromZip_reads_the_cross_notation_pack is the whole point of teaching
// epmarker the ##x## form, stated at the level a user feels it. This is a real
// SubSource pack's naming: ten members, every one of them numbered this way, and
// before the notation was read the pack was unusable for every episode it held.
func TestExtractFromZip_reads_the_cross_notation_pack(t *testing.T) {
	t.Parallel()
	data := makeZip(t,
		zipEntry{"Black Sails - 01x08 - VIII..720p 2HD.English.srt", []byte("ep 8")},
		zipEntry{"Black Sails - 01x09 - IX..720p 2HD.English.srt", []byte("ep 9")},
	)

	got, err := zipExtract(data, episode(1, 8))
	if err != nil {
		t.Fatalf("zipExtract(1x08-named pack, S01E08) error = %v, want the S01E08 member", err)
	}
	if string(got) != "ep 8" {
		t.Errorf("zipExtract(1x08-named pack, S01E08) = %q, want %q", got, "ep 8")
	}

	// The other episode in the same pack, so the reader is selecting rather than
	// taking the first member and getting lucky.
	got, err = zipExtract(data, episode(1, 9))
	if err != nil {
		t.Fatalf("zipExtract(1x08-named pack, S01E09) error = %v, want the S01E09 member", err)
	}
	if string(got) != "ep 9" {
		t.Errorf("zipExtract(1x08-named pack, S01E09) = %q, want %q", got, "ep 9")
	}
}

// TestExtractFromZip_no_episode_match_names_the_members pins the member list in
// the refusal. Without it an unsupported numbering scheme and a wrong-season
// pack produce the same log line, which is what forced fetching the archive by
// hand to tell them apart.
func TestExtractFromZip_no_episode_match_names_the_members(t *testing.T) {
	t.Parallel()
	// A wrong-season pack: the members are perfectly readable and numbered in a
	// notation this code understands, and none of them is the episode asked for.
	data := makeZip(t,
		zipEntry{"Black Sails - 03x01 - I..720p.English.srt", []byte("s3 ep 1")},
		zipEntry{"Black Sails - 03x02 - II..720p.English.srt", []byte("s3 ep 2")},
	)

	got, err := zipExtract(data, episode(1, 8))
	if got != nil {
		t.Errorf("zipExtract(season-3 pack, S01E08) = %q, want nil (no fallback)", got)
	}
	if err == nil {
		t.Fatal("zipExtract(season-3 pack, S01E08) err = nil, want a no-match refusal")
	}
	if !strings.Contains(err.Error(), "S01E08") {
		t.Errorf("err = %q, want the target episode named", err)
	}
	if !strings.Contains(err.Error(), "03x01") {
		t.Errorf("err = %q, want the member names listed", err)
	}
}

// TestExtractFromZip_a_directory_name_does_not_decide_the_episode pins the one
// piece of episode policy this package keeps for itself. epmarker reads a whole
// name, so a member sitting under a directory named for another episode claims
// both; narrowing to the last path element is what stops the directory answering
// for the file, and getting it wrong writes one episode's subtitle under another
// episode's name.
func TestExtractFromZip_a_directory_name_does_not_decide_the_episode(t *testing.T) {
	t.Parallel()
	data := makeZip(t, zipEntry{"Show.S02E09.PACK/Show.S01E03.srt", []byte("s1 ep 3")})

	got, err := zipExtract(data, episode(1, 3))
	if err != nil {
		t.Fatalf("zipExtract(member under a differently-numbered directory, S01E03) "+
			"error = %v, want the member", err)
	}
	if string(got) != "s1 ep 3" {
		t.Errorf("zipExtract(..., S01E03) = %q, want %q", got, "s1 ep 3")
	}

	if _, err := zipExtract(data, episode(2, 9)); err == nil {
		t.Error("zipExtract(..., S02E09) succeeded, want a refusal: S02E09 names the " +
			"DIRECTORY, and the only member in it is S01E03")
	}
}

// TestSummarizeNames_truncates_and_sanitizes covers the two hostile properties
// of a member list that reaches a log attribute: upstream names the members, and
// a crafted archive may declare thousands of them.
func TestSummarizeNames_truncates_and_sanitizes(t *testing.T) {
	t.Parallel()

	t.Run("truncates past the cap", func(t *testing.T) {
		t.Parallel()
		names := make([]string, maxNamesInError+3)
		for i := range names {
			names[i] = fmt.Sprintf("member%02d.srt", i)
		}
		got := summarizeNames(names)
		if !strings.Contains(got, "(+3 more)") {
			t.Errorf("summarizeNames(%d names) = %q, want a (+3 more) marker", len(names), got)
		}
		if strings.Contains(got, fmt.Sprintf("member%02d.srt", maxNamesInError)) {
			t.Errorf("summarizeNames(%d names) = %q, want no name past the cap", len(names), got)
		}
	})

	t.Run("strips a record-forging newline", func(t *testing.T) {
		t.Parallel()
		got := summarizeNames([]string{"ok.srt\nlevel=ERROR msg=\"forged\""})
		if strings.Contains(got, "\n") {
			t.Errorf("summarizeNames(newline) = %q, want no raw newline", got)
		}
	})
}

// TestExtractFromZip_refusal_distinguishes_unreadable_naming pins the difference
// between the two ways a pack can fail to answer, because they call for opposite
// responses and used to produce the same sentence.
//
// A wrong-season pack is a release problem: something else should be downloaded.
// A pack whose members carry no readable episode marker is a subflux problem: the
// notation needs teaching. Both rendered as "no member for S01E08" until the
// refusal started saying which it was, and the operator's only way to tell them
// apart was to read the filenames out of the log and judge for themselves.
func TestExtractFromZip_refusal_distinguishes_unreadable_naming(t *testing.T) {
	t.Parallel()

	t.Run("members claim other episodes", func(t *testing.T) {
		t.Parallel()
		data := makeZip(t,
			zipEntry{"Show.S03E01.srt", []byte("s3 ep 1")},
			zipEntry{"Show.S03E02.srt", []byte("s3 ep 2")},
		)
		_, err := zipExtract(data, episode(1, 8))
		if err == nil {
			t.Fatal("zipExtract(season-3 pack, S01E08) = nil error, want a refusal")
		}
		msg := err.Error()
		if !strings.Contains(msg, "claim S03E01, S03E02") {
			t.Errorf("err = %q, want it to say what the members DO claim", msg)
		}
		if strings.Contains(msg, "no episode this scanner can read") {
			t.Errorf("err = %q, want the wrong-season wording, not the unreadable-naming one", msg)
		}
	})

	// Real member naming, measured on a SubSource Game of Thrones pack: a bare
	// episode number and a title, with no season anywhere in the name. The
	// numbers ARE read now, so the refusal has to say the episode is absent
	// rather than that the naming is unreadable.
	t.Run("members state bare numbers that do not include the target", func(t *testing.T) {
		t.Parallel()
		data := makeZip(t,
			zipEntry{"1 - Winter Is Coming..srt", []byte("ep 1")},
			zipEntry{"2 - The Kingsroad..srt", []byte("ep 2")},
		)
		_, err := zipExtract(data, episode(1, 8))
		if err == nil {
			t.Fatal("zipExtract(bare-numbered pack, S01E08) = nil error, want a refusal")
		}
		msg := err.Error()
		if !strings.Contains(msg, "bare episode numbers 1, 2") {
			t.Errorf("err = %q, want it to report the bare numbers it did read", msg)
		}
		if !strings.Contains(msg, "S01E08") {
			t.Errorf("err = %q, want the episode that could not be matched named", msg)
		}
	})

	// Nothing states an episode in ANY form, so this is the arm that says the
	// notation needs teaching.
	t.Run("members name no episode in any form", func(t *testing.T) {
		t.Parallel()
		data := makeZip(t,
			zipEntry{"Winter Is Coming.srt", []byte("ep 1")},
			zipEntry{"The Kingsroad.srt", []byte("ep 2")},
		)
		_, err := zipExtract(data, episode(1, 8))
		if err == nil {
			t.Fatal("zipExtract(title-only pack, S01E08) = nil error, want a refusal")
		}
		msg := err.Error()
		if !strings.Contains(msg, "names no episode this scanner can read") {
			t.Errorf("err = %q, want it to state that the naming is unreadable", msg)
		}
		if !strings.Contains(msg, "Winter Is Coming") {
			t.Errorf("err = %q, want the member names listed so the notation is visible", msg)
		}
	})
}

// TestExtractFromZip_reads_a_bare_numbered_pack is the other half of the same
// change: a pack whose members state an episode number and no season.
//
// The season is not being inferred. The provider search sends seasonNumber to the
// API and stamps the result's season from that same request, so the target's
// season came from Sonarr; the only thing missing from the member name is the
// episode, which the leading number states outright.
func TestExtractFromZip_reads_a_bare_numbered_pack(t *testing.T) {
	t.Parallel()
	data := makeZip(t,
		zipEntry{"1 - Winter Is Coming..srt", []byte("ep 1")},
		zipEntry{"2 - The Kingsroad..srt", []byte("ep 2")},
		zipEntry{"10 - Fire And Blood..srt", []byte("ep 10")},
	)

	for _, tc := range []struct {
		ep   int
		want string
	}{{1, "ep 1"}, {2, "ep 2"}, {10, "ep 10"}} {
		got, err := zipExtract(data, episode(1, tc.ep))
		if err != nil {
			t.Errorf("zipExtract(bare-numbered pack, S01E%02d) error = %v, want %q",
				tc.ep, err, tc.want)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("zipExtract(bare-numbered pack, S01E%02d) = %q, want %q", tc.ep, got, tc.want)
		}
	}
}

// TestExtractFromZip_a_readable_notation_outvotes_a_bare_number pins the other
// half of the bare-number contract. The reading is only sound where the archive
// states no season anywhere, so a single member carrying a real marker has to
// disqualify it for the whole archive — otherwise a pack that parses could be
// answered by a number that happens to match.
func TestExtractFromZip_a_readable_notation_outvotes_a_bare_number(t *testing.T) {
	t.Parallel()
	// "8 - …" would match S01E08 as a bare number, but a sibling member claims
	// S03E01, so this archive names a season and the bare reading is off.
	data := makeZip(t,
		zipEntry{"8 - Some Title..srt", []byte("bare 8")},
		zipEntry{"Show.S03E01.srt", []byte("s3 ep 1")},
	)

	got, err := zipExtract(data, episode(1, 8))
	if err == nil {
		t.Fatalf("zipExtract(mixed pack, S01E08) = %q, want a refusal: the archive claims "+
			"S03E01, so it names a season and the bare reading must not apply", got)
	}
	if !strings.Contains(err.Error(), "claim S03E01") {
		t.Errorf("err = %q, want the wrong-season wording", err)
	}
}

// TestExtractFromZip_read_failure_renders_on_one_line pins the log-shape half of
// the same concern. This text becomes a slog attribute, and errors.Join renders
// its members newline-separated, which splits one record across several lines in
// the operator's log. The chain still has to reach the cause.
func TestExtractFromZip_read_failure_renders_on_one_line(t *testing.T) {
	t.Parallel()
	// Two members both claiming the episode, so a joined rendering would put a
	// newline between their two failures. With one member there is nothing to
	// join and the test could not tell the two renderings apart.
	data := makeZipUnreadableMethod(t, "Show.S01E08.PROPER.srt", "Show.S01E08.REPACK.srt")

	_, err := zipExtract(data, episode(1, 8))
	if err == nil {
		t.Fatal("zipExtract(unreadable member) = nil error, want a read failure")
	}
	if strings.ContainsAny(err.Error(), "\n\r") {
		t.Errorf("err = %q, want no line break: this lands in a slog attribute", err)
	}
	if !errors.Is(err, zip.ErrAlgorithm) {
		t.Errorf("err = %v, want the chain to still reach zip.ErrAlgorithm", err)
	}
}
