package coveragehandlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/arrsvc"
)

// The collections are two of the five ?recovery=1-honoring endpoints (A3):
// they mark the request context for the arr-read wrapper and resolve exclude
// tags through the error-returning form, exactly like the summaries. The
// other coverage endpoints ignore the parameter.

func TestCoverageCollections_recovery_honoring(t *testing.T) {
	t.Parallel()

	t.Run("series_collection_interprets_recovery_1", func(t *testing.T) {
		t.Parallel()
		sonarr := &summarySonarrFake{series: summarySeriesFixture(), tagIDs: map[int]struct{}{1: {}}}
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageSeries(rec,
			httptest.NewRequest(http.MethodGet, "/api/coverage/series?recovery=1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if !sonarr.listMarked {
			t.Error("marked request: Series ctx carried no recovery marker")
		}
		// A marked read resolves exclude tags through the ERROR-RETURNING
		// form, so a wave failure can never fail open into an
		// empty-exclusion 200.
		if sonarr.tagsErrCalls != 1 || !sonarr.tagsMarked || sonarr.tagsCalls != 0 {
			t.Errorf("marked tag reads: err-form %d (marked %v), fail-open %d; want 1 (marked), 0",
				sonarr.tagsErrCalls, sonarr.tagsMarked, sonarr.tagsCalls)
		}
	})

	t.Run("movies_collection_interprets_recovery_1", func(t *testing.T) {
		t.Parallel()
		radarr := &summaryRadarrFake{movies: summaryMoviesFixture(), tagIDs: map[int]struct{}{2: {}}}
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), nil, radarr)
		rec := httptest.NewRecorder()
		h.HandleCoverageMovies(rec,
			httptest.NewRequest(http.MethodGet, "/api/coverage/movies?recovery=1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if !radarr.listMarked || radarr.tagsErrCalls != 1 || !radarr.tagsMarked {
			t.Errorf("marked movies collection: list marked %v, err-form calls %d (marked %v); want all marked",
				radarr.listMarked, radarr.tagsErrCalls, radarr.tagsMarked)
		}
	})

	t.Run("plain_reads_stay_unmarked_and_fail_open", func(t *testing.T) {
		t.Parallel()
		sonarr := &summarySonarrFake{series: summarySeriesFixture()}
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageSeries(rec,
			httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if sonarr.listMarked {
			t.Error("plain request: Series ctx carried a recovery marker")
		}
		if sonarr.tagsCalls != 1 || sonarr.tagsErrCalls != 0 {
			t.Errorf("plain tag reads: fail-open %d, err-form %d; want 1, 0",
				sonarr.tagsCalls, sonarr.tagsErrCalls)
		}
	})

	t.Run("only_the_literal_1_marks", func(t *testing.T) {
		t.Parallel()
		radarr := &summaryRadarrFake{movies: summaryMoviesFixture()}
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), nil, radarr)
		rec := httptest.NewRecorder()
		h.HandleCoverageMovies(rec,
			httptest.NewRequest(http.MethodGet, "/api/coverage/movies?recovery=0", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if radarr.listMarked {
			t.Error("recovery=0 marked the read; only recovery=1 may")
		}
	})

	// The sample of non-honoring endpoints: they never interpret the
	// parameter, so their store reads run on an unmarked context.
	t.Run("scan_state_ignores_recovery_1", func(t *testing.T) {
		t.Parallel()
		store := &prefixStore{}
		h := newCoverageHandler(store, summarySeriesCfg(), nil, nil)
		rec := httptest.NewRecorder()
		h.HandleScanStates(rec,
			httptest.NewRequest(http.MethodGet, "/api/coverage/scan-state?recovery=1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if len(store.scanMarked) != 1 || store.scanMarked[0] {
			t.Errorf("scan-state store reads marked = %v, want one unmarked read", store.scanMarked)
		}
	})

	t.Run("coverage_detail_ignores_recovery_1", func(t *testing.T) {
		t.Parallel()
		store := &prefixStore{}
		h := newCoverageHandler(store, summarySeriesCfg(), nil, nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageDetail(rec,
			httptest.NewRequest(http.MethodGet, "/api/coverage/series/81189?recovery=1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if len(store.marked) != 1 || store.marked[0] {
			t.Errorf("coverage detail store reads marked = %v, want one unmarked read", store.marked)
		}
	})
}

func TestCoverageCollections_sentinel_mapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		name string
		want int
	}{
		{name: "refused_maps_to_429", err: fmt.Errorf("%w: budget", arrsvc.ErrRecoveryRefused), want: http.StatusTooManyRequests},
		{name: "failed_maps_to_502", err: fmt.Errorf("%w: upstream", arrsvc.ErrRecoveryFailed), want: http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run("series_list_"+tc.name, func(t *testing.T) {
			t.Parallel()
			sonarr := &summarySonarrFake{listErr: tc.err}
			h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, nil)
			rec := httptest.NewRecorder()
			h.HandleCoverageSeries(rec,
				httptest.NewRequest(http.MethodGet, "/api/coverage/series?recovery=1", nil))
			if rec.Code != tc.want {
				t.Errorf("series collection status = %d, want %d (err %v)", rec.Code, tc.want, tc.err)
			}
		})
		t.Run("movie_list_"+tc.name, func(t *testing.T) {
			t.Parallel()
			radarr := &summaryRadarrFake{listErr: tc.err}
			h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), nil, radarr)
			rec := httptest.NewRecorder()
			h.HandleCoverageMovies(rec,
				httptest.NewRequest(http.MethodGet, "/api/coverage/movies?recovery=1", nil))
			if rec.Code != tc.want {
				t.Errorf("movies collection status = %d, want %d (err %v)", rec.Code, tc.want, tc.err)
			}
		})
		t.Run("marked_tag_leg_"+tc.name, func(t *testing.T) {
			t.Parallel()
			sonarr := &summarySonarrFake{series: summarySeriesFixture(), tagsErr: tc.err}
			h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, nil)
			rec := httptest.NewRecorder()
			h.HandleCoverageSeries(rec,
				httptest.NewRequest(http.MethodGet, "/api/coverage/series?recovery=1", nil))
			if rec.Code != tc.want {
				t.Errorf("marked tag leg status = %d, want %d (never a silent empty-exclusion 200)",
					rec.Code, tc.want)
			}
		})
	}
}
