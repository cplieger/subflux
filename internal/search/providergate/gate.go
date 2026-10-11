// Package providergate decides whether a subtitle provider may be called for
// one operation right now. It owns what the transient health tracker does
// not: a credential disable reached through a persisted ladder (a rejected
// credential is retried once after 5 minutes and once after 30, and a third
// rejection disables the provider), a per-operation rate-limit pause held in
// memory, and rejected optional settings.
//
// Every engine holds a Binding, the gate's view for the provider settings it
// was built from. A binding whose settings for a provider are not the live
// ones is refused and its observations are ignored, so an engine that
// outlives a settings save can neither reuse the old credentials nor charge
// their failures to the new ones. A disable survives restart and ends only
// when the settings change, a credential test passes for the recorded
// settings, or ResetAll runs. A mutation's records are written before its
// call returns and ahead of its metric calls, logs and events.
package providergate

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/auth/v6"
	"github.com/cplieger/subflux/internal/effectqueue"
	"github.com/cplieger/subflux/internal/required"
	"github.com/cplieger/subflux/internal/subflux"
)

// Op is the provider operation a decision is about.
type Op string

// The two operations the engine performs against a provider.
const (
	OpSearch   Op = "search"
	OpDownload Op = "download"
)

// Store persists credential-failure records. *boltstore.DB satisfies it.
type Store interface {
	ProviderAuthRecords(ctx context.Context) ([]subflux.ProviderAuthRecord, error)
	PutProviderAuthRecord(ctx context.Context, rec *subflux.ProviderAuthRecord) error
	DeleteProviderAuthRecord(ctx context.Context, id subflux.ProviderID) error
}

// Metrics records the gate's state. *obs.Metrics satisfies it.
type Metrics interface {
	SetProviderDisabled(id subflux.ProviderID, disabled bool)
	DeleteProviderDisabled(id subflux.ProviderID)
	IncProviderAuthFailure(id subflux.ProviderID)
	IncProviderRateLimited(id subflux.ProviderID, op Op)
	// SetProviderSettingRejected with rejected false deletes the series.
	SetProviderSettingRejected(id subflux.ProviderID, setting string, rejected bool)
}

// Config configures Open. Metrics is required; Hasher is required when Store
// is set. A nil Store keeps every record in memory only; a nil Now uses
// time.Now.
type Config struct {
	Store   Store
	Hasher  *auth.Hasher
	Metrics Metrics
	Now     func() time.Time
}

// Kind classifies an Event.
type Kind string

// Event kinds.
const (
	Disabled        Kind = "disabled"
	Enabled         Kind = "enabled"
	Inactive        Kind = "inactive"
	SettingRejected Kind = "setting_rejected"
	SettingCleared  Kind = "setting_cleared"
	Paused          Kind = "paused"
)

// cause says why a provider was re-enabled.
type cause string

// Re-enable causes.
const (
	causeSettingsChanged      cause = "settings_changed"
	causeCredentialTestPassed cause = "credential_test_passed" //nolint:gosec // G101: an event cause label, not a credential
	causeCredentialsAccepted  cause = "credentials_accepted"
	causeReset                cause = "reset"
)

// Event is one state transition, delivered to the SetOnChange hook. Reason is
// the provider's sanitized error text and never carries a credential.
type Event struct {
	Provider subflux.ProviderID
	Kind     Kind
	Reason   string
	Setting  string
}

// ClearResult is ClearIfMatches' answer.
type ClearResult int

// ClearIfMatches results.
const (
	// noRecord: nothing was recorded against the provider.
	noRecord ClearResult = iota
	// Cleared: the recorded state matched the tested settings and was cleared.
	Cleared
	// Mismatch: a record exists for settings other than the tested ones.
	Mismatch
)

// Status is the gate's view of one live provider.
type Status struct {
	DisabledReason   string
	RejectedSettings []string
	PausedFor        time.Duration
	AuthFailures     int
	Disabled         bool
}

