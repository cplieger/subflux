package metrics

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"subflux/internal/api"
)

func TestGetOrCreateProvider_concurrent(t *testing.T) {
	t.Parallel()
	m := New()

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			providers := []api.ProviderID{
				api.ProviderID(fmt.Sprintf("prov-%d-a", id)),
				api.ProviderID(fmt.Sprintf("prov-%d-b", id)),
			}
			for _, p := range providers {
				m.RecordSearch(p, time.Millisecond, nil)
			}
		}(i)
	}
	wg.Wait()
}
