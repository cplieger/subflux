package server

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/scanning"
	"pgregory.net/rapid"
)

// --- manualops.IsValidLangCode ---

func TestIsValidLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		// Valid codes.
		{"simple two char", "en", true},
		{"BCP 47 with region", "pt-BR", true},
		{"three char", "fra", true},
		{"exactly at max length", "en-US-x-custom12long", true},
		{"contains spaces", "en US", true},
		{"single char", "e", true},
		{"single dot is valid", "en.fr", true},

		// Invalid codes.
		{"empty string", "", false},
		{"one over max length", "abcdefghijklmnopqrstu", false},
		{"contains forward slash", "en/fr", false},
		{"contains backslash", "en\\fr", false},
		{"contains dot-dot traversal", "en..fr", false},
		{"contains null byte", "en\x00fr", false},
		{"contains newline", "en\nfr", false},
		{"contains tab", "en\tfr", false},
		{"contains carriage return", "en\rfr", false},
		{"only null byte", "\x00", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := manualops.IsValidLangCode(tt.input)
			if got != tt.want {
				t.Errorf("manualops.IsValidLangCode(%q) = %v, want %v",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestIsValidLangCode_property_accepted_codes_are_path_safe(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		lang := rapid.StringMatching(`[a-zA-Z0-9\-]{1,20}`).Draw(t, "lang")
		if !manualops.IsValidLangCode(lang) {
			t.Skip("generated code rejected by manualops.IsValidLangCode")
		}
		if strings.ContainsAny(lang, "/\\") {
			t.Errorf("manualops.IsValidLangCode(%q) = true, but contains path separator", lang)
		}
		if strings.Contains(lang, "..") {
			t.Errorf("manualops.IsValidLangCode(%q) = true, but contains '..'", lang)
		}
		if len(lang) > manualops.MaxLangCodeLen {
			t.Errorf("manualops.IsValidLangCode(%q) = true, but len=%d > max=%d", lang, len(lang), manualops.MaxLangCodeLen)
		}
	})
}

func TestIsValidLangCode_property_rejects_dangerous_inputs(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		base := rapid.String().Draw(t, "base")
		danger := rapid.SampledFrom([]string{"/", "\\", "..", "\x00", "\n", "\r"}).Draw(t, "danger")
		pos := rapid.IntRange(0, len(base)).Draw(t, "pos")
		input := base[:pos] + danger + base[pos:]
		if manualops.IsValidLangCode(input) {
			t.Errorf("manualops.IsValidLangCode(%q) = true, but contains dangerous sequence %q", input, danger)
		}
	})
}

// --- manualops.QueryInt ---

func TestQueryInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  string
		want int
	}{
		{"valid positive", "42", 42},
		{"zero", "0", 0},
		{"negative returns zero", "-1", 0},
		{"empty returns zero", "", 0},
		{"non-numeric returns zero", "abc", 0},
		{"float returns zero", "3.14", 0},
		{"large valid number", "2147483647", 2147483647},
		{"leading whitespace returns zero", " 42", 0},
		{"overflow returns zero", "99999999999999999999", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := &fakeQuery{val: tt.val}
			got := manualops.QueryInt(q, "key")
			if got != tt.want {
				t.Errorf("manualops.QueryInt(%q) = %d, want %d",
					tt.val, got, tt.want)
			}
		})
	}
}

// fakeQuery satisfies the interface{ Get(string) string } constraint
// used by manualops.QueryInt.
type fakeQuery struct {
	val string
}

func (f *fakeQuery) Get(_ string) string { return f.val }

func TestQueryInt_property_result_always_non_negative(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		val := rapid.String().Draw(t, "val")
		q := &fakeQuery{val: val}
		got := manualops.QueryInt(q, "key")
		if got < 0 {
			t.Errorf("manualops.QueryInt(%q) = %d, want >= 0", val, got)
		}
	})
}

// --- sleepCtx ---

func TestSleepCtx_zero_duration_returns_immediately(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	err := sleepCtx(ctx, 0)
	if err != nil {
		t.Errorf("sleepCtx(ctx, 0) = %v, want nil", err)
	}
}

func TestSleepCtx_negative_duration_returns_immediately(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	err := sleepCtx(ctx, -1)
	if err != nil {
		t.Errorf("sleepCtx(ctx, -1) = %v, want nil", err)
	}
}

