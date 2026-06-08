package api

import (
	"bytes"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestSubtitlePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		videoPath string
		lang      string
		want      string
		hi        bool
		forced    bool
	}{
		{
			name:      "basic subtitle path",
			videoPath: "/media/movie.mkv", lang: "en",
			want: "/media/movie.en.srt",
		},
		{
			name:      "hearing impaired suffix",
			videoPath: "/media/movie.mkv", lang: "en", hi: true,
			want: "/media/movie.en.hi.srt",
		},
		{
			name:      "forced suffix",
			videoPath: "/media/movie.mkv", lang: "fr", forced: true,
			want: "/media/movie.fr.forced.srt",
		},
		{
			name:      "both hi and forced",
			videoPath: "/media/movie.mkv", lang: "de", hi: true, forced: true,
			want: "/media/movie.de.hi.forced.srt",
		},
		{
			name:      "mp4 extension",
			videoPath: "/media/show.mp4", lang: "es",
			want: "/media/show.es.srt",
		},
		{
			name:      "nested path with spaces",
			videoPath: "/media/TV Shows/Show Name/S01E01.mkv", lang: "pt",
			want: "/media/TV Shows/Show Name/S01E01.pt.srt",
		},
		{
			name:      "no extension on video",
			videoPath: "/media/videofile", lang: "en",
			want: "/media/videofile.en.srt",
		},
		{
			name:      "multi-dot filename strips only last extension",
			videoPath: "/media/Movie.2024.BluRay.mkv", lang: "en",
			want: "/media/Movie.2024.BluRay.en.srt",
		},
		{
			name:      "dot-only extension",
			videoPath: "/media/movie.", lang: "fr",
			want: "/media/movie.fr.srt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := SubtitlePath(tt.videoPath, tt.lang, tt.hi, tt.forced)

			if got != tt.want {
				t.Errorf("SubtitlePath(%q, %q, hi=%v, forced=%v) = %q, want %q",
					tt.videoPath, tt.lang, tt.hi, tt.forced, got, tt.want)
			}
		})
	}
}

func TestManualSubtitlePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		videoPath string
		lang      string
		want      string
		n         int
	}{
		{
			"first manual subtitle",
			"/media/movie.mkv", "fr",
			"/media/movie.fr.1.srt", 1,
		},
		{
			"second manual subtitle",
			"/media/movie.mkv", "fr",
			"/media/movie.fr.2.srt", 2,
		},
		{
			"mp4 extension",
			"/media/show.mp4", "en",
			"/media/show.en.3.srt", 3,
		},
		{
			"zero index",
			"/media/movie.mkv", "de",
			"/media/movie.de.0.srt", 0,
		},
		{
			"multi-dot filename strips only last extension",
			"/media/Movie.2024.BluRay.mkv", "en",
			"/media/Movie.2024.BluRay.en.1.srt", 1,
		},
		{
			"negative index",
			"/media/movie.mkv", "en",
			"/media/movie.en.-1.srt", -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ManualSubtitlePath(tt.videoPath, tt.lang, tt.n, false, false)

			if got != tt.want {
				t.Errorf("ManualSubtitlePath(%q, %q, %d) = %q, want %q",
					tt.videoPath, tt.lang, tt.n, got, tt.want)
			}
		})
	}
}

func TestManualSubtitlePath_variants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		videoPath string
		lang      string
		want      string
		n         int
		hi        bool
		forced    bool
	}{
		{
			name:      "hi variant",
			videoPath: "/media/movie.mkv",
			lang:      "fr",
			n:         1,
			hi:        true,
			want:      "/media/movie.fr.hi.1.srt",
		},
		{
			name:      "forced variant",
			videoPath: "/media/movie.mkv",
			lang:      "fr",
			n:         2,
			forced:    true,
			want:      "/media/movie.fr.forced.2.srt",
		},
		{
			name:      "hi overrides forced",
			videoPath: "/media/movie.mkv",
			lang:      "fr",
			n:         3,
			hi:        true,
			forced:    true,
			want:      "/media/movie.fr.hi.forced.3.srt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ManualSubtitlePath(tt.videoPath, tt.lang, tt.n, tt.hi, tt.forced)
			if got != tt.want {
				t.Errorf("ManualSubtitlePath(%q, %q, %d, hi=%t, forced=%t) = %q, want %q",
					tt.videoPath, tt.lang, tt.n, tt.hi, tt.forced, got, tt.want)
			}
		})
	}
}

