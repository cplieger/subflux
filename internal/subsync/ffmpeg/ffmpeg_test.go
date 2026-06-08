package ffmpeg

import (
	"context"
	"testing"

	"github.com/cplieger/subflux/internal/provider/classify"
)

var testLangMapper LangMapper = classify.Alpha2FromAlpha3

// mockRunner implements CommandRunner for testing without real ffmpeg binaries.
type mockRunner struct {
	err    error
	output []byte
}

func (m mockRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return m.output, m.err
}

// Compile-time assertion: mockRunner satisfies CommandRunner.
var _ CommandRunner = mockRunner{}

func TestFFprobeStreams_with_mock(t *testing.T) {
	t.Parallel()
	orig := DefaultRunner
	defer func() { DefaultRunner = orig }()

	DefaultRunner = mockRunner{
		output: []byte(`{"streams":[{"index":0,"codec_name":"subrip","codec_type":"subtitle","r_frame_rate":"0/0","tags":{"language":"eng"},"disposition":{"forced":0}}]}`),
	}

	tracks, err := ParseProbeOutput(DefaultRunner.(mockRunner).output)
	if err != nil {
		t.Fatalf("ParseProbeOutput with mock data: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	if tracks[0].CodecName != "subrip" {
		t.Errorf("CodecName = %q, want subrip", tracks[0].CodecName)
	}
}

func TestNormalizeFFprobeLang(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want string
	}{
		{input: "eng", want: "en"},
		{input: "fre", want: "fr"},
		{input: "fra", want: "fr"},
		{input: "en", want: "en"},
		{input: "fr", want: "fr"},
		{input: "en-US", want: "en"},
		{input: "fr-FR", want: "fr"},
		{input: "pt-BR", want: "pt"},
		{input: "und", want: ""},
		{input: "undetermined", want: ""},
		{input: "", want: ""},
		{input: "pob", want: "pb"},
		{input: "ger", want: "de"},
		{input: "deu", want: "de"},
		{input: "jpn", want: "ja"},
		{input: "chi", want: "zh"},
		{input: "zho", want: "zh"},
		{input: "spa", want: "es"},
		{input: "ita", want: "it"},
		{input: "por", want: "pt"},
		{input: "rus", want: "ru"},
		{input: "kor", want: "ko"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := NormalizeFFprobeLang(tt.input, testLangMapper)
			if got != tt.want {
				t.Errorf("NormalizeFFprobeLang(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestIsTextSubtitleCodec(t *testing.T) {
	t.Parallel()
	text := []string{
		"subrip", "srt", "ass", "ssa", "mov_text", "webvtt",
		"text", "ttml", "stl", "realtext", "subviewer",
		"subviewer1", "microdvd", "mpl2", "jacosub", "sami",
	}
	for _, c := range text {
		if !IsTextSubtitleCodec(c) {
			t.Errorf("IsTextSubtitleCodec(%q) = false, want true", c)
		}
	}

	bitmap := []string{
		"hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle",
		"unknown_codec", "",
	}
	for _, c := range bitmap {
		if IsTextSubtitleCodec(c) {
			t.Errorf("IsTextSubtitleCodec(%q) = true, want false", c)
		}
	}
}

func TestSelectBestSubTrack(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		lang        string
		excludeLang string
		tracks      []Track
		wantIdx     int
		wantNil     bool
	}{
		{
			name:    "prefers_srt",
			tracks:  []Track{{Index: 2, CodecName: "ass", Language: "eng"}, {Index: 3, CodecName: "subrip", Language: "eng"}},
			wantIdx: 3,
		},
		{
			name:    "skips_forced",
			tracks:  []Track{{Index: 2, CodecName: "subrip", Language: "eng", Forced: true}, {Index: 3, CodecName: "subrip", Language: "eng"}},
			wantIdx: 3,
		},
		{
			name:    "filters_by_lang",
			tracks:  []Track{{Index: 2, CodecName: "subrip", Language: "eng"}, {Index: 3, CodecName: "subrip", Language: "fre"}},
			lang:    "fr",
			wantIdx: 3,
		},
		{
			name:        "excludes_lang",
			tracks:      []Track{{Index: 2, CodecName: "subrip", Language: "eng"}},
			excludeLang: "en",
			wantNil:     true,
		},
		{
			name:    "skips_bitmap",
			tracks:  []Track{{Index: 2, CodecName: "hdmv_pgs_subtitle", Language: "eng"}},
			wantNil: true,
		},
		{
			name:    "empty",
			tracks:  nil,
			wantNil: true,
		},
		{
			name:    "forced_only",
			tracks:  []Track{{Index: 2, CodecName: "subrip", Language: "eng", Forced: true}},
			wantIdx: 2,
		},
		{
			name:    "prefers_non_forced_ass",
			tracks:  []Track{{Index: 2, CodecName: "ass", Language: "eng", Forced: true}, {Index: 3, CodecName: "ass", Language: "eng"}},
			wantIdx: 3,
		},
		{
			name:    "no_matching_lang",
			tracks:  []Track{{Index: 2, CodecName: "subrip", Language: "eng"}},
			lang:    "ja",
			wantNil: true,
		},
		{
			name:        "lang_and_excludeLang_combined",
			tracks:      []Track{{Index: 1, CodecName: "subrip", Language: "eng"}, {Index: 2, CodecName: "subrip", Language: "fre"}, {Index: 3, CodecName: "subrip", Language: "spa"}},
			lang:        "fr",
			excludeLang: "en",
			wantIdx:     2,
		},
		{
			name:        "lang_equals_excludeLang",
			tracks:      []Track{{Index: 1, CodecName: "subrip", Language: "eng"}, {Index: 2, CodecName: "subrip", Language: "fre"}},
			lang:        "en",
			excludeLang: "en",
			wantNil:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			best := SelectBestSubTrack(tt.tracks, tt.lang, tt.excludeLang, testLangMapper)
			if tt.wantNil {
				if best != nil {
					t.Fatalf("expected nil, got index %d", best.Index)
				}
				return
			}
			if best == nil {
				t.Fatal("expected a track, got nil")
			}
			if best.Index != tt.wantIdx {
				t.Errorf("got index %d, want %d", best.Index, tt.wantIdx)
			}
		})
	}
}

func TestShortStreamType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want string
	}{
		{input: "subtitle", want: "s"},
		{input: "audio", want: "a"},
		{input: "video", want: "v"},
		{input: "other", want: "other"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := shortStreamType(tt.input); got != tt.want {
				t.Errorf("shortStreamType(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeFFprobeLang_three_letter_unknown(t *testing.T) {
	t.Parallel()
	got := NormalizeFFprobeLang("xyz", testLangMapper)
	if got != "xyz" {
		t.Errorf("NormalizeFFprobeLang(%q) = %q, want %q", "xyz", got, "xyz")
	}
}

func TestNormalizeFFprobeLang_bcp47_with_region(t *testing.T) {
	t.Parallel()
	got := NormalizeFFprobeLang("por-BR", testLangMapper)
	if got != "pt" {
		t.Errorf("NormalizeFFprobeLang(%q) = %q, want %q", "por-BR", got, "pt")
	}
}

func TestAlpha2FromAlpha3(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want string
	}{
		{input: "eng", want: "en"},
		{input: "fre", want: "fr"},
		{input: "fra", want: "fr"},
		{input: "unknown", want: ""},
		{input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := classify.Alpha2FromAlpha3(tt.input); got != tt.want {
				t.Errorf("Alpha2FromAlpha3(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestParseFrameRate_fraction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  float64
	}{
		{name: "NTSC film", input: "24000/1001", want: 23.976023976023978},
		{name: "cinema", input: "24/1", want: 24.0},
		{name: "PAL", input: "25/1", want: 25.0},
		{name: "NTSC video", input: "30000/1001", want: 29.97002997002997},
		{name: "integer 30", input: "30/1", want: 30.0},
		{name: "plain float", input: "23.976", want: 23.976},
		{name: "plain integer", input: "25", want: 25.0},
		{name: "zero denominator", input: "24/0", want: 0},
		{name: "empty string", input: "", want: 0},
		{name: "garbage", input: "abc", want: 0},
		{name: "fraction bad num", input: "abc/1001", want: 0},
		{name: "fraction bad den", input: "24000/xyz", want: 0},
		{name: "no slash plain bad", input: "not-a-number", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseFrameRate(tt.input)
			if got != tt.want {
				t.Errorf("parseFrameRate(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeFFprobeLang_case_insensitive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want string
	}{
		{input: "ENG", want: "en"},
		{input: "Fre", want: "fr"},
		{input: "EN", want: "en"},
		{input: "UND", want: ""},
		{input: "Undetermined", want: ""},
		{input: "EN-US", want: "en"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := NormalizeFFprobeLang(tt.input, testLangMapper)
			if got != tt.want {
				t.Errorf("NormalizeFFprobeLang(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestParseProbeOutput(t *testing.T) {
	t.Parallel()
	data := []byte(`{"streams":[{"index":0,"codec_name":"subrip","codec_type":"subtitle","r_frame_rate":"0/0","tags":{"language":"eng","title":"English"},"disposition":{"forced":0,"hearing_impaired":1}}]}`)
	tracks, err := ParseProbeOutput(data)
	if err != nil {
		t.Fatalf("ParseProbeOutput: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	tr := tracks[0]
	if tr.CodecName != "subrip" {
		t.Errorf("CodecName = %q, want subrip", tr.CodecName)
	}
	if tr.Language != "eng" {
		t.Errorf("Language = %q, want eng", tr.Language)
	}
	if tr.Title != "English" {
		t.Errorf("Title = %q, want English", tr.Title)
	}
	if !tr.HearingImpaired {
		t.Error("expected HearingImpaired=true")
	}
	if tr.Forced {
		t.Error("expected Forced=false")
	}
}
