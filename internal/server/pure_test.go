package server

import (
	"slices"
	"testing"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/scanning"
	"pgregory.net/rapid"
)

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

// --- extractAltTitles ---

func TestExtractAltTitles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		alts    []arrapi.AlternateTitle
		primary string
		want    []string
	}{
		{"nil alts", nil, "Breaking Bad", nil},
		{"empty alts", []arrapi.AlternateTitle{}, "Breaking Bad", nil},
		{"excludes primary", []arrapi.AlternateTitle{
			{Title: "Breaking Bad"},
		}, "Breaking Bad", nil},
		{"excludes primary case insensitive", []arrapi.AlternateTitle{
			{Title: "breaking bad"},
		}, "Breaking Bad", nil},
		{"returns unique alts", []arrapi.AlternateTitle{
			{Title: "Metástasis"},
			{Title: "Во все тяжкие"},
		}, "Breaking Bad", []string{"Metástasis", "Во все тяжкие"}},
		{"deduplicates case insensitive", []arrapi.AlternateTitle{
			{Title: "Alt Title"},
			{Title: "alt title"},
			{Title: "ALT TITLE"},
		}, "Primary", []string{"Alt Title"}},
		{"skips empty titles", []arrapi.AlternateTitle{
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
		{name: "episode", item: scanning.ScanItem{Ep: &arrapi.Episode{SeasonNumber: 3, EpisodeNumber: 7}}, wantSeason: 3, wantEp: 7},
		{name: "movie returns zero", item: scanning.ScanItem{Movie: &arrapi.Movie{Title: "Test"}}, wantSeason: 0, wantEp: 0},
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
		{"series", scanning.ScanItem{Series: &arrapi.Series{Title: "Breaking Bad"}}, "Breaking Bad"},
		{"movie", scanning.ScanItem{Movie: &arrapi.Movie{Title: "Inception"}}, "Inception"},
		{"both nil", scanning.ScanItem{}, ""},
		{"series takes priority over movie", scanning.ScanItem{
			Series: &arrapi.Series{Title: "Series"},
			Movie:  &arrapi.Movie{Title: "Movie"},
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
