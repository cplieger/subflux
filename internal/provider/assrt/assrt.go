// Package assrt is the Assrt provider: Chinese and English subtitles from
// assrt.net's API at api.assrt.net, searched by release name and downloaded
// as archives from file0.assrt.net. It needs an API token, and Assrt's
// per-minute quota paces every request.
package assrt

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/provider"
	"golang.org/x/time/rate"
)

// ID is Assrt's provider id.
const ID provider.ProviderID = "assrt"

const (
	apiHost  = "api.assrt.net"
	fileHost = "file0.assrt.net"
	apiURL   = "https://" + apiHost + "/v1"

	keyToken = "token"

	// quotaWindow is the period Assrt's quota counts requests over; its
	// bucket refills fully within it (testdata/README.md).
	quotaWindow = time.Minute
	// maxQuota is the documented starting quota, the most the pacer admits
	// a window whatever user/quota reports (https://assrt.net/api/doc).
	maxQuota = 20

	maxListBody = 2 << 20
)

// NewEntry is Assrt's row of the provider table. Its pacer lives with the
// entry, which a process's providers.Table builds once, so an engine swap
// keeps the quota's budget.
func NewEntry() provider.Entry {
	p := &pacer{turn: make(chan struct{}, 1)}
	return provider.Entry{
		ID:      ID,
		Summary: "Chinese and English subtitles from Assrt by release name; needs an API token.",
		Hosts:   []string{apiHost, fileHost},
		Schema: formschema.Schema{Fields: []formschema.Field{
			{Key: keyToken, Label: "API token", Help: "From your assrt.net user panel", Kind: formschema.Secret, Required: true},
		}},
		Languages: languages,
		Quirk:     quirk,
		New: func(c provider.Client, v formschema.Values) (provider.Adapter, error) {
			return &adapter{c: c, token: v.Secret(keyToken), p: p}, nil
		},
	}
}

type adapter struct {
	c     provider.Client
	p     *pacer
	token string
}

// Test reads the token's quota, which refuses a wrong token and spends none
// of the quota (testdata/README.md), so it is not paced.
func (a *adapter) Test(ctx context.Context) error {
	_, err := a.quota(ctx)
	return err
}

// apiStatus is the status every Assrt answer carries, 0 for success, with
// its errmsg otherwise.
type apiStatus struct {
	Errmsg string `json:"errmsg"`
	Status int    `json:"status"`
}

// The statuses testdata/status.md records.
const (
	statusNoUser       = 1
	statusInvalidToken = 20001
	statusNotFound     = 20900
	statusOverQuota    = 30900
)

// statusFailure is a non-zero status's failure (testdata/status.md).
func statusFailure(s apiStatus) *provider.Failure {
	msg := "Assrt status " + strconv.Itoa(s.Status) + ": " + s.Errmsg
	switch s.Status {
	case statusNoUser, statusInvalidToken:
		return provider.Fail(provider.Auth, msg)
	case statusOverQuota:
		return &provider.Failure{Kind: provider.RateLimited, RetryAfter: quotaWindow, Msg: msg}
	case statusNotFound:
		return provider.Fail(provider.NotFound, msg)
	case 30000, 30001, 30002, 30300:
		return provider.Fail(provider.Transient, msg)
	}
	return provider.Fail(provider.Permanent, msg)
}

// quirk reads a non-2xx answer's status, which Assrt sends with its 4xx and
// 5xx answers: an invalid token answers 400, which is Auth.
func quirk(_ *http.Response, body []byte) error {
	var s apiStatus
	if json.Unmarshal(body, &s) != nil || s.Status == 0 {
		return nil
	}
	return statusFailure(s)
}

// quota is the requests the token's bucket holds now: the account's quota
// once a window passed without a request (testdata/README.md).
func (a *adapter) quota(ctx context.Context) (int, error) {
	var q struct {
		User struct {
			Quota int `json:"quota"`
		} `json:"user"`
	}
	resp, err := a.send(ctx, apiURL+"/user/quota")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	err = decode(resp, &q)
	return q.User.Quota, err
}

// call admits a request to the API at path with query, sends it and decodes
// its answer into v.
func (a *adapter) call(ctx context.Context, path, query string, v any) error {
	if err := a.p.admit(ctx, a.token, a.quota); err != nil {
		return err
	}
	resp, err := a.send(ctx, apiURL+path+"?"+query)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return a.spentOn(decode(resp, v))
}

// send GETs u with the token as a bearer header, never in the URL.
func (a *adapter) send(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.c.Do(req)
	return resp, a.spentOn(err)
}

// spentOn passes err on, first marking the pacer's rate stale when err is a
// rate limit, so it is read again.
func (a *adapter) spentOn(err error) error {
	if f, ok := err.(*provider.Failure); ok && f.Kind == provider.RateLimited { //nolint:errorlint // the Client and statusFailure return *Failure unwrapped
		a.p.spent()
	}
	return err
}

// decode reads resp's JSON into v after its status: a non-zero one is its
// failure.
func decode(resp *http.Response, v any) error {
	b, err := provider.ReadAll(resp, maxListBody)
	if err != nil {
		return err
	}
	var s apiStatus
	if err := json.Unmarshal(b, &s); err != nil {
		return provider.Fail(provider.Permanent, "decode Assrt answer: "+err.Error())
	}
	if s.Status != 0 {
		return statusFailure(s)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return provider.Fail(provider.Permanent, "decode Assrt answer: "+err.Error())
	}
	return nil
}

// pacer admits the requests that spend Assrt's quota, burst 1 at the rate
// user/quota reports, at most maxQuota a window, so no window holds more
// than the quota.
type pacer struct {
	readAfter time.Time
	lim       *rate.Limiter
	// turn serializes reading the rate; mu guards the fields.
	turn  chan struct{}
	token string
	mu    sync.Mutex
	stale bool
}

// admit waits until a request may be sent, first reading the rate through
// read when none is held, token is not the one it was read for, or a quota
// status made it stale. A read of 0 waits a window and reads again.
func (p *pacer) admit(ctx context.Context, token string, read func(context.Context) (int, error)) error {
	select {
	case p.turn <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	lim, err := p.rate(ctx, token, read)
	<-p.turn
	if err != nil {
		return err
	}
	return lim.Wait(ctx)
}

// rate is the limiter for token, read again while due; the caller holds
// p.turn.
func (p *pacer) rate(ctx context.Context, token string, read func(context.Context) (int, error)) (*rate.Limiter, error) {
	for {
		p.mu.Lock()
		lim, due, after := p.lim, p.lim == nil || p.stale || p.token != token, p.readAfter
		p.mu.Unlock()
		if !due {
			return lim, nil
		}
		if err := sleepUntil(ctx, after); err != nil {
			return nil, err
		}
		n, err := read(ctx)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		if n <= 0 {
			p.readAfter = time.Now().Add(quotaWindow)
		} else {
			every := rate.Every(quotaWindow / time.Duration(min(n, maxQuota)))
			if p.lim == nil {
				p.lim = rate.NewLimiter(every, 1)
			} else {
				p.lim.SetLimit(every)
			}
			p.token, p.stale = token, false
		}
		p.mu.Unlock()
	}
}

// spent marks the rate stale after a quota status. It is read a window
// later, once the bucket has refilled: a read before that reports what is
// left, not the quota.
func (p *pacer) spent() {
	p.mu.Lock()
	p.stale, p.readAfter = true, time.Now().Add(quotaWindow)
	p.mu.Unlock()
}

func sleepUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
