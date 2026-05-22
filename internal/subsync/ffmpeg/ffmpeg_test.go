package ffmpeg

import (
	"context"
	"subflux/internal/provider/classify"
	"testing"
)

var testLangMapper LangMapper = classify.Alpha2FromAlpha3

// mockRunner implements CommandRunner for testing without real ffmpeg binaries.
type mockRunner struct {
	output []byte
	err    error
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
		{"eng", "en"},
		{"fre", "fr"},
		{"fra", "fr"},
		{"en", "en"},
		{"fr", "fr"},
		{"en-US", "en"},
		{"fr-FR", "fr"},
		{"pt-BR", "pt"},
		{"und", ""},
		{"undetermined", ""},
		{"", ""},
		{"pob", "pb"},
		{"ger", "de"},
		{"deu", "de"},
		{"jpn", "ja"},
		{"chi", "zh"},
		{"zho", "zh"},
		{"spa", "es"},
		{"ita", "it"},
		{"por", "pt"},
		{"rus", "ru"},
		{"kor", "ko"},
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
		tracks      []Track
		lang        string
		excludeLang string
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
		{"subtitle", "s"},
		{"audio", "a"},
		{"video", "v"},
		{"other", "other"},
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
		{"eng", "en"},
		{"fre", "fr"},
		{"fra", "fr"},
		{"unknown", ""},
		{"", ""},
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
		{"NTSC film", "24000/1001", 23.976023976023978},
		{"cinema", "24/1", 24.0},
		{"PAL", "25/1", 25.0},
		{"NTSC video", "30000/1001", 29.97002997002997},
		{"integer 30", "30/1", 30.0},
		{"plain float", "23.976", 23.976},
		{"plain integer", "25", 25.0},
		{"zero denominator", "24/0", 0},
		{"empty string", "", 0},
		{"garbage", "abc", 0},
		{"fraction bad num", "abc/1001", 0},
		{"fraction bad den", "24000/xyz", 0},
		{"no slash plain bad", "not-a-number", 0},
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
		{"ENG", "en"},
		{"Fre", "fr"},
		{"EN", "en"},
		{"UND", ""},
		{"Undetermined", ""},
		{"EN-US", "en"},
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
