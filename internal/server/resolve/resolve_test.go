package resolve_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/resolve"
)

// fakeStore returns a fixed row set for any media query.
type fakeStore struct {
	rows []api.SubtitleEntry
	err  error
}

func (f *fakeStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return f.rows, f.err
}

// okValidator accepts every path; denyValidator refuses every path.
type okValidator struct{}

func (okValidator) ValidatePath(context.Context, string) error { return nil }

type denyValidator struct{}

func (denyValidator) ValidatePath(context.Context, string) error { return errors.New("denied") }

type fakeSonarr struct {
	episodes []arrapi.Episode
	err      error
}

func (f *fakeSonarr) GetEpisodes(context.Context, int) ([]arrapi.Episode, error) {
	return f.episodes, f.err
}

type fakeRadarr struct {
	movie arrapi.Movie
	err   error
}

func (f *fakeRadarr) GetMovieByID(context.Context, int) (arrapi.Movie, error) {
	return f.movie, f.err
}

func newResolver(store *fakeStore, sonarr *fakeSonarr, radarr *fakeRadarr) *resolve.Resolver {
	st := &resolve.State{Cfg: okValidator{}}
	if sonarr != nil {
		st.Sonarr = sonarr
	}
	if radarr != nil {
		st.Radarr = radarr
	}
	return &resolve.Resolver{Store: store, State: func() *resolve.State { return st }}
}

func extRow(mediaID, lang, variant, path, videoPath string) api.SubtitleEntry {
	return api.SubtitleEntry{
		MediaID: mediaID, Language: lang, Variant: variant,
		Source: string(api.SourceExternal), Path: path, VideoPath: videoPath,
		Ordinal: api.ManualOrdinal(path),
	}
}

func movieRef(lang, variant string, ordinal int) *resolve.FileRef {
	return &resolve.FileRef{
		MediaType: api.MediaTypeMovie, MediaID: "tmdb-1271",
		Language: lang, Variant: variant, Source: string(api.SourceExternal), Ordinal: ordinal,
	}
}

