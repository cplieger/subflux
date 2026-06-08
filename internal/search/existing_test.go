package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

// --- hasSubtitle ---

func TestHasSubtitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lang     string
		variant  api.Variant
		existing existingSubs
		want     bool
	}{
		{
			name: "normal external match",
			existing: existingSubs{
				External: []externalSub{
					{Path: "/movie.fr.srt", Lang: "fr"},
				},
			},
			lang: "fr", variant: "standard", want: true,
		},
		{
			name: "hi external match",
			existing: existingSubs{
				External: []externalSub{
					{Path: "/movie.fr.hi.srt", Lang: "fr", HI: true},
				},
			},
			lang: "fr", variant: "hi", want: true,
		},
		{
			name: "forced external match",
			existing: existingSubs{
				External: []externalSub{
					{Path: "/movie.fr.forced.srt", Lang: "fr", Forced: true},
				},
			},
			lang: "fr", variant: "forced", want: true,
		},
		{
			name: "standard embedded match",
			existing: existingSubs{
				Embedded: []embeddedSub{
					{Lang: "en", Codec: "subrip"},
				},
			},
			lang: "en", variant: "standard", want: true,
		},
		{
			name: "hi embedded match",
			existing: existingSubs{
				Embedded: []embeddedSub{
					{Lang: "en", HI: true},
				},
			},
			lang: "en", variant: "hi", want: true,
		},
		{
			name: "forced embedded match",
			existing: existingSubs{
				Embedded: []embeddedSub{
					{Lang: "en", Forced: true},
				},
			},
			lang: "en", variant: "forced", want: true,
		},
		{
			name: "wrong variant",
			existing: existingSubs{
				External: []externalSub{
					{Path: "/movie.fr.srt", Lang: "fr"},
				},
			},
			lang: "fr", variant: "hi", want: false,
		},
		{
			name: "wrong language",
			existing: existingSubs{
				External: []externalSub{
					{Path: "/movie.en.srt", Lang: "en"},
				},
			},
			lang: "fr", variant: "standard", want: false,
		},
		{
			name: "embedded wrong language skips",
			existing: existingSubs{
				Embedded: []embeddedSub{
					{Lang: "de", Codec: "subrip"},
				},
			},
			lang: "en", variant: "standard", want: false,
		},
		{
			name:     "empty subs",
			existing: existingSubs{},
			lang:     "en", variant: "standard", want: false,
		},
		{
			name: "embedded ignored codec returns false",
			existing: existingSubs{
				Embedded:      []embeddedSub{{Lang: "en", Codec: "pgs"}},
				IgnoredCodecs: map[string]bool{"pgs": true},
			},
			lang: "en", variant: "standard", want: false,
		},
		{
			name: "embedded non-ignored codec still matches",
			existing: existingSubs{
				Embedded:      []embeddedSub{{Lang: "en", Codec: "subrip"}},
				IgnoredCodecs: map[string]bool{"pgs": true},
			},
			lang: "en", variant: "standard", want: true,
		},
		{
			name: "external match takes priority over ignored embedded",
			existing: existingSubs{
				External:      []externalSub{{Path: "/movie.en.srt", Lang: "en"}},
				Embedded:      []embeddedSub{{Lang: "en", Codec: "pgs"}},
				IgnoredCodecs: map[string]bool{"pgs": true},
			},
			lang: "en", variant: "standard", want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.existing.hasSubtitle(tt.lang, tt.variant)
			if got != tt.want {
				t.Errorf("hasSubtitle(%q, %q) = %v, want %v",
					tt.lang, tt.variant, got, tt.want)
			}
		})
	}
}

// --- matchesVariant ---

func TestMatchesVariant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		variant api.Variant
		hi      bool
		forced  bool
		want    bool
	}{
		{"standard matches standard", "standard", false, false, true},
		{"hi matches hi", "hi", true, false, true},
		{"forced matches forced", "forced", false, true, true},
		{"hi does not match standard", "standard", true, false, false},
		{"forced does not match standard", "standard", false, true, false},
		{"standard does not match hi", "hi", false, false, false},
		{"standard does not match forced", "forced", false, false, false},
		{"empty variant treated as standard", "", false, false, true},
		{"unknown variant treated as standard", "unknown", false, false, true},
		{"both hi and forced matches hi", "hi", true, true, true},
		{"both hi and forced matches forced", "forced", true, true, true},
		{"both hi and forced does not match standard", "standard", true, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchesVariant(tt.hi, tt.forced, tt.variant)
			if got != tt.want {
				t.Errorf("matchesVariant(%v, %v, %q) = %v, want %v",
					tt.hi, tt.forced, tt.variant, got, tt.want)
			}
		})
	}
}

