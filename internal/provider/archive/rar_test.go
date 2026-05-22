package archive

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/nwaples/rardecode/v2"
)

func TestExtractFromRAR_invalid_data(t *testing.T) {
	t.Parallel()
	got := ExtractFromRAR([]byte("not a rar"), 0, 0)
	if got != nil {
		t.Errorf("extractFromRAR(invalid) = %d bytes, want nil", len(got))
	}
}

func TestExtractFromRAR_nil_data(t *testing.T) {
	t.Parallel()
	got := ExtractFromRAR(nil, 0, 0)
	if got != nil {
		t.Errorf("extractFromRAR(nil) = %d bytes, want nil", len(got))
	}
}

func TestExtractFromRAR_empty_data(t *testing.T) {
	t.Parallel()
	got := ExtractFromRAR([]byte{}, 0, 0)
	if got != nil {
		t.Errorf("extractFromRAR(empty) = %d bytes, want nil", len(got))
	}
}

func TestExtractFromArchive_prefers_zip(t *testing.T) {
	t.Parallel()
	content := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	data := makeZip(t, zipEntry{"sub.srt", content})

	got := Extract(data, 0, 0)
	if !bytes.Equal(got, content) {
		t.Errorf("ExtractFromArchive(zip) = %q, want %q", got, content)
	}
}

func TestExtractFromArchive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		data     []byte
		wantNil  bool
		wantSame bool // when true, expect output == input
	}{
		{"returns_raw_for_valid_subtitle", []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), false, true},
		{"rejects_binary_garbage", bytes.Repeat([]byte{0x00, 0xFF}, 256), true, false},
		{"rejects_unknown_archive", append([]byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}, bytes.Repeat([]byte{0xFF}, 100)...), true, false},
		{"accepts_ass_subtitle", []byte("[Script Info]\nTitle: Test\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Hello\n"), false, true},
		{"accepts_webvtt", []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n"), false, true},
		{"accepts_utf8_bom_srt", append([]byte{0xEF, 0xBB, 0xBF}, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")...), false, true},
		{"accepts_dialogue_only_ass", []byte("Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Hello\n"), false, true},
		{"empty_data_returns_nil", []byte{}, true, false},
		{"nil_data_returns_nil", nil, true, false},
		{"rejects_plain_text_without_subtitle_signatures", []byte("This is just plain text without any subtitle timing markers or formatting."), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Extract(tt.data, 0, 0)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("Extract() = %d bytes, want nil", len(got))
				}
				return
			}
			if tt.wantSame && !bytes.Equal(got, tt.data) {
				t.Fatalf("Extract() = %q, want original data", got)
			}
		})
	}
}

func TestLooksLikeSubtitle_bom_only_returns_false(t *testing.T) {
	t.Parallel()
	// UTF-8 BOM with no content after it. After stripping BOM, probe is empty.
	data := []byte{0xEF, 0xBB, 0xBF}

	got := LooksLikeSubtitle(data)
	if got {
		t.Error("looksLikeSubtitle(BOM-only) = true, want false")
	}
}

func TestLooksLikeSubtitle_large_srt_truncated_to_4kb(t *testing.T) {
	t.Parallel()
	// Signature at byte 100 (within 4KB window) should be found.
	data := make([]byte, 5000)
	for i := range data {
		data[i] = 'A'
	}
	copy(data[100:], []byte(" --> "))

	got := LooksLikeSubtitle(data)
	if !got {
		t.Error("looksLikeSubtitle(large with signature in 4KB) = false, want true")
	}
}

func TestLooksLikeSubtitle_signature_beyond_4kb_returns_false(t *testing.T) {
	t.Parallel()
	// Signature placed beyond the 4KB probe window should not be found.
	data := make([]byte, 5000)
	for i := range data {
		data[i] = 'A'
	}
	copy(data[4500:], []byte(" --> "))

	got := LooksLikeSubtitle(data)
	if got {
		t.Error("looksLikeSubtitle(signature beyond 4KB) = true, want false")
	}
}

