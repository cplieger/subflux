package archive

// Tests in this file target surviving gremlins mutants for unit subflux-u15
// in package internal/provider/archive. Tests only; no production code is
// changed. Helpers/identifiers use the gk_subflux_u15_ prefix. Existing
// package helpers (makeZip, zipEntry, loadRARFixture) are reused.

import (
	"archive/zip"
	"bytes"
	"fmt"
	"testing"
)

// --- archive.go: hasZIPMagic length boundary (>= 4) ---

// kills archive.go:82:19 CONDITIONALS_BOUNDARY (len(data) >= 4 vs > 4):
// exactly 4 magic bytes must qualify; a `> 4` mutation would reject them.
func Test_gk_subflux_u15_hasZIPMagic_length_boundary(t *testing.T) {
	t.Parallel()
	if !hasZIPMagic([]byte{'P', 'K', 0x03, 0x04}) {
		t.Errorf("hasZIPMagic(4-byte PK\\x03\\x04) = false, want true")
	}
	if hasZIPMagic([]byte{'P', 'K', 0x03}) {
		t.Errorf("hasZIPMagic(3 bytes) = true, want false")
	}
	if hasZIPMagic([]byte{'P', 'K', 0x03, 0x05}) {
		t.Errorf("hasZIPMagic(wrong 4th byte) = true, want false")
	}
}

// --- archive.go: hasRARMagic length boundary (>= 6) ---

// kills archive.go:88:19 CONDITIONALS_BOUNDARY (len(data) >= 6 vs > 6):
// exactly 6 magic bytes must qualify.
func Test_gk_subflux_u15_hasRARMagic_length_boundary(t *testing.T) {
	t.Parallel()
	if !hasRARMagic([]byte{'R', 'a', 'r', '!', 0x1A, 0x07}) {
		t.Errorf("hasRARMagic(6-byte Rar!\\x1a\\x07) = false, want true")
	}
	if hasRARMagic([]byte{'R', 'a', 'r', '!', 0x1A}) {
		t.Errorf("hasRARMagic(5 bytes) = true, want false")
	}
}

// --- archive.go: LooksLikeSubtitle non-text-ratio boundary ---

// kills archive.go:125:37 CONDITIONALS_BOUNDARY (nonText*10 > len vs >= len).
// probe has exactly len/10 non-text bytes (1 non-text in 10 total) AND a
// valid SRT signature: with `>` the data is accepted (10 > 10 is false), with
// `>=` it is rejected (10 >= 10 is true).
func Test_gk_subflux_u15_looksLikeSubtitle_nontext_ratio_boundary(t *testing.T) {
	t.Parallel()
	probe := append([]byte(" --> abcd"), 0x00) // 10 bytes, 1 non-text (NUL)
	if len(probe) != 10 {
		t.Fatalf("probe length = %d, want 10 (test setup)", len(probe))
	}
	if !LooksLikeSubtitle(probe) {
		t.Errorf("LooksLikeSubtitle(nonText*10 == len, with signature) = false, "+
			"want true (boundary nonText*10 > len must be strict)")
	}
}

// --- decompress.go: isXZ length boundary + magic-byte equalities ---

// kills decompress.go:28:19 CONDITIONALS_BOUNDARY (len > 6 vs >= 6) via the
// 6-byte magic case, and 29:11/29:30/29:49/30:11/30:30/30:49
// CONDITIONALS_NEGATION (each `data[i] == b`) via the valid 7-byte case.
func Test_gk_subflux_u15_isXZ_length_and_magic(t *testing.T) {
	t.Parallel()
	magic6 := []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}       // exactly 6
	magic7 := []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00, 0x00} // 7 (valid length)

	if isXZ(magic6) {
		t.Errorf("isXZ(6-byte magic) = true, want false (length must be > 6)")
	}
	if !isXZ(magic7) {
		t.Errorf("isXZ(7-byte full magic) = false, want true")
	}
	// Any single corrupted magic byte must make it non-xz.
	for i := range 6 {
		bad := append([]byte(nil), magic7...)
		bad[i] ^= 0xFF
		if isXZ(bad) {
			t.Errorf("isXZ(7-byte, magic byte %d corrupted) = true, want false", i)
		}
	}
}

// --- decompress.go: isGzip length boundary + magic-byte equalities ---

// kills decompress.go:35:19 CONDITIONALS_BOUNDARY (len > 2 vs >= 2) via the
// 2-byte case, and 35:34/35:53 CONDITIONALS_NEGATION via the valid 3-byte case.
func Test_gk_subflux_u15_isGzip_length_and_magic(t *testing.T) {
	t.Parallel()
	if isGzip([]byte{0x1F, 0x8B}) {
		t.Errorf("isGzip(2-byte magic) = true, want false (length must be > 2)")
	}
	if !isGzip([]byte{0x1F, 0x8B, 0x00}) {
		t.Errorf("isGzip(3-byte full magic) = false, want true")
	}
	if isGzip([]byte{0x00, 0x8B, 0x00}) {
		t.Errorf("isGzip(wrong byte 0) = true, want false")
	}
	if isGzip([]byte{0x1F, 0x00, 0x00}) {
		t.Errorf("isGzip(wrong byte 1) = true, want false")
	}
}

// --- zip.go: IsValidSubtitleEntry size guards ---

