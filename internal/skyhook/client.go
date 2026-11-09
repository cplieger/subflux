// Package skyhook reads SkyHook, Sonarr's metadata service, for a series'
// episode list in one of its orders, the lists Sonarr builds its own from. A
// request reaches only https://skyhook.sonarr.tv, on a public address.
package skyhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/ssrf"
)

const (
	origin      = "https://skyhook.sonarr.tv"
	maxAttempts = 3
	maxTVDBID   = 1<<31 - 1
	maxOrderLen = 32
)

// transportOptions extend the ssrf transport New builds; tests add a resolver.
var transportOptions []ssrf.TransportOption

var errAbsent = errors.New("skyhook lists no such series")

// Client reads SkyHook; it is safe for concurrent use.
type Client struct {
	http *http.Client
	log  *slog.Logger
}

// New copies base, nil meaning a zero client, onto ssrf's transport, which
// dials only public addresses on port 443, with an exact-origin redirect
// policy; a nil log is slog.Default.
func New(base *http.Client, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	c := &http.Client{}
	if base != nil {
		*c = *base
	}
	c.Transport = ssrf.SafeTransport(append(slices.Clone(transportOptions), ssrf.WithLogger(log))...)
	c.CheckRedirect = httpclient.RedirectPolicy(httpclient.WithSameOrigin(true))
	return &Client{http: c, log: log}
}

// Show reads GET /v2/tvdb/shows/{lang}/{tvdbID}/{order}, the request Sonarr 5
// makes, in at most 3 attempts inside ctx's deadline. tvdbID is 1 to 2^31-1,
// lang three ASCII lowercase letters and order 1 to 32 bytes of [a-z0-9]; any
// other is an error with nothing sent. A 404 answers (Show{}, false, nil). A
// 408, 429, 5xx or transport failure, after the last attempt, is an
// httpclient.IsTransient error, and no other failure is. An error's text is one
// line naming the series and order, never the answer's body; the caller logs
// the failure, which Show logs only at DEBUG.
func (c *Client) Show(ctx context.Context, tvdbID int, lang, order string) (Show, bool, error) {
	if err := checkSegments(tvdbID, lang, order); err != nil {
		return Show{}, false, err
	}
	u := origin + "/v2/tvdb/shows/" + lang + "/" + strconv.Itoa(tvdbID) + "/" + order
	body, err := httpclient.Do(ctx, func(ctx context.Context) ([]byte, error) { return c.get(ctx, u) },
		httpclient.WithMaxAttempts(maxAttempts), httpclient.WithLogger(c.log), httpclient.WithLabel("skyhook"),
		httpclient.WithExhaustedLevel(slog.LevelDebug))
	switch {
	case errors.Is(err, errAbsent):
		return Show{}, false, nil
	case err != nil:
		return Show{}, false, showError(tvdbID, lang, order, err)
	}
	s, err := decode(body)
	if err != nil {
		return Show{}, false, showError(tvdbID, lang, order, err)
	}
	return s, true, nil
}

func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req) // #nosec G704 -- u is the constant origin plus segments checkSegments accepted
	if err != nil {
		return nil, httpclient.LogSafeError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return httpclient.ReadLimitedBody(resp.Body, maxBody)
	}
	httpclient.Drain(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, errAbsent
	}
	return nil, &statusError{code: resp.StatusCode, retryAfter: httpclient.ParseRetryAfter(resp.Header.Get("Retry-After"))}
}

// statusError is an answer other than 200 or 404; httpclient.Do retries one
// httpclient.IsRetryableStatus holds for, after its capped Retry-After when it
// sent one.
type statusError struct {
	code       int
	retryAfter time.Duration
}

func (e *statusError) Error() string { return "HTTP " + strconv.Itoa(e.code) }

func (e *statusError) IsTransient() bool             { return httpclient.IsRetryableStatus(e.code) }
func (e *statusError) RetryAfterHint() time.Duration { return e.retryAfter }

func showError(tvdbID int, lang, order string, err error) error {
	return fmt.Errorf("skyhook: series %d, order %s in %s: %w", tvdbID, order, lang, err)
}

func checkSegments(tvdbID int, lang, order string) error {
	switch {
	case tvdbID < 1 || tvdbID > maxTVDBID:
		return fmt.Errorf("skyhook: series id %d is outside 1 to %d", tvdbID, maxTVDBID)
	case len(lang) != 3 || !segmentOK(lang, false):
		return fmt.Errorf("skyhook: language %q is not three lowercase letters", lang)
	case order == "" || len(order) > maxOrderLen || !segmentOK(order, true):
		return fmt.Errorf("skyhook: order %q is not 1 to %d bytes of [a-z0-9]", order, maxOrderLen)
	}
	return nil
}

// segmentOK reports whether every byte of s is a to z, or 0 to 9 when digits.
func segmentOK(s string, digits bool) bool {
	for i := range len(s) {
		b := s[i]
		if (b < 'a' || b > 'z') && (!digits || b < '0' || b > '9') {
			return false
		}
	}
	return true
}