// --- parseExternalSubPath ---

func TestParseExternalSubPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		base string
		ext  string
		want externalSub
	}{
		{
			"simple language",
			"/path/movie.en.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.en.srt", Lang: "en"},
		},
		{
			"language with HI",
			"/path/movie.en.hi.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.en.hi.srt", Lang: "en", HI: true},
		},
		{
			"language with SDH",
			"/path/movie.en.sdh.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.en.sdh.srt", Lang: "en", HI: true},
		},
		{
			"language with forced",
			"/path/movie.fr.forced.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.fr.forced.srt", Lang: "fr", Forced: true},
		},
		{
			"language with foreign",
			"/path/movie.fr.foreign.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.fr.foreign.srt", Lang: "fr", Forced: true},
		},
		{
			"ass extension",
			"/path/movie.ja.ass",
			"/path/movie",
			".ass",
			externalSub{Path: "/path/movie.ja.ass", Lang: "ja"},
		},
		{
			"multiple flags hi and forced",
			"/path/movie.en.hi.forced.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.en.hi.forced.srt", Lang: "en", HI: true, Forced: true},
		},
		{
			"uppercase flag tags are case-insensitive",
			"/path/movie.en.HI.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.en.HI.srt", Lang: "en", HI: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseExternalSubPath(tt.path, tt.base, tt.ext)
			if got.Lang != tt.want.Lang {
				t.Errorf("parseExternalSubPath(%q).Lang = %q, want %q",
					tt.path, got.Lang, tt.want.Lang)
			}
			if got.HI != tt.want.HI {
				t.Errorf("parseExternalSubPath(%q).HI = %v, want %v",
					tt.path, got.HI, tt.want.HI)
			}
			if got.Forced != tt.want.Forced {
				t.Errorf("parseExternalSubPath(%q).Forced = %v, want %v",
					tt.path, got.Forced, tt.want.Forced)
			}
			if got.Path != tt.want.Path {
				t.Errorf("parseExternalSubPath(%q).Path = %q, want %q",
					tt.path, got.Path, tt.want.Path)
			}
		})
	}
}

func TestParseExternalSubPath_empty_lang(t *testing.T) {
	t.Parallel()
	// A double-dot path like "movie..srt" produces an empty language segment
	// after trimming base+"." and ext. The caller (detectExisting) filters
	// these out via the sub.Lang != "" check.
	sub := parseExternalSubPath("/path/movie..srt", "/path/movie", ".srt")
	if sub.Lang != "" {
		t.Errorf("parseExternalSubPath(empty lang).Lang = %q, want empty", sub.Lang)
	}
}

// --- detectExisting ---

// trackDetector returns preconfigured tracks for testing.
type trackDetector struct {
	tracks []api.EmbeddedTrack
}

func (d trackDetector) DetectTracks(_ context.Context, _ string) []api.EmbeddedTrack { return d.tracks }

func TestDetectExisting_embedded_tracks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	detector := trackDetector{tracks: []api.EmbeddedTrack{
		{Lang: "en", Codec: "subrip", HearingImpaired: false, Forced: false},
		{Lang: "fr", Codec: "ass", HearingImpaired: true, Forced: false},
		{Lang: "de", Codec: "subrip", HearingImpaired: false, Forced: true},
	}}

	result := detectExisting(context.Background(), videoPath, detector, nil)

	if len(result.Embedded) != 3 {
		t.Fatalf("detectExisting(context.Background(), ).Embedded = %d tracks, want 3", len(result.Embedded))
	}
	if result.Embedded[0].Lang != "en" || result.Embedded[0].Codec != "subrip" {
		t.Errorf("Embedded[0] = %+v, want en/subrip", result.Embedded[0])
	}
	if result.Embedded[1].Lang != "fr" || !result.Embedded[1].HI {
		t.Errorf("Embedded[1] = %+v, want fr/HI", result.Embedded[1])
	}
	if result.Embedded[2].Lang != "de" || !result.Embedded[2].Forced {
		t.Errorf("Embedded[2] = %+v, want de/forced", result.Embedded[2])
	}
}