// --- SubtitlePath PBT ---

func TestSubtitlePath_always_ends_with_srt(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		// Exclude language codes that collide with flag markers ("hi" is a real
		// ISO 639-1 code for Hindi, "forced" is outside the length range today
		// but excluded defensively). Otherwise the suffix-contains assertions
		// below cannot distinguish a lang code from a flag.
		lang := rapid.StringMatching(`[a-z]{2,3}`).
			Filter(func(s string) bool { return s != "hi" && s != "forced" }).
			Draw(t, "lang")
		hi := rapid.Bool().Draw(t, "hi")
		forced := rapid.Bool().Draw(t, "forced")

		got := SubtitlePath(videoPath, lang, hi, forced)

		if !strings.HasSuffix(got, ".srt") {
			t.Errorf("SubtitlePath(%q, %q, %v, %v) = %q, should end with .srt",
				videoPath, lang, hi, forced, got)
		}
		if hi && !strings.Contains(got, ".hi") {
			t.Errorf("SubtitlePath(%q, %q, hi=true) = %q, missing .hi",
				videoPath, lang, got)
		}
		if forced && !strings.Contains(got, ".forced") {
			t.Errorf("SubtitlePath(%q, %q, forced=true) = %q, missing .forced",
				videoPath, lang, got)
		}
		base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
		suffix := strings.TrimPrefix(got, base)
		if !hi && strings.Contains(suffix, ".hi.") {
			t.Errorf("SubtitlePath(%q, %q, hi=false) = %q, suffix %q should not contain .hi flag",
				videoPath, lang, got, suffix)
		}
		if !forced && strings.Contains(suffix, ".forced.") {
			t.Errorf("SubtitlePath(%q, %q, forced=false) = %q, suffix %q should not contain .forced flag",
				videoPath, lang, got, suffix)
		}
	})
}

func TestSubtitlePath_always_contains_language_code(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		lang := rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "lang")
		hi := rapid.Bool().Draw(t, "hi")
		forced := rapid.Bool().Draw(t, "forced")

		got := SubtitlePath(videoPath, lang, hi, forced)

		if !strings.Contains(got, "."+lang+".") {
			t.Errorf("SubtitlePath(%q, %q, %v, %v) = %q, should contain .%s.",
				videoPath, lang, hi, forced, got, lang)
		}
	})
}

func TestSubtitlePath_hi_forced_suffix_ordering(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		lang := rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "lang")

		got := SubtitlePath(videoPath, lang, true, true)

		hiIdx := strings.Index(got, ".hi.")
		forcedIdx := strings.Index(got, ".forced.")
		if hiIdx < 0 || forcedIdx < 0 {
			t.Errorf("SubtitlePath(%q, %q, true, true) = %q, missing .hi. or .forced.",
				videoPath, lang, got)
		} else if hiIdx >= forcedIdx {
			t.Errorf("SubtitlePath(%q, %q, true, true) = %q, .hi. should come before .forced.",
				videoPath, lang, got)
		}
	})
}

func TestManualSubtitlePath_always_ends_with_srt(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		lang := rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "lang")
		n := rapid.IntRange(0, 100).Draw(t, "n")

		got := ManualSubtitlePath(videoPath, lang, n, false, false)

		if !strings.HasSuffix(got, ".srt") {
			t.Errorf("ManualSubtitlePath(%q, %q, %d) = %q, should end with .srt",
				videoPath, lang, n, got)
		}
	})
}

func TestManualSubtitlePath_always_contains_language_code(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		lang := rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "lang")
		n := rapid.IntRange(0, 100).Draw(t, "n")

		got := ManualSubtitlePath(videoPath, lang, n, false, false)

		if !strings.Contains(got, "."+lang+".") {
			t.Errorf("ManualSubtitlePath(%q, %q, %d) = %q, should contain .%s.",
				videoPath, lang, n, got, lang)
		}
	})
}

func TestManualSubtitlePath_always_contains_number(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		lang := rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "lang")
		n := rapid.IntRange(0, 100).Draw(t, "n")

		got := ManualSubtitlePath(videoPath, lang, n, false, false)

		numStr := "." + strconv.Itoa(n) + ".srt"
		if !strings.HasSuffix(got, numStr) {
			t.Errorf("ManualSubtitlePath(%q, %q, %d) = %q, should end with %q",
				videoPath, lang, n, got, numStr)
		}
	})
}