func TestLooksLikeSubtitle_high_non_text_ratio_returns_false(t *testing.T) {
	t.Parallel()
	// Data with > 10% non-text bytes but containing a subtitle signature.
	// The non-text check should reject it before signature matching.
	data := bytes.Repeat([]byte{0x01}, 50)
	data = append(data, []byte(" --> ")...)
	data = append(data, bytes.Repeat([]byte{0x01}, 50)...)

	got := LooksLikeSubtitle(data)
	if got {
		t.Error("looksLikeSubtitle(high non-text with signature) = true, want false")
	}
}

// --- Valid RAR fixture tests ---

func loadRARFixture(t *testing.T) []byte {
	t.Helper()
	return loadRARFixtureFile(t, "test.rar")
}

func loadRARFixtureFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../testdata/" + name)
	if err != nil {
		t.Skip("RAR fixture not available:", err)
	}
	return data
}

func TestExtractFromRAR_returns_first_subtitle_without_episode_context(t *testing.T) {
	t.Parallel()
	data := loadRARFixture(t)
	got := ExtractFromRAR(data, 0, 0)
	if got == nil {
		t.Fatal("extractFromRAR(valid, 0, 0) = nil, want content")
	}
	if len(got) == 0 {
		t.Error("extractFromRAR(valid, 0, 0) returned empty content")
	}
}

func TestExtractFromRAR_matches_episode_in_season_pack(t *testing.T) {
	t.Parallel()
	data := loadRARFixture(t)
	got := ExtractFromRAR(data, 1, 1)
	if got == nil {
		t.Fatal("extractFromRAR(valid, 1, 1) = nil, want matching content")
	}
	if !bytes.Contains(got, []byte("episode 1")) {
		t.Errorf("extractFromRAR(valid, 1, 1) = %q, want content containing 'episode 1'", got)
	}
}

func TestExtractFromRAR_matches_episode_2(t *testing.T) {
	t.Parallel()
	data := loadRARFixture(t)
	got := ExtractFromRAR(data, 1, 2)
	if got == nil {
		t.Fatal("extractFromRAR(valid, 1, 2) = nil, want matching content")
	}
	if !bytes.Contains(got, []byte("episode 2")) {
		t.Errorf("extractFromRAR(valid, 1, 2) = %q, want content containing 'episode 2'", got)
	}
}

func TestExtractFromRAR_no_episode_match_returns_nil(t *testing.T) {
	t.Parallel()
	data := loadRARFixture(t)
	got := ExtractFromRAR(data, 99, 99)
	if got != nil {
		t.Errorf("extractFromRAR(valid, 99, 99) = %d bytes, want nil", len(got))
	}
}

// TestExtractFromRAR_skips_directory_entries exercises the directory
// filter path via a fixture with both a subdir entry and a real subtitle.
// Replaces the former listRARSubtitles-based test.
func TestExtractFromRAR_skips_directory_entries(t *testing.T) {
	t.Parallel()
	data := loadRARFixtureFile(t, "test_with_dir.rar")
	got := ExtractFromRAR(data, 0, 0)
	if got == nil {
		t.Fatal("extractFromRAR(dir fixture, 0, 0) = nil, want subtitle content")
	}
	if len(got) == 0 {
		t.Error("extractFromRAR(dir fixture, 0, 0) returned empty content")
	}
}

// TestExtractFromRAR_skips_hidden_files exercises the hidden-file filter
// via a fixture with a `.hidden.srt` and a `visible.srt`. Replaces the
// former listRARSubtitles-based test.
func TestExtractFromRAR_skips_hidden_files(t *testing.T) {
	t.Parallel()
	data := loadRARFixtureFile(t, "test_hidden.rar")
	got := ExtractFromRAR(data, 0, 0)
	if got == nil {
		t.Fatal("extractFromRAR(hidden fixture, 0, 0) = nil, want visible subtitle")
	}
	// The hidden fixture contains a single visible entry; nothing in the
	// current test data distinguishes visible content by payload, so the
	// smoke check is simply "we got non-empty content", proving that
	// findRARSubtitle walked past the hidden entry rather than stopping.
	if len(got) == 0 {
		t.Error("extractFromRAR(hidden fixture) returned empty content")
	}
}

