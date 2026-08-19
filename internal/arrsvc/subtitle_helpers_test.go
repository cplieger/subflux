package arrsvc

import (
	"slices"
	"testing"

	"github.com/cplieger/arrapi/v2"
)

// --- HistoryEntry.ImportedPath (promoted from arrapi.HistoryRecord via alias) ---

func TestHistoryEntry_ImportedPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data map[string]string
		want string
	}{
		{"present", map[string]string{"importedPath": "/media/movie.mkv"}, "/media/movie.mkv"},
		{"absent", map[string]string{"other": "value"}, ""},
		{"nil data", nil, ""},
		{"empty string", map[string]string{"importedPath": ""}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := &arrapi.HistoryRecord{Data: tt.data}

			got := h.ImportedPath()

			if got != tt.want {
				t.Errorf("ImportedPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- LangNameToISO ---

// --- OriginalLangCode (free function over an arr language reference) ---

func TestSeries_OriginalLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lang *arrapi.Language
		want string
	}{
		{"nil language", nil, ""},
		{"known language", &arrapi.Language{Name: "English", ID: 1}, "en"},
		{"unknown language", &arrapi.Language{Name: "Klingon", ID: 99}, ""},
		{"empty name", &arrapi.Language{Name: "", ID: 0}, ""},
		{"two-letter code", &arrapi.Language{Name: "fr", ID: 2}, "fr"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := OriginalLangCode(tt.lang)

			if got != tt.want {
				t.Errorf("OriginalLangCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMovie_OriginalLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lang *arrapi.Language
		want string
	}{
		{"nil language", nil, ""},
		{"known language", &arrapi.Language{Name: "French", ID: 2}, "fr"},
		{"unknown language", &arrapi.Language{Name: "Dothraki", ID: 99}, ""},
		{"empty name", &arrapi.Language{Name: "", ID: 0}, ""},
		{"two-letter code", &arrapi.Language{Name: "de", ID: 3}, "de"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := OriginalLangCode(tt.lang)

			if got != tt.want {
				t.Errorf("OriginalLangCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- SeasonEpisodeFileCount (free function) ---

// --- SeasonEpisodeFileCount (free function) ---

func TestSeries_SeasonEpisodeFileCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		seasons   []arrapi.Season
		seasonNum int
		want      int
	}{
		{"no seasons", nil, 1, 0},
		{"season found with stats", []arrapi.Season{
			{SeasonNumber: 1, Statistics: &arrapi.SeasonStatistics{EpisodeFileCount: 10}},
			{SeasonNumber: 2, Statistics: &arrapi.SeasonStatistics{EpisodeFileCount: 5}},
		}, 1, 10},
		{"season found nil stats", []arrapi.Season{
			{SeasonNumber: 1, Statistics: nil},
		}, 1, 0},
		{"season not found", []arrapi.Season{
			{SeasonNumber: 1, Statistics: &arrapi.SeasonStatistics{EpisodeFileCount: 10}},
		}, 99, 0},
		{"season zero (specials)", []arrapi.Season{
			{SeasonNumber: 0, Statistics: &arrapi.SeasonStatistics{EpisodeFileCount: 3}},
		}, 0, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := SeasonEpisodeFileCount(&arrapi.Series{Seasons: tt.seasons}, tt.seasonNum)

			if got != tt.want {
				t.Errorf("SeasonEpisodeFileCount(%d) = %d, want %d",
					tt.seasonNum, got, tt.want)
			}
		})
	}
}

// --- AudioLanguages (free function over a file's MediaInfo) ---

// --- AudioLanguages (free function over a file's MediaInfo) ---

func TestEpisodeFile_AudioLanguages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file *arrapi.EpisodeFile
		want []string
	}{
		{"nil media info", &arrapi.EpisodeFile{MediaInfo: nil}, nil},
		{"empty audio languages", &arrapi.EpisodeFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: ""},
		}, nil},
		{"single language", &arrapi.EpisodeFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: "English"},
		}, []string{"en"}},
		{"multiple languages", &arrapi.EpisodeFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: "English/Japanese"},
		}, []string{"en", "ja"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := AudioLanguages(tt.file.MediaInfo)

			if !slices.Equal(got, tt.want) {
				t.Errorf("AudioLanguages() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMovieFile_AudioLanguages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file *arrapi.MovieFile
		want []string
	}{
		{"nil media info", &arrapi.MovieFile{MediaInfo: nil}, nil},
		{"empty audio languages", &arrapi.MovieFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: ""},
		}, nil},
		{"single language", &arrapi.MovieFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: "French"},
		}, []string{"fr"}},
		{"multiple languages", &arrapi.MovieFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: "English,French,German"},
		}, []string{"en", "fr", "de"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := AudioLanguages(tt.file.MediaInfo)

			if !slices.Equal(got, tt.want) {
				t.Errorf("AudioLanguages() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Property-based tests ---

// --- HasExcludeTag ---

func TestHasExcludeTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		excludeIDs map[int]struct{}
		name       string
		tags       []int
		want       bool
	}{
		{name: "no tags no excludes", tags: nil, excludeIDs: nil, want: false},
		{name: "no tags with excludes", tags: nil, excludeIDs: map[int]struct{}{1: {}}, want: false},
		{name: "tags with no excludes", tags: []int{1, 2}, excludeIDs: nil, want: false},
		{name: "tags with empty excludes", tags: []int{1, 2}, excludeIDs: map[int]struct{}{}, want: false},
		{name: "matching tag", tags: []int{1, 2, 3}, excludeIDs: map[int]struct{}{2: {}}, want: true},
		{name: "no matching tag", tags: []int{1, 2, 3}, excludeIDs: map[int]struct{}{4: {}}, want: false},
		{name: "first tag matches", tags: []int{5, 6, 7}, excludeIDs: map[int]struct{}{5: {}}, want: true},
		{name: "last tag matches", tags: []int{5, 6, 7}, excludeIDs: map[int]struct{}{7: {}}, want: true},
		{name: "multiple excludes one match", tags: []int{1}, excludeIDs: map[int]struct{}{1: {}, 2: {}, 3: {}}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := HasExcludeTag(tt.tags, tt.excludeIDs)

			if got != tt.want {
				t.Errorf("HasExcludeTag(%v, %v) = %v, want %v",
					tt.tags, tt.excludeIDs, got, tt.want)
			}
		})
	}
}