// TestSubtitlePath_strips_video_extension verifies the original video file
// extension is replaced, not preserved in the output path.
func TestSubtitlePath_strips_video_extension(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		ext := rapid.SampledFrom([]string{"mkv", "mp4", "avi", "wmv"}).Draw(t, "ext")
		video := rapid.StringMatching(`/media/[a-z]+`).Draw(t, "base") + "." + ext
		lang := rapid.StringMatching(`[a-z]{2}`).Draw(t, "lang")

		path := SubtitlePath(video, lang, false, false)

		if !strings.HasSuffix(path, ".srt") {
			t.Errorf("SubtitlePath() = %q, does not end with .srt", path)
		}
		if strings.HasSuffix(path, "."+ext+".srt") {
			t.Errorf("SubtitlePath() = %q, still contains video extension .%s", path, ext)
		}
	})
}

// --- ValidateSubtitleData ---

func TestValidateSubtitleData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{"empty data is valid", []byte{}, false},
		{"nil data is valid", nil, false},
		{"valid SRT content", []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), false},
		{"valid ASS content", []byte("[Script Info]\nTitle: Test\n"), false},
		{"rar4 magic detected", append([]byte("Rar!\x1a\x07\x00"), make([]byte, 100)...), true},
		{"rar5 magic detected", append([]byte("Rar!\x1a\x07\x01\x00"), make([]byte, 100)...), true},
		{"7z magic detected", append([]byte{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}, make([]byte, 100)...), true},
		{"gzip magic detected", append([]byte{0x1f, 0x8b}, bytes.Repeat([]byte("subtitle text\n"), 8)...), true},
		{"xz magic detected", append([]byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}, make([]byte, 100)...), true},
		{"bzip2 magic detected", append([]byte("BZh9"), bytes.Repeat([]byte("subtitle text\n"), 8)...), true},
		{"high non-text ratio rejected", bytes.Repeat([]byte{0x01}, 100), true},
		{"mostly text with few control chars accepted", append(
			bytes.Repeat([]byte("Hello world\n"), 40),
			0x01,
		), false},
		{"exactly 10% non-text passes", append(bytes.Repeat([]byte{0x01}, 51), bytes.Repeat([]byte("A"), 459)...), false},
		{"just over 10% non-text rejected", append(bytes.Repeat([]byte{0x01}, 52), bytes.Repeat([]byte("A"), 458)...), true},
		{"single byte matching start of bzip2 magic passes", []byte("B"), false},
		{"two bytes partial bzip2 magic passes", []byte("Bz"), false},
		{"zip magic detected", append([]byte("PK\x03\x04"), make([]byte, 100)...), true},
		{"zip empty archive magic detected", append([]byte("PK\x05\x06"), make([]byte, 100)...), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateSubtitleData(tt.data)

			if tt.wantErr {
				if err == nil {
					t.Fatal("ValidateSubtitleData() = nil, want error")
				}
				if !errors.Is(err, ErrBinaryData) {
					t.Errorf("ValidateSubtitleData() error = %v, want ErrBinaryData", err)
				}
				return
			}
			if err != nil {
				t.Errorf("ValidateSubtitleData() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateSubtitleData_checks_only_first_512_bytes(t *testing.T) {
	t.Parallel()

	// 512 bytes of valid text followed by binary garbage.
	// Should pass because only the first 512 bytes are checked.
	header := bytes.Repeat([]byte("subtitle text\n"), 37) // 37*14 = 518 > 512
	header = header[:512]
	data := append(header, bytes.Repeat([]byte{0x01}, 1000)...)

	err := ValidateSubtitleData(data)
	if err != nil {
		t.Errorf("ValidateSubtitleData() with clean header = %v, want nil", err)
	}
}

// --- CountNonTextBytes ---

func TestCountNonTextBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want int
	}{
		{"empty", []byte{}, 0},
		{"all printable ASCII", []byte("Hello, World!"), 0},
		{"tab is text", []byte{0x09}, 0},
		{"newline is text", []byte{0x0A}, 0},
		{"carriage return is text", []byte{0x0D}, 0},
		{"ESC is text", []byte{0x1B}, 0},
		{"NUL is non-text", []byte{0x00}, 1},
		{"BEL is non-text", []byte{0x07}, 1},
		{"mixed", []byte{0x00, 'A', 0x01, 'B', 0x09, 0x02}, 3},
		{"all non-text control chars", []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, 9},
		{"bytes between CR and SPACE excluding ESC", []byte{0x0E, 0x0F, 0x10, 0x11, 0x1A, 0x1C, 0x1D, 0x1E, 0x1F}, 9},
		{"high bytes are text", []byte{0x80, 0xFF, 0xC0}, 0},
		{"space is text", []byte{0x20}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := CountNonTextBytes(tt.data)

			if got != tt.want {
				t.Errorf("CountNonTextBytes(%v) = %d, want %d", tt.data, got, tt.want)
			}
		})
	}
}

