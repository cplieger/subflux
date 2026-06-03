package metrics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"subflux/internal/api"
)

func BenchmarkRender_20_metrics(b *testing.B) {
	for _, nProviders := range []int{1, 5, 20} {
		b.Run(fmt.Sprintf("providers_%d", nProviders), func(b *testing.B) {
			m := New()
			providers := make([]api.ProviderID, nProviders)
			for i := range providers {
				providers[i] = api.ProviderID(fmt.Sprintf("provider-%d", i))
			}

			for _, p := range providers {
				for range 10 {
					m.RecordSearch(p, 100*time.Millisecond, nil)
				}
			}

			handler := m.Handler()
			b.ResetTimer()
			for range b.N {
				w := httptest.NewRecorder()
				handler(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			}
		})
	}
}
