package testsupport

import "testing"

// THE SYNC PIN (Go half): these hardcoded values are shared verbatim with
// internal/server/static-src/reference-fixture.test.ts. A drift in either
// generator fails its own suite; regenerate BOTH pins together when the
// fixture spec changes (see reffixture.go's sync contract).

func TestRefFixture_pins_the_cross_language_contract(t *testing.T) {
	t.Parallel()
	series := RefSeriesItems()
	movies := RefMovieItems()

	if len(series) != 500 || len(movies) != 4360 {
		t.Fatalf("scale = %d series / %d movies, want 500 / 4360", len(series), len(movies))
	}
	if got := RefTotalEpisodes(); got != 10049 {
		t.Errorf("total episodes = %d, want 10049 (the ~10k reference scale)", got)
	}
	if got := RefChecksum(); got != 1400457312 {
		t.Errorf("fixture checksum = %d, want 1400457312 (TS mirror drifted?)", got)
	}

	samples := []struct {
		want RefSeries
		idx  int
	}{
		{idx: 0, want: RefSeries{
			Title: "Reference Series 0000", TvdbID: 100001, ArrID: 1, Year: 1967,
			Episodes: 9, EpisodesPerSeason: 11, Seasons: 1, HaveEN: 0, HaveFR: 0,
			AudioIdx: 0, HasFR: false,
		}},
		{idx: 123, want: RefSeries{
			Title: "Reference Series 0123", TvdbID: 100124, ArrID: 124, Year: 1993,
			Episodes: 8, EpisodesPerSeason: 11, Seasons: 1, HaveEN: 5, HaveFR: 8,
			AudioIdx: 3, HasFR: false,
		}},
		{idx: 499, want: RefSeries{
			Title: "Reference Series 0499", TvdbID: 100500, ArrID: 500, Year: 2024,
			Episodes: 11, EpisodesPerSeason: 10, Seasons: 2, HaveEN: 1, HaveFR: 6,
			AudioIdx: 4, HasFR: false,
		}},
	}
	for _, s := range samples {
		if got := series[s.idx]; got != s.want {
			t.Errorf("series[%d] = %+v, want %+v", s.idx, got, s.want)
		}
	}

	movieSamples := []struct {
		want RefMovie
		idx  int
	}{
		{idx: 0, want: RefMovie{
			Title:     "Reference Movie 0000",
			SceneName: "Reference.Movie.0000.2009.2160p.WEB-DL.x265-GRP",
			TmdbID:    500001, ArrID: 1, Year: 2009, HaveEN: 0, HaveFR: 1,
			EmbeddedTracks: 6, SceneIdx: 1, AudioIdx: 4, HasFR: true,
		}},
		{idx: 2179, want: RefMovie{
			Title:     "Reference Movie 2179",
			SceneName: "Reference.Movie.2179.1959.1080p.BluRay.x264-REF",
			TmdbID:    502180, ArrID: 2180, Year: 1959, HaveEN: 1, HaveFR: 0,
			EmbeddedTracks: 5, SceneIdx: 0, AudioIdx: 1, HasFR: true,
		}},
		{idx: 4359, want: RefMovie{
			Title:     "Reference Movie 4359",
			SceneName: "Reference.Movie.4359.2012.1080p.WEB.h264-STD",
			TmdbID:    504360, ArrID: 4360, Year: 2012, HaveEN: 1, HaveFR: 0,
			EmbeddedTracks: 1, SceneIdx: 3, AudioIdx: 3, HasFR: false,
		}},
	}
	for _, m := range movieSamples {
		if got := movies[m.idx]; got != m.want {
			t.Errorf("movie[%d] = %+v, want %+v", m.idx, got, m.want)
		}
	}
}
