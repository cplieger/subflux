package providergate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/cplieger/keyenc"
	"github.com/cplieger/runesafe/v2"
	"github.com/cplieger/subflux/internal/subflux"
)

// MaxAuthFailures is the credential rejection that disables a provider; each
// earlier rejection n waits authCooldowns[n-1] before one probe call.
const MaxAuthFailures = 3

var authCooldowns = [MaxAuthFailures - 1]time.Duration{5 * time.Minute, 30 * time.Minute}

const (
	// probeTimeout frees a probe slot whose caller never reported, so a
	// cancelled call cannot wedge the provider.
	probeTimeout = 2 * time.Minute
	// defaultRateLimitPause applies when the provider gave no Retry-After.
	defaultRateLimitPause = 10 * time.Minute
	maxRateLimitPause     = 24 * time.Hour
	maxReasonBytes        = 256
)

// Admit refusal reasons with no variable part.
const (
	reasonSettingsChanged = "settings changed; waiting for the new settings to load"
	reasonDisabled        = "credentials rejected; disabled until the settings change or a Test passes"
	reasonProbing         = "credential check in progress"
)

type digest [sha256.Size]byte

// Binding is the gate's view for one set of provider settings. Engines hold
// one; the zero Binding is not usable, use Gate.Bind.
type Binding struct {
	gate      *Gate
	canonical map[subflux.ProviderID]string
	digests   map[subflux.ProviderID]digest
	// verdicts is the gate's change count each provider's instance last
	// caught up with. Guarded by gate.mu.
	verdicts map[subflux.ProviderID]uint64
}

var (
	_ fmt.Stringer   = (*Binding)(nil)
	_ fmt.GoStringer = (*Binding)(nil)
)

// Bind returns a binding for the given per-provider settings, which the caller
// has already normalized. A provider absent from settings is absent from the
// binding: it is admitted only while it is absent from the live binding too.
func (g *Gate) Bind(settings map[subflux.ProviderID]map[string]any) *Binding {
	b := &Binding{
		gate:      g,
		canonical: make(map[subflux.ProviderID]string, len(settings)),
		digests:   make(map[subflux.ProviderID]digest, len(settings)),
		verdicts:  make(map[subflux.ProviderID]uint64, len(settings)),
	}
	for id, s := range settings {
		b.canonical[id], b.digests[id] = canonicalize(s)
	}
	g.mu.Lock()
	for id := range settings {
		b.verdicts[id] = g.verdicts[id]
	}
	g.mu.Unlock()
	return b
}

// canonicalize renders settings as one string that two maps produce alike
// exactly when every non-empty value prints alike: fmt.Sprint makes the
// string "true" from a form equal the YAML bool, and an empty or null value
// equals an absent key.
func canonicalize(settings map[string]any) (string, digest) {
	parts := make([]string, 0, 2*len(settings))
	for _, key := range slices.Sorted(maps.Keys(settings)) {
		v := settings[key]
		if v == nil {
			continue
		}
		if s := fmt.Sprint(v); s != "" {
			parts = append(parts, key, s)
		}
	}
	canonical := keyenc.Join(parts...)
	return canonical, sha256.Sum256([]byte(canonical))
}

// String reports the provider count only, never a setting.
func (b *Binding) String() string {
	return fmt.Sprintf("providergate.Binding(%d providers)", len(b.digests))
}

// GoString is String, so %#v cannot print a setting either.
func (b *Binding) GoString() string { return b.String() }

// has reports whether id is bound. A nil binding binds nothing.
func (b *Binding) has(id subflux.ProviderID) bool {
	if b == nil {
		return false
	}
	_, ok := b.digests[id]
	return ok
}

