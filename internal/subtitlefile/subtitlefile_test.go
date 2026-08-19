package subtitlefile

import (
	"bytes"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

func TestPath(t *testing.T) {
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

			got := Path(tt.videoPath, Tags{Lang: tt.lang, HearingImpaired: tt.hi, Forced: tt.forced})

			if got != tt.want {
				t.Errorf("Path(%q, %q, hi=%v, forced=%v) = %q, want %q",
					tt.videoPath, tt.lang, tt.hi, tt.forced, got, tt.want)
			}
		})
	}
}

func TestManualPath(t *testing.T) {
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

			got := ManualPath(tt.videoPath, tt.n, Tags{Lang: tt.lang})

			if got != tt.want {
				t.Errorf("ManualPath(%q, %q, %d) = %q, want %q",
					tt.videoPath, tt.lang, tt.n, got, tt.want)
			}
		})
	}
}

func TestManualPath_variants(t *testing.T) {
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
			got := ManualPath(tt.videoPath, tt.n, Tags{Lang: tt.lang, HearingImpaired: tt.hi, Forced: tt.forced})
			if got != tt.want {
				t.Errorf("ManualPath(%q, %q, %d, hi=%t, forced=%t) = %q, want %q",
					tt.videoPath, tt.lang, tt.n, tt.hi, tt.forced, got, tt.want)
			}
		})
	}
}

// --- Path PBT ---

func TestPath_always_ends_with_srt(t *testing.T) {
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

		got := Path(videoPath, Tags{Lang: lang, HearingImpaired: hi, Forced: forced})

		if !strings.HasSuffix(got, ".srt") {
			t.Errorf("Path(%q, %q, %v, %v) = %q, should end with .srt",
				videoPath, lang, hi, forced, got)
		}
		if hi && !strings.Contains(got, ".hi") {
			t.Errorf("Path(%q, %q, hi=true) = %q, missing .hi",
				videoPath, lang, got)
		}
		if forced && !strings.Contains(got, ".forced") {
			t.Errorf("Path(%q, %q, forced=true) = %q, missing .forced",
				videoPath, lang, got)
		}
		base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
		suffix := strings.TrimPrefix(got, base)
		if !hi && strings.Contains(suffix, ".hi.") {
			t.Errorf("Path(%q, %q, hi=false) = %q, suffix %q should not contain .hi flag",
				videoPath, lang, got, suffix)
		}
		if !forced && strings.Contains(suffix, ".forced.") {
			t.Errorf("Path(%q, %q, forced=false) = %q, suffix %q should not contain .forced flag",
				videoPath, lang, got, suffix)
		}
	})
}

func TestPath_always_contains_language_code(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		lang := rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "lang")
		hi := rapid.Bool().Draw(t, "hi")
		forced := rapid.Bool().Draw(t, "forced")

		got := Path(videoPath, Tags{Lang: lang, HearingImpaired: hi, Forced: forced})

		if !strings.Contains(got, "."+lang+".") {
			t.Errorf("Path(%q, %q, %v, %v) = %q, should contain .%s.",
				videoPath, lang, hi, forced, got, lang)
		}
	})
}

func TestPath_hi_forced_suffix_ordering(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		lang := rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "lang")

		got := Path(videoPath, Tags{Lang: lang, HearingImpaired: true, Forced: true})

		hiIdx := strings.Index(got, ".hi.")
		forcedIdx := strings.Index(got, ".forced.")
		if hiIdx < 0 || forcedIdx < 0 {
			t.Errorf("Path(%q, %q, true, true) = %q, missing .hi. or .forced.",
				videoPath, lang, got)
		} else if hiIdx >= forcedIdx {
			t.Errorf("Path(%q, %q, true, true) = %q, .hi. should come before .forced.",
				videoPath, lang, got)
		}
	})
}

func TestManualPath_always_ends_with_srt(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		lang := rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "lang")
		n := rapid.IntRange(0, 100).Draw(t, "n")

		got := ManualPath(videoPath, n, Tags{Lang: lang})

		if !strings.HasSuffix(got, ".srt") {
			t.Errorf("ManualPath(%q, %q, %d) = %q, should end with .srt",
				videoPath, lang, n, got)
		}
	})
}

func TestManualPath_always_contains_language_code(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		lang := rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "lang")
		n := rapid.IntRange(0, 100).Draw(t, "n")

		got := ManualPath(videoPath, n, Tags{Lang: lang})

		if !strings.Contains(got, "."+lang+".") {
			t.Errorf("ManualPath(%q, %q, %d) = %q, should contain .%s.",
				videoPath, lang, n, got, lang)
		}
	})
}