// Gate is the process-wide provider gate. Its methods are safe for concurrent
// use.
type Gate struct {
	store    Store
	hasher   *auth.Hasher
	metrics  Metrics
	now      func() time.Time
	onChange func(Event)
	live     *Binding
	records  map[subflux.ProviderID]*record
	pauses   map[pauseKey]pause
	rejected map[rejectKey]digest
	// verdicts counts the changes to each provider's rejected settings. An
	// instance whose binding is behind forgets its upstream's answer before it
	// is trusted again, so an answer from before a change cannot contradict
	// the change.
	verdicts map[subflux.ProviderID]uint64
	retired  map[subflux.ProviderID]struct{}
	commits  []*commit // mutations whose records flush has not yet written
	queue    effectqueue.Queue
	mu       sync.Mutex
	flushing bool
}

// record is one provider's credential-failure state. digest names the binding
// the record is verified for: set by the Observe that created it or by a
// Reconcile whose Verify matched, and zero for a record loaded from the
// store, which is therefore refused until Reconcile verifies it.
type record struct {
	canonical  *string
	probeUntil time.Time
	rec        subflux.ProviderAuthRecord
	digest     digest
}

type pauseKey struct {
	id subflux.ProviderID
	op Op
}

type pause struct {
	until  time.Time
	digest digest
}

type rejectKey struct {
	id      subflux.ProviderID
	setting string
}

// SettingReporter is a provider instance that remembers what its upstream
// answered about one of its optional settings. The gate calls both methods
// under its lock, so neither may call back into the gate.
type SettingReporter interface {
	// SettingVerdict names the setting the upstream answered about and its
	// refusal, nil for an acceptance; setting is "" until the upstream answers.
	SettingVerdict() (setting string, refusal error)
	// ForgetSettingVerdict drops the answer, so the next call asks the
	// upstream again.
	ForgetSettingVerdict()
}

// Open builds the gate and, with a Store, loads its records. It fails only
// for an invalid Config; a store read failure is logged and the gate starts
// with no records.
func Open(ctx context.Context, cfg Config) (*Gate, error) {
	if required.Missing(cfg.Metrics) {
		return nil, errors.New("providergate: Config.Metrics is required")
	}
	if cfg.Store != nil && required.Missing(cfg.Store) {
		return nil, errors.New("providergate: Config.Store is a nil pointer; leave it unset to keep records in memory")
	}
	if cfg.Store != nil && cfg.Hasher == nil {
		return nil, errors.New("providergate: Config.Hasher is required with a Store")
	}
	g := &Gate{
		store:    cfg.Store,
		hasher:   cfg.Hasher,
		metrics:  cfg.Metrics,
		now:      cfg.Now,
		records:  make(map[subflux.ProviderID]*record),
		pauses:   make(map[pauseKey]pause),
		rejected: make(map[rejectKey]digest),
		verdicts: make(map[subflux.ProviderID]uint64),
		retired:  make(map[subflux.ProviderID]struct{}),
	}
	if g.now == nil {
		g.now = time.Now
	}
	if g.store == nil {
		return g, nil
	}
	recs, err := g.store.ProviderAuthRecords(ctx)
	if err != nil {
		slog.Warn("provider credential records unreadable; starting with none", "error", err)
	}
	for i := range recs {
		g.records[recs[i].Provider] = &record{rec: recs[i]}
	}
	return g, nil
}

// SetOnChange installs the hook every Event is delivered to. The hook runs
// after the gate's locks are released, so it may call Status.
func (g *Gate) SetOnChange(fn func(Event)) {
	g.mu.Lock()
	g.onChange = fn
	g.mu.Unlock()
}

// Activate makes b the live binding. Call Reconcile afterwards: until it runs,
// a record whose provider's settings changed is refused rather than trusted.
func (g *Gate) Activate(b *Binding) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.live != nil {
		for id := range g.live.digests {
			if _, kept := b.digests[id]; !kept {
				g.retired[id] = struct{}{}
			}
		}
	}
	for id := range b.digests {
		delete(g.retired, id)
	}
	g.live = b
}

// liveDigest is the live binding's digest for id, zero when id is absent or
// nothing was activated. Callers hold g.mu.
func (g *Gate) liveDigest(id subflux.ProviderID) digest {
	if g.live == nil {
		return digest{}
	}
	return g.live.digests[id]
}

// Status reports every live provider the gate has something to say about.
func (g *Gate) Status() map[subflux.ProviderID]Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[subflux.ProviderID]Status)
	if g.live == nil {
		return out
	}
	now := g.now()
	for id, want := range g.live.digests {
		if st, ok := g.statusLocked(id, want, now); ok {
			out[id] = st
		}
	}
	return out
}

