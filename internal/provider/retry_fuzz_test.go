package provider

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzWrapRetryAll exercises WrapRetryAll with varying provider counts and
// retry parameters, verifying structural invariants.
func FuzzWrapRetryAll(f *testing.F) {
	f.Add(1, 3, int64(2*time.Second))
	f.Add(0, 0, int64(0))
	f.Add(5, 1, int64(time.Millisecond))
	f.Add(10, -1, int64(time.Hour))

	f.Fuzz(func(t *testing.T, count, maxAttempts int, initBackoffNs int64) {
		if count < 0 || count > 50 {
			return // reasonable bounds
		}

		providers := make([]api.Provider, count)
		for i := range count {
			providers[i] = &fakeWrapProvider{name: api.ProviderID(string(rune('a' + i%26)))}
		}

		initBackoff := max(time.Duration(initBackoffNs), 0)

		wrapped := WrapRetryAll(providers, maxAttempts, initBackoff)

		// Invariant 1: output length equals input length.
		if len(wrapped) != count {
			t.Fatalf("WrapRetryAll returned %d providers, want %d", len(wrapped), count)
		}

		// Invariant 2: each wrapped provider preserves the original Name().
		for i, w := range wrapped {
			if w.Name() != providers[i].Name() {
				t.Fatalf("wrapped[%d].Name() = %q, want %q", i, w.Name(), providers[i].Name())
			}
		}
	})
}

type fakeWrapProvider struct {
	name api.ProviderID
}

func (f *fakeWrapProvider) Name() api.ProviderID { return f.name }
func (f *fakeWrapProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return nil, nil
}

func (f *fakeWrapProvider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return nil, nil
}
