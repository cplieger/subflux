package scanning

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/subflux"
)

// ExtractAltTitles returns the alternative titles that differ from the
// primary; a single distinct alt is returned unchanged.
func TestExtractAltTitles_returns_distinct_alt(t *testing.T) {
	t.Parallel()
	got := ExtractAltTitles([]arrapi.AlternateTitle{{Title: "Alt"}}, "Main")
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
		series *arrapi.Series
		ep     *arrapi.Episode
		langs  []string
		want   subflux.SearchRequest
	}{
		{
			name: "scene name and original language",
			series: &arrapi.Series{
				Title:            "Breaking Bad",
				Year:             2008,
				ImdbID:           "tt0903747",
				TvdbID:           81189,
				OriginalLanguage: &arrapi.Language{Name: "English"},
				AlternateTitles:  []arrapi.AlternateTitle{{Title: "BrBa"}, {Title: "Breaking Bad"}},
			},
			ep: &arrapi.Episode{
				Title:                 "Pilot",
				SeasonNumber:          1,
				EpisodeNumber:         1,
				SceneSeasonNumber:     1,
				SceneEpisodeNumber:    1,
				AbsoluteEpisodeNumber: 1,
				EpisodeFile: &arrapi.EpisodeFile{
					SceneName: "Breaking.Bad.S01E01.720p",
					Path:      "/tv/bb/s01e01.mkv",
					MediaInfo: &arrapi.MediaInfo{AudioLanguages: "English/Japanese"},
				},
			},
			langs: []string{"en", "fr"},
			want: subflux.SearchRequest{
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
				MediaType:         subflux.MediaTypeEpisode,
				AudioLang:         "en",
			},
		},
		{
			name: "no original language falls back to first audio track; path used when scene empty",
			series: &arrapi.Series{
				Title:  "Anime",
				Year:   2010,
				ImdbID: "tt1",
				TvdbID: 5,
			},
			ep: &arrapi.Episode{
				Title:         "Ep1",
				SeasonNumber:  1,
				EpisodeNumber: 2,
				EpisodeFile: &arrapi.EpisodeFile{
					Path:      "/anime/ep.mkv",
					MediaInfo: &arrapi.MediaInfo{AudioLanguages: "Japanese/English"},
				},
			},
			langs: []string{"en"},
			want: subflux.SearchRequest{
				Title:        "Anime",
				EpisodeTitle: "Ep1",
				Year:         2010,
				Season:       1,
				Episode:      2,
				ImdbID:       "tt1",
				TvdbID:       5,
				Languages:    []string{"en"},
				ReleaseName:  "/anime/ep.mkv",
				MediaType:    subflux.MediaTypeEpisode,
				AudioLang:    "ja",
			},
		},
		{
			name: "nil episode file yields empty release name and audio lang",
			series: &arrapi.Series{
				Title:  "NoFile",
				Year:   2020,
				ImdbID: "tt2",
				TvdbID: 7,
			},
			ep: &arrapi.Episode{
				Title:         "EpX",
				SeasonNumber:  3,
				EpisodeNumber: 4,
			},
			langs: []string{"de"},
			want: subflux.SearchRequest{
				Title:        "NoFile",
				EpisodeTitle: "EpX",
				Year:         2020,
				Season:       3,
				Episode:      4,
				ImdbID:       "tt2",
				TvdbID:       7,
				Languages:    []string{"de"},
				MediaType:    subflux.MediaTypeEpisode,
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
		movie *arrapi.Movie
		langs []string
		want  subflux.SearchRequest
	}{
		{
			name: "scene name and original language",
			movie: &arrapi.Movie{
				Title:            "Inception",
				Year:             2010,
				ImdbID:           "tt1375666",
				TmdbID:           27205,
				OriginalLanguage: &arrapi.Language{Name: "English"},
				AlternateTitles:  []arrapi.AlternateTitle{{Title: "Origin"}},
				MovieFile: &arrapi.MovieFile{
					SceneName: "Inception.2010.1080p",
					Path:      "/movies/inception.mkv",
					MediaInfo: &arrapi.MediaInfo{AudioLanguages: "English"},
				},
			},
			langs: []string{"en"},
			want: subflux.SearchRequest{
				Title:             "Inception",
				AlternativeTitles: []string{"Origin"},
				Year:              2010,
				ImdbID:            "tt1375666",
				TmdbID:            27205,
				Languages:         []string{"en"},
				ReleaseName:       "Inception.2010.1080p",
				MediaType:         subflux.MediaTypeMovie,
				AudioLang:         "en",
			},
		},
		{
			name: "no original language falls back to first audio track; path used when scene empty",
			movie: &arrapi.Movie{
				Title:  "Amelie",
				Year:   2001,
				ImdbID: "tt3",
				TmdbID: 194,
				MovieFile: &arrapi.MovieFile{
					Path:      "/movies/amelie.mkv",
					MediaInfo: &arrapi.MediaInfo{AudioLanguages: "French,German"},
				},
			},
			langs: []string{"fr"},
			want: subflux.SearchRequest{
				Title:       "Amelie",
				Year:        2001,
				ImdbID:      "tt3",
				TmdbID:      194,
				Languages:   []string{"fr"},
				ReleaseName: "/movies/amelie.mkv",
				MediaType:   subflux.MediaTypeMovie,
				AudioLang:   "fr",
			},
		},
		{
			name: "nil movie file yields empty release name and audio lang",
			movie: &arrapi.Movie{
				Title:  "NoFile",
				Year:   2022,
				ImdbID: "tt4",
				TmdbID: 99,
			},
			langs: []string{"es"},
			want: subflux.SearchRequest{
				Title:     "NoFile",
				Year:      2022,
				ImdbID:    "tt4",
				TmdbID:    99,
				Languages: []string{"es"},
				MediaType: subflux.MediaTypeMovie,
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

// --- Outcome classification of one scanned item ---
//
// ScanEpisode and ScanMovie translate one engine SearchResult into the four
// scan outcomes the stats, the season tracker and the pacing signal all key
// on, and decide whether the item's coverage badge needs republishing. The
// cases below drive the engine's answer directly, because that answer IS the
// only input to the translation.

// captureLogs swaps the global slog default for a Debug-level text buffer.
// The caller must not be parallel: the default logger is process-global.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// oneLang wraps a single language group as a whole search result.
func oneLang(o subflux.LangOutcome) subflux.SearchResult {
	return subflux.SearchResult{Langs: []subflux.LangOutcome{o}}
}

func scanTestSeries() *arrapi.Series {
	return &arrapi.Series{
		ID: 42, Title: "Test Show", Year: 2019, ImdbID: "tt1",
		OriginalLanguage: &arrapi.Language{Name: "English"},
	}
}

func scanTestEpisode() *arrapi.Episode {
	return &arrapi.Episode{
		ID: 100, SeasonNumber: 1, EpisodeNumber: 2, HasFile: true,
		EpisodeFile: &arrapi.EpisodeFile{Path: "/media/e.mkv", SceneName: "Show.S01E02"},
	}
}

func scanTestMovie() *arrapi.Movie {
	return &arrapi.Movie{
		ID: 7, Title: "Test Movie", Year: 2020, ImdbID: "tt2", TmdbID: 27205,
		OriginalLanguage: &arrapi.Language{Name: "English"},
		MovieFile:        &arrapi.MovieFile{Path: "/media/m.mkv", SceneName: "Movie.2020"},
	}
}

func TestScanEpisode_classifies_the_engine_result(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		result       subflux.SearchResult
		want         ScanOutcome
		wantCoverage int
		wantQueried  bool
	}{
		{
			name: "a downloaded subtitle is found",
			result: oneLang(subflux.LangOutcome{
				Lang: "fr", Kind: subflux.LangSearched,
				Paths: []string{"/media/e.fr.srt"}, Searched: 1, Queried: 2,
			}),
			want: ScanFound, wantCoverage: 1, wantQueried: true,
		},
		{
			name: "a searched language that downloaded nothing is a no-result",
			result: oneLang(subflux.LangOutcome{
				Lang: "fr", Kind: subflux.LangSearched, Searched: 1, Queried: 2,
			}),
			want: ScanNoResult, wantCoverage: 0, wantQueried: true,
		},
		{
			name: "a language that needed no search is skipped",
			result: oneLang(subflux.LangOutcome{
				Lang: "fr", Kind: subflux.LangSkipped, Skipped: 1,
			}),
			want: ScanSkipped, wantCoverage: 0, wantQueried: false,
		},
		{
			name: "a language whose providers are all in backoff is backed off",
			result: oneLang(subflux.LangOutcome{
				Lang: "fr", Kind: subflux.LangBackedOff,
			}),
			want: ScanBackedOff, wantCoverage: 0, wantQueried: false,
		},
		{
			name: "an inventory change republishes coverage without a download",
			result: subflux.SearchResult{
				CoverageChanged: true,
				Langs: []subflux.LangOutcome{
					{Lang: "fr", Kind: subflux.LangSkipped, Skipped: 1},
				},
			},
			want: ScanSkipped, wantCoverage: 1, wantQueried: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev := &recEvents{}
			engine := &fakeEngine{result: tc.result}
			ls := &LiveState{Cfg: &fakeScanCfg{languages: []string{"fr"}}, Engine: engine}
			deps := &Deps{Events: ev, Activity: activity.New(10), Alerts: nopAlerts{}}

			got, langs, queried := ScanEpisode(t.Context(), deps, ls,
				scanTestSeries(), scanTestEpisode())

			if got != tc.want {
				t.Errorf("ScanEpisode(%s) outcome = %q, want %q", tc.name, got, tc.want)
			}
			if queried != tc.wantQueried {
				t.Errorf("ScanEpisode(%s) queried = %t, want %t", tc.name, queried, tc.wantQueried)
			}
			if !reflect.DeepEqual(langs, tc.result.Langs) {
				t.Errorf("ScanEpisode(%s) langs = %+v, want the engine's groups %+v",
					tc.name, langs, tc.result.Langs)
			}
			if n := len(ev.coverageEvents()); n != tc.wantCoverage {
				t.Errorf("ScanEpisode(%s) published %d coverage updates, want %d",
					tc.name, n, tc.wantCoverage)
			}
		})
	}
}

func TestScanMovie_classifies_the_engine_result(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		result       subflux.SearchResult
		want         ScanOutcome
		wantCoverage int
		wantQueried  bool
	}{
		{
			name: "a downloaded subtitle is found",
			result: oneLang(subflux.LangOutcome{
				Lang: "fr", Kind: subflux.LangSearched,
				Paths: []string{"/media/m.fr.srt"}, Searched: 1, Queried: 2,
			}),
			want: ScanFound, wantCoverage: 1, wantQueried: true,
		},
		{
			name: "a searched language that downloaded nothing is a no-result",
			result: oneLang(subflux.LangOutcome{
				Lang: "fr", Kind: subflux.LangSearched, Searched: 1, Queried: 2,
			}),
			want: ScanNoResult, wantCoverage: 0, wantQueried: true,
		},
		{
			name: "a language that needed no search is skipped",
			result: oneLang(subflux.LangOutcome{
				Lang: "fr", Kind: subflux.LangSkipped, Skipped: 1,
			}),
			want: ScanSkipped, wantCoverage: 0, wantQueried: false,
		},
		{
			name: "a language whose providers are all in backoff is backed off",
			result: oneLang(subflux.LangOutcome{
				Lang: "fr", Kind: subflux.LangBackedOff,
			}),
			want: ScanBackedOff, wantCoverage: 0, wantQueried: false,
		},
		{
			name: "an inventory change republishes coverage without a download",
			result: subflux.SearchResult{
				CoverageChanged: true,
				Langs: []subflux.LangOutcome{
					{Lang: "fr", Kind: subflux.LangSkipped, Skipped: 1},
				},
			},
			want: ScanSkipped, wantCoverage: 1, wantQueried: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev := &recEvents{}
			engine := &fakeEngine{result: tc.result}
			ls := &LiveState{Cfg: &fakeScanCfg{languages: []string{"fr"}}, Engine: engine}
			deps := &Deps{Events: ev, Activity: activity.New(10), Alerts: nopAlerts{}}

			got, queried := ScanMovie(t.Context(), deps, ls, scanTestMovie())

			if got != tc.want {
				t.Errorf("ScanMovie(%s) outcome = %q, want %q", tc.name, got, tc.want)
			}
			if queried != tc.wantQueried {
				t.Errorf("ScanMovie(%s) queried = %t, want %t", tc.name, queried, tc.wantQueried)
			}
			if n := len(ev.coverageEvents()); n != tc.wantCoverage {
				t.Errorf("ScanMovie(%s) published %d coverage updates, want %d",
					tc.name, n, tc.wantCoverage)
			}
		})
	}
}

// The force-upgrade flag is opt-in: a scheduled scan leaves it unset (an
// existing good-enough subtitle is not re-downloaded), and only the manual
// endpoints pass it, which is what reaches the engine in the request.
func TestScanEpisode_force_upgrade_reaches_the_engine_only_when_asked(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		forceUpgrade []bool
		want         bool
	}{
		{name: "omitted", forceUpgrade: nil, want: false},
		{name: "false", forceUpgrade: []bool{false}, want: false},
		{name: "true", forceUpgrade: []bool{true}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := &fakeEngine{}
			ls := &LiveState{Cfg: &fakeScanCfg{languages: []string{"fr"}}, Engine: engine}
			deps := &Deps{Events: &recEvents{}, Activity: activity.New(10), Alerts: nopAlerts{}}

			ScanEpisode(t.Context(), deps, ls, scanTestSeries(), scanTestEpisode(), tc.forceUpgrade...)

			if got := engine.lastRequest().ForceUpgrade; got != tc.want {
				t.Errorf("ScanEpisode(forceUpgrade=%v) request ForceUpgrade = %t, want %t",
					tc.forceUpgrade, got, tc.want)
			}
		})
	}
}

// A search the engine answered without error is silent: the warn line is
// reserved for a real provider failure, and a scan that emits it on every
// item buries the one that matters.
func TestScanItem_successful_search_logs_no_warning(t *testing.T) {
	// No t.Parallel: this test swaps the global slog default logger.
	buf := captureLogs(t)

	engine := &fakeEngine{result: oneLang(subflux.LangOutcome{
		Lang: "fr", Kind: subflux.LangSkipped, Skipped: 1,
	})}
	ls := &LiveState{Cfg: &fakeScanCfg{languages: []string{"fr"}}, Engine: engine}
	deps := &Deps{Events: &recEvents{}, Activity: activity.New(10), Alerts: nopAlerts{}}

	ScanEpisode(t.Context(), deps, ls, scanTestSeries(), scanTestEpisode())
	ScanMovie(t.Context(), deps, ls, scanTestMovie())

	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("a successful scan logged a warning; log was:\n%s", buf.String())
	}
}
