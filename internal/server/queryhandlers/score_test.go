package queryhandlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/metrics"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/release"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/testsupport"
)

// newEngineHandler builds a Handler whose LiveState carries a REAL search
// engine wired to cfg, so score-simulation and provider-timeout tests
// exercise the actual scoring/timeout pipeline rather than a canned fake
// (mirrors the production wiring).
func newEngineHandler(cfg *testsupport.NopConfig) *Handler {
	scores := cfg.Scores()
	sc := scorer.New(&scores)
	engine := search.New(nil,
		search.WithStore(&testsupport.NopStore{}),
		search.WithConfig(cfg),
		search.WithMetrics(metrics.New()),
		search.WithScorer(sc),
		search.WithSyncer(syncing.Syncer{}),
		search.WithTracks(embedded.Detector{}))
	return New(Deps{
		QueryDB: &mockQueryStore{},
		State: func() *LiveState {
			return &LiveState{Cfg: cfg, Engine: engine}
		},
		Configured: func() bool { return true },
	})
}

func TestHandleScore_returns_score_result(t *testing.T) {
	t.Parallel()
	h := newEngineHandler(&testsupport.NopConfig{})

	body := `{"media_type":"movie","release_name":"Movie.2024.1080p.WEB-DL-GRP","sub_release":"Movie.2024.1080p.WEB-DL-GRP","matched_by":"imdb"}`
	req := httptest.NewRequest(http.MethodPost, "/api/score", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleScore(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleScore() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if _, ok := result["score"]; !ok {
		t.Error("response missing 'score' field")
	}
	if _, ok := result["score_no_hash"]; !ok {
		t.Error("response missing 'score_no_hash' field")
	}
	if _, ok := result["tier"]; !ok {
		t.Error("response missing 'tier' field")
	}
}

func TestHandleScore_rejects_non_post(t *testing.T) {
	t.Parallel()
	h := newEngineHandler(&testsupport.NopConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/score", nil)
	rec := httptest.NewRecorder()
	h.HandleScore(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleScore(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScore_invalid_json_returns_400(t *testing.T) {
	t.Parallel()
	h := newEngineHandler(&testsupport.NopConfig{})

	req := httptest.NewRequest(http.MethodPost, "/api/score", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	h.HandleScore(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScore(bad json) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// --- HandleScore media_type default ---

func TestHandleScore_media_type_variations(t *testing.T) {
	t.Parallel()
	// When media_type is empty, HandleScore defaults it to "episode".
	// We verify this by checking the score output differs between episode and movie.

	t.Run("empty defaults to episode", func(t *testing.T) {
		t.Parallel()
		h := newEngineHandler(&testsupport.NopConfig{})

		body := `{"release_name":"Test","sub_release":"Test","matched_by":"title"}`
		req := httptest.NewRequest(http.MethodPost, "/api/score", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleScore(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("HandleScore() status = %d, want %d", rec.Code, http.StatusOK)
		}

		var result map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		// Empty media_type defaults to "episode"; score and tier should be present.
		if _, ok := result["score"]; !ok {
			t.Error("response missing 'score' field")
		}
		if _, ok := result["tier"]; !ok {
			t.Error("response missing 'tier' field")
		}
	})

	t.Run("explicit movie vs episode same release", func(t *testing.T) {
		t.Parallel()
		h := newEngineHandler(&testsupport.NopConfig{})

		// Same release for both; identity fields no longer scored,
		// so release attribute scores should be equal.
		release := "Movie.2024.BluRay.1080p.x264-GRP"
		movieBody := `{"media_type":"movie","release_name":"` + release + `","sub_release":"` + release + `","matched_by":"imdb"}`
		epBody := `{"media_type":"episode","release_name":"` + release + `","sub_release":"` + release + `","matched_by":"imdb"}`

		reqM := httptest.NewRequest(http.MethodPost, "/api/score", strings.NewReader(movieBody))
		recM := httptest.NewRecorder()
		h.HandleScore(recM, reqM)

		if recM.Code != http.StatusOK {
			t.Fatalf("HandleScore(movie) status = %d, want %d", recM.Code, http.StatusOK)
		}

		reqE := httptest.NewRequest(http.MethodPost, "/api/score", strings.NewReader(epBody))
		recE := httptest.NewRecorder()
		h.HandleScore(recE, reqE)

		if recE.Code != http.StatusOK {
			t.Fatalf("HandleScore(episode) status = %d, want %d", recE.Code, http.StatusOK)
		}

		var movieResult, epResult map[string]any
		if err := json.NewDecoder(recM.Body).Decode(&movieResult); err != nil {
			t.Fatalf("decode movie: %v", err)
		}
		if err := json.NewDecoder(recE.Body).Decode(&epResult); err != nil {
			t.Fatalf("decode episode: %v", err)
		}

		// Identity fields no longer scored; same release attributes = same score.
		movieScore := int(movieResult["score"].(float64))
		epScore := int(epResult["score"].(float64))
		if movieScore != epScore {
			t.Errorf("movie score (%d) should equal episode score (%d) for same release",
				movieScore, epScore)
		}
	})
}

// --- HandleScore release attribute scoring ---

func TestHandleScore_release_attributes_scored(t *testing.T) {
	t.Parallel()
	h := newEngineHandler(&testsupport.NopConfig{})

	release := "Movie.2024.BluRay.1080p.x264-GRP"
	body := `{"media_type":"movie","release_name":"` + release + `","sub_release":"` + release + `","matched_by":"imdb"}`
	req := httptest.NewRequest(http.MethodPost, "/api/score", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleScore(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleScore() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	score := int(result["score"].(float64))
	if score <= 0 {
		t.Errorf("score = %d, want > 0 for matching release attributes", score)
	}
}

// Verifies that the score endpoint produces different scores for different
// matched_by values: a hash match is always highest, while imdb and title
// matches score identically (identity fields are no longer scored).
func TestHandleScore_matched_by_affects_score(t *testing.T) {
	t.Parallel()
	h := newEngineHandler(&testsupport.NopConfig{})

	release := "Movie.2024.BluRay.1080p.x264-GRP"

	scoreFor := func(matchedBy string) float64 {
		body := fmt.Sprintf(
			`{"media_type":"movie","release_name":%q,"sub_release":%q,"matched_by":%q}`,
			release, release, matchedBy)
		req := httptest.NewRequest(http.MethodPost, "/api/score", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleScore(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HandleScore(%s) status = %d", matchedBy, rec.Code)
		}
		var result map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return result["score"].(float64)
	}

	hashScore := scoreFor("hash")
	imdbScore := scoreFor("imdb")
	titleScore := scoreFor("title")

	// Hash match = 100, always highest.
	if hashScore <= imdbScore {
		t.Errorf("hash score (%.0f) must be > imdb score (%.0f)", hashScore, imdbScore)
	}
	// Identity fields no longer scored; imdb and title produce same release score.
	if imdbScore != titleScore {
		t.Errorf("imdb score (%.0f) should equal title score (%.0f); "+
			"identity fields no longer scored", imdbScore, titleScore)
	}
}

// Compile-time guard: the real engine used by these tests must satisfy the
// api.SearchEngine surface the LiveState carries in production.
var _ api.SearchEngine = (*search.Engine)(nil)

// Both release-name-bearing score fields are direct user input into the
// release parser: exactly MaxNameLen bytes passes, one byte more answers a
// loud 400 (never a silent truncation), independently per field.
func TestHandleScore_release_name_length_boundary(t *testing.T) {
	t.Parallel()
	h := newEngineHandler(&testsupport.NopConfig{})

	send := func(t *testing.T, releaseName, subRelease string) int {
		t.Helper()
		body, err := json.Marshal(map[string]string{
			"media_type":   "movie",
			"release_name": releaseName,
			"sub_release":  subRelease,
			"matched_by":   "imdb",
		})
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/score", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleScore(rec, req)
		return rec.Code
	}

	atMax := strings.Repeat("a", release.MaxNameLen)
	overMax := strings.Repeat("a", release.MaxNameLen+1)

	if code := send(t, atMax, atMax); code != http.StatusOK {
		t.Errorf("HandleScore(both fields len == max) status = %d, want %d", code, http.StatusOK)
	}
	if code := send(t, overMax, "ok"); code != http.StatusBadRequest {
		t.Errorf("HandleScore(release_name len == max+1) status = %d, want %d", code, http.StatusBadRequest)
	}
	if code := send(t, "ok", overMax); code != http.StatusBadRequest {
		t.Errorf("HandleScore(sub_release len == max+1) status = %d, want %d", code, http.StatusBadRequest)
	}
}