// kills zip.go:76:53 (boundary + negation), 79:24 (boundary + negation), and
// 80:43 (boundary). Each *zip.File is built directly so only the declared
// sizes vary; IsValidSubtitleEntry only reads Name + the two size fields.
func Test_gk_subflux_u15_isValidSubtitleEntry_size_guards(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		comp, uncomp uint64
		want         bool
	}{
		// zero-compressed guard: reject. `Uncomp > 0` negation (-> <= 0) accepts.
		{"zero compressed, positive uncompressed", 0, 10, false},
		// `Uncomp > 0` boundary (-> >= 0) would wrongly reject an empty entry;
		// `Comp > 0` boundary (-> >= 0) divides by zero (0/0) and panics.
		{"both sizes zero", 0, 0, true},
		// ratio boundary: exactly 50 is allowed; `> 50` -> `>= 50` rejects it.
		{"ratio exactly 50", 1, 50, true},
		// bomb: reject. `Comp > 0` negation (-> <= 0) skips the ratio check.
		{"ratio over 50", 1, 1000, false},
		{"normal entry", 100, 200, true},
	}
	for _, tc := range cases {
		f := &zip.File{FileHeader: zip.FileHeader{
			Name:               "a.srt",
			CompressedSize64:   tc.comp,
			UncompressedSize64: tc.uncomp,
		}}
		got := IsValidSubtitleEntry(f)
		if got != tc.want {
			t.Errorf("IsValidSubtitleEntry(comp=%d, uncomp=%d) [%s] = %v, want %v",
				tc.comp, tc.uncomp, tc.name, got, tc.want)
		}
	}
}

// --- zip.go: central-directory entry-count boundary ---

// kills zip.go:34:17 CONDITIONALS_BOUNDARY (len(r.File) > maxZipEntries vs >=).
// A zip with EXACTLY maxZipEntries valid entries must be processed (4096 > 4096
// is false); a `>=` mutation would reject it and return nil.
func Test_gk_subflux_u15_extractFromZip_entry_count_boundary(t *testing.T) {
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

// --- rar.go: episodeCtx season>0 boundary ---

// kills rar.go:37:23 CONDITIONALS_BOUNDARY (season > 0 vs >= 0). With season 0
// and episode 5, episodeCtx must be FALSE so the first subtitle is returned
// regardless of name. A `>= 0` mutation makes episodeCtx true, then filters on
// a non-existent S00E05 and returns nil.
func Test_gk_subflux_u15_findRARSubtitle_season_zero_boundary(t *testing.T) {
	t.Parallel()
	data := loadRARFixture(t)
	got := ExtractFromRAR(data, 0, 5)
	if got == nil {
		t.Fatalf("ExtractFromRAR(fixture, 0, 5) = nil, want first subtitle " +
			"(season 0 must disable episode filtering)")
	}
}

// --- rar.go: episodeCtx episode>0 boundary ---

// kills rar.go:37:38 CONDITIONALS_BOUNDARY (episode > 0 vs >= 0). With season 1
// and episode 0, episodeCtx must be FALSE. A `>= 0` mutation makes episodeCtx
// true, filters on S01E00, and returns nil.
func Test_gk_subflux_u15_findRARSubtitle_episode_zero_boundary(t *testing.T) {
	t.Parallel()
	data := loadRARFixture(t)
	got := ExtractFromRAR(data, 1, 0)
	if got == nil {
		t.Fatalf("ExtractFromRAR(fixture, 1, 0) = nil, want first subtitle " +
			"(episode 0 must disable episode filtering)")
	}
}

// --- archive.go: default switch branch, ExtractFromZip success ---

// targets archive.go:56:68 CONDITIONALS_NEGATION (extracted != nil vs == nil)
// in the default (unknown-magic) branch. A 1-byte prefix makes hasZIPMagic
// false (routing to default) while Go's archive/zip still reads the
// self-extracting-style prefixed archive; ExtractFromZip therefore returns the
// subtitle. With `== nil` the branch is skipped and Extract returns nil.
func Test_gk_subflux_u15_extract_default_branch_zip(t *testing.T) {
	t.Parallel()
	content := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	z := makeZip(t, zipEntry{name: "sub.srt", content: content})
	prefixed := append([]byte{'X'}, z...)

	if HasArchiveSignature(prefixed) {
		t.Fatalf("prefixed zip unexpectedly has an archive signature (test setup)")
	}
	got := Extract(prefixed, 0, 0)
	if !bytes.Equal(got, content) {
		t.Fatalf("Extract(prefixed-zip) = %q, want %q "+
			"(default-branch ExtractFromZip success must be returned)", got, content)
	}
}

// --- archive.go: default switch branch, ExtractFromRAR success ---

// targets archive.go:59:68 CONDITIONALS_NEGATION (extracted != nil vs == nil)
// in the default branch. A byte prefix makes hasRARMagic false (routing to
// default); if rardecode still reads the prefixed RAR, ExtractFromRAR returns
// content and Extract returns it. With `== nil` Extract returns nil.
func Test_gk_subflux_u15_extract_default_branch_rar(t *testing.T) {
	t.Parallel()
	rar := loadRARFixture(t)
	prefixed := append([]byte("ZZZZ"), rar...)

	if HasArchiveSignature(prefixed) {
		t.Fatalf("prefixed rar unexpectedly has an archive signature (test setup)")
	}
	got := Extract(prefixed, 0, 0)
	if got == nil {
		t.Fatalf("Extract(prefixed-rar) = nil, want extracted subtitle " +
			"(default-branch ExtractFromRAR success must be returned)")
	}
}
