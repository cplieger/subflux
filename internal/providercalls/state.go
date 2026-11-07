package providercalls

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"golang.org/x/time/rate"
)

const (
	maxAuthFailures  = 3
	ratePauseDefault = 10 * time.Minute
	ratePauseMax     = 24 * time.Hour
	healthThreshold  = 5
	healthWindow     = 10 * time.Minute
	healthCooldown   = time.Hour
	maxCredentials   = 4 // per provider; the least recently used goes first
	// busyWait refuses a credential's first call while its record cannot be
	// read or checked, so a disabled key is never sent on a guess.
	busyWait = 5 * time.Second
	// recordTimeout bounds a disable record's write, which outlives a caller
	// that walks away: the refusal it records already happened.
	recordTimeout = 10 * time.Second
	maxReason     = 256 // bytes of a disable record's Reason
)

// authCooldowns[n-1] is the wait after the nth refusal of a credential.
var authCooldowns = [maxAuthFailures - 1]time.Duration{5 * time.Minute, 30 * time.Minute}

// credState is one credential's memory; disabled mirrors a stored record
// whose fingerprint matches it.
type credState struct {
	authUntil time.Time
	failures  int
	digest    [32]byte
	checked   bool // the first call compared it with the stored record
	disabled  bool
}

// providerState is one provider's state. mu guards every field but slots and
// limiter, which are safe on their own; a transition holds mu across its
// record write, so no other transition of the provider interleaves.
type providerState struct {
	limiter   *rate.Limiter
	published status.ProviderState
	// health: the Transient observations inside the window, the cooldown
	// they set, and the last one's cause.
	coolUntil time.Time
	paused    [Test + 1]time.Time // by op
	id        provider.ProviderID
	host      string
	// disableReason is the stored record's Reason while the latest
	// credential is disabled.
	disableReason string
	window        []time.Time
	creds         []*credState // most recently used first
	slots         slots
	status        int
	mu            sync.Mutex
	latest        [32]byte
	cause         httpclient.Cause
	alert         bool
}

func newProviderState(id provider.ProviderID) *providerState {
	return &providerState{id: id, limiter: rate.NewLimiter(rate.Inf, 1), slots: slots{free: maxInFlight}}
}

// credential returns digest's state, creating it, and marks it most
// recently used.
func (p *providerState) credential(digest [32]byte) *credState {
	i := slices.IndexFunc(p.creds, func(c *credState) bool { return c.digest == digest })
	var c *credState
	if i >= 0 {
		c = p.creds[i]
		p.creds = slices.Delete(p.creds, i, i+1)
	} else {
		c = &credState{digest: digest}
	}
	p.creds = slices.Insert(p.creds, 0, c)
	if len(p.creds) > maxCredentials {
		p.creds = p.creds[:maxCredentials]
	}
	return c
}

// lookup returns digest's state without creating or touching it.
func (p *providerState) lookup(digest [32]byte) *credState {
	if i := slices.IndexFunc(p.creds, func(c *credState) bool { return c.digest == digest }); i >= 0 {
		return p.creds[i]
	}
	return nil
}

// setDisabled makes the mirror match a record naming digest, or no record.
func (p *providerState) setDisabled(digest *[32]byte, reason string) {
	for _, c := range p.creds {
		c.disabled = digest != nil && c.digest == *digest
	}
	p.disableReason = reason
}

// firstCall is the first-call row: it compares the credential's first call
// with the stored record. A nil error admits the call to the remaining rows;
// p.mu is held.
func (f *Funnel) firstCall(ctx context.Context, p *providerState, c *credState, credential string) error {
	rec, found, err := f.db.CredentialDisable(ctx, string(p.id))
	if err != nil {
		slog.Error("provider credential record unreadable", "provider", p.id, "err", err)
		return &BackoffError{Provider: p.id, Until: time.Now().Add(busyWait)}
	}
	if !found {
		c.checked = true
		return nil
	}
	match, err := f.hasher.Verify(ctx, rec.Fingerprint, credential)
	if err != nil {
		return &BackoffError{Provider: p.id, Until: time.Now().Add(busyWait)}
	}
	switch {
	case match:
		p.setDisabled(&c.digest, rec.Reason)
		if c.digest == p.latest {
			f.raise(p, disabledText(p.id, rec.Reason))
		}
	case c.digest == p.latest:
		if err := f.db.DeleteCredentialDisable(ctx, string(p.id)); err != nil {
			slog.Error("provider credential record delete failed", "provider", p.id, "err", err)
			return nil
		}
		p.setDisabled(nil, "")
	}
	c.checked = true
	f.publish(p)
	return nil
}

// authFailure is the Auth row for a live binding's non-empty credential;
// p.mu is held.
func (f *Funnel) authFailure(ctx context.Context, p *providerState, b *Binding, fail *provider.Failure) {
	c := p.credential(b.digest)
	now := time.Now()
	switch n := c.failures + 1; {
	case n < maxAuthFailures:
		c.failures, c.authUntil = n, now.Add(authCooldowns[n-1])
	case b.digest != p.latest:
		c.failures, c.authUntil = maxAuthFailures-1, now.Add(authCooldowns[maxAuthFailures-2])
	default:
		reason := cutReason("refused " + strconv.Itoa(maxAuthFailures) + " times: " + fail.Error())
		if !f.writeRecord(ctx, p, b.credential, reason) {
			return
		}
		p.setDisabled(&c.digest, reason)
		c.failures, c.authUntil, c.checked = 0, time.Time{}, true
		f.raise(p, disabledText(p.id, reason))
	}
	f.publish(p)
}

