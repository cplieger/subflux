package scanning

import (
	"reflect"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// ExtractAltTitles returns the alternative titles that differ from the
// primary; a single distinct alt is returned unchanged.
func TestExtractAltTitles_returns_distinct_alt(t *testing.T) {
	t.Parallel()
	got := ExtractAltTitles([]api.AlternateTitle{{Title: "Alt"}}, "Main")
	if len(got) != 1 || got[0] != "Alt" {
		t.Errorf("ExtractAltTitles([Alt], Main) = %v, want [Alt]", got)
	}
}

// EpisodeSearchRequest is the single source of truth for the
// episode->SearchRequest mapping. It resolves the audio language from the
// series' original language, falling back to the first audio track when the
// original language is unknown, and derives the release name from the scene
// name or the file path.
func TestEpisodeSearchRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		series *api.Series
		ep     *api.Episode
		langs  []string
		want   api.SearchRequest
	}{
		{
			name: "scene name and original language",
			series: &api.Series{
				Title:            "Breaking Bad",
				Year:             2008,
				ImdbID:           "tt0903747",
				TvdbID:           81189,
				OriginalLanguage: &api.LanguageInfo{Name: "English"},
				AlternateTitles:  []api.AlternateTitle{{Title: "BrBa"}, {Title: "Breaking Bad"}},
			},
			ep: &api.Episode{
				Title:                 "Pilot",
				SeasonNumber:          1,
				EpisodeNumber:         1,
				SceneSeasonNumber:     1,
				SceneEpisodeNumber:    1,
				AbsoluteEpisodeNumber: 1,
				EpisodeFile: &api.EpisodeFile{
					SceneName: "Breaking.Bad.S01E01.720p",
					Path:      "/tv/bb/s01e01.mkv",
					MediaInfo: &api.MediaInfo{AudioLanguages: "English/Japanese"},
				},
			},
			langs: []string{"en", "fr"},
			want: api.SearchRequest{
				Title:             "Breaking Bad",
				AlternativeTitles: []string{"BrBa"},
				EpisodeTitle:      "Pilot",
				Year:              2008,
				Season:            1,
				Episode:           1,
				SceneSeason:       1,
				SceneEpisode:      1,
				AbsoluteEpisode:   1,
				ImdbID:            "tt0903747",
				TvdbID:            81189,
				Languages:         []string{"en", "fr"},
				ReleaseName:       "Breaking.Bad.S01E01.720p",
				MediaType:         api.MediaTypeEpisode,
				AudioLang:         "en",
			},
		},
		{
			name: "no original language falls back to first audio track; path used when scene empty",
			series: &api.Series{
				Title:  "Anime",
				Year:   2010,
				ImdbID: "tt1",
				TvdbID: 5,
			},
			ep: &api.Episode{
				Title:         "Ep1",
				SeasonNumber:  1,
				EpisodeNumber: 2,
				EpisodeFile: &api.EpisodeFile{
					Path:      "/anime/ep.mkv",
					MediaInfo: &api.MediaInfo{AudioLanguages: "Japanese/English"},
				},
			},
			langs: []string{"en"},
			want: api.SearchRequest{
				Title:        "Anime",
				EpisodeTitle: "Ep1",
				Year:         2010,
				Season:       1,
				Episode:      2,
				ImdbID:       "tt1",
				TvdbID:       5,
				Languages:    []string{"en"},
				ReleaseName:  "/anime/ep.mkv",
				MediaType:    api.MediaTypeEpisode,
				AudioLang:    "ja",
			},
		},
		{
			name: "nil episode file yields empty release name and audio lang",
			series: &api.Series{
				Title:  "NoFile",
				Year:   2020,
				ImdbID: "tt2",
				TvdbID: 7,
			},
			ep: &api.Episode{
				Title:         "EpX",
				SeasonNumber:  3,
				EpisodeNumber: 4,
			},
			langs: []string{"de"},
			want: api.SearchRequest{
				Title:        "NoFile",
				EpisodeTitle: "EpX",
				Year:         2020,
				Season:       3,
				Episode:      4,
				ImdbID:       "tt2",
				TvdbID:       7,
				Languages:    []string{"de"},
				MediaType:    api.MediaTypeEpisode,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EpisodeSearchRequest(tc.series, tc.ep, tc.langs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("EpisodeSearchRequest()\n  got  %+v\n  want %+v", got, tc.want)
			}
		})
	}
}

// MovieSearchRequest is the single source of truth for the
// movie->SearchRequest mapping, mirroring EpisodeSearchRequest's audio-language
// and release-name resolution for movies.
func TestMovieSearchRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		movie *api.Movie
		langs []string
		want  api.SearchRequest
	}{
		{
			name: "scene name and original language",
			movie: &api.Movie{
				Title:            "Inception",
				Year:             2010,
				ImdbID:           "tt1375666",
				TmdbID:           27205,
				OriginalLanguage: &api.LanguageInfo{Name: "English"},
				AlternateTitles:  []api.AlternateTitle{{Title: "Origin"}},
				MovieFile: &api.MovieFile{
					SceneName: "Inception.2010.1080p",
					Path:      "/movies/inception.mkv",
					MediaInfo: &api.MediaInfo{AudioLanguages: "English"},
				},
			},
			langs: []string{"en"},
			want: api.SearchRequest{
				Title:             "Inception",
				AlternativeTitles: []string{"Origin"},
				Year:              2010,
				ImdbID:            "tt1375666",
				TmdbID:            27205,
				Languages:         []string{"en"},
				ReleaseName:       "Inception.2010.1080p",
				MediaType:         api.MediaTypeMovie,
				AudioLang:         "en",
			},
		},
		{
			name: "no original language falls back to first audio track; path used when scene empty",
			movie: &api.Movie{
				Title:  "Amelie",
				Year:   2001,
				ImdbID: "tt3",
				TmdbID: 194,
				MovieFile: &api.MovieFile{
					Path:      "/movies/amelie.mkv",
					MediaInfo: &api.MediaInfo{AudioLanguages: "French,German"},
				},
			},
			langs: []string{"fr"},
			want: api.SearchRequest{
				Title:       "Amelie",
				Year:        2001,
				ImdbID:      "tt3",
				TmdbID:      194,
				Languages:   []string{"fr"},
				ReleaseName: "/movies/amelie.mkv",
				MediaType:   api.MediaTypeMovie,
				AudioLang:   "fr",
			},
		},
		{
			name: "nil movie file yields empty release name and audio lang",
			movie: &api.Movie{
				Title:  "NoFile",
				Year:   2022,
				ImdbID: "tt4",
				TmdbID: 99,
			},
			langs: []string{"es"},
			want: api.SearchRequest{
				Title:     "NoFile",
				Year:      2022,
				ImdbID:    "tt4",
				TmdbID:    99,
				Languages: []string{"es"},
				MediaType: api.MediaTypeMovie,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MovieSearchRequest(tc.movie, tc.langs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MovieSearchRequest()\n  got  %+v\n  want %+v", got, tc.want)
			}
		})
	}
}
