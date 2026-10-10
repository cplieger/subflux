package hdbits

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cplieger/subflux/internal/cache"
	"github.com/cplieger/subflux/internal/subflux"
)

const testPasskey = "placeholder-passkey-0123456789ab"

// routedProvider answers each HDBits API path with its own canned body and
// counts the subtitle lookups it serves.
func routedProvider(t *testing.T, bodies map[string]string, subtitleCalls *atomic.Int32) *source {
	t.Helper()
	cfg := defaultHDBitsConfig
	cfg.TorrentLookupDelay = 0
	return &source{
		username: "placeholder-user",
		passkey:  testPasskey,
		cfg:      cfg,
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/api/subtitles" && subtitleCalls != nil {
				subtitleCalls.Add(1)
			}
			body, ok := bodies[r.URL.Path]
			if !ok {
				t.Errorf("unexpected request to %s", r.URL.Path)
				body = `{"status":1}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})},
		torrentCache: cache.New[[]int](0),
		dlCache:      newDownloadCache(10, 1<<20),
	}
}

func episodeRequest() *subflux.SearchRequest {
	return &subflux.SearchRequest{
		MediaType: subflux.MediaTypeEpisode, TvdbID: 81189, Season: 1, Episode: 1,
		Languages: []string{"en"}, Title: "Unforgotten",
	}
}

func TestSearch_reports_a_refused_login_on_the_torrent_lookup(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"status":5,"message":"Auth failed"}`,
		`{"status":4,"message":"Auth data missing"}`,
	} {
		p := routedProvider(t, map[string]string{"/api/torrents": body}, nil)
		subs, err := p.Search(t.Context(), episodeRequest())
		if _, ok := errors.AsType[*subflux.AuthError](err); !ok {
			t.Errorf("Search() with torrents answering %s = (%v, %v), want *subflux.AuthError", body, subs, err)
		}
	}
}

func TestSearch_reports_a_refused_login_on_the_subtitle_lookup(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	p := routedProvider(t, map[string]string{
		"/api/torrents":  `{"status":0,"data":[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5},{"id":6}]}`,
		"/api/subtitles": `{"status":5,"message":"Auth failed"}`,
	}, &calls)
	p.cfg.TorrentLookupConcurrency = 1
	subs, err := p.Search(t.Context(), episodeRequest())
	if _, ok := errors.AsType[*subflux.AuthError](err); !ok {
		t.Fatalf("Search() = (%v, %v), want *subflux.AuthError", subs, err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("subtitle lookups with one lookup slot and a refused first lookup = %d, want 1", got)
	}
}

func TestSearch_skips_a_torrent_whose_lookup_failed_otherwise(t *testing.T) {
	t.Parallel()
	p := routedProvider(t, map[string]string{
		"/api/torrents":  `{"status":0,"data":[{"id":1}]}`,
		"/api/subtitles": `{"status":7,"message":"Invalid parameter"}`,
	}, nil)
	subs, err := p.Search(t.Context(), episodeRequest())
	if err != nil || len(subs) != 0 {
		t.Errorf("Search() with one failing torrent = (%v, %v), want no results and no error", subs, err)
	}
}

func TestSearch_reports_a_non_auth_verdict_on_the_torrent_lookup(t *testing.T) {
	t.Parallel()
	p := routedProvider(t, map[string]string{"/api/torrents": `{"status":1,"message":"Failure"}`}, nil)
	_, err := p.Search(t.Context(), episodeRequest())
	if err == nil {
		t.Fatal("Search() with torrents answering status 1 = nil error, want the verdict")
	}
	if _, ok := errors.AsType[*subflux.AuthError](err); ok {
		t.Errorf("Search() status 1 = %v, want a plain error, not an AuthError", err)
	}
}

func TestDownload_reports_a_json_refusal_and_does_not_cache_it(t *testing.T) {
	t.Parallel()
	p := routedProvider(t, map[string]string{
		"/getdox.php": `{"status":5,"message":"Auth failed for ` + testPasskey + `"}`,
	}, nil)
	_, err := p.Download(t.Context(), &subflux.Subtitle{ID: "123"})
	if _, ok := errors.AsType[*subflux.AuthError](err); !ok {
		t.Fatalf("Download() with a JSON refusal = %v, want *subflux.AuthError", err)
	}
	if strings.Contains(err.Error(), testPasskey) {
		t.Errorf("Download() error leaked the passkey: %q", err.Error())
	}
	if _, cached := p.dlCache.get("123"); cached {
		t.Error("the refusal body was cached as a download")
	}
}

func TestDownloadVerdict_ignores_a_subtitle_body(t *testing.T) {
	t.Parallel()
	p := &source{passkey: testPasskey}
	for _, body := range []string{
		"1\n00:00:01,000 --> 00:00:02,000\nHello\n",
		"PK\x03\x04archive",
		`{"data":"no status"}`,
		`{"status":"5"}`,
	} {
		if err := p.downloadVerdict([]byte(body)); err != nil {
			t.Errorf("downloadVerdict(%q) = %v, want nil", body, err)
		}
	}
}
