package arr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/ssrf"
	"github.com/cplieger/subflux/internal/mediasource"
)

// maxPosterBytes bounds one poster; a larger one is refused, never cut.
const maxPosterBytes = 2 << 20

// posterBudget bounds a Fetch whose context has no deadline, as arrapi's
// WithTimeout bounds the adapters' undeadlined reads.
const posterBudget = 30 * time.Second

// maxPosterHops caps the redirects of one fetch across both legs, httpclient's
// default.
const maxPosterHops = 5

var (
	errPosterTooLarge = fmt.Errorf("poster exceeds %d bytes", maxPosterBytes)
	errPosterHops     = fmt.Errorf("poster redirects more than %d times", maxPosterHops)
)

// Poster fetches a cover across two trust zones. The credentialed leg
// follows only same-origin redirects; its first cross-origin redirect is
// refetched with no header through the public leg, which reaches only
// public https addresses and never returns to the credentialed one.
type Poster struct{ arr, public *http.Client }

// NewPoster builds both legs: the credentialed one a copy of arr, nil
// meaning a zero client, and the public one over ssrf's transport with the
// same Timeout.
func NewPoster(arr *http.Client) *Poster {
	c := &http.Client{}
	if arr != nil {
		*c = *arr
	}
	return &Poster{arr: c, public: &http.Client{Transport: ssrf.SafeTransport(), Timeout: c.Timeout}}
}

// Fetch reads the whole cover at url, sending apiKey only to url's origin,
// before returning it with its content type, both legs inside ctx's deadline
// or, when it has none, 30 s. A 404 answers false; a body over
// maxPosterBytes is Protocol.
func (p *Poster) Fetch(ctx context.Context, url, apiKey string) (body io.ReadCloser, contentType string, found bool, err error) {
	ctx, cancel := httpclient.ContextWithDefaultTimeout(ctx, posterBudget)
	defer cancel()
	hops := 0
	sameOrigin := httpclient.RedirectPolicy(httpclient.WithSameOrigin(true), httpclient.WithMaxHops(maxPosterHops))
	arr := *p.arr
	arr.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		var refused *httpclient.RedirectRefusedError
		verdict := sameOrigin(req, via)
		if errors.As(verdict, &refused) && refused.Reason != httpclient.RedirectTooMany {
			return http.ErrUseLastResponse
		}
		if verdict == nil {
			hops = len(via)
		}
		return verdict
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, "", false, sourceError(mediasource.Protocol, errors.New("poster URL is not a valid URL"))
	}
	req.Header.Set("X-Api-Key", apiKey)
	resp, err := arr.Do(req) // #nosec G704 -- url is the configured arr origin's; CheckRedirect stops before any other origin
	if err != nil {
		// A message holding the key comes back redacted with no chain, so the
		// cause is classified from err itself.
		safe := Classify(httpclient.RedactTransportError(err, "poster", httpclient.Secret(apiKey)))
		safe.Cause = Classify(err).Cause
		return nil, "", false, safe
	}
	defer drain(resp)
	if !isRedirect(resp.StatusCode) {
		return readPoster(resp)
	}
	var public *http.Response
	if public, err = p.refetchPublic(ctx, resp, hops+1); err != nil {
		return nil, "", false, err
	}
	defer drain(public)
	return readPoster(public)
}

// refetchPublic refetches a credentialed leg's cross-origin redirect through the
// public leg, with no header, counting hops already followed.
func (p *Poster) refetchPublic(ctx context.Context, redirect *http.Response, hops int) (*http.Response, error) {
	loc, err := redirect.Location()
	if err != nil {
		return nil, sourceError(mediasource.Protocol, errors.New("poster redirect has no valid Location"))
	}
	if hops > maxPosterHops {
		return nil, sourceError(mediasource.Protocol, errPosterHops)
	}
	if err = ssrf.ValidateURL(loc.String()); err != nil {
		return nil, sourceError(mediasource.Protocol, fmt.Errorf("poster redirect refused: %w", err))
	}
	public := *p.public
	safe := ssrf.SafeRedirectPolicy(nil)
	public.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if hops+len(via) > maxPosterHops {
			return errPosterHops
		}
		return safe(req, via)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc.String(), http.NoBody)
	if err != nil {
		return nil, sourceError(mediasource.Protocol, errors.New("poster redirect has no valid Location"))
	}
	resp, err := public.Do(req) // #nosec G704 -- loc passed ssrf.ValidateURL and the public leg dials through ssrf.SafeTransport
	var serr *ssrf.Error
	switch {
	case errors.Is(err, errPosterHops):
		return nil, sourceError(mediasource.Protocol, errPosterHops)
	case errors.As(err, &serr):
		return nil, sourceError(mediasource.Protocol, fmt.Errorf("poster redirect refused: %w", serr))
	case err != nil:
		return nil, Classify(httpclient.LogSafeError(err))
	}
	return resp, nil
}

// readPoster reads resp's whole cover; the caller drains and closes resp.
func readPoster(resp *http.Response) (body io.ReadCloser, contentType string, found bool, err error) {
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", false, nil
	case resp.StatusCode != http.StatusOK:
		return nil, "", false, sourceError(statusCause(resp.StatusCode), fmt.Errorf("poster: HTTP %d", resp.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPosterBytes+1))
	switch {
	case err != nil:
		return nil, "", false, sourceError(mediasource.Unreachable, fmt.Errorf("poster read: %w", httpclient.LogSafeError(err)))
	case len(b) > maxPosterBytes:
		return nil, "", false, sourceError(mediasource.Protocol, errPosterTooLarge)
	}
	return io.NopCloser(bytes.NewReader(b)), resp.Header.Get("Content-Type"), true, nil
}

func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxPosterBytes))
	_ = resp.Body.Close()
}