// cutReason cuts s to maxReason bytes, on a rune boundary.
func cutReason(s string) string {
	if len(s) <= maxReason {
		return s
	}
	s = s[:maxReason]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// writeRecord stores the disable record for credential, reporting whether
// it did; a failure is logged and changes nothing.
func (f *Funnel) writeRecord(ctx context.Context, p *providerState, credential, reason string) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	fp, err := f.hasher.Hash(ctx, credential)
	if err == nil {
		err = f.db.PutCredentialDisable(ctx, statedb.CredentialDisable{At: time.Now(), Provider: string(p.id), Fingerprint: fp, Reason: reason})
	}
	if err != nil {
		slog.Error("provider credential record write failed", "provider", p.id, "err", err)
		return false
	}
	return true
}

// ratePause is the RateLimited row; p.mu is held.
func (f *Funnel) ratePause(p *providerState, op Op, fail *provider.Failure) {
	d := fail.RetryAfter
	if d <= 0 {
		d = ratePauseDefault
	}
	if until := time.Now().Add(min(d, ratePauseMax)); until.After(p.paused[op]) {
		p.paused[op] = until
	}
	f.publish(p)
}

// transient is the Transient row for a live binding under a live caller;
// p.mu is held.
func (f *Funnel) transient(p *providerState, fail *provider.Failure) {
	now := time.Now()
	p.window = slices.DeleteFunc(p.window, func(t time.Time) bool { return now.Sub(t) >= healthWindow })
	p.window = append(p.window, now)
	p.cause, p.host, p.status = fail.Cause, fail.Host, fail.Status
	if len(p.window) >= healthThreshold {
		p.coolUntil, p.window = now.Add(healthCooldown), p.window[:0]
		if p.cause == httpclient.CauseDNS || p.cause == httpclient.CauseRefused || p.cause == httpclient.CauseTLS {
			f.raise(p, string(p.id)+": "+p.healthReason())
		}
	}
	f.publish(p)
}

// success is the nil row for a live binding; p.mu is held.
func (f *Funnel) success(p *providerState, b *Binding) {
	p.credential(b.digest).failures = 0
	f.clear(p)
	f.publish(p)
}

// healthReason names the last Transient failure's cause and host.
func (p *providerState) healthReason() string {
	host := p.host
	if host == "" {
		host = "the provider"
	}
	switch p.cause {
	case httpclient.CauseDNS:
		return host + " does not resolve"
	case httpclient.CauseRefused:
		return host + " refused the connection"
	case httpclient.CauseTLS:
		return host + " failed the TLS handshake"
	case httpclient.CauseReset:
		return host + " reset the connection"
	case httpclient.CauseTimeout:
		return host + " timed out"
	case httpclient.CauseTooLarge:
		return host + " sent a body over the limit"
	}
	if p.status != 0 {
		return host + " answered " + strconv.Itoa(p.status)
	}
	return host + " failed"
}

// state is what the status store shows for the provider now.
func (p *providerState) state(now time.Time) status.ProviderState {
	s := status.ProviderState{ID: string(p.id), Health: status.OK}
	latest := p.lookup(p.latest)
	switch {
	case latest != nil && latest.disabled:
		s.Health, s.Reason = status.Disabled, "credential "+p.disableReason
	case p.coolUntil.After(now):
		s.Health, s.Until, s.Reason = status.Degraded, p.coolUntil, p.healthReason()
	case latest != nil && latest.authUntil.After(now):
		s.Health, s.Until, s.Reason = status.Degraded, latest.authUntil, "credential refused; retrying after a wait"
	default:
		for op, until := range p.paused {
			if until.After(now) && until.After(s.Until) {
				s.Health, s.Until, s.Reason = status.Degraded, until, "rate limited on "+Op(op).String()
			}
		}
	}
	return s
}

// publish reports the provider's state when it changed; p.mu is held.
func (f *Funnel) publish(p *providerState) {
	s := p.state(time.Now())
	if old := p.published; s.Health == old.Health && s.Reason == old.Reason && s.Until.Equal(old.Until) {
		return
	}
	p.published = s
	f.status.SetProvider(s)
	f.metrics.setHealth(p.id, s.Health)
	level := slog.LevelWarn
	if s.Health == status.OK {
		level = slog.LevelInfo
	}
	slog.Log(context.Background(), level, "provider state changed", "provider", p.id, "health", healthNames[s.Health], "until", s.Until, "reason", s.Reason)
}

func disabledText(id provider.ProviderID, reason string) string {
	return string(id) + ": the credential was " + reason + "; a passing connection test re-enables it"
}

// raise and clear set the provider's alert; p.mu is held.
func (f *Funnel) raise(p *providerState, text string) {
	p.alert = true
	f.status.Reconcile(status.ProviderAlert(string(p.id)), true, text)
}

func (f *Funnel) clear(p *providerState) {
	if p.alert {
		p.alert = false
		f.status.Reconcile(status.ProviderAlert(string(p.id)), false, "")
	}
}