func TestSleepCtx_cancelled_context_returns_error(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := sleepCtx(ctx, 10*time.Second)
	if err == nil {
		t.Fatal("sleepCtx(cancelled, 10s) = nil, want context error")
	}
	if err != context.Canceled {
		t.Errorf("sleepCtx(cancelled, 10s) = %v, want %v", err, context.Canceled)
	}
}

func TestSleepCtx_short_duration_completes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	err := sleepCtx(ctx, 1*time.Millisecond)
	if err != nil {
		t.Errorf("sleepCtx(ctx, 1ms) = %v, want nil", err)
	}
}

// --- extractAltTitles ---

func TestExtractAltTitles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		alts    []api.AlternateTitle
		primary string
		want    []string
	}{
		{"nil alts", nil, "Breaking Bad", nil},
		{"empty alts", []api.AlternateTitle{}, "Breaking Bad", nil},
		{"excludes primary", []api.AlternateTitle{
			{Title: "Breaking Bad"},
		}, "Breaking Bad", nil},
		{"excludes primary case insensitive", []api.AlternateTitle{
			{Title: "breaking bad"},
		}, "Breaking Bad", nil},
		{"returns unique alts", []api.AlternateTitle{
			{Title: "Metástasis"},
			{Title: "Во все тяжкие"},
		}, "Breaking Bad", []string{"Metástasis", "Во все тяжкие"}},
		{"deduplicates case insensitive", []api.AlternateTitle{
			{Title: "Alt Title"},
			{Title: "alt title"},
			{Title: "ALT TITLE"},
		}, "Primary", []string{"Alt Title"}},
		{"skips empty titles", []api.AlternateTitle{
			{Title: ""},
			{Title: "Valid Alt"},
			{Title: ""},
		}, "Primary", []string{"Valid Alt"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scanning.ExtractAltTitles(tt.alts, tt.primary)
			if !slices.Equal(got, tt.want) {
				t.Errorf("ExtractAltTitles(%v, %q) = %v, want %v",
					tt.alts, tt.primary, got, tt.want)
			}
		})
	}
}

// --- sceneOrPath ---

func TestSceneOrPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sceneName string
		filePath  string
		want      string
	}{
		{"scene name present", "Movie.2024.BluRay-GRP", "/media/movie.mkv", "Movie.2024.BluRay-GRP"},
		{"scene name empty", "", "/media/movie.mkv", "/media/movie.mkv"},
		{"both empty", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scanning.SceneOrPath(tt.sceneName, tt.filePath)
			if got != tt.want {
				t.Errorf("SceneOrPath(%q, %q) = %q, want %q",
					tt.sceneName, tt.filePath, got, tt.want)
			}
		})
	}
}

// --- scanning.ScanItemSeasonEp ---

func TestScanItemSeasonEp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		item       scanning.ScanItem
		name       string
		wantSeason int
		wantEp     int
	}{
		{name: "episode", item: scanning.ScanItem{Ep: &api.Episode{SeasonNumber: 3, EpisodeNumber: 7}}, wantSeason: 3, wantEp: 7},
		{name: "movie returns zero", item: scanning.ScanItem{Movie: &api.Movie{Title: "Test"}}, wantSeason: 0, wantEp: 0},
		{name: "nil ep and movie", item: scanning.ScanItem{}, wantSeason: 0, wantEp: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, e := scanning.ScanItemSeasonEp(tt.item)
			if s != tt.wantSeason || e != tt.wantEp {
				t.Errorf("scanning.ScanItemSeasonEp() = (%d, %d), want (%d, %d)",
					s, e, tt.wantSeason, tt.wantEp)
			}
		})
	}
}

// --- scanning.ScanItemTitle ---

func TestScanItemTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item scanning.ScanItem
		want string
	}{
		{"series", scanning.ScanItem{Series: &api.Series{Title: "Breaking Bad"}}, "Breaking Bad"},
		{"movie", scanning.ScanItem{Movie: &api.Movie{Title: "Inception"}}, "Inception"},
		{"both nil", scanning.ScanItem{}, ""},
		{"series takes priority over movie", scanning.ScanItem{
			Series: &api.Series{Title: "Series"},
			Movie:  &api.Movie{Title: "Movie"},
		}, "Series"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scanning.ScanItemTitle(tt.item)
			if got != tt.want {
				t.Errorf("scanning.ScanItemTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}
