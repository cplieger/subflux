package coverage_test

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/server/coverage"
	"github.com/cplieger/subflux/internal/subflux"
)

// statusFor fetches the status for a media/lang/variant triple, failing the
// test if the index has no entry for it.
func statusFor(t *testing.T, idx map[string]map[coverage.Key]*coverage.Status, mediaID, lang, variant string) *coverage.Status {
	t.Helper()
	m, ok := idx[mediaID]
	if !ok {
		t.Fatalf("IndexSubStatus: no entry for media %q", mediaID)
	}
	st := m[coverage.Key{Lang: lang, Variant: variant}]
	if st == nil {
		t.Fatalf("IndexSubStatus: no status for media %q key {%q,%q}", mediaID, lang, variant)
	}
	return st
}

// --- IndexSubStatus ---

func TestIndexSubStatus_externalIsUsable(t *testing.T) {
	t.Parallel()
	idx := coverage.IndexSubStatus([]subflux.SubtitleEntry{
		{MediaID: "m1", Language: "en", Variant: "", Source: "external", Codec: "srt"},
	}, nil)

	st := statusFor(t, idx, "m1", "en", "")
	if !st.Usable {
		t.Errorf("external sub: Usable = false, want true")
	}
	if st.IgnoredOnly {
		t.Errorf("external sub: IgnoredOnly = true, want false")
	}
}

// An embedded subtitle whose codec is in the ignored set must be recorded as
// IgnoredOnly, never Usable.
func TestIndexSubStatus_embeddedIgnoredCodecIsIgnoredOnly(t *testing.T) {
	t.Parallel()
	idx := coverage.IndexSubStatus([]subflux.SubtitleEntry{
		{MediaID: "m1", Language: "en", Variant: "", Source: "embedded", Codec: "hdmv_pgs_subtitle"},
	}, map[string]bool{"hdmv_pgs_subtitle": true})

	st := statusFor(t, idx, "m1", "en", "")
	if st.Usable {
		t.Errorf("embedded ignored-codec sub: Usable = true, want false")
	}
	if !st.IgnoredOnly {
		t.Errorf("embedded ignored-codec sub: IgnoredOnly = false, want true")
	}
}

func TestIndexSubStatus_embeddedNonIgnoredCodecIsUsable(t *testing.T) {
	t.Parallel()
	idx := coverage.IndexSubStatus([]subflux.SubtitleEntry{
		{MediaID: "m1", Language: "en", Variant: "", Source: "embedded", Codec: "subrip"},
	}, map[string]bool{"hdmv_pgs_subtitle": true})

	st := statusFor(t, idx, "m1", "en", "")
	if !st.Usable {
		t.Errorf("embedded non-ignored-codec sub: Usable = false, want true")
	}
	if st.IgnoredOnly {
		t.Errorf("embedded non-ignored-codec sub: IgnoredOnly = true, want false")
	}
}

func TestIndexSubStatus_usableAfterIgnoredIsUsable(t *testing.T) {
	t.Parallel()
	// Ignored embedded row first, then a usable external row for the same key.
	idx := coverage.IndexSubStatus([]subflux.SubtitleEntry{
		{MediaID: "m1", Language: "en", Variant: "", Source: "embedded", Codec: "pgs"},
		{MediaID: "m1", Language: "en", Variant: "", Source: "external", Codec: "srt"},
	}, map[string]bool{"pgs": true})

	st := statusFor(t, idx, "m1", "en", "")
	if !st.Usable {
		t.Errorf("usable after ignored: Usable = false, want true")
	}
	if st.IgnoredOnly {
		t.Errorf("usable after ignored: IgnoredOnly = true, want false")
	}
}

func TestIndexSubStatus_ignoredAfterUsableStaysUsable(t *testing.T) {
	t.Parallel()
	// Usable external row first, then an ignored embedded row for the same key.
	idx := coverage.IndexSubStatus([]subflux.SubtitleEntry{
		{MediaID: "m1", Language: "en", Variant: "", Source: "external", Codec: "srt"},
		{MediaID: "m1", Language: "en", Variant: "", Source: "embedded", Codec: "pgs"},
	}, map[string]bool{"pgs": true})

	st := statusFor(t, idx, "m1", "en", "")
	if !st.Usable {
		t.Errorf("ignored after usable: Usable = false, want true")
	}
	if st.IgnoredOnly {
		t.Errorf("ignored after usable: IgnoredOnly = true, want false")
	}
}

