package providercalls

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"golang.org/x/time/rate"
)

// Op is what a call does at the provider.
type Op uint8

// The ops; Test is Probe's alone.
const (
	Search Op = iota + 1
	Fetch
	Test
)

var opNames = [...]string{Search: "search", Fetch: "fetch", Test: "test"}

func (o Op) String() string {
	if o >= Search && o <= Test {
		return opNames[o]
	}
	return fmt.Sprintf("Op(%d)", o)
}

// deadline is the op's own bound on its fn, under the caller's context.
func (o Op) deadline() time.Duration {
	switch o {
	case Search:
		return 30 * time.Second
	case Fetch:
		return 60 * time.Second
	default:
		return 20 * time.Second
	}
}

// Class orders waiters for a provider's in-flight slots.
type Class uint8

// The classes.
const (
	Automatic   Class = iota + 1 // the drainer: paced, refused while the provider cools down
	Interactive                  // a user's search, download or test: first in line, never paced
)

var (
	// ErrReleased refuses a call on a binding whose engine was replaced, so
	// it would run under settings no longer live.
	ErrReleased = errors.New("providercalls: the binding's settings were replaced")
	// ErrCredentialDisabled refuses a call under a credential the provider
	// refused three times; a passing Probe of it lifts the refusal.
	ErrCredentialDisabled = errors.New("providercalls: the credential is disabled")

	errBadCall = errors.New("providercalls: Call takes Search or Fetch, Automatic or Interactive")
)

// BackoffError refuses a call, sending nothing, until the provider's pause,
// cooldown or credential wait lifts at Until.
type BackoffError struct {
	Until    time.Time
	Provider provider.ProviderID
}

func (e *BackoffError) Error() string {
	return fmt.Sprintf("providercalls: %s backs off until %s", e.Provider, e.Until.UTC().Format(time.RFC3339))
}

// credentialHasher fingerprints a credential for its disable record and
// checks a credential against a stored fingerprint.
type credentialHasher interface {
	Hash(ctx context.Context, credential string) (string, error)
	Verify(ctx context.Context, fingerprint, credential string) (bool, error)
}

// Funnel holds every provider's call state; app builds one per process and
// every engine binds to it.
type Funnel struct {
	db        *statedb.Store
	hasher    credentialHasher
	status    *status.Store
	metrics   *funnelMetrics
	providers map[provider.ProviderID]*providerState
	mu        sync.Mutex
}

// NewFunnel builds the funnel over the disable records in db, the hasher
// that fingerprints them and the status store its states are reported to.
// Every argument is required; a nil one panics.
func NewFunnel(db *statedb.Store, h credentialHasher, st *status.Store) *Funnel {
	if db == nil || h == nil || st == nil {
		panic("providercalls: NewFunnel needs a state store, a hasher and a status store")
	}
	return &Funnel{db: db, hasher: h, status: st, metrics: newFunnelMetrics(), providers: map[provider.ProviderID]*providerState{}}
}

// Bind returns a binding for one provider's credential, the keyenc join of
// the entry's Secret and Required String values ("" for a provider without
// one), and makes it the provider's most recently bound. interval spaces the
// provider's Automatic calls (search.scan_delay); 0 or less is no spacing.
func (f *Funnel) Bind(id provider.ProviderID, credential string, interval time.Duration) *Binding {
	p := f.provider(id)
	digest := sha256.Sum256([]byte(credential))
	limit := rate.Inf
	if interval > 0 {
		limit = rate.Every(interval)
	}
	p.mu.Lock()
	p.credential(digest)
	p.latest = digest
	p.limiter.SetLimit(limit)
	p.mu.Unlock()
	return &Binding{f: f, p: p, credential: credential, digest: digest}
}

func (f *Funnel) provider(id provider.ProviderID) *providerState {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.providers[id]
	if !ok {
		p = newProviderState(id)
		f.providers[id] = p
	}
	return p
}
