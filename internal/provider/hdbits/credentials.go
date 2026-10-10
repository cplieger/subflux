package hdbits

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/subflux/internal/httpwire"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/subflux"
)

// Compile-time check on the CredentialChecker opt-in: discovered by type
// assertion in provider.Registry.CheckCredentials, so nothing else would catch
// a rename.
var _ provider.CredentialChecker = (*source)(nil)

// testURL is HDBits' own credential-validation endpoint: it takes the
// username/passkey pair and nothing else, so no torrent or subtitle lookup
// happens. https://hdbits.org/wiki/API
const testURL = "https://hdbits.org/api/test"

// HDBits answers HTTP 200 for every outcome and puts the verdict in a `status`
// field, so these are the two codes that mean the pair was refused. The rest of
// the enum (failure, ssl required, malformed json, missing or invalid
// parameters) describes the request, not the credentials.
const (
	statusSuccess         = 0
	statusAuthDataMissing = 4
	statusAuthFailed      = 5
)

// apiStatusError classifies one API verdict: nil for success, *subflux.AuthError
// for a refused username and passkey, a plain error for anything else. message
// is upstream text the caller has already redacted.
func apiStatusError(status int, message string) error {
	switch status {
	case statusSuccess:
		return nil
	case statusAuthDataMissing, statusAuthFailed:
		return &subflux.AuthError{
			Msg: fmt.Sprintf("HDBits refused the username and passkey (status %d: %s)", status, message),
		}
	default:
		return fmt.Errorf("HDBits answered status %d: %s", status, message)
	}
}

// CheckCredentials reports whether HDBits accepts the configured username and
// passkey. A refusal is *subflux.AuthError; every other failure means the check
// did not complete.
func (p *source) CheckCredentials(ctx context.Context) error {
	body, err := json.Marshal(map[string]string{
		settingUsername: p.username,
		settingPasskey:  p.passkey,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, testURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(httpwire.HeaderContentType, httpwire.ContentTypeJSON)

	resp, err := p.client.Do(req)
	if err != nil {
		return httpx.RedactTransportError(err, "hdbits credential check", httpx.Secret(p.passkey))
	}
	defer resp.Body.Close()
	if statusErr := httpwire.CheckHTTPStatus(resp); statusErr != nil {
		return httpx.RedactSecret(statusErr, p.passkey)
	}

	var result struct {
		Message string `json:"message"`
		Status  int    `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, httpwire.MaxErrorBodyBytes)).Decode(&result); err != nil {
		return httpx.RedactSecret(fmt.Errorf("decode credential check: %w", err), p.passkey)
	}
	return apiStatusError(result.Status, p.redact(result.Message))
}
