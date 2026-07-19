package embedded

import (
	"context"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// --- detectHIFromName ---

func TestDetectHIFromName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "contains_sdh_lower", input: "English SDH", want: true},
		{name: "contains_sdh_mixed", input: "english sdh", want: true},
		{name: "contains_hearing_impaired", input: "English (Hearing Impaired)", want: true},
		{name: "contains_hearing_impaired_lower", input: "hearing impaired", want: true},
		{name: "contains_hard_of_hearing", input: "Hard of Hearing", want: true},
		{name: "contains_hard_of_hearing_lower", input: "hard of hearing", want: true},
		{name: "no_hi_markers", input: "English", want: false},
		{name: "empty_string", input: "", want: false},
		{name: "partial_match_sd", input: "SD quality", want: false},
		{name: "partial_match_hear", input: "hearing", want: false},
		{name: "forced_not_hi", input: "English Forced", want: false},
		{name: "sdh_in_middle", input: "Track SDH English", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := detectHIFromName(tt.input)
			if got != tt.want {
				t.Errorf("detectHIFromName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- detectForcedFromName ---

func TestDetectForcedFromName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "contains_forced", input: "English Forced", want: true},
		{name: "contains_forced_lower", input: "forced", want: true},
		{name: "contains_foreign", input: "Foreign Parts Only", want: true},
		{name: "contains_foreign_lower", input: "foreign", want: true},
		{name: "no_forced_markers", input: "English", want: false},
		{name: "empty_string", input: "", want: false},
		{name: "sdh_not_forced", input: "English SDH", want: false},
		{name: "forced_in_middle", input: "Track Forced English", want: true},
		{name: "foreign_in_middle", input: "Track Foreign Parts", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := detectForcedFromName(tt.input)
			if got != tt.want {
				t.Errorf("detectForcedFromName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- normalizeTrack ---

func TestNormalizeTrack_valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		codec     string
		lang      string
		trackName string
		wantLang  string
		index     int
		forced    bool
		hi        bool
		wantForce bool
		wantHI    bool
	}{
		{
			name:      "alpha3_to_alpha2",
			index:     1,
			codec:     "srt",
			lang:      "eng",
			trackName: "English",
			wantLang:  "en",
		},
		{
			name:      "alpha2_passthrough",
			index:     2,
			codec:     "ass",
			lang:      "fr",
			trackName: "French",
			wantLang:  "fr",
		},
		{
			name:      "forced_from_flag",
			index:     3,
			codec:     "srt",
			lang:      "eng",
			trackName: "English",
			forced:    true,
			wantLang:  "en",
			wantForce: true,
		},
		{
			name:      "forced_from_name",
			index:     4,
			codec:     "srt",
			lang:      "eng",
			trackName: "English Forced",
			wantLang:  "en",
			wantForce: true,
		},
		{
			name:      "hi_from_flag",
			index:     5,
			codec:     "srt",
			lang:      "eng",
			trackName: "English",
			hi:        true,
			wantLang:  "en",
			wantHI:    true,
		},
		{
			name:      "hi_from_name_sdh",
			index:     6,
			codec:     "srt",
			lang:      "eng",
			trackName: "English SDH",
			wantLang:  "en",
			wantHI:    true,
		},
		{
			name:      "hi_from_name_hearing_impaired",
			index:     7,
			codec:     "srt",
			lang:      "eng",
			trackName: "English (Hearing Impaired)",
			wantLang:  "en",
			wantHI:    true,
		},
		{
			name:      "unknown_alpha3_used_as_is",
			index:     8,
			codec:     "srt",
			lang:      "xyz",
			trackName: "Unknown",
			wantLang:  "xyz",
		},
		{
			name:      "bcp47_extracts_primary_subtag",
			index:     9,
			codec:     "srt",
			lang:      "en-US",
			trackName: "English US",
			wantLang:  "en",
		},
		{
			name:      "bcp47_with_alpha3_primary",
			index:     10,
			codec:     "srt",
			lang:      "por-BR",
			trackName: "Portuguese BR",
			wantLang:  "pt",
		},
		{
			// A leading dash is not a subtag boundary (index 0): the tag is
			// left intact rather than truncated to an empty language.
			name:      "leading_dash_not_truncated",
			index:     11,
			codec:     "srt",
			lang:      "-en",
			trackName: "name",
			wantLang:  "-en",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeTrack(tt.index, tt.codec, tt.lang, tt.trackName, tt.forced, tt.hi)
			if got == nil {
				t.Fatalf("normalizeTrack(%d, %q, %q, %q, %v, %v) = nil, want non-nil",
					tt.index, tt.codec, tt.lang, tt.trackName, tt.forced, tt.hi)
			}
			if got.lang != tt.wantLang {
				t.Errorf("lang = %q, want %q", got.lang, tt.wantLang)
			}
			if got.forced != tt.wantForce {
				t.Errorf("forced = %v, want %v", got.forced, tt.wantForce)
			}
			if got.hearingImpaired != tt.wantHI {
				t.Errorf("hearingImpaired = %v, want %v", got.hearingImpaired, tt.wantHI)
			}
			if got.codec != tt.codec {
				t.Errorf("codec = %q, want %q", got.codec, tt.codec)
			}
			if got.index != tt.index {
				t.Errorf("index = %d, want %d", got.index, tt.index)
			}
		})
	}
}

func TestNormalizeTrack_returns_nil(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		lang string
	}{
		{name: "empty_language", lang: ""},
		{name: "undefined_language", lang: "und"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeTrack(1, "srt", tt.lang, "Test", false, false)
			if got != nil {
				t.Errorf("normalizeTrack(1, \"srt\", %q, ...) = %+v, want nil",
					tt.lang, got)
			}
		})
	}
}