// --- HasExcludeTag ---

func TestHasExcludeTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		excludeIDs map[int]struct{}
		name       string
		tags       []int
		want       bool
	}{
		{name: "no tags no excludes", tags: nil, excludeIDs: nil, want: false},
		{name: "no tags with excludes", tags: nil, excludeIDs: map[int]struct{}{1: {}}, want: false},
		{name: "tags with no excludes", tags: []int{1, 2}, excludeIDs: nil, want: false},
		{name: "tags with empty excludes", tags: []int{1, 2}, excludeIDs: map[int]struct{}{}, want: false},
		{name: "matching tag", tags: []int{1, 2, 3}, excludeIDs: map[int]struct{}{2: {}}, want: true},
		{name: "no matching tag", tags: []int{1, 2, 3}, excludeIDs: map[int]struct{}{4: {}}, want: false},
		{name: "first tag matches", tags: []int{5, 6, 7}, excludeIDs: map[int]struct{}{5: {}}, want: true},
		{name: "last tag matches", tags: []int{5, 6, 7}, excludeIDs: map[int]struct{}{7: {}}, want: true},
		{name: "multiple excludes one match", tags: []int{1}, excludeIDs: map[int]struct{}{1: {}, 2: {}, 3: {}}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := HasExcludeTag(tt.tags, tt.excludeIDs)

			if got != tt.want {
				t.Errorf("HasExcludeTag(%v, %v) = %v, want %v",
					tt.tags, tt.excludeIDs, got, tt.want)
			}
		})
	}
}

// --- ValidateSubtitleData PBT ---

func TestValidateSubtitleData_pure_text_never_rejected(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		// Generate printable ASCII text (0x20-0x7E) plus common whitespace.
		length := rapid.IntRange(1, 1024).Draw(t, "length")
		data := make([]byte, length)
		for i := range data {
			data[i] = byte(rapid.IntRange(0x20, 0x7E).Draw(t, "byte"))
		}

		err := ValidateSubtitleData(data)
		if err != nil {
			t.Errorf("ValidateSubtitleData(pure text, len=%d) = %v, want nil",
				length, err)
		}
	})
}

func TestCountNonTextBytes_never_negative(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		data := rapid.SliceOf(rapid.Byte()).Draw(t, "data")

		got := CountNonTextBytes(data)

		if got < 0 {
			t.Errorf("CountNonTextBytes() = %d, must be >= 0", got)
		}
		if got > len(data) {
			t.Errorf("CountNonTextBytes() = %d, must be <= len(data) %d", got, len(data))
		}
	})
}

func TestValidateSubtitleData_archive_magic_always_detected(t *testing.T) {
	t.Parallel()

	magics := [][]byte{
		[]byte("Rar!\x1a\x07\x00"),
		[]byte("Rar!\x1a\x07\x01\x00"),
		{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C},
		{0x1f, 0x8b},
		{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00},
		[]byte("BZh"),
		[]byte("PK\x03\x04"),
		[]byte("PK\x05\x06"),
	}
	rapid.Check(t, func(t *rapid.T) {
		magic := magics[rapid.IntRange(0, len(magics)-1).Draw(t, "magic_idx")]
		tail := rapid.SliceOfN(rapid.Byte(), 0, 100).Draw(t, "tail")
		data := append(append([]byte{}, magic...), tail...)

		err := ValidateSubtitleData(data)

		if !errors.Is(err, ErrBinaryData) {
			t.Errorf("ValidateSubtitleData(magic=%x + %d tail bytes) = %v, want ErrBinaryData",
				magic, len(tail), err)
		}
	})
}
