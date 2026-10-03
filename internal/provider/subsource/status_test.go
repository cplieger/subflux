package subsource

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/cache"
	"github.com/cplieger/subflux/internal/subflux"
)

const statusAPIKey = "placeholder-subsource-key"

// pathProvider answers the title search and the subtitle list with their own
// canned 200 bodies.
func pathProvider(searchBody, subtitlesBody string) *Provider {
	return &Provider{
		apiKey:     statusAPIKey,
		titleCache: cache.New[int](0),
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := searchBody
			if strings.HasSuffix(r.URL.Path, "/subtitles") {
				body = subtitlesBody
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})},
	}
}

const titleFound = `{"data":[{"title":"Unforgotten","releaseYear":2015,"movieId":42}]}`

func movieRequest() *subflux.SearchRequest {
	return &subflux.SearchRequest{
		MediaType: subflux.MediaTypeEpisode, ImdbID: "tt4396196", Title: "Unforgotten",
		Year: 2015, Season: 1, Episode: 1, Languages: []string{"en"},
	}
}

func TestSearch_classifies_a_success_false_subtitle_answer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		wantAuth bool
		wantErr  bool
	}{
		{name: "key refused", body: `{"success":false,"error":"Invalid API key ` + statusAPIKey + `"}`, wantAuth: true, wantErr: true},
		{name: "not found", body: `{"success":false,"error":"No subtitles found"}`},
		{name: "other failure", body: `{"success":false,"error":"internal error"}`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			subs, err := pathProvider(titleFound, tc.body).Search(t.Context(), movieRequest())
			if (err != nil) != tc.wantErr {
				t.Fatalf("Search() = (%v, %v), want error %v", subs, err, tc.wantErr)
			}
			if _, ok := errors.AsType[*subflux.AuthError](err); ok != tc.wantAuth {
				t.Errorf("Search() error = %v, want AuthError %v", err, tc.wantAuth)
			}
			if err != nil && strings.Contains(err.Error(), statusAPIKey) {
				t.Errorf("Search() error leaked the API key: %q", err.Error())
			}
			if len(subs) != 0 {
				t.Errorf("Search() results = %v, want none", subs)
			}
		})
	}
}

func TestSearch_reports_a_refused_key_on_the_title_search(t *testing.T) {
	t.Parallel()
	p := pathProvider(`{"success":false,"error":"Unauthorized"}`, `{"success":true,"data":[]}`)
	_, err := p.Search(t.Context(), movieRequest())
	if _, ok := errors.AsType[*subflux.AuthError](err); !ok {
		t.Errorf("Search() = %v, want *subflux.AuthError", err)
	}
}

func TestDoSearch_treats_a_missing_success_field_as_an_answer(t *testing.T) {
	t.Parallel()
	got, err := pathProvider(titleFound, "").doSearch(t.Context(), nil)
	if err != nil || len(got) != 1 {
		t.Errorf("doSearch() = (%v, %v), want the one title and no error", got, err)
	}
}
