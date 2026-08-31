package arrsvc

// cachedarr_stress_test.go — task 19's MEASURED full arr list fetch at the
// reference library (design A4): the wrapper's cold full-list pass (series +
// movies) against a local httptest arr fake serving realistic payload sizes,
// recorded as the wave-delay trade's stated basis. The 2s wave floor and the
// 20s execution budget are priced against THIS number: a full list pass at
// 500 series / 4,360 movies is upstream work in the same order of magnitude
// as one floor, and far under one execution budget, even with transfer and
// decode included. Environment-sensitive, so the numbers land in the test
// log and only sanity bounds are asserted.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/testsupport"
)

// marshalJSON encodes a fake payload, failing the test on the impossible.
func marshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fake payload: %v", err)
	}
	return b
}

// refOverview builds the deterministic overview filler that gives each wire
// row a realistic size (arr payloads are dominated by fields subflux never
// decodes; they still cost transfer and parse).
func refOverview(kind string, i, sentences int) string {
	var b strings.Builder
	for s := range sentences {
		fmt.Fprintf(&b, "Deterministic %s overview sentence %d for item %04d, standing in for the synopsis text a real arr instance serves. ", kind, s, i)
	}
	return b.String()
}

// refImages is the images block every arr row carries.
func refImages(kind string, id int) []map[string]any {
	cover := func(t string) map[string]any {
		return map[string]any{
			"coverType": t,
			"url":       fmt.Sprintf("/MediaCover/%d/%s.jpg?lastWrite=638612345678901234", id, t),
			"remoteUrl": fmt.Sprintf("https://artworks.example.invalid/%s/%d/%s.jpg", kind, id, t),
		}
	}
	return []map[string]any{cover("banner"), cover("poster"), cover("fanart")}
}

// refSonarrSeriesJSON renders the fixture's 500 series as a realistic Sonarr
// /api/v3/series payload: the fields arrapi decodes plus the padding a real
// response carries (seasons with statistics, images, overview, ratings).
func refSonarrSeriesJSON(t *testing.T) []byte {
	t.Helper()
	items := testsupport.RefSeriesItems()
	rows := make([]map[string]any, 0, len(items))
	for i, s := range items {
		seasons := make([]map[string]any, 0, s.Seasons)
		remaining := s.Episodes
		for sn := 1; sn <= s.Seasons; sn++ {
			eps := min(remaining, s.EpisodesPerSeason)
			remaining -= eps
			seasons = append(seasons, map[string]any{
				"seasonNumber": sn,
				"monitored":    true,
				"statistics": map[string]any{
					"episodeFileCount":  eps,
					"episodeCount":      eps,
					"totalEpisodeCount": eps,
					"sizeOnDisk":        int64(eps) * 1_400_000_000,
					"percentOfEpisodes": 100.0,
				},
			})
		}
		rows = append(rows, map[string]any{
			"id":                s.ArrID,
			"title":             s.Title,
			"sortTitle":         strings.ToLower(s.Title),
			"titleSlug":         fmt.Sprintf("reference-series-%04d", i),
			"tvdbId":            s.TvdbID,
			"tvMazeId":          70000 + i,
			"tvRageId":          0,
			"imdbId":            fmt.Sprintf("tt%07d", 1000000+s.TvdbID),
			"year":              s.Year,
			"firstAired":        fmt.Sprintf("%d-01-15T00:00:00Z", s.Year),
			"status":            "continuing",
			"overview":          refOverview("series", i, 3),
			"network":           "Reference Network",
			"airTime":           "21:00",
			"images":            refImages("series", s.ArrID),
			"seasons":           seasons,
			"path":              fmt.Sprintf("/tv/%s", s.Title),
			"rootFolderPath":    "/tv",
			"qualityProfileId":  6,
			"monitored":         true,
			"seasonFolder":      true,
			"useSceneNumbering": false,
			"runtime":           45,
			"seriesType":        "standard",
			"cleanTitle":        strings.ReplaceAll(strings.ToLower(s.Title), " ", ""),
			"certification":     "TV-14",
			"genres":            []string{"Drama", "Reference", "Deterministic"},
			"tags":              []int{1},
			"added":             "2024-05-01T10:00:00Z",
			"ratings":           map[string]any{"votes": 1200 + i, "value": 7.5},
			"originalLanguage":  map[string]any{"id": s.AudioIdx + 1, "name": testsupport.RefAudioNames[s.AudioIdx]},
			"alternateTitles":   []map[string]any{{"title": s.Title + " (alt)"}},
			"statistics": map[string]any{
				"seasonCount":       s.Seasons,
				"episodeFileCount":  s.Episodes,
				"episodeCount":      s.Episodes,
				"totalEpisodeCount": s.Episodes,
				"sizeOnDisk":        int64(s.Episodes) * 1_400_000_000,
				"percentOfEpisodes": 100.0,
			},
		})
	}
	return marshalJSON(t, rows)
}