func TestDetectExisting_empty_video_path(t *testing.T) {
	t.Parallel()
	result := detectExisting(context.Background(), "", noopDetector{}, nil)
	if len(result.Embedded) != 0 {
		t.Errorf("detectExisting(context.Background(), \"\").Embedded = %d, want 0", len(result.Embedded))
	}
	if len(result.External) != 0 {
		t.Errorf("detectExisting(context.Background(), \"\").External = %d, want 0", len(result.External))
	}
}

// --- globEscape ---

func TestGlobEscape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no metacharacters", "/path/to/movie", "/path/to/movie"},
		{"brackets", "/path/movie [2024]", `/path/movie \[2024]`},
		{"asterisk", "/path/movie*special", `/path/movie\*special`},
		{"question mark", "/path/movie?name", `/path/movie\?name`},
		{"backslash", `/path/movie\name`, `/path/movie\\name`},
		{"all metacharacters", `[*?\]`, `\[\*\?\\]`},
		{"unicode passthrough", "/path/映画 (2024)", "/path/映画 (2024)"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := globEscape(tt.input)
			if got != tt.want {
				t.Errorf("globEscape(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- detectExisting external sub scanning ---

func TestDetectExisting_multiple_extensions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create subs with different extensions.
	for _, name := range []string{"movie.en.srt", "movie.en.ass", "movie.en.ssa", "movie.en.sub"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("sub"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	result := detectExisting(context.Background(), videoPath, noopDetector{}, nil)
	if len(result.External) != 4 {
		t.Errorf("detectExisting(context.Background(), ) found %d external subs, want 4 (all extensions)", len(result.External))
	}
}

func TestDetectExisting_no_matching_subs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create a file that doesn't match the glob pattern.
	path := filepath.Join(dir, "other.en.srt")
	if err := os.WriteFile(path, []byte("sub"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result := detectExisting(context.Background(), videoPath, noopDetector{}, nil)
	if len(result.External) != 0 {
		t.Errorf("detectExisting(context.Background(), ) found %d external subs, want 0 (no match)", len(result.External))
	}
}

// --- existingToSubtitleFiles ---

func TestExistingToSubtitleFiles_embedded_and_external(t *testing.T) {
	t.Parallel()

	existing := existingSubs{
		Embedded: []embeddedSub{
			{Lang: "en", Codec: "subrip", HI: false, Forced: false},
			{Lang: "fr", Codec: "ass", HI: true, Forced: false},
			{Lang: "de", Codec: "subrip", HI: false, Forced: true},
		},
		External: []externalSub{
			{Path: "/movie.es.srt", Lang: "es", HI: false, Forced: false},
			{Path: "/movie.it.hi.srt", Lang: "it", HI: true, Forced: false},
		},
	}

	files := existingToSubtitleFiles(existing)

	if len(files) != 5 {
		t.Fatalf("existingToSubtitleFiles() returned %d files, want 5", len(files))
	}

	// Embedded subs come first.
	if files[0].Language != "en" || files[0].Source != "embedded" || files[0].Codec != "subrip" || files[0].Variant != "standard" {
		t.Errorf("files[0] = %+v, want en/embedded/subrip/standard", files[0])
	}
	if files[1].Language != "fr" || files[1].Source != "embedded" || files[1].Variant != "hi" {
		t.Errorf("files[1] = %+v, want fr/embedded/hi", files[1])
	}
	if files[2].Language != "de" || files[2].Source != "embedded" || files[2].Variant != "forced" {
		t.Errorf("files[2] = %+v, want de/embedded/forced", files[2])
	}

	// External subs come after.
	if files[3].Language != "es" || files[3].Source != "external" || files[3].Path != "/movie.es.srt" || files[3].Variant != "standard" {
		t.Errorf("files[3] = %+v, want es/external/standard", files[3])
	}
	if files[4].Language != "it" || files[4].Source != "external" || files[4].Variant != "hi" {
		t.Errorf("files[4] = %+v, want it/external/hi", files[4])
	}
}

func TestExistingToSubtitleFiles_empty(t *testing.T) {
	t.Parallel()
	files := existingToSubtitleFiles(existingSubs{})
	if len(files) != 0 {
		t.Errorf("existingToSubtitleFiles(empty) returned %d files, want 0", len(files))
	}
}

func TestExistingToSubtitleFiles_dedup_same_lang(t *testing.T) {
	t.Parallel()

	// Three embedded English tracks: two subrip (deduped by codec) and one
	// ass. External files each get their own row (path is part of the PK).
	existing := existingSubs{
		Embedded: []embeddedSub{
			{Lang: "en", Codec: "subrip", HI: false, Forced: false},
			{Lang: "en", Codec: "ass", HI: false, Forced: false},
			{Lang: "en", Codec: "subrip", HI: false, Forced: false},
		},
		External: []externalSub{
			{Path: "/movie.en.srt", Lang: "en", HI: false, Forced: false},
		},
	}

	files := existingToSubtitleFiles(existing)

	// 2 embedded (subrip deduped, ass kept) + 1 external = 3
	if len(files) != 3 {
		t.Fatalf("existingToSubtitleFiles() returned %d files, want 3", len(files))
	}
	if files[0].Source != "embedded" || files[0].Codec != "subrip" {
		t.Errorf("files[0] = %+v, want en/embedded/subrip", files[0])
	}
	if files[1].Source != "embedded" || files[1].Codec != "ass" {
		t.Errorf("files[1] = %+v, want en/embedded/ass", files[1])
	}
	if files[2].Source != "external" || files[2].Path != "/movie.en.srt" {
		t.Errorf("files[2] = %+v, want en/external", files[2])
	}
}

func TestExistingToSubtitleFiles_external_multiple_paths(t *testing.T) {
	t.Parallel()

	// Multiple external subs with the same language but different paths
	// each get their own row (path is part of the PK).
	existing := existingSubs{
		External: []externalSub{
			{Path: "/movie.en.srt", Lang: "en", HI: false, Forced: false},
			{Path: "/movie.en.ass", Lang: "en", HI: false, Forced: false},
		},
	}

	files := existingToSubtitleFiles(existing)

	if len(files) != 2 {
		t.Fatalf("existingToSubtitleFiles(multi external) returned %d files, want 2", len(files))
	}
	if files[0].Path != "/movie.en.srt" {
		t.Errorf("files[0].Path = %q, want /movie.en.srt", files[0].Path)
	}
	if files[1].Path != "/movie.en.ass" {
		t.Errorf("files[1].Path = %q, want /movie.en.ass", files[1].Path)
	}
}

// --- Property-based tests ---

func TestMatchesVariant_standard_is_default(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Any unknown variant string should behave like "standard".
		variant := rapid.StringMatching(`[a-z]{5,10}`).Draw(t, "variant")
		if variant == "hi" || variant == "forced" {
			return // Skip known variants.
		}

		v := api.Variant(variant)
		// Standard means: not HI and not forced.
		if !matchesVariant(false, false, v) {
			t.Errorf("matchesVariant(false, false, %q) = false, want true (standard)", variant)
		}
		if matchesVariant(true, false, v) {
			t.Errorf("matchesVariant(true, false, %q) = true, want false (HI != standard)", variant)
		}
		if matchesVariant(false, true, v) {
			t.Errorf("matchesVariant(false, true, %q) = true, want false (forced != standard)", variant)
		}
	})
}

func TestGlobEscape_idempotent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Generate paths that may contain glob metacharacters.
		input := rapid.StringMatching(`[/a-zA-Z0-9._\[\]*?\\ ]{0,50}`).Draw(t, "path")

		first := globEscape(input)
		second := globEscape(first)

		// globEscape is NOT idempotent (escaping a backslash adds another backslash).
		// But applying it to an already-escaped string should still produce a valid
		// glob pattern. The key invariant: the first application should escape all
		// metacharacters.
		_ = second // Just verify no panic.

		// Verify all metacharacters in the output are escaped.
		for i, c := range first {
			if c == '*' || c == '?' || c == '[' {
				// Must be preceded by a backslash.
				if i == 0 || first[i-1] != '\\' {
					t.Errorf("globEscape(%q) = %q: unescaped %c at position %d",
						input, first, c, i)
				}
			}
		}
	})
}

