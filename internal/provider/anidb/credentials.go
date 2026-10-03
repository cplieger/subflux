package anidb

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/subflux/internal/httpwire"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/xmlx"
)

// errClientRejected is the AniDB error code for a client string the API does
// not know. Measured 2026-09-19 against the live API: an unregistered client
// answers HTTP 200 with `<error code="302">client version missing or invalid</error>`
// whatever clientver is sent, because AniDB has no registered version to
// compare against. Every other code describes the request or the account state
// (a ban, an unknown anime) rather than the key.
const errClientRejected = "302"

// CheckClientKey reports whether AniDB accepts the configured client key.
//
// `request=main` is the cheapest call behind AniDB's client gate: no anime id, so
// nothing is looked up. A refused key is *subflux.AuthError; a ban or any other
// error envelope reports as an incomplete check, because waiting is a different
// remedy from fixing a field. An absent key is a refusal whose text says so —
// the key is optional and AnimeTosho searches by title without one.
func (m *Mapper) CheckClientKey(ctx context.Context) error {
	if m.clientKey == "" {
		return &subflux.AuthError{
			Msg: "no AniDB client key is set; AnimeTosho searches by title without one",
		}
	}
	if err := m.rateLimitAniDB(ctx); err != nil {
		return err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, episodesFetchTimeout)
	defer cancel()

	reqURL := fmt.Sprintf("%s?request=main&client=%s&clientver=%d&protover=1",
		apiURL, url.QueryEscape(m.clientKey), clientVer)
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return httpx.RedactTransportError(err, "anidb credential check", httpx.Secret(m.clientKey))
	}
	defer resp.Body.Close()
	if statusErr := httpwire.CheckHTTPStatus(resp); statusErr != nil {
		return statusErr
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httpwire.MaxJSONResponseBytes))
	if err != nil {
		return err
	}
	data, err = decompressIfGzipped(data, httpwire.MaxJSONResponseBytes)
	if err != nil {
		return fmt.Errorf("anidb: credential check decompress: %w", err)
	}
	if err := xmlx.Preflight(data, episodeLimits); err != nil {
		return fmt.Errorf("anidb: credential check outside decode bounds: %w", err)
	}

	var errCheck anidbError
	if err := xml.Unmarshal(data, &errCheck); err != nil || errCheck.Message == "" {
		return nil
	}
	msg := m.upstreamText(errCheck.Message)
	if errCheck.Code == errClientRejected {
		return &subflux.AuthError{Msg: "AniDB refused the client key: " + msg}
	}
	return fmt.Errorf("anidb API error: %s", msg)
}
