package arr

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Classify maps an arrapi or transport error to a *SourceError, first
// matching row: a command outcome unknown, a server change, an unsupported
// API, a refused redirect, an answer too large, a status code, else
// Unreachable, so a *arrapi.ServerChangedError is Unreachable whatever status
// it unwraps. Source and Op are the Set's to fill. A decode failure reads
// Unreachable: arrapi v3.1.0 exports no decode error.
func Classify(err error) *mediasource.SourceError {
	var (
		changed     *arrapi.ServerChangedError
		unsupported *arrapi.UnsupportedAPIError
		refused     *httpx.RedirectRefusedError
		tooLarge    *arrapi.ResponseTooLargeError
		status      *arrapi.StatusError
	)
	switch {
	case errors.Is(err, arrapi.ErrCommandOutcomeUnknown):
		return sourceError(mediasource.Unreachable, fmt.Errorf("%w: %w", mediasource.ErrRescanOutcomeUnknown, err))
	case errors.As(err, &changed):
		return sourceError(mediasource.Unreachable, err)
	case errors.As(err, &unsupported) && unsupported.Reason == arrapi.ReasonNotAPI:
		return sourceError(mediasource.NotFound, err)
	case errors.As(err, &unsupported):
		return sourceError(mediasource.Protocol, err)
	case errors.As(err, &refused):
		return sourceError(mediasource.Protocol, redirectError{refused: refused, err: err})
	case errors.As(err, &tooLarge):
		return sourceError(mediasource.Protocol, err)
	case errors.As(err, &status):
		return sourceError(statusCause(status.Code), err)
	}
	return sourceError(mediasource.Unreachable, err)
}

func sourceError(c mediasource.Cause, err error) *mediasource.SourceError {
	return &mediasource.SourceError{Cause: c, Err: err}
}

// statusCause partitions every status code: 401 and 403 the credential,
// 404 nothing at the base path, 408, 429 and 5xx a source that may answer
// later, and every other code, a 2xx a GET was answered with included, a
// server answering outside its API.
func statusCause(code int) mediasource.Cause {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return mediasource.Unauthorized
	case code == http.StatusNotFound:
		return mediasource.NotFound
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || (code >= 500 && code <= 599):
		return mediasource.Unreachable
	}
	return mediasource.Protocol
}

// redirectError renders a refused redirect as the fix the user makes: the
// hop cap as a loop, any other refusal as the origin to set url to.
type redirectError struct {
	refused *httpx.RedirectRefusedError
	err     error
}

func (e redirectError) Error() string {
	if e.refused.Reason == httpx.RedirectTooMany {
		return "redirects too many times; check url for a redirect loop"
	}
	return "redirects to " + e.refused.Origin + "; set url to that address"
}

func (e redirectError) Unwrap() error { return e.err }