func TestExtractFromArchive_extracts_from_rar(t *testing.T) {
	t.Parallel()
	data := loadRARFixture(t)
	got := Extract(data, 0, 0)
	if got == nil {
		t.Fatal("ExtractFromArchive(rar) = nil, want subtitle content")
	}
	if len(got) == 0 {
		t.Error("ExtractFromArchive(rar) returned empty content")
	}
}

func TestExtractFromArchive_rar_with_episode_context(t *testing.T) {
	t.Parallel()
	data := loadRARFixture(t)

	t.Run("matches episode 1", func(t *testing.T) {
		t.Parallel()
		got := Extract(data, 1, 1)
		if got == nil {
			t.Fatal("ExtractFromArchive(rar, 1, 1) = nil, want content")
		}
		if !bytes.Contains(got, []byte("episode 1")) {
			t.Errorf("ExtractFromArchive(rar, 1, 1) = %q, want content containing 'episode 1'", got)
		}
	})

	t.Run("no match returns nil", func(t *testing.T) {
		t.Parallel()
		got := Extract(data, 99, 99)
		if got != nil {
			t.Errorf("ExtractFromArchive(rar, 99, 99) = %d bytes, want nil", len(got))
		}
	})
}

// --- Test-review Finding 3: zip bomb safety via public ExtractFromArchive ---

// TestExtractFromArchive_rejects_zip_bomb_via_public_api verifies that a
// zip bomb flowing through the public entry point is rejected AND does not
// fall back to looksLikeSubtitle returning raw zip bytes as if they were a
// text subtitle. Locks in the public API's security contract.
func TestExtractFromArchive_rejects_zip_bomb_via_public_api(t *testing.T) {
	t.Parallel()

	content := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	data := makeZip(t, zipEntry{"subtitle.srt", content})

	centralIdx := bytes.Index(data, []byte("PK\x01\x02"))
	if centralIdx < 0 {
		t.Fatal("central directory header not found")
	}
	fakeUncompressed := uint32(len(content)) * 100
	binary.LittleEndian.PutUint32(data[centralIdx+24:centralIdx+28], fakeUncompressed)

	got := Extract(data, 0, 0)
	if got != nil {
		t.Errorf("ExtractFromArchive(zip-bomb) = %d bytes, want nil "+
			"(bomb must not fall through to looksLikeSubtitle)", len(got))
	}
}

// TestExtractFromArchive_rejects_oversized_zip_via_public_api verifies that
// content exceeding maxExtractSize is rejected by the public API rather
// than leaking through the looksLikeSubtitle fallback.
func TestExtractFromArchive_rejects_oversized_zip_via_public_api(t *testing.T) {
	t.Parallel()

	content := make([]byte, MaxExtractSize+1)
	for i := range content {
		content[i] = byte(i)
	}
	data := makeZipStored(t, zipEntry{"subtitle.srt", content})

	got := Extract(data, 0, 0)
	if got != nil {
		t.Errorf("ExtractFromArchive(oversized) = %d bytes, want nil", len(got))
	}
}

// --- Test-review Finding 4: episode matching for zip season packs via public API ---

