package arrapi

// Round-2 mutant-killing test for internal/arrapi.
//
// Kills client.go:45:27 ARITHMETIC_BASE (`90 * time.Second` -> `90 / time.Second`).
// The `/` mutant yields 90 / 1e9 == 0, leaving IdleConnTimeout disabled.

import (
	"testing"
	"time"
)

func TestGkSubfluxR2_DefaultTransportIdleConnTimeout(t *testing.T) {
	tr := defaultTransport()
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("defaultTransport().IdleConnTimeout = %v, want 90s", tr.IdleConnTimeout)
	}
}
