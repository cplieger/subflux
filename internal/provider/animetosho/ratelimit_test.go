package animetosho

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/provider/anidb"
	"github.com/cplieger/subflux/internal/subflux"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeMapper resolves every episode to no AniDB episode id and reports a
// fixed client-key verdict.
type fakeMapper struct {
	rejected error
	answered bool
}

func (fakeMapper) Resolve(context.Context, int, int, int) *anidb.EpisodeResult {
	return &anidb.EpisodeResult{}
}
func (fakeMapper) CheckClientKey(context.Context) error { return nil }
func (f fakeMapper) ClientKeyVerdict() (bool, error)    { return f.answered, f.rejected }
func (fakeMapper) ForgetClientKeyVerdict()              {}

// forgettingMapper is a fakeMapper whose answer ForgetClientKeyVerdict drops.
type forgettingMapper struct{ fakeMapper }

func (m *forgettingMapper) ClientKeyVerdict() (bool, error) { return m.answered, m.rejected }
func (m *forgettingMapper) ForgetClientKeyVerdict()         { m.answered, m.rejected = false, nil }

// feedProvider answers the entry search with two complete entries and each
// entry's detail with detail(id).
func feedProvider(detail func(id string) (int, string), mapper episodeMapper) *source {
	return &source{
		anidbMapper: mapper,
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			status, body := http.StatusOK, `[{"title":"[Grp] Show - 01","status":"complete","id":11},{"title":"[Grp] Show - 01 v2","status":"complete","id":12}]`
			if id := r.URL.Query().Get("id"); id != "" {
				status, body = detail(id)
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})},
	}
}

const englishDetail = `{"files":[{"filename":"[Grp] Show - 01.mkv","attachments":[{"type":"subtitle","id":501,"info":{"lang":"eng"}}]}]}`

func showRequest() *subflux.SearchRequest {
	return &subflux.SearchRequest{
		MediaType: subflux.MediaTypeEpisode, Title: "Show", TvdbID: 321, Season: 1, Episode: 1,
		Languages: []string{"en"},
	}
}

func TestSearch_returns_an_entry_rate_limit(t *testing.T) {
	t.Parallel()
	p := feedProvider(func(id string) (int, string) {
		if id == "12" {
			return http.StatusTooManyRequests, ""
		}
		return http.StatusOK, englishDetail
	}, fakeMapper{})
	subs, err := p.Search(t.Context(), showRequest())
	if _, ok := errors.AsType[*subflux.RateLimitError](err); !ok {
		t.Errorf("Search() with one entry answering 429 = (%v, %v), want *subflux.RateLimitError", subs, err)
	}
}

func TestSearch_skips_an_entry_that_failed_otherwise(t *testing.T) {
	t.Parallel()
	p := feedProvider(func(id string) (int, string) {
		if id == "12" {
			return http.StatusBadGateway, ""
		}
		return http.StatusOK, englishDetail
	}, fakeMapper{})
	subs, err := p.Search(t.Context(), showRequest())
	if err != nil || len(subs) != 1 {
		t.Errorf("Search() with one entry answering 502 = (%v, %v), want the other entry's subtitle", subs, err)
	}
}

func TestSettingVerdict_names_a_refused_anidb_key_and_title_search_continues(t *testing.T) {
	t.Parallel()
	refused := &subflux.AuthError{Msg: "AniDB refused the client key: client version missing or invalid"}
	p := feedProvider(func(string) (int, string) { return http.StatusOK, englishDetail }, fakeMapper{answered: true, rejected: refused})

	setting, reason := p.SettingVerdict()
	if setting != "anidb_client_key" || !errors.Is(reason, refused) {
		t.Errorf("SettingVerdict() = (%q, %v), want (anidb_client_key, the refusal)", setting, reason)
	}
	subs, err := p.Search(t.Context(), showRequest())
	if err != nil || len(subs) != 1 {
		t.Errorf("Search() with a refused AniDB key = (%v, %v), want the title-search result", subs, err)
	}

	accepted := feedProvider(nil, fakeMapper{answered: true})
	if setting, reason := accepted.SettingVerdict(); setting != "anidb_client_key" || reason != nil {
		t.Errorf("SettingVerdict() after an accepted lookup = (%q, %v), want (anidb_client_key, nil)", setting, reason)
	}
	unasked := feedProvider(nil, fakeMapper{})
	if setting, reason := unasked.SettingVerdict(); setting != "" || reason != nil {
		t.Errorf("SettingVerdict() before AniDB answered = (%q, %v), want (\"\", nil)", setting, reason)
	}
}

func TestForgetSettingVerdict_drops_the_anidb_answer(t *testing.T) {
	t.Parallel()
	refused := &subflux.AuthError{Msg: "AniDB refused the client key: client version missing or invalid"}
	p := feedProvider(nil, &forgettingMapper{fakeMapper{answered: true, rejected: refused}})
	p.ForgetSettingVerdict()
	if setting, reason := p.SettingVerdict(); setting != "" || reason != nil {
		t.Errorf("SettingVerdict() after ForgetSettingVerdict = (%q, %v), want (\"\", nil)", setting, reason)
	}
}