// Admit reports whether id may be called for op now and, when not, why, in
// words that never carry a credential. Admitting a provider with a recorded
// failure claims its one probe slot until Observe reports the call.
func (b *Binding) Admit(id subflux.ProviderID, op Op) (ok bool, reason string) {
	g := b.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	want := b.digests[id]
	if g.liveDigest(id) != want {
		return false, reasonSettingsChanged
	}
	now := g.now()
	rec := g.records[id]
	if rec != nil {
		switch {
		case rec.digest != want:
			return false, reasonSettingsChanged
		case !rec.rec.DisabledAt.IsZero():
			return false, reasonDisabled
		case now.Before(rec.rec.NextAttemptAt):
			return false, "credentials rejected; next attempt in " + formatWait(rec.rec.NextAttemptAt.Sub(now))
		case now.Before(rec.probeUntil):
			return false, reasonProbing
		}
	}
	if p, paused := g.pauses[pauseKey{id: id, op: op}]; paused && now.Before(p.until) {
		return false, "rate limited until " + p.until.Local().Format("15:04")
	}
	if rec != nil {
		rec.probeUntil = now.Add(probeTimeout)
	}
	return true, ""
}

// formatWait renders a remaining wait rounded up to whole minutes, or whole
// seconds under a minute.
func formatWait(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int((d+time.Second-1)/time.Second))
	}
	return fmt.Sprintf("%dm", int((d+time.Minute-1)/time.Minute))
}

// Observe classifies the outcome of one provider call the engine made for op:
// a *subflux.AuthError advances the ladder, a *subflux.RateLimitError pauses
// op, success from an operation that failed before clears the record, and any
// other error only frees the probe slot. A stale binding's observation is
// ignored, and so is any observation once the provider is disabled.
func (b *Binding) Observe(ctx context.Context, id subflux.ProviderID, op Op, err error) {
	g := b.gate
	g.mu.Lock()
	fx := b.observeLocked(id, op, err)
	g.commitLocked(ctx, &fx)
}

// observeLocked is Observe's state change. Callers hold g.mu.
func (b *Binding) observeLocked(id subflux.ProviderID, op Op, err error) effects {
	g := b.gate
	var fx effects
	want := b.digests[id]
	if g.liveDigest(id) != want {
		return fx
	}
	rec := g.records[id]
	if rec != nil && (rec.digest != want || !rec.rec.DisabledAt.IsZero()) {
		return fx
	}
	if err == nil {
		g.observeSuccess(&fx, id, op, rec)
		return fx
	}
	if rec != nil {
		rec.probeUntil = time.Time{}
	}
	if _, ok := errors.AsType[*subflux.AuthError](err); ok {
		b.observeAuthFailure(&fx, id, op, rec, err)
	} else if rl, ok := errors.AsType[*subflux.RateLimitError](err); ok {
		b.observeRateLimit(&fx, id, op, rl, err)
	}
	return fx
}

func (g *Gate) observeSuccess(fx *effects, id subflux.ProviderID, op Op, rec *record) {
	delete(g.pauses, pauseKey{id: id, op: op})
	if rec == nil {
		return
	}
	rec.probeUntil = time.Time{}
	if slices.Contains(rec.rec.FailedOps, string(op)) {
		g.dropRecord(fx, id, CauseCredentialsAccepted)
	}
}

func (b *Binding) observeAuthFailure(fx *effects, id subflux.ProviderID, op Op, rec *record, err error) {
	g := b.gate
	now := g.now()
	if rec != nil && now.Before(rec.rec.NextAttemptAt) {
		return
	}
	if rec == nil {
		canonical := b.canonical[id]
		rec = &record{canonical: &canonical, digest: b.digests[id], rec: subflux.ProviderAuthRecord{Provider: id}}
		g.records[id] = rec
	}
	rec.rec.Failures++
	if !slices.Contains(rec.rec.FailedOps, string(op)) {
		rec.rec.FailedOps = append(rec.rec.FailedOps, string(op))
	}
	reason := describe(err)
	rec.rec.LastError = reason
	rec.rec.LastFailureAt = now
	attempt := rec.rec.Failures
	fx.persist(id)
	fx.metric(func() { g.metrics.IncProviderAuthFailure(id) })
	if attempt >= MaxAuthFailures {
		rec.rec.DisabledAt = now
		rec.rec.NextAttemptAt = time.Time{}
		fx.metric(func() { g.metrics.SetProviderDisabled(id, true) })
		fx.do(func() {
			slog.Error("provider disabled: credentials rejected", "provider", id, "attempts", attempt, "reason", reason)
		})
		fx.event(Event{Provider: id, Kind: Disabled, Reason: reason})
		return
	}
	wait := authCooldowns[attempt-1]
	rec.rec.NextAttemptAt = now.Add(wait)
	fx.do(func() {
		slog.Warn("provider credentials rejected", "provider", id, "attempt", attempt, "retry_in", wait.String(), "error", reason)
	})
}