// refRadarrMoviesJSON renders the fixture's 4,360 movies as a realistic
// Radarr /api/v3/movie payload (the fleet's measured largest arr payload).
func refRadarrMoviesJSON(t *testing.T) []byte {
	t.Helper()
	items := testsupport.RefMovieItems()
	rows := make([]map[string]any, 0, len(items))
	for i, m := range items {
		ratings := map[string]any{
			"imdb":           map[string]any{"votes": 5000 + i, "value": 6.8, "type": "user"},
			"tmdb":           map[string]any{"votes": 3000 + i, "value": 7.1, "type": "user"},
			"metacritic":     map[string]any{"votes": 40 + i%50, "value": 61, "type": "critic"},
			"rottenTomatoes": map[string]any{"votes": 0, "value": 74, "type": "critic"},
		}
		rows = append(rows, map[string]any{
			"id":                    m.ArrID,
			"title":                 m.Title,
			"originalTitle":         m.Title,
			"sortTitle":             strings.ToLower(m.Title),
			"tmdbId":                m.TmdbID,
			"imdbId":                fmt.Sprintf("tt%07d", 2000000+m.TmdbID),
			"year":                  m.Year,
			"inCinemas":             fmt.Sprintf("%d-03-01T00:00:00Z", m.Year),
			"physicalRelease":       fmt.Sprintf("%d-07-01T00:00:00Z", m.Year),
			"digitalRelease":        fmt.Sprintf("%d-06-01T00:00:00Z", m.Year),
			"status":                "released",
			"overview":              refOverview("movie", i, 8),
			"studio":                "Reference Pictures",
			"website":               fmt.Sprintf("https://movies.example.invalid/%d", m.TmdbID),
			"youTubeTrailerId":      fmt.Sprintf("ref%08d", i),
			"images":                refImages("movie", m.ArrID)[:2],
			"path":                  fmt.Sprintf("/movies/%s (%d)", m.Title, m.Year),
			"rootFolderPath":        "/movies",
			"folderName":            fmt.Sprintf("/movies/%s (%d)", m.Title, m.Year),
			"qualityProfileId":      7,
			"hasFile":               true,
			"monitored":             true,
			"minimumAvailability":   "announced",
			"isAvailable":           true,
			"runtime":               90 + i%60,
			"cleanTitle":            strings.ReplaceAll(strings.ToLower(m.Title), " ", ""),
			"certification":         "PG-13",
			"genres":                []string{"Drama", "Reference"},
			"tags":                  []int{1, 3},
			"added":                 "2024-05-01T10:00:00Z",
			"ratings":               ratings,
			"popularity":            42.5,
			"secondaryYearSourceId": 0,
			"sizeOnDisk":            int64(4_000_000_000 + i*1000),
			"originalLanguage":      map[string]any{"id": m.AudioIdx + 1, "name": testsupport.RefAudioNames[m.AudioIdx]},
			"alternateTitles": []map[string]any{
				{"title": m.Title + " (alternate)"},
				{"title": m.Title + " (working title)"},
				{"title": m.Title + " (original release)"},
			},
			"collection": map[string]any{
				"title":  m.Title + " Collection",
				"tmdbId": 900000 + i,
			},
			"movieFile": map[string]any{
				"id":           m.ArrID,
				"movieId":      m.ArrID,
				"relativePath": fmt.Sprintf("%s (%d).mkv", m.Title, m.Year),
				"path":         fmt.Sprintf("/movies/%s (%d)/%s (%d).mkv", m.Title, m.Year, m.Title, m.Year),
				"size":         int64(4_000_000_000 + i*1000),
				"dateAdded":    "2024-05-02T10:00:00Z",
				"sceneName":    m.SceneName,
				"releaseGroup": "REF",
				"edition":      "",
				"mediaInfo": map[string]any{
					"audioCodec":     "DTS",
					"audioChannels":  5.1,
					"audioLanguages": testsupport.RefAudioLangs[m.AudioIdx],
					"videoCodec":     "x264",
					"resolution":     "1920x1080",
					"runTime":        "1:45:00",
					"subtitles":      "eng/fre",
				},
				"quality": map[string]any{
					"quality":  map[string]any{"id": 7, "name": "Bluray-1080p", "source": "bluray", "resolution": 1080},
					"revision": map[string]any{"version": 1, "real": 0, "isRepack": false},
				},
			},
		})
	}
	return marshalJSON(t, rows)
}