// --- hasExternalSubtitle ---

func TestHasExternalSubtitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lang     string
		variant  api.Variant
		existing existingSubs
		want     bool
	}{
		{name: "standard external match", lang: "en", variant: "standard", existing: existingSubs{External: []externalSub{{Path: "/movie.en.srt", Lang: "en"}}}, want: true},
		{name: "hi external match", lang: "en", variant: "hi", existing: existingSubs{External: []externalSub{{Path: "/movie.en.hi.srt", Lang: "en", HI: true}}}, want: true},
		{name: "forced external match", lang: "en", variant: "forced", existing: existingSubs{External: []externalSub{{Path: "/movie.en.forced.srt", Lang: "en", Forced: true}}}, want: true},
		{name: "wrong language returns false", lang: "en", variant: "standard", existing: existingSubs{External: []externalSub{{Path: "/movie.fr.srt", Lang: "fr"}}}, want: false},
		{name: "wrong variant returns false", lang: "en", variant: "standard", existing: existingSubs{External: []externalSub{{Path: "/movie.en.hi.srt", Lang: "en", HI: true}}}, want: false},
		{name: "empty external returns false", lang: "en", variant: "standard", existing: existingSubs{}, want: false},
		{name: "embedded subs not checked", lang: "en", variant: "standard", existing: existingSubs{Embedded: []embeddedSub{{Lang: "en", Codec: "subrip"}}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.existing.hasExternalSubtitle(tt.lang, tt.variant)
			if got != tt.want {
				t.Errorf("hasExternalSubtitle(%q, %q) = %v, want %v", tt.lang, tt.variant, got, tt.want)
			}
		})
	}
}

