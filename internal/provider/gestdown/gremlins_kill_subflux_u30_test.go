package gestdown

import (
	"context"
	"errors"
	"testing"

	"github.com/cplieger/ssrf/v2"
	"github.com/cplieger/subflux/internal/api"
)

// gk_subflux_u30_canceledCtx returns an already-cancelled context so that any
// code path which (incorrectly, post-mutation) falls through to an HTTP call
// fails immediately and deterministically rather than attempting real network
// IO. The early-return paths under test never touch the context.
func gk_subflux_u30_canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// Kills gestdown.go:71:19 CONDITIONALS_NEGATION (req.MediaType != Episode, != -> ==).
// A movie request with a non-zero TVDB ID is skipped early by the original
// (returns nil, nil). The "==" mutant treats only episodes as skippable, so a
// movie falls through to findShow and errors on the cancelled context.
func Test_gk_subflux_u30_SearchMovieSkippedEarly(t *testing.T) {
	p, err := Factory(context.Background(), nil)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	req := &api.SearchRequest{MediaType: api.MediaTypeMovie, TvdbID: 12345, Languages: []string{"en"}}
	subs, searchErr := p.Search(gk_subflux_u30_canceledCtx(), req)
	if searchErr != nil {
		t.Errorf("Search(movie) err = %v, want nil (a movie must be skipped before any network call)", searchErr)
	}
	if subs != nil {
		t.Errorf("Search(movie) subs = %v, want nil", subs)
	}
}

// Kills gestdown.go:71:57 CONDITIONALS_NEGATION (req.TvdbID == 0, == -> !=).
// An episode with TvdbID == 0 is skipped early by the original (returns nil,
// nil). The "!=" mutant treats only non-zero IDs as the skip trigger, so
// TvdbID == 0 falls through to findShow and errors on the cancelled context.
func Test_gk_subflux_u30_SearchEpisodeZeroTvdbSkippedEarly(t *testing.T) {
	p, err := Factory(context.Background(), nil)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	req := &api.SearchRequest{MediaType: api.MediaTypeEpisode, TvdbID: 0, Languages: []string{"en"}}
	subs, searchErr := p.Search(gk_subflux_u30_canceledCtx(), req)
	if searchErr != nil {
		t.Errorf("Search(episode, tvdb=0) err = %v, want nil (must be skipped before any network call)", searchErr)
	}
	if subs != nil {
		t.Errorf("Search(episode, tvdb=0) subs = %v, want nil", subs)
	}
}

// Kills gestdown.go:159:51 CONDITIONALS_NEGATION (validate err != nil, != -> ==).
// Download validates the URL first; an invalid (http + loopback) URL makes
// ssrf.ValidateURL return an *ssrf.Error which the original wraps and returns.
// The "== nil" mutant skips the validation-failure return and proceeds to the
// HTTP request, so the returned error is no longer an *ssrf.Error.
func Test_gk_subflux_u30_DownloadRejectsInvalidURL(t *testing.T) {
	p, err := Factory(context.Background(), nil)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	_, dlErr := p.Download(gk_subflux_u30_canceledCtx(), &api.Subtitle{DownloadURL: "http://127.0.0.1/x.srt"})
	if dlErr == nil {
		t.Fatalf("Download(invalid url) err = nil, want non-nil")
	}
	var se *ssrf.Error
	if !errors.As(dlErr, &se) {
		t.Errorf("Download(invalid url) err = %v, want it to wrap *ssrf.Error (validation must run first)", dlErr)
	}
}