// statusLocked is id's status under want, the live binding's digest for it;
// ok is false when the gate has nothing to report. Callers hold g.mu.
func (g *Gate) statusLocked(id subflux.ProviderID, want digest, now time.Time) (st Status, ok bool) {
	if rec := g.records[id]; rec != nil && rec.digest == want {
		st.AuthFailures = rec.rec.Failures
		if !rec.rec.DisabledAt.IsZero() {
			st.Disabled = true
			st.DisabledReason = rec.rec.LastError
		}
	}
	for _, op := range []Op{OpSearch, OpDownload} {
		if p, paused := g.pauses[pauseKey{id: id, op: op}]; paused && now.Before(p.until) {
			st.PausedFor = max(st.PausedFor, p.until.Sub(now))
		}
	}
	for key := range g.rejected {
		if key.id == id {
			st.RejectedSettings = append(st.RejectedSettings, key.setting)
		}
	}
	slices.Sort(st.RejectedSettings)
	return st, st.AuthFailures > 0 || st.PausedFor > 0 || len(st.RejectedSettings) > 0
}

// verdict is one record's Reconcile outcome, computed outside g.mu because a
// fingerprint check is an Argon2id derivation.
type verdict struct {
	rec     *record
	id      subflux.ProviderID
	matched bool
	corrupt bool
}

// Reconcile brings the gate in line with the live binding: a record whose
// settings no longer match is deleted, a surviving disable is re-announced
// (persistent alerts and gauges live in memory, so this is what restores them
// after a restart), a record of a provider absent from the binding goes
// inactive but is kept, and pauses and rejected settings recorded under other
// settings are dropped. Call it after every Activate.
func (g *Gate) Reconcile(ctx context.Context) {
	g.mu.Lock()
	live := g.live
	var pending []verdict
	if live != nil {
		for id, rec := range g.records {
			if want, ok := live.digests[id]; ok && rec.digest != want {
				pending = append(pending, verdict{id: id, rec: rec})
			}
		}
	}
	g.mu.Unlock()
	if live == nil {
		return
	}
	for i := range pending {
		pending[i].matched, pending[i].corrupt = g.matches(pending[i].rec, live.canonical[pending[i].id])
	}

	g.mu.Lock()
	var fx effects
	g.settleLocked(&fx, pending, live)
	g.reannounce(&fx, live)
	g.commitLocked(ctx, &fx)
}

// settleLocked applies the verdicts that still describe the current record
// and binding: a match verifies the record for the live settings, anything
// else deletes it. Callers hold g.mu.
func (g *Gate) settleLocked(fx *effects, pending []verdict, live *Binding) {
	for _, v := range pending {
		if g.records[v.id] != v.rec || g.live != live {
			continue
		}
		if v.matched {
			v.rec.digest = live.digests[v.id]
			continue
		}
		if v.corrupt {
			fx.do(func() {
				slog.Warn("provider credential record unreadable; deleting it", "provider", v.id)
			})
		}
		g.dropRecord(fx, v.id, causeSettingsChanged)
	}
}

// reannounce applies Reconcile's gauge, inactive and stale-state steps against
// the live binding. Callers hold g.mu.
func (g *Gate) reannounce(fx *effects, live *Binding) {
	for id := range live.digests {
		rec := g.records[id]
		if rec == nil || rec.rec.DisabledAt.IsZero() {
			fx.metric(func() { g.metrics.SetProviderDisabled(id, false) })
			continue
		}
		reason := rec.rec.LastError
		fx.metric(func() { g.metrics.SetProviderDisabled(id, true) })
		fx.do(func() {
			slog.Warn("provider still disabled: credentials rejected", "provider", id, "reason", reason)
		})
		fx.event(Event{Provider: id, Kind: Disabled, Reason: reason})
	}
	for id := range g.records {
		if _, ok := live.digests[id]; !ok {
			fx.metric(func() { g.metrics.DeleteProviderDisabled(id) })
			fx.event(Event{Provider: id, Kind: Inactive})
		}
	}
	for id := range g.retired {
		fx.metric(func() { g.metrics.DeleteProviderDisabled(id) })
	}
	clear(g.retired)
	for key, dig := range g.rejected {
		if dig != live.digests[key.id] {
			g.clearRejection(fx, key)
		}
	}
	maps.DeleteFunc(g.pauses, func(key pauseKey, p pause) bool {
		return p.digest != live.digests[key.id]
	})
}