// --- IgnoredCodecsFromConfig ---

type ignoredCodecConfig struct {
	providers map[api.ProviderID]api.ProviderCfg
}

func (c *ignoredCodecConfig) ProviderConfigs() map[api.ProviderID]api.ProviderCfg { return c.providers }

func TestIgnoredCodecsFromConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		providers map[api.ProviderID]api.ProviderCfg
		want      map[string]bool
		name      string
	}{
		{
			name:      "no embedded provider",
			providers: map[api.ProviderID]api.ProviderCfg{"opensubtitles": {Settings: map[string]any{"api_key": "x"}}},
			want:      nil,
		},
		{
			name:      "embedded provider with nil settings",
			providers: map[api.ProviderID]api.ProviderCfg{"embedded": {Settings: nil}},
			want:      nil,
		},
		{
			name:      "no ignore flags set",
			providers: map[api.ProviderID]api.ProviderCfg{"embedded": {Settings: map[string]any{}}},
			want:      nil,
		},
		{
			name:      "ignore_pgs only",
			providers: map[api.ProviderID]api.ProviderCfg{"embedded": {Settings: map[string]any{"ignore_pgs": true}}},
			want:      map[string]bool{"pgs": true},
		},
		{
			name:      "ignore_vobsub only",
			providers: map[api.ProviderID]api.ProviderCfg{"embedded": {Settings: map[string]any{"ignore_vobsub": true}}},
			want:      map[string]bool{"vobsub": true},
		},
		{
			name:      "ignore_ass adds both ass and ssa",
			providers: map[api.ProviderID]api.ProviderCfg{"embedded": {Settings: map[string]any{"ignore_ass": true}}},
			want:      map[string]bool{"ass": true, "ssa": true},
		},
		{
			name: "all ignore flags set",
			providers: map[api.ProviderID]api.ProviderCfg{"embedded": {Settings: map[string]any{
				"ignore_pgs":    true,
				"ignore_vobsub": true,
				"ignore_ass":    true,
			}}},
			want: map[string]bool{"pgs": true, "vobsub": true, "ass": true, "ssa": true},
		},
		{
			name: "false flags return nil",
			providers: map[api.ProviderID]api.ProviderCfg{"embedded": {Settings: map[string]any{
				"ignore_pgs":    false,
				"ignore_vobsub": false,
				"ignore_ass":    false,
			}}},
			want: nil,
		},
		{
			name:      "string true accepted by SettingBool",
			providers: map[api.ProviderID]api.ProviderCfg{"embedded": {Settings: map[string]any{"ignore_pgs": "true"}}},
			want:      map[string]bool{"pgs": true},
		},
		{
			name:      "nil providers map",
			providers: nil,
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &ignoredCodecConfig{providers: tt.providers}
			got := IgnoredCodecsFromConfig(cfg)
			if tt.want == nil {
				if got != nil {
					t.Errorf("IgnoredCodecsFromConfig() = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("IgnoredCodecsFromConfig() has %d entries, want %d", len(got), len(tt.want))
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("IgnoredCodecsFromConfig()[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestVariantFromFlags_roundtrip_matchesVariant(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		hi := rapid.Bool().Draw(t, "hi")
		forced := rapid.Bool().Draw(t, "forced")

		variant := api.VariantFromFlags(hi, forced)

		// VariantFromFlags picks hi over forced when both are true,
		// so the round-trip holds for all combinations.
		if !matchesVariant(hi, forced, variant) {
			t.Errorf("matchesVariant(%v, %v, api.VariantFromFlags(%v, %v)=%q) = false, want true",
				hi, forced, hi, forced, variant)
		}
	})
}

func TestParseExternalSubPath_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		base   string
		ext    string
		lang   string
		hi     bool
		forced bool
	}{
		{"simple english", "/video.en.srt", "/video", ".srt", "en", false, false},
		{"english HI", "/video.en.hi.srt", "/video", ".srt", "en", true, false},
		{"english forced", "/video.en.forced.srt", "/video", ".srt", "en", false, true},
		{"three letter lang", "/video.eng.srt", "/video", ".srt", "eng", false, false},
		{"SDH variant", "/video.en.sdh.srt", "/video", ".srt", "en", true, false},
		{"foreign variant", "/video.en.foreign.srt", "/video", ".srt", "en", false, true},
		{"HI and forced", "/video.en.hi.forced.srt", "/video", ".srt", "en", true, true},
		{"ass extension", "/video.ja.ass", "/video", ".ass", "ja", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := parseExternalSubPath(tt.path, tt.base, tt.ext)
			if sub.Lang != tt.lang {
				t.Errorf("lang = %q, want %q", sub.Lang, tt.lang)
			}
			if sub.HI != tt.hi {
				t.Errorf("HI = %v, want %v", sub.HI, tt.hi)
			}
			if sub.Forced != tt.forced {
				t.Errorf("Forced = %v, want %v", sub.Forced, tt.forced)
			}
		})
	}
}