func (b *Binding) observeRateLimit(fx *effects, id subflux.ProviderID, op Op, rl *subflux.RateLimitError, err error) {
	g := b.gate
	now := g.now()
	wait := rl.RetryAfter
	if wait <= 0 {
		wait = defaultRateLimitPause
	}
	until := now.Add(min(wait, maxRateLimitPause))
	key := pauseKey{id: id, op: op}
	prev, had := g.pauses[key]
	running := had && now.Before(prev.until)
	if !running || until.After(prev.until) {
		g.pauses[key] = pause{until: until, digest: b.digests[id]}
	}
	fx.metric(func() { g.metrics.IncProviderRateLimited(id, op) })
	if running {
		return
	}
	reason := describe(err)
	fx.do(func() {
		slog.Warn("provider rate limited; pausing", "provider", id, "op", string(op),
			"until", until.UTC().Format(time.RFC3339), "error", reason)
	})
	fx.event(Event{Provider: id, Kind: Paused, Reason: reason})
}

// PrepareSetting runs before a call to sr, id's instance under b: when the
// gate's verdict on id's optional settings changed since sr last caught up, sr
// forgets its upstream's answer, so the call asks the upstream again.
func (b *Binding) PrepareSetting(id subflux.ProviderID, sr SettingReporter) {
	g := b.gate
	g.mu.Lock()
	b.catchUpLocked(id, sr)
	g.mu.Unlock()
}

// ObserveSetting records, after a call, what sr, id's instance under b, says
// its upstream answered about an optional setting while the provider itself
// kept working. A refusal under the live settings marks the setting rejected;
// an acceptance clears the rejection. Either change makes every other
// instance of id forget its answer before its next call, an answer sr held
// from before the last change is forgotten rather than applied, and a stale
// binding's is ignored.
func (b *Binding) ObserveSetting(id subflux.ProviderID, sr SettingReporter) {
	g := b.gate
	g.mu.Lock()
	var fx effects
	b.catchUpLocked(id, sr)
	if g.liveDigest(id) == b.digests[id] {
		b.applyVerdictLocked(&fx, id, sr)
	}
	g.commitLocked(context.Background(), &fx)
}

// catchUpLocked makes sr forget its answer when the gate's verdict on id
// changed since b last caught up. Callers hold g.mu.
func (b *Binding) catchUpLocked(id subflux.ProviderID, sr SettingReporter) {
	if n := b.gate.verdicts[id]; b.verdicts[id] != n {
		sr.ForgetSettingVerdict()
		b.verdicts[id] = n
	}
}

// applyVerdictLocked applies sr's answer about its optional setting. sr stays
// caught up with a change its own answer made. Callers hold g.mu.
func (b *Binding) applyVerdictLocked(fx *effects, id subflux.ProviderID, sr SettingReporter) {
	g := b.gate
	setting, refusal := sr.SettingVerdict()
	if setting == "" {
		return
	}
	key := rejectKey{id: id, setting: setting}
	_, rejected := g.rejected[key]
	switch {
	case refusal != nil && !rejected:
		g.rejected[key] = b.digests[id]
		g.verdicts[id]++
		fx.metric(func() { g.metrics.SetProviderSettingRejected(id, setting, true) })
		fx.event(Event{Provider: id, Kind: SettingRejected, Setting: setting, Reason: describe(refusal)})
	case refusal == nil && rejected:
		g.clearRejection(fx, key, CauseCredentialsAccepted)
	default:
		return
	}
	b.verdicts[id] = g.verdicts[id]
}

// ResetAll is Gate.ResetAll.
func (b *Binding) ResetAll(ctx context.Context) { b.gate.ResetAll(ctx) }

// Status is Gate.Status.
func (b *Binding) Status() map[subflux.ProviderID]Status { return b.gate.Status() }

// describe renders a provider error for a record, a log line or an alert. The
// provider already redacted its own secrets out of the text.
func describe(err error) string {
	if err == nil {
		return ""
	}
	return runesafe.SanitizeSingleLineBounded(err.Error(), maxReasonBytes)
}
