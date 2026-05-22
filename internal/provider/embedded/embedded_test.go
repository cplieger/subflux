package embedded

import (
	"context"
	"strings"
	"testing"

	"subflux/internal/api"

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
		{"contains_sdh_lower", "English SDH", true},
		{"contains_sdh_mixed", "english sdh", true},
		{"contains_hearing_impaired", "English (Hearing Impaired)", true},
		{"contains_hearing_impaired_lower", "hearing impaired", true},
		{"contains_hard_of_hearing", "Hard of Hearing", true},
		{"contains_hard_of_hearing_lower", "hard of hearing", true},
		{"no_hi_markers", "English", false},
		{"empty_string", "", false},
		{"partial_match_sd", "SD quality", false},
		{"partial_match_hear", "hearing", false},
		{"forced_not_hi", "English Forced", false},
		{"sdh_in_middle", "Track SDH English", true},
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
		{"contains_forced", "English Forced", true},
		{"contains_forced_lower", "forced", true},
		{"contains_foreign", "Foreign Parts Only", true},
		{"contains_foreign_lower", "foreign", true},
		{"no_forced_markers", "English", false},
		{"empty_string", "", false},
		{"sdh_not_forced", "English SDH", false},
		{"forced_in_middle", "Track Forced English", true},
		{"foreign_in_middle", "Track Foreign Parts", true},
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
		index     int
		codec     string
		lang      string
		trackName string
		forced    bool
		hi        bool
		wantLang  string
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
		{"empty_language", ""},
		{"undefined_language", "und"},
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

// --- isIgnoredCodec ---

func TestIsIgnoredCodec(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		ignorePGS    bool
		ignoreVobSub bool
		ignoreASS    bool
		codec        string
		want         bool
	}{
		{"pgs_ignored", true, false, false, "pgs", true},
		{"pgs_not_ignored", false, false, false, "pgs", false},
		{"vobsub_ignored", false, true, false, "vobsub", true},
		{"vobsub_not_ignored", false, false, false, "vobsub", false},
		{"ass_ignored", false, false, true, "ass", true},
		{"ssa_ignored", false, false, true, "ssa", true},
		{"ass_not_ignored", false, false, false, "ass", false},
		{"srt_never_ignored", true, true, true, "srt", false},
		{"webvtt_never_ignored", true, true, true, "webvtt", false},
		{"all_off_pgs", false, false, false, "pgs", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Provider{
				ignorePGS:    tt.ignorePGS,
				ignoreVobSub: tt.ignoreVobSub,
				ignoreASS:    tt.ignoreASS,
			}
			got := p.isIgnoredCodec(tt.codec)
			if got != tt.want {
				t.Errorf("isIgnoredCodec(%q) = %v, want %v",
					tt.codec, got, tt.want)
			}
		})
	}
}

// --- Factory ---

func TestFactory(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings map[string]any
		wantPGS  bool
		wantVob  bool
		wantASS  bool
	}{
		{"nil_settings", nil, false, false, false},
		{"all_defaults", map[string]any{}, false, false, false},
		{"ignore_pgs", map[string]any{"ignore_pgs": true}, true, false, false},
		{"ignore_vobsub", map[string]any{"ignore_vobsub": true}, false, true, false},
		{"ignore_ass", map[string]any{"ignore_ass": true}, false, false, true},
		{
			"all_ignored",
			map[string]any{"ignore_pgs": true, "ignore_vobsub": true, "ignore_ass": true},
			true, true, true,
		},
		{"string_true_accepted", map[string]any{"ignore_pgs": "true"}, true, false, false},
		{"non_true_value_ignored", map[string]any{"ignore_pgs": "yes"}, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prov, err := Factory(context.Background(), tt.settings)
			if err != nil {
				t.Fatalf("Factory(%v) error: %v", tt.settings, err)
			}
			p, ok := prov.(*Provider)
			if !ok {
				t.Fatalf("Factory returned %T, want *Provider", prov)
			}
			if p.ignorePGS != tt.wantPGS {
				t.Errorf("ignorePGS = %v, want %v", p.ignorePGS, tt.wantPGS)
			}
			if p.ignoreVobSub != tt.wantVob {
				t.Errorf("ignoreVobSub = %v, want %v", p.ignoreVobSub, tt.wantVob)
			}
			if p.ignoreASS != tt.wantASS {
				t.Errorf("ignoreASS = %v, want %v", p.ignoreASS, tt.wantASS)
			}
		})
	}
}

// --- Provider.Name ---

func TestProviderName(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	if got := p.Name(); got != api.ProviderNameEmbedded {
		t.Errorf("Name() = %q, want %q", got, api.ProviderNameEmbedded)
	}
}

// --- ProviderDirect.DetectTracks: nonexistent file ---

func TestProviderDirect_DetectTracks_nonexistent(t *testing.T) {
	t.Parallel()
	p := ProviderDirect{}
	tracks := p.DetectTracks(context.Background(), "/nonexistent/file.mkv")
	if tracks != nil {
		t.Errorf("DetectTracks(nonexistent) = %v, want nil", tracks)
	}
}

// --- Search: nonexistent file returns error ---

func TestSearch_nonexistent_file_returns_error(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	req := &api.SearchRequest{
		VideoPath: "/nonexistent/file.mkv",
		Languages: []string{"en"},
	}
	_, err := p.Search(context.Background(), req)
	if err == nil {
		t.Fatal("Search(nonexistent) = nil error, want error")
	}
}

// --- Search: empty VideoPath returns nil ---

func TestSearch_empty_video_path_returns_nil(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	req := &api.SearchRequest{
		ReleaseName: "",
		Languages:   []string{"en"},
	}
	results, err := p.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("Search(empty) error: %v", err)
	}
	if results != nil {
		t.Errorf("Search(empty) = %v, want nil", results)
	}
}

// --- Download: always returns error ---

func TestDownload_always_returns_error(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	data, err := p.Download(context.Background(), &api.Subtitle{})
	if err == nil {
		t.Fatal("Download() = nil error, want error")
	}
	if data != nil {
		t.Errorf("Download() data = %v, want nil", data)
	}
	if !strings.Contains(err.Error(), "cannot be downloaded") {
		t.Errorf("Download() error = %q, want message containing 'cannot be downloaded'", err)
	}
}

// --- normalizeCodecName ---

func TestNormalizeCodecName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want string
	}{
		{"subrip", "srt"},
		{"ass", "ass"},
		{"ssa", "ssa"},
		{"webvtt", "webvtt"},
		{"mov_text", "mov_text"},
		{"hdmv_pgs_subtitle", "pgs"},
		{"dvd_subtitle", "vobsub"},
		{"dvb_subtitle", "dvbsub"},
		{"dvb_teletext", "teletext"},
		{"eia_608", "cea608"},
		{"ttml", "ttml"},
		{"text", "mov_text"},
		{"unknown_codec", "unknown_codec"},
		{"", ""},
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

func TestIsIgnoredCodec_never_ignores_text_codecs(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		p := &Provider{
			ignorePGS:    rapid.Bool().Draw(t, "ignorePGS"),
			ignoreVobSub: rapid.Bool().Draw(t, "ignoreVobSub"),
			ignoreASS:    rapid.Bool().Draw(t, "ignoreASS"),
		}
		for _, codec := range []string{"srt", "webvtt", "mov_text", "ttml"} {
			if p.isIgnoredCodec(codec) {
				t.Errorf("isIgnoredCodec(%q) = true; text codecs should never be ignored", codec)
			}
		}
	})
}
