package animemap

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/provider"
)

const (
	// maxAge is how long a good refresh holds before the next search checks
	// the latest tag again.
	maxAge = 6 * time.Hour
	// holdOff is the wait after a failed attempt, of either class.
	holdOff = 30 * time.Minute
	// staleAfter is the age of the last good refresh at which a failed
	// attempt raises the alert.
	staleAfter = 24 * time.Hour
	// refreshTimeout bounds one attempt: the file is about 13 MB.
	refreshTimeout = 2 * time.Minute
	// callerWait keeps 20 of a search's 30 seconds for the search itself.
	callerWait = 10 * time.Second
)

var source = mapread.Source{Repo: "cplieger/animap"}

// permanentErrs are mapread's refusals of a release or its document, which
// the same tag answers again; anything else (a 408, 425, 429 or 5xx
// *StatusError, a transport error, a body cut short, the attempt's timeout)
// may pass on a retry.
var permanentErrs = []error{
	mapread.ErrRelease, mapread.ErrRedirect, mapread.ErrChecksum, mapread.ErrTooLarge,
	mapread.ErrNotDocument, mapread.ErrInvalid, mapread.ErrVersion,
}

func permanent(err error) bool {
	for _, p := range permanentErrs {
		if errors.Is(err, p) {
			return true
		}
	}
	return false
}

// outcome is one attempt's result: the latest tag, the index of a new one,
// and whether its Fetch ran, so a failure refuses that tag.
type outcome struct {
	err     error
	index   *mapread.Index
	tag     string
	fetched bool
}

func (m *Map) refresh(ctx context.Context, c provider.Client) {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	m.mu.Lock()
	held := m.held
	m.mu.Unlock()
	o := attemptFn(ctx, c, held, m.refusal)
	cancel()
	m.finish(&o)
}

// refusal is why tag was refused, nil when it was not.
func (m *Map) refusal(tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refused[tag]
}

// attemptFn is attempt; a test holds a refresh by replacing it.
var attemptFn = attempt

// attempt asks for the latest tag and fetches it unless it is the held one
// or a refused one, which fails again with its remembered cause.
func attempt(ctx context.Context, c provider.Client, held string, refused func(string) error) outcome {
	hc := c.HTTPClient()
	rel, err := source.Latest(ctx, hc)
	switch {
	case err != nil:
		return outcome{err: err}
	case rel.Tag == held:
		return outcome{tag: rel.Tag}
	}
	if cause := refused(rel.Tag); cause != nil {
		return outcome{tag: rel.Tag, err: cause}
	}
	got, err := rel.Fetch(ctx, hc, mapread.Limits{})
	if err != nil {
		return outcome{tag: rel.Tag, err: err, fetched: true}
	}
	return outcome{tag: rel.Tag, index: mapread.NewIndex(got.Document), fetched: true}
}

// finish records an attempt's outcome, logs it and reports the condition to
// every registered entry; an attempt ending after Close records nothing. It
// is the refresh's last act and closes the flight it clears before unlocking:
// a Close finding no flight returns at once, so it must never see the flight
// cleared while its goroutine still runs.
func (m *Map) finish(o *outcome) {
	m.mu.Lock()
	defer m.mu.Unlock()
	done := m.flight
	defer close(done)
	m.flight = nil
	m.cancel()
	if m.closed {
		return
	}
	now := time.Now()
	if o.err == nil {
		if o.index != nil {
			m.index, m.held = o.index, o.tag
			slog.Info("animemap: indexed the anime map", "tag", o.tag)
		}
		m.good, m.streak = now, false
		m.cond = condition{}
	} else {
		m.holdUntil = now.Add(holdOff)
		m.logFailure(o)
		m.cond = condition{since: m.good, cause: o.err, active: m.index == nil || now.Sub(m.good) >= staleAfter}
	}
	for name, report := range m.reports {
		report(m.cond.text(name))
	}
}

// logFailure logs WARN once per refused tag and once per streak of other
// failures, and DEBUG for the rest.
func (m *Map) logFailure(o *outcome) {
	class := "transient"
	if permanent(o.err) {
		class = "permanent"
	}
	attrs := []any{"class", class, "tag", o.tag, "err", o.err}
	if se, ok := errors.AsType[*mapread.StatusError](o.err); ok {
		attrs = append(attrs, "url", se.URL, "status", se.Code)
	}
	if ve, ok := errors.AsType[*mapread.VersionError](o.err); ok {
		attrs = append(attrs, "version", ve.Found)
	}
	newRefusal := o.fetched && class == "permanent"
	if newRefusal {
		m.refused[o.tag] = o.err
	}
	if newRefusal || !m.streak {
		slog.Warn("animemap: anime map refresh failed, keeping the held one", attrs...)
	} else {
		slog.Debug("animemap: anime map refresh failed, keeping the held one", attrs...)
	}
	m.streak = true
}
