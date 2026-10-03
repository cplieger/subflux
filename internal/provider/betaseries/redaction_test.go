package betaseries

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// Not parallel: it swaps slog's default logger.
func TestSearch_an_echoed_token_reaches_no_log(t *testing.T) {
	var logs bytes.Buffer
	testsupport.SwapDefaultLogger(t, slog.New(slog.NewTextHandler(&logs, nil)))
	body := `{"errors":[{"code":2001,"text":"bad key placeholder token, placeholder\ntoken"}],"episodes":[]}`
	p := &Provider{
		token: "placeholder token",
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})},
	}
	if _, err := p.Search(t.Context(), &subflux.SearchRequest{
		MediaType: subflux.MediaTypeEpisode, TvdbID: 1, Season: 1, Episode: 1, Languages: []string{"fr"},
	}); err != nil {
		t.Fatalf("Search() = %v, want the API errors logged and no error", err)
	}
	if !strings.Contains(logs.String(), "betaseries: API returned errors") {
		t.Fatalf("Search() logged no API error line:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "placeholder") {
		t.Errorf("log carries the token:\n%s", logs.String())
	}
}
