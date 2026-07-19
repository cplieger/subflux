package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cplieger/ssrf/v3"
)

// posterMaxRedirects caps the complete redirect chain across both the trusted
// arr leg and the public CDN leg.
const posterMaxRedirects = 5

// posterClient fetches a poster across two distinct trust zones. The arr
// client may reach the operator-configured private Sonarr/Radarr origin. After
// the first cross-origin redirect, the public client validates every URL and
// dials exclusively through ssrf.SafeTransport. That transition is
// irreversible, and the fresh public request carries none of the arr request's
// headers (especially X-Api-Key).
type posterClient struct {
	arr    *http.Client
	public *http.Client
}

type posterRedirectState struct {
	hops int
}

type posterRedirectStateKey struct{}

// newPosterClient builds the client used by the poster-image proxy. The arr
// client follows only strict same-origin redirects. Its first cross-origin 3xx
// is returned to posterClient.Do, which refetches through the public client.
func newPosterClient() *posterClient {
	return &posterClient{
		arr: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: posterArrRedirectPolicy,
		},
		public: &http.Client{
			Transport:     ssrf.SafeTransport(),
			Timeout:       30 * time.Second,
			CheckRedirect: newPosterPublicRedirectPolicy(),
		},
	}
}

// Do performs the initial trusted-arr request. If arr redirects to another
// origin, Do validates that URL, closes the 3xx response, and starts a new
// header-free request through the public-only client. Subsequent redirects can
// never return to the private transport.
func (c *posterClient) Do(req *http.Request) (*http.Response, error) {
	state := &posterRedirectState{}
	ctx := context.WithValue(req.Context(), posterRedirectStateKey{}, state)
	resp, err := c.arr.Do(req.Clone(ctx))
	if err != nil {
		return nil, err
	}
	if !isPosterRedirect(resp.StatusCode) || resp.Header.Get("Location") == "" {
		return resp, nil
	}

	nextURL, err := resp.Location()
	if err != nil {
		closePosterRedirect(resp.Body)
		return nil, fmt.Errorf("poster proxy: invalid redirect location: %w", err)
	}
	if validateErr := ssrf.ValidateURL(nextURL.String()); validateErr != nil {
		closePosterRedirect(resp.Body)
		return nil, fmt.Errorf("poster proxy: unsafe public redirect: %w", validateErr)
	}

	publicReq, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL.String(), http.NoBody)
	if err != nil {
		closePosterRedirect(resp.Body)
		return nil, fmt.Errorf("poster proxy: build public request: %w", err)
	}
	closePosterRedirect(resp.Body)
	return c.public.Do(publicReq)
}

// posterArrRedirectPolicy keeps authenticated requests on the exact configured
// origin. http.ErrUseLastResponse stops before a cross-origin request is sent,
// leaving posterClient.Do to make the credential-free public refetch.
func posterArrRedirectPolicy(req *http.Request, via []*http.Request) error {
	if err := countPosterRedirect(via); err != nil {
		return err
	}
	if samePosterOrigin(via[0].URL, req.URL) {
		return nil
	}
	return http.ErrUseLastResponse
}

func newPosterPublicRedirectPolicy() func(*http.Request, []*http.Request) error {
	safeRedirect := ssrf.SafeRedirectPolicy(nil)
	return func(req *http.Request, via []*http.Request) error {
		if err := countPosterRedirect(via); err != nil {
			return err
		}
		return safeRedirect(req, via)
	}
}

func countPosterRedirect(via []*http.Request) error {
	if len(via) == 0 {
		return errors.New("poster proxy: redirect missing request history")
	}
	state, ok := via[0].Context().Value(posterRedirectStateKey{}).(*posterRedirectState)
	if !ok {
		return errors.New("poster proxy: redirect missing state")
	}
	if state.hops >= posterMaxRedirects {
		return fmt.Errorf("poster proxy: stopped after %d redirects", posterMaxRedirects)
	}
	state.hops++
	return nil
}

// samePosterOrigin compares scheme, hostname, and effective port. Matching only
// the hostname would let a redirect send the arr API key to another service on
// the same machine, and would not model an HTTP origin correctly.
func samePosterOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePosterPort(a) == effectivePosterPort(b)
}

func effectivePosterPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func isPosterRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// closePosterRedirect drains a small redirect body to permit connection reuse
// without accepting an unbounded body from an upstream redirect response.
func closePosterRedirect(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 4<<10))
	_ = body.Close()
}
