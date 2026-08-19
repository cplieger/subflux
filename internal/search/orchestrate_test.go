package search

import (
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

func TestProviderConcurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		max  int
		want int
	}{
		{"zero returns default", 0, subflux.DefaultProviderConcurrency},
		{"positive returns value", 8, 8},
		{"one returns one", 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &mockConfig{searchCfg: subflux.SearchConfig{MaxProviderConcurrency: tt.max}}
			e := &Engine{cfg: cfg}
			got := e.providerConcurrency()
			if got != tt.want {
				t.Errorf("providerConcurrency() = %d, want %d", got, tt.want)
			}
		})
	}
}