func TestParseExternalSubPath_edge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		base string
		ext  string
		want externalSub
	}{
		{
			"three-letter language code",
			"/path/movie.eng.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.eng.srt", Lang: "eng"},
		},
		{
			"uppercase language",
			"/path/movie.EN.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.EN.srt", Lang: "EN"},
		},
		{
			"forced before hi",
			"/path/movie.en.forced.hi.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.en.forced.hi.srt", Lang: "en", HI: true, Forced: true},
		},
		{
			"only flags no language",
			"/path/movie.forced.srt",
			"/path/movie",
			".srt",
			externalSub{Path: "/path/movie.forced.srt", Lang: "forced"},
		},
		{
			"vtt extension",
			"/path/movie.fr.vtt",
			"/path/movie",
			".vtt",
			externalSub{Path: "/path/movie.fr.vtt", Lang: "fr"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseExternalSubPath(tt.path, tt.base, tt.ext)
			if got.Lang != tt.want.Lang {
				t.Errorf("parseExternalSubPath(%q).Lang = %q, want %q",
					tt.path, got.Lang, tt.want.Lang)
			}
			if got.HI != tt.want.HI {
				t.Errorf("parseExternalSubPath(%q).HI = %v, want %v",
					tt.path, got.HI, tt.want.HI)
			}
			if got.Forced != tt.want.Forced {
				t.Errorf("parseExternalSubPath(%q).Forced = %v, want %v",
					tt.path, got.Forced, tt.want.Forced)
			}
		})
	}
}
