package mediasource

import (
	"context"
	"errors"
	"testing"

	"github.com/cplieger/httpx/v5"
)

func TestSourceErrorUnwraps(t *testing.T) {
	t.Run("errors.Is sees the deadline", func(t *testing.T) {
		err := error(&SourceError{Source: "sonarr", Op: "Items", Cause: Unreachable, Err: context.DeadlineExceeded})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("errors.Is(%v, DeadlineExceeded) = false", err)
		}
	})
	t.Run("httpx.IsTransient sees a wrapped 503", func(t *testing.T) {
		err := error(&SourceError{Source: "sonarr", Op: "Items", Cause: Unreachable, Err: &httpx.HTTPStatusError{Code: 503}})
		if !httpx.IsTransient(err) {
			t.Errorf("httpx.IsTransient(%v) = false", err)
		}
	})
}

func TestSourceErrorText(t *testing.T) {
	cases := map[string]struct {
		err  *SourceError
		want string
	}{
		"every field":  {&SourceError{Source: "sonarr-4k", Op: "Root", Cause: NotFound, Err: errors.New("HTTP 404")}, "source sonarr-4k Root: not_found: HTTP 404"},
		"no source":    {&SourceError{Op: "Ping", Cause: Unauthorized}, "source Ping: unauthorized"},
		"cause alone":  {&SourceError{Cause: Expired}, "source: expired"},
		"no cause set": {&SourceError{Source: "radarr", Err: errors.New("boom")}, "source radarr: cause(0): boom"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := c.err.Error(); got != c.want {
				t.Errorf("Error() = %q; want %q", got, c.want)
			}
		})
	}
}

func TestCauseString(t *testing.T) {
	want := map[Cause]string{
		Unreachable:   "unreachable",
		Unauthorized:  "unauthorized",
		NotFound:      "not_found",
		Protocol:      "protocol",
		Unsupported:   "unsupported",
		UnknownSource: "unknown_source",
		Expired:       "expired",
		0:             "cause(0)",
		Expired + 1:   "cause(8)",
	}
	for c, s := range want {
		if got := c.String(); got != s {
			t.Errorf("Cause(%d).String() = %q; want %q", uint8(c), got, s)
		}
	}
}
