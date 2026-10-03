package testsupport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/subflux"
)

// NopGateMetrics is a providergate.Metrics that records nothing.
type NopGateMetrics struct{}

// SetProviderDisabled records nothing.
func (NopGateMetrics) SetProviderDisabled(subflux.ProviderID, bool) {}

// DeleteProviderDisabled records nothing.
func (NopGateMetrics) DeleteProviderDisabled(subflux.ProviderID) {}

// IncProviderAuthFailure records nothing.
func (NopGateMetrics) IncProviderAuthFailure(subflux.ProviderID) {}

// IncProviderRateLimited records nothing.
func (NopGateMetrics) IncProviderRateLimited(subflux.ProviderID, providergate.Op) {}

// SetProviderSettingRejected records nothing.
func (NopGateMetrics) SetProviderSettingRejected(subflux.ProviderID, string, bool) {}

// ProviderGateBinding returns a binding over a memory-only gate that was never
// activated, so every provider starts admitted and the ladder still applies.
func ProviderGateBinding() *providergate.Binding {
	g, err := providergate.Open(context.Background(), providergate.Config{Metrics: NopGateMetrics{}})
	if err != nil {
		panic("testsupport: " + err.Error())
	}
	return g.Bind(nil)
}

// AuthRecordStore is an in-memory providergate.Store that keeps the last
// record written for each provider.
type AuthRecordStore struct {
	recs map[subflux.ProviderID]subflux.ProviderAuthRecord
	mu   sync.Mutex
}

// NewAuthRecordStore returns an empty store.
func NewAuthRecordStore() *AuthRecordStore {
	return &AuthRecordStore{recs: map[subflux.ProviderID]subflux.ProviderAuthRecord{}}
}

// ProviderAuthRecords returns nothing, so a gate opened over the store starts
// with every provider admitted.
func (s *AuthRecordStore) ProviderAuthRecords(context.Context) ([]subflux.ProviderAuthRecord, error) {
	return nil, nil
}

// PutProviderAuthRecord keeps rec as the provider's record.
func (s *AuthRecordStore) PutProviderAuthRecord(_ context.Context, rec *subflux.ProviderAuthRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs[rec.Provider] = *rec
	return nil
}

// DeleteProviderAuthRecord drops the provider's record.
func (s *AuthRecordStore) DeleteProviderAuthRecord(_ context.Context, id subflux.ProviderID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recs, id)
	return nil
}

// Record returns the provider's last written record.
func (s *AuthRecordStore) Record(id subflux.ProviderID) subflux.ProviderAuthRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recs[id]
}

// DisableThroughLadder runs call through the binding's admission once per rung
// of the credential ladder, observing each failure and moving the clock past
// the rung's pause with advance, and returns every error text call produced.
// It fails the test when the binding refuses a call before the disable or
// when call succeeds.
func DisableThroughLadder(t testing.TB, b *providergate.Binding, id subflux.ProviderID, op providergate.Op,
	call func() error, advance func(time.Duration),
) []string {
	t.Helper()
	waits := []time.Duration{5 * time.Minute, 30 * time.Minute, 0}
	texts := make([]string, 0, len(waits))
	for _, wait := range waits {
		if ok, why := b.Admit(id, op); !ok {
			t.Fatalf("Admit(%s) refused before the disable: %s", id, why)
		}
		err := call()
		if err == nil {
			t.Fatalf("%s call = nil error, want the refusal", id)
		}
		texts = append(texts, err.Error())
		b.Observe(t.Context(), id, op, err)
		advance(wait)
	}
	return texts
}