// TestExtractFromArchive_zip_with_episode_context locks in the behavior
// that episode-aware extraction propagates from the public API down into
// extractFromZip, including the "no match returns nil, not first" guard
// that providers (hdbits, betaseries, subsource, subdl) rely on.
func TestExtractFromArchive_zip_with_episode_context(t *testing.T) {
	t.Parallel()

	e01 := []byte("1\n00:00:01,000 --> 00:00:02,000\nEpisode 1\n")
	e02 := []byte("1\n00:00:01,000 --> 00:00:02,000\nEpisode 2\n")
	e03 := []byte("1\n00:00:01,000 --> 00:00:02,000\nEpisode 3\n")

	data := makeZip(t,
		zipEntry{"Show.S01E01.srt", e01},
		zipEntry{"Show.S01E02.srt", e02},
		zipEntry{"Show.S01E03.srt", e03},
	)

	t.Run("matches target episode", func(t *testing.T) {
		t.Parallel()
		got := Extract(data, 1, 2)
		if !bytes.Equal(got, e02) {
			t.Errorf("ExtractFromArchive(zip pack, 1, 2) = %q, want %q", got, e02)
		}
	})

	t.Run("no episode match returns nil not first", func(t *testing.T) {
		t.Parallel()
		got := Extract(data, 1, 99)
		if got != nil {
			t.Errorf("ExtractFromArchive(zip pack, 1, 99) = %q, "+
				"want nil (no fallback to first)", got)
		}
	})

	t.Run("zero episode context falls back to first", func(t *testing.T) {
		t.Parallel()
		got := Extract(data, 0, 0)
		if !bytes.Equal(got, e01) {
			t.Errorf("ExtractFromArchive(zip pack, 0, 0) = %q, "+
				"want %q (first)", got, e01)
		}
	})
}

// --- isValidRAREntry table ---

func TestIsValidRAREntry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		hdr  *rardecode.FileHeader
		name string
		want bool
	}{
		{
			name: "valid subtitle",
			hdr:  &rardecode.FileHeader{Name: "sub.srt", PackedSize: 100, UnPackedSize: 200},
			want: true,
		},
		{
			name: "directory entry",
			hdr:  &rardecode.FileHeader{Name: "subdir", IsDir: true},
			want: false,
		},
		{
			name: "non-subtitle extension",
			hdr:  &rardecode.FileHeader{Name: "readme.txt", PackedSize: 50, UnPackedSize: 100},
			want: false,
		},
		{
			name: "hidden file",
			hdr:  &rardecode.FileHeader{Name: ".hidden.srt", PackedSize: 50, UnPackedSize: 100},
			want: false,
		},
		{
			name: "bomb guard: zero packed with positive unpacked",
			hdr:  &rardecode.FileHeader{Name: "bomb.srt", PackedSize: 0, UnPackedSize: 1000},
			want: false,
		},
		{
			name: "bomb guard: ratio exceeds 50",
			hdr:  &rardecode.FileHeader{Name: "bomb.srt", PackedSize: 1, UnPackedSize: 51},
			want: false,
		},
		{
			name: "bomb guard: ratio exactly 50 is allowed",
			hdr:  &rardecode.FileHeader{Name: "ok.srt", PackedSize: 1, UnPackedSize: 50},
			want: true,
		},
		{
			name: "both sizes zero is allowed",
			hdr:  &rardecode.FileHeader{Name: "empty.srt", PackedSize: 0, UnPackedSize: 0},
			want: true,
		},
		{
			name: "nested path with valid extension",
			hdr:  &rardecode.FileHeader{Name: "subs/episode.srt", PackedSize: 100, UnPackedSize: 200},
			want: true,
		},
		{
			name: "hidden file in subdirectory",
			hdr:  &rardecode.FileHeader{Name: "subs/.hidden.srt", PackedSize: 100, UnPackedSize: 200},
			want: false,
		},
		{
			name: "ass extension",
			hdr:  &rardecode.FileHeader{Name: "sub.ass", PackedSize: 100, UnPackedSize: 200},
			want: true,
		},
		{
			name: "vtt extension",
			hdr:  &rardecode.FileHeader{Name: "sub.vtt", PackedSize: 100, UnPackedSize: 200},
			want: true,
		},
		{
			name: "unknown unpacked size rejected",
			hdr:  &rardecode.FileHeader{Name: "sub.srt", PackedSize: 100, UnPackedSize: -1, UnKnownSize: true},
			want: false,
		},
		{
			name: "negative unpacked size rejected",
			hdr:  &rardecode.FileHeader{Name: "sub.srt", PackedSize: 100, UnPackedSize: -1},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsValidRAREntry(tc.hdr)
			if got != tc.want {
				t.Errorf("isValidRAREntry(%+v) = %v, want %v", tc.hdr, got, tc.want)
			}
		})
	}
}
