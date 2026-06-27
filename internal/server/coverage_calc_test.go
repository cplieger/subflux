package server

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

// --- indexSubStatus ---

func TestIndexSubStatus(t *testing.T) {
	t.Parallel()
	ignoredCodecs := map[string]bool{"pgs": true}

	cases := []struct {
		ignored      map[string]bool
		checkKey     covKey
		name         string
		checkMediaID string
		files        []api.SubtitleEntry
		wantMediaIDs int
		wantUsable   bool
		wantIgnored  bool
	}{
		{
			name:         "empty_input",
			files:        nil,
			ignored:      nil,
			wantMediaIDs: 0,
		},
		{
			name:         "external_sub_is_usable",
			files:        []api.SubtitleEntry{{MediaID: "tvdb-123-s01e01", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"}},
			ignored:      nil,
			wantMediaIDs: 1,
			checkMediaID: "tvdb-123-s01e01",
			checkKey:     covKey{Lang: "fr", Variant: "standard"},
			wantUsable:   true,
			wantIgnored:  false,
		},
		{
			name:         "embedded_ignored_codec",
			files:        []api.SubtitleEntry{{MediaID: "tvdb-123-s01e01", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"}},
			ignored:      ignoredCodecs,
			wantMediaIDs: 1,
			checkMediaID: "tvdb-123-s01e01",
			checkKey:     covKey{Lang: "fr", Variant: "standard"},
			wantUsable:   false,
			wantIgnored:  true,
		},
		{
			name:         "embedded_non_ignored_codec",
			files:        []api.SubtitleEntry{{MediaID: "tvdb-123-s01e01", Language: "fr", Variant: "standard", Source: "embedded", Codec: "srt"}},
			ignored:      ignoredCodecs,
			wantMediaIDs: 1,
			checkMediaID: "tvdb-123-s01e01",
			checkKey:     covKey{Lang: "fr", Variant: "standard"},
			wantUsable:   true,
			wantIgnored:  false,
		},
		{
			name: "usable_overrides_ignored",
			files: []api.SubtitleEntry{
				{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
				{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
			},
			ignored:      ignoredCodecs,
			wantMediaIDs: 1,
			checkMediaID: "ep1",
			checkKey:     covKey{Lang: "fr", Variant: "standard"},
			wantUsable:   true,
			wantIgnored:  false,
		},
		{
			name: "ignored_does_not_override_usable",
			files: []api.SubtitleEntry{
				{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
				{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
			},
			ignored:      ignoredCodecs,
			wantMediaIDs: 1,
			checkMediaID: "ep1",
			checkKey:     covKey{Lang: "fr", Variant: "standard"},
			wantUsable:   true,
			wantIgnored:  false,
		},
		{
			name: "multiple_media_ids",
			files: []api.SubtitleEntry{
				{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
				{MediaID: "ep2", Language: "en", Variant: "hi", Source: "embedded", Codec: "pgs"},
			},
			ignored:      ignoredCodecs,
			wantMediaIDs: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idx := indexSubStatus(tc.files, tc.ignored)

			if tc.wantMediaIDs == 0 {
				if len(idx) != 0 {
					t.Errorf("indexSubStatus() returned %d entries, want 0", len(idx))
				}
				return
			}

			if len(idx) != tc.wantMediaIDs {
				t.Fatalf("indexSubStatus() returned %d media IDs, want %d", len(idx), tc.wantMediaIDs)
			}

			if tc.checkMediaID == "" {
				// Special case: multiple_media_ids — check both.
				if !idx["ep1"][covKey{Lang: "fr", Variant: "standard"}].Usable {
					t.Error("ep1 fr/standard should be usable")
				}
				if idx["ep2"][covKey{Lang: "en", Variant: "hi"}].Usable {
					t.Error("ep2 en/hi should not be usable (ignored pgs)")
				}
				return
			}

			st := idx[tc.checkMediaID][tc.checkKey]
			if st == nil {
				t.Fatalf("expected non-nil status for %s %v", tc.checkMediaID, tc.checkKey)
			}
			if st.Usable != tc.wantUsable {
				t.Errorf("Usable = %v, want %v", st.Usable, tc.wantUsable)
			}
			if st.IgnoredOnly != tc.wantIgnored {
				t.Errorf("IgnoredOnly = %v, want %v", st.IgnoredOnly, tc.wantIgnored)
			}
		})
	}
}

// --- resolveRuleName ---

func TestResolveRuleName_with_audio_lang(t *testing.T) {
	t.Parallel()
	got := resolveRuleName("en", []api.SubtitleTarget{{Code: "fr"}})
	if got != "en" {
		t.Errorf("resolveRuleName(en, targets) = %q, want %q", got, "en")
	}
}

func TestResolveRuleName_empty_lang_returns_default(t *testing.T) {
	t.Parallel()
	got := resolveRuleName("", []api.SubtitleTarget{{Code: "fr"}})
	if got != ruleDefault {
		t.Errorf("resolveRuleName('', targets) = %q, want %q", got, ruleDefault)
	}
}

func TestResolveRuleName_no_targets_returns_no_targets(t *testing.T) {
	t.Parallel()
	got := resolveRuleName("en", nil)
	if got != ruleNoTargets {
		t.Errorf("resolveRuleName(en, nil) = %q, want %q", got, ruleNoTargets)
	}
}

// --- deduplicateFileRows ---

func TestDeduplicateFileRows_collapses_duplicates(t *testing.T) {
	t.Parallel()
	rows := []api.SubtitleEntry{
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Path: "/a.srt"},
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Path: "/b.srt"},
		{MediaID: "ep1", Language: "en", Variant: "hi", Source: "embedded"},
	}
	got := deduplicateFileRows(rows)
	if len(got) != 2 {
		t.Errorf("deduplicateFileRows() returned %d rows, want 2", len(got))
	}
}

func TestDeduplicateFileRows_preserves_distinct_rows(t *testing.T) {
	t.Parallel()
	rows := []api.SubtitleEntry{
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external"},
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "embedded"},
		{MediaID: "ep1", Language: "en", Variant: "standard", Source: "external"},
		{MediaID: "ep2", Language: "fr", Variant: "standard", Source: "external"},
	}
	got := deduplicateFileRows(rows)
	if len(got) != 4 {
		t.Errorf("deduplicateFileRows() returned %d rows, want 4 (all distinct)", len(got))
	}
}

func TestDeduplicateFileRows_empty_input(t *testing.T) {
	t.Parallel()
	got := deduplicateFileRows([]api.SubtitleEntry{})
	if len(got) != 0 {
		t.Errorf("deduplicateFileRows(empty) returned %d rows, want 0", len(got))
	}
}

func TestDeduplicateFileRows_preserves_order(t *testing.T) {
	t.Parallel()
	rows := []api.SubtitleEntry{
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Path: "/first.srt"},
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Path: "/second.srt"},
	}
	got := deduplicateFileRows(rows)
	if len(got) != 1 {
		t.Fatalf("deduplicateFileRows() returned %d rows, want 1", len(got))
	}
	if got[0].Path != "/first.srt" {
		t.Errorf("deduplicateFileRows() kept path %q, want %q (first seen)", got[0].Path, "/first.srt")
	}
}

// --- extractSeriesPrefix ---

func TestExtractSeriesPrefix_table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "standard_episode", input: "tvdb-12345-s01e01", want: "tvdb-12345-"},
		{name: "double_digit_season", input: "tvdb-99999-s12e05", want: "tvdb-99999-"},
		{name: "imdb_prefix", input: "imdb-tt1234567-s03e10", want: "imdb-tt1234567-"},
		{name: "empty_string", input: "", want: ""},
		{name: "no_dash_s_pattern", input: "tmdb-12345", want: ""},
		{name: "single_char", input: "s", want: ""},
		{name: "just_dash_s", input: "-s", want: "-"},
		{name: "trailing_s_no_dash", input: "abcs", want: ""},
		{name: "multiple_dash_s", input: "tvdb-123-s01-s02e01", want: "tvdb-123-s01-"},
		{name: "dash_s_at_start", input: "-s01e01", want: "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractSeriesPrefix(tc.input)
			if got != tc.want {
				t.Errorf("extractSeriesPrefix(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExtractSeriesPrefix_property_roundtrip_with_BuildEpisodeID(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		tvdbID := rapid.IntRange(1, 999999).Draw(t, "tvdb_id")
		season := rapid.IntRange(0, 99).Draw(t, "season")
		episode := rapid.IntRange(1, 999).Draw(t, "episode")

		epID := api.BuildEpisodeID(tvdbID, "", season, episode)
		prefix := extractSeriesPrefix(epID)
		wantPrefix := api.BuildSeriesPrefix(tvdbID, "")

		if prefix != wantPrefix {
			t.Fatalf("extractSeriesPrefix(%q) = %q, want %q", epID, prefix, wantPrefix)
		}
	})
}

// --- countEpisodeCoverageGrouped ---

func TestCountEpisodeCoverageGrouped_empty_episodes(t *testing.T) {
	t.Parallel()
	targets := []api.SubtitleTarget{{Code: "fr"}}
	got := countEpisodeCoverageGrouped(nil, targets, 10)
	if len(got) != 1 {
		t.Fatalf("countEpisodeCoverageGrouped(nil, 1 target, 10) len = %d, want 1", len(got))
	}
	if got[0].Have != 0 || got[0].HaveIgnored != 0 || got[0].Total != 10 {
		t.Errorf("countEpisodeCoverageGrouped(nil) = {Have:%d, HaveIgnored:%d, Total:%d}, want {0, 0, 10}",
			got[0].Have, got[0].HaveIgnored, got[0].Total)
	}
}

func TestCountEpisodeCoverageGrouped_counts_usable_and_ignored(t *testing.T) {
	t.Parallel()
	episodes := []map[covKey]*covStatus{
		{covKey{Lang: "fr", Variant: "standard"}: {Usable: true}},
		{covKey{Lang: "fr", Variant: "standard"}: {IgnoredOnly: true}},
		{covKey{Lang: "en", Variant: "standard"}: {Usable: true}},
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}
	got := countEpisodeCoverageGrouped(episodes, targets, 5)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Have != 1 {
		t.Errorf("countEpisodeCoverageGrouped Have = %d, want 1", got[0].Have)
	}
	if got[0].HaveIgnored != 1 {
		t.Errorf("countEpisodeCoverageGrouped HaveIgnored = %d, want 1", got[0].HaveIgnored)
	}
	if got[0].Total != 5 {
		t.Errorf("countEpisodeCoverageGrouped Total = %d, want 5", got[0].Total)
	}
}

func TestCountEpisodeCoverageGrouped_multiple_targets(t *testing.T) {
	t.Parallel()
	episodes := []map[covKey]*covStatus{
		{
			covKey{Lang: "fr", Variant: "standard"}: {Usable: true},
			covKey{Lang: "en", Variant: "forced"}:   {Usable: true},
		},
	}
	targets := []api.SubtitleTarget{
		{Code: "fr"},
		{Code: "en", Variant: "forced"},
	}
	got := countEpisodeCoverageGrouped(episodes, targets, 3)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Language != "fr" || got[0].Have != 1 {
		t.Errorf("target[0] = {lang:%q, have:%d}, want {fr, 1}", got[0].Language, got[0].Have)
	}
	if got[1].Language != "en" || got[1].Have != 1 {
		t.Errorf("target[1] = {lang:%q, have:%d}, want {en, 1}", got[1].Language, got[1].Have)
	}
}

func TestCountEpisodeCoverageGrouped_no_targets(t *testing.T) {
	t.Parallel()
	episodes := []map[covKey]*covStatus{
		{covKey{Lang: "fr", Variant: "standard"}: {Usable: true}},
	}
	got := countEpisodeCoverageGrouped(episodes, nil, 5)
	if len(got) != 0 {
		t.Errorf("countEpisodeCoverageGrouped(no targets) len = %d, want 0", len(got))
	}
}

// --- countMovieCoverage ---

func TestCountMovieCoverage_nil_subs(t *testing.T) {
	t.Parallel()
	targets := []api.SubtitleTarget{{Code: "fr"}}
	got := countMovieCoverage(nil, targets)
	if len(got) != 1 {
		t.Fatalf("countMovieCoverage(nil) len = %d, want 1", len(got))
	}
	if got[0].Have != 0 || got[0].HaveIgnored != 0 || got[0].Total != 1 {
		t.Errorf("countMovieCoverage(nil) = {Have:%d, HaveIgnored:%d, Total:%d}, want {0, 0, 1}",
			got[0].Have, got[0].HaveIgnored, got[0].Total)
	}
}

func TestCountMovieCoverage_usable_sub(t *testing.T) {
	t.Parallel()
	subs := map[covKey]*covStatus{
		{Lang: "fr", Variant: "standard"}: {Usable: true},
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}
	got := countMovieCoverage(subs, targets)
	if got[0].Have != 1 {
		t.Errorf("countMovieCoverage(usable) Have = %d, want 1", got[0].Have)
	}
	if got[0].HaveIgnored != 0 {
		t.Errorf("countMovieCoverage(usable) HaveIgnored = %d, want 0", got[0].HaveIgnored)
	}
}

func TestCountMovieCoverage_ignored_only(t *testing.T) {
	t.Parallel()
	subs := map[covKey]*covStatus{
		{Lang: "fr", Variant: "standard"}: {IgnoredOnly: true},
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}
	got := countMovieCoverage(subs, targets)
	if got[0].Have != 0 {
		t.Errorf("countMovieCoverage(ignored) Have = %d, want 0", got[0].Have)
	}
	if got[0].HaveIgnored != 1 {
		t.Errorf("countMovieCoverage(ignored) HaveIgnored = %d, want 1", got[0].HaveIgnored)
	}
}

func TestCountMovieCoverage_multiple_targets(t *testing.T) {
	t.Parallel()
	subs := map[covKey]*covStatus{
		{Lang: "fr", Variant: "standard"}: {Usable: true},
		{Lang: "en", Variant: "forced"}:   {IgnoredOnly: true},
	}
	targets := []api.SubtitleTarget{
		{Code: "fr"},
		{Code: "en", Variant: "forced"},
		{Code: "de"},
	}
	got := countMovieCoverage(subs, targets)
	if len(got) != 3 {
		t.Fatalf("countMovieCoverage len = %d, want 3", len(got))
	}
	if got[0].Have != 1 || got[0].HaveIgnored != 0 {
		t.Errorf("target fr = {Have:%d, HaveIgnored:%d}, want {1, 0}", got[0].Have, got[0].HaveIgnored)
	}
	if got[1].Have != 0 || got[1].HaveIgnored != 1 {
		t.Errorf("target en = {Have:%d, HaveIgnored:%d}, want {0, 1}", got[1].Have, got[1].HaveIgnored)
	}
	if got[2].Have != 0 || got[2].HaveIgnored != 0 {
		t.Errorf("target de = {Have:%d, HaveIgnored:%d}, want {0, 0}", got[2].Have, got[2].HaveIgnored)
	}
}

func TestCountMovieCoverage_no_targets(t *testing.T) {
	t.Parallel()
	subs := map[covKey]*covStatus{
		{Lang: "fr", Variant: "standard"}: {Usable: true},
	}
	got := countMovieCoverage(subs, nil)
	if len(got) != 0 {
		t.Errorf("countMovieCoverage(no targets) len = %d, want 0", len(got))
	}
}