// TestSubtitlePath_resolutionTable covers the design's resolve table:
// ref -> path, missing -> subtitle_not_found, ordinal disambiguation, and
// ambiguity -> invariant error.
func TestSubtitlePath_resolutionTable(t *testing.T) {
	t.Parallel()
	rows := []api.SubtitleEntry{
		extRow("tmdb-1271", "fr", "standard", "/media/movie.fr.srt", "/media/movie.mkv"),
		extRow("tmdb-1271", "fr", "standard", "/media/movie.fr.1.srt", "/media/movie.mkv"),
		extRow("tmdb-1271", "fr", "standard", "/media/movie.fr.2.srt", "/media/movie.mkv"),
		extRow("tmdb-1271", "fr", "forced", "/media/movie.fr.forced.1.srt", "/media/movie.mkv"),
		extRow("tmdb-1271", "de", "standard", "/media/movie.de.srt", "/media/movie.mkv"),
		{MediaID: "tmdb-1271", Language: "en", Variant: "standard", Source: string(api.SourceEmbedded)},
	}
	r := newResolver(&fakeStore{rows: rows}, nil, nil)
	ctx := context.Background()

	tests := []struct {
		name     string
		ref      *resolve.FileRef
		wantPath string
		wantErr  error
	}{
		{"auto file ordinal 0", movieRef("fr", "standard", 0), "/media/movie.fr.srt", nil},
		{"manual sibling ordinal 1", movieRef("fr", "standard", 1), "/media/movie.fr.1.srt", nil},
		{"manual sibling ordinal 2", movieRef("fr", "standard", 2), "/media/movie.fr.2.srt", nil},
		{"forced variant ordinal 1", movieRef("fr", "forced", 1), "/media/movie.fr.forced.1.srt", nil},
		{"other language", movieRef("de", "standard", 0), "/media/movie.de.srt", nil},
		{"missing ordinal", movieRef("fr", "standard", 7), "", resolve.ErrSubtitleNotFound},
		{"missing language", movieRef("pt", "standard", 0), "", resolve.ErrSubtitleNotFound},
		{"embedded not addressable", &resolve.FileRef{
			MediaType: api.MediaTypeMovie, MediaID: "tmdb-1271",
			Language: "en", Variant: "standard", Source: string(api.SourceEmbedded),
		}, "", resolve.ErrSubtitleNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.SubtitlePath(ctx, tc.ref)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantPath {
				t.Fatalf("path = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

// TestSubtitlePath_ambiguity: two rows sharing quad+ordinal (differing only
// by extension) is an internal invariant error, never a guess.
func TestSubtitlePath_ambiguity(t *testing.T) {
	t.Parallel()
	rows := []api.SubtitleEntry{
		extRow("tmdb-1271", "fr", "standard", "/media/movie.fr.srt", ""),
		extRow("tmdb-1271", "fr", "standard", "/media/movie.fr.ass", ""),
	}
	r := newResolver(&fakeStore{rows: rows}, nil, nil)
	_, err := r.SubtitlePath(context.Background(), movieRef("fr", "standard", 0))
	if !errors.Is(err, resolve.ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
}

// TestSubtitlePath_containmentInvariant: a resolved path failing the
// containment check is a 500-class invariant error, not a 4xx sentinel.
func TestSubtitlePath_containmentInvariant(t *testing.T) {
	t.Parallel()
	rows := []api.SubtitleEntry{extRow("tmdb-1271", "fr", "standard", "/media/movie.fr.srt", "")}
	st := &resolve.State{Cfg: denyValidator{}}
	r := &resolve.Resolver{Store: &fakeStore{rows: rows}, State: func() *resolve.State { return st }}
	_, err := r.SubtitlePath(context.Background(), movieRef("fr", "standard", 0))
	if !errors.Is(err, resolve.ErrPathInvariant) {
		t.Fatalf("err = %v, want ErrPathInvariant", err)
	}
	if errors.Is(err, resolve.ErrSubtitleNotFound) || errors.Is(err, resolve.ErrMediaNotFound) {
		t.Fatal("containment invariant must not map to a 404 sentinel")
	}
}

// TestVideoPathForFile covers the row join, the sibling fallback, and the
// no-video-recorded 404.
func TestVideoPathForFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("row join", func(t *testing.T) {
		t.Parallel()
		rows := []api.SubtitleEntry{extRow("tmdb-1271", "fr", "standard", "/media/movie.fr.srt", "/media/movie.mkv")}
		r := newResolver(&fakeStore{rows: rows}, nil, nil)
		got, err := r.VideoPathForFile(ctx, movieRef("fr", "standard", 0))
		if err != nil || got != "/media/movie.mkv" {
			t.Fatalf("got %q err %v", got, err)
		}
	})

	t.Run("sibling fallback", func(t *testing.T) {
		t.Parallel()
		rows := []api.SubtitleEntry{
			extRow("tmdb-1271", "fr", "standard", "/media/movie.fr.srt", ""),
			extRow("tmdb-1271", "de", "standard", "/media/movie.de.srt", "/media/movie.mkv"),
		}
		r := newResolver(&fakeStore{rows: rows}, nil, nil)
		got, err := r.VideoPathForFile(ctx, movieRef("fr", "standard", 0))
		if err != nil || got != "/media/movie.mkv" {
			t.Fatalf("got %q err %v", got, err)
		}
	})

	t.Run("no video recorded", func(t *testing.T) {
		t.Parallel()
		rows := []api.SubtitleEntry{extRow("tmdb-1271", "fr", "standard", "/media/movie.fr.srt", "")}
		r := newResolver(&fakeStore{rows: rows}, nil, nil)
		_, err := r.VideoPathForFile(ctx, movieRef("fr", "standard", 0))
		if !errors.Is(err, resolve.ErrMediaNotFound) {
			t.Fatalf("err = %v, want ErrMediaNotFound", err)
		}
	})
}

// TestVideoPath_mediaRef covers the arr resolution: movie file, episode
// file by season/episode, and the not-found taxonomy.
func TestVideoPath_mediaRef(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	radarr := &fakeRadarr{movie: arrapi.Movie{
		ID: 42, MovieFile: &arrapi.MovieFile{Path: "/media/movies/Inception.mkv"},
	}}
	sonarr := &fakeSonarr{episodes: []arrapi.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, EpisodeFile: &arrapi.EpisodeFile{Path: "/media/tv/S01E01.mkv"}},
		{SeasonNumber: 1, EpisodeNumber: 2}, // no file
	}}
	r := newResolver(&fakeStore{}, sonarr, radarr)

	tests := []struct {
		name     string
		ref      *resolve.MediaRef
		wantPath string
		wantErr  error
	}{
		{"movie", &resolve.MediaRef{MediaType: api.MediaTypeMovie, MediaID: 42}, "/media/movies/Inception.mkv", nil},
		{"episode", &resolve.MediaRef{MediaType: api.MediaTypeEpisode, MediaID: 7, Season: 1, Episode: 1}, "/media/tv/S01E01.mkv", nil},
		{"episode without file", &resolve.MediaRef{MediaType: api.MediaTypeEpisode, MediaID: 7, Season: 1, Episode: 2}, "", resolve.ErrMediaNotFound},
		{"episode unknown", &resolve.MediaRef{MediaType: api.MediaTypeEpisode, MediaID: 7, Season: 9, Episode: 9}, "", resolve.ErrMediaNotFound},
		{"zero arr id", &resolve.MediaRef{MediaType: api.MediaTypeMovie}, "", resolve.ErrMediaNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.VideoPath(ctx, tc.ref)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantPath {
				t.Fatalf("path = %q, want %q", got, tc.wantPath)
			}
		})
	}

	t.Run("arr not configured", func(t *testing.T) {
		t.Parallel()
		bare := newResolver(&fakeStore{}, nil, nil)
		if _, err := bare.VideoPath(ctx, &resolve.MediaRef{MediaType: api.MediaTypeMovie, MediaID: 42}); !errors.Is(err, resolve.ErrMediaNotFound) {
			t.Fatalf("err = %v, want ErrMediaNotFound", err)
		}
	})

	t.Run("movie without file", func(t *testing.T) {
		t.Parallel()
		r := newResolver(&fakeStore{}, nil, &fakeRadarr{movie: arrapi.Movie{ID: 42}})
		if _, err := r.VideoPath(ctx, &resolve.MediaRef{MediaType: api.MediaTypeMovie, MediaID: 42}); !errors.Is(err, resolve.ErrMediaNotFound) {
			t.Fatalf("err = %v, want ErrMediaNotFound", err)
		}
	})

	t.Run("movie upstream error", func(t *testing.T) {
		t.Parallel()
		r := newResolver(&fakeStore{}, nil, &fakeRadarr{err: errors.New("radarr 500")})
		if _, err := r.VideoPath(ctx, &resolve.MediaRef{MediaType: api.MediaTypeMovie, MediaID: 42}); !errors.Is(err, resolve.ErrMediaNotFound) {
			t.Fatalf("err = %v, want ErrMediaNotFound", err)
		}
	})

	t.Run("episode upstream error", func(t *testing.T) {
		t.Parallel()
		r := newResolver(&fakeStore{}, &fakeSonarr{err: errors.New("sonarr 500")}, nil)
		if _, err := r.VideoPath(ctx, &resolve.MediaRef{MediaType: api.MediaTypeEpisode, MediaID: 7, Season: 1, Episode: 1}); !errors.Is(err, resolve.ErrMediaNotFound) {
			t.Fatalf("err = %v, want ErrMediaNotFound", err)
		}
	})

	t.Run("episode sonarr not configured", func(t *testing.T) {
		t.Parallel()
		bare := newResolver(&fakeStore{}, nil, nil)
		if _, err := bare.VideoPath(ctx, &resolve.MediaRef{MediaType: api.MediaTypeEpisode, MediaID: 7, Season: 1, Episode: 1}); !errors.Is(err, resolve.ErrMediaNotFound) {
			t.Fatalf("err = %v, want ErrMediaNotFound", err)
		}
	})
}

// singleSnapshotState returns a StateFunc whose FIRST call yields a fully
// working state and whose every LATER call yields a poisoned generation
// (deny-all validator, no arr clients). A resolution that fetches State more
// than once therefore fails twice over: the call counter trips the explicit
// assertion, and the poisoned second generation breaks validation — exactly
// the hot-reload torn-snapshot bug (lookup against generation N, containment
// validation against generation N+1) the single-capture contract prevents.
func singleSnapshotState(sonarr *fakeSonarr, radarr *fakeRadarr) (fn func() *resolve.State, calls *int) {
	n := 0
	good := &resolve.State{Cfg: okValidator{}}
	if sonarr != nil {
		good.Sonarr = sonarr
	}
	if radarr != nil {
		good.Radarr = radarr
	}
	return func() *resolve.State {
		n++
		if n == 1 {
			return good
		}
		return &resolve.State{Cfg: denyValidator{}}
	}, &n
}

// TestResolver_singleStateSnapshot pins CQ-008: every public resolution call
// captures State exactly once and threads that snapshot through lookup and
// containment validation.
func TestResolver_singleStateSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rows := []api.SubtitleEntry{
		extRow("tmdb-1271", "fr", "standard", "/media/movie.fr.srt", ""),
		extRow("tmdb-1271", "de", "standard", "/media/movie.de.srt", "/media/movie.mkv"),
	}

	tests := []struct {
		resolve func(t *testing.T, r *resolve.Resolver)
		sonarr  *fakeSonarr
		radarr  *fakeRadarr
		name    string
	}{
		{
			name: "SubtitleRow",
			resolve: func(t *testing.T, r *resolve.Resolver) {
				t.Helper()
				if _, err := r.SubtitleRow(ctx, movieRef("fr", "standard", 0)); err != nil {
					t.Fatalf("SubtitleRow() unexpected error: %v", err)
				}
			},
		},
		{
			name: "SubtitlePath",
			resolve: func(t *testing.T, r *resolve.Resolver) {
				t.Helper()
				if _, err := r.SubtitlePath(ctx, movieRef("fr", "standard", 0)); err != nil {
					t.Fatalf("SubtitlePath() unexpected error: %v", err)
				}
			},
		},
		// VideoPathForFile validates the subtitle row AND the sibling row's
		// video path: two validations, still one snapshot.
		{
			name: "VideoPathForFile",
			resolve: func(t *testing.T, r *resolve.Resolver) {
				t.Helper()
				if _, err := r.VideoPathForFile(ctx, movieRef("fr", "standard", 0)); err != nil {
					t.Fatalf("VideoPathForFile() unexpected error: %v", err)
				}
			},
		},
		{
			name: "VideoPath movie",
			radarr: &fakeRadarr{movie: arrapi.Movie{
				ID: 42, MovieFile: &arrapi.MovieFile{Path: "/media/movies/Inception.mkv"},
			}},
			resolve: func(t *testing.T, r *resolve.Resolver) {
				t.Helper()
				ref := &resolve.MediaRef{MediaType: api.MediaTypeMovie, MediaID: 42}
				if _, err := r.VideoPath(ctx, ref); err != nil {
					t.Fatalf("VideoPath() unexpected error: %v", err)
				}
			},
		},
		{
			name: "VideoPath episode",
			sonarr: &fakeSonarr{episodes: []arrapi.Episode{
				{SeasonNumber: 1, EpisodeNumber: 1, EpisodeFile: &arrapi.EpisodeFile{Path: "/media/tv/S01E01.mkv"}},
			}},
			resolve: func(t *testing.T, r *resolve.Resolver) {
				t.Helper()
				ref := &resolve.MediaRef{MediaType: api.MediaTypeEpisode, MediaID: 7, Season: 1, Episode: 1}
				if _, err := r.VideoPath(ctx, ref); err != nil {
					t.Fatalf("VideoPath() unexpected error: %v", err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stateFn, calls := singleSnapshotState(tc.sonarr, tc.radarr)
			r := &resolve.Resolver{Store: &fakeStore{rows: rows}, State: stateFn}
			tc.resolve(t, r)
			if *calls != 1 {
				t.Errorf("State fetched %d times within one resolution, want exactly 1", *calls)
			}
		})
	}
}

// TestFileRefFromQuery pins the query-parameter parsing incl. defaults.
func TestFileRefFromQuery(t *testing.T) {
	t.Parallel()
	q := url.Values{
		"media_type": {"movie"}, "media_id": {"tmdb-1"}, "language": {"fr"},
	}
	ref, err := resolve.FileRefFromQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Variant != string(api.VariantStandard) || ref.Source != string(api.SourceExternal) || ref.Ordinal != 0 {
		t.Fatalf("defaults not applied: %+v", ref)
	}

	q.Set("ordinal", "3")
	q.Set("variant", "forced")
	ref, err = resolve.FileRefFromQuery(q)
	if err != nil || ref.Ordinal != 3 || ref.Variant != "forced" {
		t.Fatalf("explicit values not parsed: %+v err %v", ref, err)
	}

	q.Set("ordinal", "-1")
	if _, err := resolve.FileRefFromQuery(q); err == nil {
		t.Fatal("negative ordinal accepted")
	}
	q.Set("ordinal", "x")
	if _, err := resolve.FileRefFromQuery(q); err == nil {
		t.Fatal("non-numeric ordinal accepted")
	}
	if _, err := resolve.FileRefFromQuery(url.Values{}); err == nil {
		t.Fatal("empty query accepted")
	}
}

// TestMediaRefFromQuery pins the MediaRef query parsing.
func TestMediaRefFromQuery(t *testing.T) {
	t.Parallel()
	ref, err := resolve.MediaRefFromQuery(url.Values{
		"media_type": {"episode"}, "media_id": {"7"}, "season": {"1"}, "episode": {"5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.MediaID != 7 || ref.Season != 1 || ref.Episode != 5 {
		t.Fatalf("parsed %+v", ref)
	}
	if _, err := resolve.MediaRefFromQuery(url.Values{"media_type": {"episode"}, "media_id": {"7"}}); err == nil {
		t.Fatal("episode without season/episode accepted")
	}
	if _, err := resolve.MediaRefFromQuery(url.Values{"media_type": {"movie"}, "media_id": {"abc"}}); err == nil {
		t.Fatal("non-numeric media_id accepted")
	}
}