// --- Detector.DetectTracks: error is observable ---

// A nonexistent file must surface an error — "error" stays distinguishable
// from "no tracks" (nil, nil) at the seam.
func TestDetector_DetectTracks_nonexistent_returns_error(t *testing.T) {
	t.Parallel()
	d := Detector{}
	tracks, err := d.DetectTracks(context.Background(), "/nonexistent/file.mkv")
	if err == nil {
		t.Fatal("DetectTracks(nonexistent) error = nil, want error")
	}
	if tracks != nil {
		t.Errorf("DetectTracks(nonexistent) = %v, want nil tracks with error", tracks)
	}
}

// --- normalizeCodecName ---

func TestNormalizeCodecName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want string
	}{
		{input: "subrip", want: "srt"},
		{input: "ass", want: "ass"},
		{input: "ssa", want: "ssa"},
		{input: "webvtt", want: "webvtt"},
		{input: "mov_text", want: "mov_text"},
		{input: "hdmv_pgs_subtitle", want: "pgs"},
		{input: "dvd_subtitle", want: "vobsub"},
		{input: "dvb_subtitle", want: "dvbsub"},
		{input: "dvb_teletext", want: "teletext"},
		{input: "eia_608", want: "cea608"},
		{input: "ttml", want: "ttml"},
		{input: "text", want: "mov_text"},
		{input: "unknown_codec", want: "unknown_codec"},
		{input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := normalizeCodecName(tt.input); got != tt.want {
				t.Errorf("normalizeCodecName(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- PBT: normalizeTrack invariants ---

func TestNormalizeTrack_valid_output_invariants(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		lang := rapid.StringMatching(`[a-z]{3}`).Draw(t, "lang")
		if lang == "und" {
			lang = "eng"
		}
		codec := rapid.StringMatching(`[a-z]{2,6}`).Draw(t, "codec")
		name := rapid.StringMatching(`[A-Za-z ]{0,30}`).Draw(t, "name")
		index := rapid.IntRange(1, 100).Draw(t, "index")
		forced := rapid.Bool().Draw(t, "forced")
		hi := rapid.Bool().Draw(t, "hi")

		got := normalizeTrack(index, codec, lang, name, forced, hi)
		if got == nil {
			t.Fatalf("normalizeTrack = nil for valid lang %q", lang)
			return
		}
		if got.lang == "" || got.lang == "und" {
			t.Errorf("lang = %q, want non-empty and not 'und'", got.lang)
		}
		if got.codec != codec {
			t.Errorf("codec = %q, want %q", got.codec, codec)
		}
		if got.index != index {
			t.Errorf("index = %d, want %d", got.index, index)
		}
		if forced && !got.forced {
			t.Error("forced = false, want true")
		}
		if hi && !got.hearingImpaired {
			t.Error("hearingImpaired = false, want true")
		}
	})
}

func TestNormalizeTrack_nil_for_invalid_lang(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		lang := rapid.SampledFrom([]string{"", "und"}).Draw(t, "lang")
		got := normalizeTrack(1, "srt", lang, "Test", false, false)
		if got != nil {
			t.Errorf("normalizeTrack(lang=%q) = %+v, want nil", lang, got)
		}
	})
}

func TestDetectHIFromName_case_insensitive(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.StringMatching(`[A-Za-z0-9 ]{0,50}`).Draw(t, "name")
		lower := strings.ToLower(s)
		upper := strings.ToUpper(s)
		if detectHIFromName(lower) != detectHIFromName(upper) {
			t.Errorf("case mismatch for %q", s)
		}
	})
}

func TestDetectForcedFromName_case_insensitive(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.StringMatching(`[A-Za-z0-9 ]{0,50}`).Draw(t, "name")
		lower := strings.ToLower(s)
		upper := strings.ToUpper(s)
		if detectForcedFromName(lower) != detectForcedFromName(upper) {
			t.Errorf("case mismatch for %q", s)
		}
	})
}