func TestReferenceLibrary_full_list_fetch_duration(t *testing.T) {
	seriesJSON := refSonarrSeriesJSON(t)
	moviesJSON := refRadarrMoviesJSON(t)

	mux := http.NewServeMux()
	serve := func(payload []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
		}
	}
	mux.Handle("/api/v3/series", serve(seriesJSON))
	mux.Handle("/api/v3/movie", serve(moviesJSON))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	gate := NewReadGate(nil, nil)
	sonarr, err := NewCachedSonarr(srv.URL, APIKey("reference-key"), gate)
	if err != nil {
		t.Fatalf("NewCachedSonarr: %v", err)
	}
	defer sonarr.Close()
	radarr, err := NewCachedRadarr(srv.URL, APIKey("reference-key"), gate)
	if err != nil {
		t.Fatalf("NewCachedRadarr: %v", err)
	}
	defer radarr.Close()

	ctx := t.Context()

	// THE MEASUREMENT: the wrapper's cold full list pass — one plain series
	// read plus one plain movie read, transfer and decode included.
	start := time.Now()
	series, err := sonarr.Series(ctx)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	seriesDur := time.Since(start)
	movieStart := time.Now()
	movies, err := radarr.Movies(ctx)
	if err != nil {
		t.Fatalf("Movies: %v", err)
	}
	movieDur := time.Since(movieStart)
	total := time.Since(start)

	if len(series) != testsupport.RefSeriesCount {
		t.Fatalf("series rows = %d, want %d", len(series), testsupport.RefSeriesCount)
	}
	if len(movies) != testsupport.RefMovieCount {
		t.Fatalf("movie rows = %d, want %d", len(movies), testsupport.RefMovieCount)
	}
	// Decode spot checks: the fake's wire rows reach the DTOs intact.
	if series[0].TvdbID != 100001 || series[0].Statistics == nil || series[0].Statistics.EpisodeCount != 9 {
		t.Errorf("series[0] decoded wrong: %+v", series[0])
	}
	if movies[0].TmdbID != 500001 || movies[0].MovieFile == nil {
		t.Errorf("movie[0] decoded wrong: %+v", movies[0])
	}

	// The warm pass: both lists inside the TTL are cache hits.
	warmStart := time.Now()
	if _, err := sonarr.Series(ctx); err != nil {
		t.Fatalf("warm Series: %v", err)
	}
	if _, err := radarr.Movies(ctx); err != nil {
		t.Fatalf("warm Movies: %v", err)
	}
	warm := time.Since(warmStart)

	t.Logf("reference library full list pass (%d series / %d movies, loopback):",
		len(series), len(movies))
	t.Logf("  series list: %d bytes in %v", len(seriesJSON), seriesDur)
	t.Logf("  movie list:  %d bytes in %v", len(moviesJSON), movieDur)
	t.Logf("  cold total:  %v (the wave-delay trade's basis — design A4)", total)
	t.Logf("  warm total:  %v (cache hits inside the %v TTL)", warm, arrCacheTTL)
	t.Logf("  vs RECOVERY_WAVE_FLOOR_MS %v / RECOVERY_EXECUTION_BUDGET_MS %v", waveFloor, executionBudget)

	// Sanity bounds only (environment-sensitive measurement, report carries
	// the numbers): a loopback pass must fit ONE execution budget with room,
	// and the warm pass must not refetch (bounded well under one cold pass).
	if total > executionBudget {
		t.Errorf("cold full list pass took %v, above the %v execution budget — the wave-delay arithmetic no longer holds", total, executionBudget)
	}
	if warm > total {
		t.Errorf("warm pass (%v) slower than cold pass (%v): cache not serving", warm, total)
	}
}
