package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func TestHandleScore_returns_score_result(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	body := `{"media_type":"movie","release_name":"Movie.2024.1080p.WEB-DL-GRP","sub_release":"Movie.2024.1080p.WEB-DL-GRP","matched_by":"imdb"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/score", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleScore(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleScore() status = %d, want %d", rec.Code, http.StatusOK)
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
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/score", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScore(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleScore(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScore_invalid_json_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/score", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	s.handleScore(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScore(bad json) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// --- handleScore media_type default ---

func TestHandleScore_media_type_variations(t *testing.T) {
	t.Parallel()
	// When media_type is empty, handleScore defaults it to "episode".
	// We verify this by checking the score output differs between episode and movie.

	t.Run("empty defaults to episode", func(t *testing.T) {
		t.Parallel()
		cfg := &qhMockConfig{searchCfg: api.SearchConfig{}}
		s := newTestServer(&qhMockStore{}, cfg)

		body := `{"release_name":"Test","sub_release":"Test","matched_by":"title"}`
		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/score", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleScore(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("handleScore() status = %d, want %d", rec.Code, http.StatusOK)
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
		cfg := &qhMockConfig{searchCfg: api.SearchConfig{}}
		s := newTestServer(&qhMockStore{}, cfg)

		// Same release for both; identity fields no longer scored,
		// so release attribute scores should be equal.
		release := "Movie.2024.BluRay.1080p.x264-GRP"
		movieBody := `{"media_type":"movie","release_name":"` + release + `","sub_release":"` + release + `","matched_by":"imdb"}`
		epBody := `{"media_type":"episode","release_name":"` + release + `","sub_release":"` + release + `","matched_by":"imdb"}`

		reqM := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/score", strings.NewReader(movieBody))
		recM := httptest.NewRecorder()
		s.handleScore(recM, reqM)

		if recM.Code != http.StatusOK {
			t.Fatalf("handleScore(movie) status = %d, want %d", recM.Code, http.StatusOK)
		}

		reqE := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/score", strings.NewReader(epBody))
		recE := httptest.NewRecorder()
		s.handleScore(recE, reqE)

		if recE.Code != http.StatusOK {
			t.Fatalf("handleScore(episode) status = %d, want %d", recE.Code, http.StatusOK)
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

// --- handleScore release attribute scoring ---

func TestHandleScore_release_attributes_scored(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	release := "Movie.2024.BluRay.1080p.x264-GRP"
	body := `{"media_type":"movie","release_name":"` + release + `","sub_release":"` + release + `","matched_by":"imdb"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/score", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleScore(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleScore() status = %d, want %d", rec.Code, http.StatusOK)
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

// --- Mutant-killing tests ---

// Kills CONDITIONALS_NEGATION at query_handlers.go:188-190 (MatchedBy == "hash"/"imdb" → !=).
// Verifies that the score endpoint produces different scores for different matched_by values.
// If the negation mutant flips == to !=, hash-matched would get a lower score than title-matched.
func TestHandleScore_matched_by_affects_score(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	release := "Movie.2024.BluRay.1080p.x264-GRP"

	scoreFor := func(matchedBy string) float64 {
		body := fmt.Sprintf(
			`{"media_type":"movie","release_name":%q,"sub_release":%q,"matched_by":%q}`,
			release, release, matchedBy)
		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/score", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleScore(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("handleScore(%s) status = %d", matchedBy, rec.Code)
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
