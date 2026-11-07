package provider

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cplieger/go/httpclient"
)

// FailureKind is what the funnel routes a failure on.
type FailureKind uint8

// The failure kinds, from 1, so the zero kind is none.
const (
	Auth FailureKind = iota + 1
	RateLimited
	Transient
	Permanent
	NotFound
)

var kindNames = [...]string{Auth: "auth", RateLimited: "rate_limited", Transient: "transient", Permanent: "permanent", NotFound: "not_found"}

func (k FailureKind) String() string {
	if k >= Auth && k <= NotFound {
		return kindNames[k]
	}
	return fmt.Sprintf("FailureKind(%d)", k)
}

// Failure is the boundary's one classification of a provider failure.
type Failure struct { //nolint:errname // design 5 names the boundary's error Failure
	// ctxErr is context.Canceled or context.DeadlineExceeded when the call's
	// context ended it, so errors.Is sees the cause without exposing the
	// unredacted error the Client was handed.
	ctxErr     error
	Host       string
	Msg        string        // one line, at most 256 bytes, secrets redacted once the wrapper returns it
	RetryAfter time.Duration // RateLimited only; 0: none given
	Status     int           // 0: no HTTP answer
	Cause      httpclient.Cause
	Kind       FailureKind
}

func (f *Failure) Error() string {
	var b []byte
	b = append(b, f.Kind.String()...)
	if f.Host != "" {
		b = append(b, ' ')
		b = append(b, f.Host...)
	}
	if f.Status != 0 {
		b = fmt.Appendf(b, " status %d", f.Status)
	}
	if f.Msg != "" {
		b = append(b, ": "...)
		b = append(b, f.Msg...)
	}
	return string(b)
}

// Unwrap answers the context error that ended the call, or nil.
func (f *Failure) Unwrap() error { return f.ctxErr }

// IsTransient lets httpclient.Do, inside the Client, retry a GET or HEAD on a
// Transient failure only.
func (f *Failure) IsTransient() bool { return f.Kind == Transient }

// Fail is how an adapter reports a fault a 200 body carries; the wrapper
// redacts its Msg like every other failure's.
func Fail(k FailureKind, msg string) *Failure { return &Failure{Kind: k, Msg: msg} }

// quirkBody bounds the body a Quirk sees.
const quirkBody = 4 << 10

// classifyAnswer is 5.1's table for a non-2xx answer, after the entry's quirk.
// credentialed is whether the schema holds a Secret: a credentialless
// provider's 401 or 403 is Permanent, so it has no credential ladder.
func classifyAnswer(resp *http.Response, body []byte, quirk func(*http.Response, []byte) error, credentialed bool) *Failure {
	host := resp.Request.URL.Hostname()
	if quirk != nil {
		if err := quirk(resp, body); err != nil {
			return quirkFailure(err, host, resp.StatusCode)
		}
	}
	f := &Failure{Host: host, Status: resp.StatusCode, Msg: http.StatusText(resp.StatusCode)}
	switch code := resp.StatusCode; {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		f.Kind = Permanent
		if credentialed {
			f.Kind = Auth
		}
	case code == http.StatusTooManyRequests:
		f.Kind = RateLimited
		f.RetryAfter = max(httpclient.ParseRetryAfterResponse(resp), 0)
	case code == http.StatusNotFound || code == http.StatusGone:
		f.Kind = NotFound
	case httpclient.IsRetryableStatus(code):
		f.Kind = Transient
	default:
		f.Kind = Permanent
	}
	return f
}

// quirkFailure is a quirk's verdict as a *Failure on host, any other error
// Permanent, with the answer's status when the quirk set none.
func quirkFailure(err error, host string, status int) *Failure {
	out := Failure{Kind: Permanent, Msg: err.Error()}
	if f := (*Failure)(nil); errors.As(err, &f) {
		out = *f
	}
	out.Host = cmp.Or(out.Host, host)
	out.Status = cmp.Or(out.Status, status)
	return &out
}

// transportFailure classifies an error below HTTP: Transient with its cause,
// carrying the context error when the call's context ended it.
func transportFailure(ctx context.Context, host string, err error) *Failure {
	if f := (*Failure)(nil); errors.As(err, &f) {
		return f
	}
	return &Failure{Kind: Transient, Cause: httpclient.CauseOf(err), Host: host, Msg: err.Error(), ctxErr: ctxErr(ctx, err)}
}

func ctxErr(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case ctx != nil && ctx.Err() != nil:
		return ctx.Err()
	}
	return nil
}