func TestIndexSubStatus_groupsByMediaAndKey(t *testing.T) {
	t.Parallel()
	idx := coverage.IndexSubStatus([]subflux.SubtitleEntry{
		{MediaID: "m1", Language: "en", Variant: "", Source: "external"},
		{MediaID: "m1", Language: "fr", Variant: "forced", Source: "external"},
		{MediaID: "m2", Language: "en", Variant: "", Source: "external"},
	}, nil)

	if len(idx) != 2 {
		t.Fatalf("media count = %d, want 2", len(idx))
	}
	if len(idx["m1"]) != 2 {
		t.Errorf("m1 key count = %d, want 2", len(idx["m1"]))
	}
	// Each distinct (media, lang, variant) must be present.
	statusFor(t, idx, "m1", "en", "")
	statusFor(t, idx, "m1", "fr", "forced")
	statusFor(t, idx, "m2", "en", "")
}

func TestIndexSubStatus_emptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	if idx := coverage.IndexSubStatus(nil, nil); len(idx) != 0 {
		t.Errorf("IndexSubStatus(nil) len = %d, want 0", len(idx))
	}
}

// --- ResolveRuleName ---

func TestResolveRuleName(t *testing.T) {
	t.Parallel()
	oneTarget := []subflux.SubtitleTarget{{Code: "en"}}
	cases := []struct {
		name      string
		audioLang string
		targets   []subflux.SubtitleTarget
		want      string
	}{
		{"nil_targets", "en", nil, "no targets"},
		{"empty_targets", "en", []subflux.SubtitleTarget{}, "no targets"},
		{"empty_audio_lang", "", oneTarget, "default"},
		{"matches_audio_lang_en", "en", oneTarget, "en"},
		{"matches_audio_lang_fra", "fra", oneTarget, "fra"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := coverage.ResolveRuleName(c.audioLang, c.targets); got != c.want {
				t.Errorf("ResolveRuleName(%q, %d targets) = %q, want %q", c.audioLang, len(c.targets), got, c.want)
			}
		})
	}
}

// --- ExtractSeriesPrefix ---

func TestExtractSeriesPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"tvdb_episode", "tvdb-12345-s01e01", "tvdb-12345-"},
		{"imdb_episode", "imdb-tt1234-s02e03", "imdb-tt1234-"},
		{"leading_dash_s", "-s01e01", "-"},
		{"minimal_dash_s", "x-s", "x-"},
		{"rightmost_dash_s_wins", "abc-s1e1-s2e2", "abc-s1e1-"},
		{"specials_season_zero", "tvdb-99-s00e00", "tvdb-99-"},
		{"no_dash_s", "nodash", ""},
		{"no_dash_at_all", "s01e01", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := coverage.ExtractSeriesPrefix(c.in); got != c.want {
				t.Errorf("ExtractSeriesPrefix(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// --- CountEpisodesGrouped ---

func TestCountEpisodesGrouped_countsUsableAndIgnoredPerTarget(t *testing.T) {
	t.Parallel()
	episodes := []map[coverage.Key]*coverage.Status{
		{coverage.Key{Lang: "en", Variant: "standard"}: {Usable: true}},
		{coverage.Key{Lang: "en", Variant: "standard"}: {IgnoredOnly: true}},
		{}, // no en subtitle for this episode
	}
	got := coverage.CountEpisodesGrouped(episodes, []subflux.SubtitleTarget{{Code: "en"}}, 3)
	want := []coverage.TargetCoverage{
		{Language: "en", Variant: "standard", Have: 1, HaveIgnored: 1, Total: 3},
	}
	if !slices.Equal(got, want) {
		t.Errorf("CountEpisodesGrouped = %+v, want %+v", got, want)
	}
}

func TestCountEpisodesGrouped_multipleTargets(t *testing.T) {
	t.Parallel()
	episodes := []map[coverage.Key]*coverage.Status{
		{
			coverage.Key{Lang: "en", Variant: "standard"}: {Usable: true},
			coverage.Key{Lang: "fr", Variant: "forced"}:   {Usable: true},
		},
		{coverage.Key{Lang: "en", Variant: "standard"}: {Usable: true}},
	}
	got := coverage.CountEpisodesGrouped(episodes, []subflux.SubtitleTarget{
		{Code: "en"},
		{Code: "fr", Variant: subflux.VariantForced},
	}, 2)
	want := []coverage.TargetCoverage{
		{Language: "en", Variant: "standard", Have: 2, HaveIgnored: 0, Total: 2},
		{Language: "fr", Variant: "forced", Have: 1, HaveIgnored: 0, Total: 2},
	}
	if !slices.Equal(got, want) {
		t.Errorf("CountEpisodesGrouped = %+v, want %+v", got, want)
	}
}

func TestCountEpisodesGrouped_noEpisodesKeepsTotal(t *testing.T) {
	t.Parallel()
	got := coverage.CountEpisodesGrouped(nil, []subflux.SubtitleTarget{{Code: "en"}}, 5)
	want := []coverage.TargetCoverage{
		{Language: "en", Variant: "standard", Have: 0, HaveIgnored: 0, Total: 5},
	}
	if !slices.Equal(got, want) {
		t.Errorf("CountEpisodesGrouped(no episodes) = %+v, want %+v", got, want)
	}
}

func TestCountEpisodesGrouped_emptyTargets(t *testing.T) {
	t.Parallel()
	got := coverage.CountEpisodesGrouped(nil, nil, 3)
	if len(got) != 0 {
		t.Errorf("CountEpisodesGrouped(no targets) len = %d, want 0", len(got))
	}
}

// --- CountMovies ---

func TestCountMovies_usableTarget(t *testing.T) {
	t.Parallel()
	subs := map[coverage.Key]*coverage.Status{
		{Lang: "en", Variant: "standard"}: {Usable: true},
	}
	got := coverage.CountMovies(subs, []subflux.SubtitleTarget{{Code: "en"}})
	want := []coverage.TargetCoverage{
		{Language: "en", Variant: "standard", Have: 1, HaveIgnored: 0, Total: 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("CountMovies(usable) = %+v, want %+v", got, want)
	}
}

func TestCountMovies_ignoredOnlyTarget(t *testing.T) {
	t.Parallel()
	subs := map[coverage.Key]*coverage.Status{
		{Lang: "en", Variant: "standard"}: {IgnoredOnly: true},
	}
	got := coverage.CountMovies(subs, []subflux.SubtitleTarget{{Code: "en"}})
	want := []coverage.TargetCoverage{
		{Language: "en", Variant: "standard", Have: 0, HaveIgnored: 1, Total: 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("CountMovies(ignored only) = %+v, want %+v", got, want)
	}
}

func TestCountMovies_missingTarget(t *testing.T) {
	t.Parallel()
	got := coverage.CountMovies(nil, []subflux.SubtitleTarget{{Code: "en"}})
	want := []coverage.TargetCoverage{
		{Language: "en", Variant: "standard", Have: 0, HaveIgnored: 0, Total: 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("CountMovies(missing) = %+v, want %+v", got, want)
	}
}

func TestCountMovies_multipleTargetsMixed(t *testing.T) {
	t.Parallel()
	subs := map[coverage.Key]*coverage.Status{
		{Lang: "en", Variant: "standard"}: {Usable: true},
		{Lang: "fr", Variant: "standard"}: {IgnoredOnly: true},
	}
	got := coverage.CountMovies(subs, []subflux.SubtitleTarget{{Code: "en"}, {Code: "fr"}})
	want := []coverage.TargetCoverage{
		{Language: "en", Variant: "standard", Have: 1, HaveIgnored: 0, Total: 1},
		{Language: "fr", Variant: "standard", Have: 0, HaveIgnored: 1, Total: 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("CountMovies(mixed) = %+v, want %+v", got, want)
	}
}

func TestCountMovies_emptyTargets(t *testing.T) {
	t.Parallel()
	if got := coverage.CountMovies(nil, nil); len(got) != 0 {
		t.Errorf("CountMovies(no targets) len = %d, want 0", len(got))
	}
}

// --- DeduplicateFileRows ---

func TestDeduplicateFileRows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []subflux.SubtitleEntry
		want []subflux.SubtitleEntry
	}{
		{
			name: "removes_exact_duplicates",
			in: []subflux.SubtitleEntry{
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "external"},
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "external"},
			},
			want: []subflux.SubtitleEntry{
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "external"},
			},
		},
		{
			name: "keeps_rows_differing_by_source",
			in: []subflux.SubtitleEntry{
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "external"},
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "embedded"},
			},
			want: []subflux.SubtitleEntry{
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "external"},
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "embedded"},
			},
		},
		{
			name: "preserves_first_occurrence_order",
			in: []subflux.SubtitleEntry{
				{MediaID: "m2", Language: "fr", Variant: "standard", Source: "external"},
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "external"},
				{MediaID: "m2", Language: "fr", Variant: "standard", Source: "external"},
			},
			want: []subflux.SubtitleEntry{
				{MediaID: "m2", Language: "fr", Variant: "standard", Source: "external"},
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "external"},
			},
		},
		{
			// Codec is not part of the dedup key: same (media,lang,variant,source)
			// with differing codec collapses to the first row seen.
			name: "differing_codec_collapses_to_first",
			in: []subflux.SubtitleEntry{
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "external", Codec: "first"},
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "external", Codec: "second"},
			},
			want: []subflux.SubtitleEntry{
				{MediaID: "m1", Language: "en", Variant: "standard", Source: "external", Codec: "first"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := coverage.DeduplicateFileRows(c.in); !slices.Equal(got, c.want) {
				t.Errorf("DeduplicateFileRows() = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestDeduplicateFileRows_emptyInput(t *testing.T) {
	t.Parallel()
	if got := coverage.DeduplicateFileRows(nil); len(got) != 0 {
		t.Errorf("DeduplicateFileRows(nil) len = %d, want 0", len(got))
	}
}