// matches reports whether rec was recorded for the settings canonical names.
// A record this process created carries its canonical form and is compared
// directly; a loaded one is checked against its fingerprint.
func (g *Gate) matches(rec *record, canonical string) (matched, corrupt bool) {
	if rec.canonical != nil {
		return *rec.canonical == canonical, false
	}
	if g.hasher == nil || rec.rec.Fingerprint == "" {
		return false, true
	}
	ok, err := g.hasher.Verify(canonical, rec.rec.Fingerprint)
	if err != nil {
		return false, true
	}
	return ok, false
}

// ClearIfMatches clears the recorded state of id when the settings (already
// normalized by the caller) are the ones it was recorded against. It compares
// against the record itself, not the live binding, so it also works with no
// live binding. A rejected optional setting is cleared when the tested
// settings are the live ones.
func (g *Gate) ClearIfMatches(ctx context.Context, id subflux.ProviderID, settings map[string]any) ClearResult {
	canonical, dig := canonicalize(settings)
	g.mu.Lock()
	rec := g.records[id]
	g.mu.Unlock()

	matched := false
	if rec != nil {
		var corrupt bool
		matched, corrupt = g.matches(rec, canonical)
		if corrupt {
			slog.Warn("provider credential record unreadable; test cannot clear it", "provider", id)
			return noRecord
		}
	}

	g.mu.Lock()
	var fx effects
	result := noRecord
	switch current := g.records[id]; {
	case current == nil:
	case current != rec || !matched:
		result = Mismatch
	default:
		g.dropRecord(&fx, id, causeCredentialTestPassed)
		result = Cleared
	}
	if g.clearRejectionsLocked(&fx, id, dig) && result == noRecord {
		result = Cleared
	}
	g.commitLocked(ctx, &fx)
	return result
}

// clearRejectionsLocked clears id's rejected settings when dig is the live
// binding's digest for id, reporting whether any were cleared. Callers hold
// g.mu.
func (g *Gate) clearRejectionsLocked(fx *effects, id subflux.ProviderID, dig digest) bool {
	if g.live == nil || g.liveDigest(id) != dig {
		return false
	}
	cleared := false
	for key := range g.rejected {
		if key.id == id {
			g.clearRejection(fx, key)
			cleared = true
		}
	}
	return cleared
}

// resetAll clears every record, pause and rejected setting.
func (g *Gate) resetAll(ctx context.Context) {
	g.mu.Lock()
	var fx effects
	cleared := make(map[subflux.ProviderID]struct{})
	for id := range g.records {
		g.dropRecord(&fx, id, causeReset)
		cleared[id] = struct{}{}
	}
	for key := range g.pauses {
		if _, done := cleared[key.id]; !done {
			cleared[key.id] = struct{}{}
			fx.event(Event{Provider: key.id, Kind: Enabled})
		}
	}
	clear(g.pauses)
	for key := range g.rejected {
		g.clearRejection(&fx, key)
	}
	if g.live != nil {
		for id := range g.live.digests {
			fx.metric(func() { g.metrics.SetProviderDisabled(id, false) })
		}
	}
	g.commitLocked(ctx, &fx)
}

// dropRecord deletes id's record and queues its re-enable. Callers hold g.mu.
func (g *Gate) dropRecord(fx *effects, id subflux.ProviderID, cause cause) {
	rec := g.records[id]
	if rec == nil {
		return
	}
	delete(g.records, id)
	fx.persist(id)
	if g.live.has(id) && !rec.rec.DisabledAt.IsZero() {
		fx.metric(func() { g.metrics.SetProviderDisabled(id, false) })
	}
	fx.do(func() { slog.Info("provider re-enabled", "provider", id, "cause", string(cause)) })
	fx.event(Event{Provider: id, Kind: Enabled})
}

// clearRejection drops one rejected setting and makes every instance of the
// provider forget its upstream's answer before its next call. Callers hold
// g.mu.
func (g *Gate) clearRejection(fx *effects, key rejectKey) {
	delete(g.rejected, key)
	g.verdicts[key.id]++
	fx.metric(func() { g.metrics.SetProviderSettingRejected(key.id, key.setting, false) })
	fx.event(Event{Provider: key.id, Kind: SettingCleared, Setting: key.setting})
}