func TestManualPath_always_contains_number(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`/media/[a-zA-Z0-9._-]{1,30}\.[a-z]{2,4}`).Draw(t, "video_path")
		lang := rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "lang")
		n := rapid.IntRange(0, 100).Draw(t, "n")

		got := ManualPath(videoPath, n, Tags{Lang: lang})

		numStr := "." + strconv.Itoa(n) + ".srt"
		if !strings.HasSuffix(got, numStr) {
			t.Errorf("ManualPath(%q, %q, %d) = %q, should end with %q",
				videoPath, lang, n, got, numStr)
		}
	})
}

// TestPath_strips_video_extension verifies the original video file
// extension is replaced, not preserved in the output path.
func TestPath_strips_video_extension(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		ext := rapid.SampledFrom([]string{"mkv", "mp4", "avi", "wmv"}).Draw(t, "ext")
		video := rapid.StringMatching(`/media/[a-z]+`).Draw(t, "base") + "." + ext
		lang := rapid.StringMatching(`[a-z]{2}`).Draw(t, "lang")

		path := Path(video, Tags{Lang: lang})

		if !strings.HasSuffix(path, ".srt") {
			t.Errorf("Path() = %q, does not end with .srt", path)
		}
		if strings.HasSuffix(path, "."+ext+".srt") {
			t.Errorf("Path() = %q, still contains video extension .%s", path, ext)
		}
	})
}

// --- Validate ---

func TestValidate(t *testing.T) {
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

			err := Validate(tt.data)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Validate() = nil, want error")
				}
				if !errors.Is(err, errBinary) {
					t.Errorf("Validate() error = %v, want errBinary", err)
				}
				return
			}
			if err != nil {
				t.Errorf("Validate() unexpected error: %v", err)
			}
		})
	}
}

func TestValidate_checks_only_first_512_bytes(t *testing.T) {
	t.Parallel()

	// 512 bytes of valid text followed by binary garbage.
	// Should pass because only the first 512 bytes are checked.
	header := bytes.Repeat([]byte("subtitle text\n"), 37) // 37*14 = 518 > 512
	header = header[:512]
	data := append(header, bytes.Repeat([]byte{0x01}, 1000)...)

	err := Validate(data)
	if err != nil {
		t.Errorf("Validate() with clean header = %v, want nil", err)
	}
}

// --- CountNonText ---

func TestCountNonText(t *testing.T) {
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

			got := CountNonText(tt.data)

			if got != tt.want {
				t.Errorf("CountNonText(%v) = %d, want %d", tt.data, got, tt.want)
			}
		})
	}
}

// --- Validate PBT ---

func TestValidate_pure_text_never_rejected(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		// Generate printable ASCII text (0x20-0x7E) plus common whitespace.
		length := rapid.IntRange(1, 1024).Draw(t, "length")
		data := make([]byte, length)
		for i := range data {
			data[i] = byte(rapid.IntRange(0x20, 0x7E).Draw(t, "byte"))
		}

		err := Validate(data)
		if err != nil {
			t.Errorf("Validate(pure text, len=%d) = %v, want nil",
				length, err)
		}
	})
}

func TestCountNonText_never_negative(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		data := rapid.SliceOf(rapid.Byte()).Draw(t, "data")

		got := CountNonText(data)

		if got < 0 {
			t.Errorf("CountNonText() = %d, must be >= 0", got)
		}
		if got > len(data) {
			t.Errorf("CountNonText() = %d, must be <= len(data) %d", got, len(data))
		}
	})
}

func TestValidate_archive_magic_always_detected(t *testing.T) {
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

		err := Validate(data)

		if !errors.Is(err, errBinary) {
			t.Errorf("Validate(magic=%x + %d tail bytes) = %v, want errBinary",
				magic, len(tail), err)
		}
	})
}

func TestVariantFromFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		want   api.Variant
		hi     bool
		forced bool
	}{
		{name: "standard", hi: false, forced: false, want: api.DefaultVariant},
		{name: "hi", hi: true, forced: false, want: api.VariantHI},
		{name: "forced", hi: false, forced: true, want: api.VariantForced},
		{name: "hi takes precedence over forced", hi: true, forced: true, want: api.VariantHI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := VariantFromFlags(Tags{HearingImpaired: tt.hi, Forced: tt.forced})
			if got != tt.want {
				t.Errorf("VariantFromFlags(Tags{HearingImpaired: %v, Forced: %v}) = %q, want %q",
					tt.hi, tt.forced, got, tt.want)
			}
		})
	}
}
