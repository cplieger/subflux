package provider

// Round-2 mutant-killing test for internal/provider/settings.go.
//
// The float64 range guard was tightened from `n > math.MaxInt` to
// `n >= math.MaxInt` (a standalone correctness fix: float64(math.MaxInt) rounds
// up to 2^63, which is NOT representable as an int, so int(n) would yield a
// garbage value). This test pins that behavior so the boundary stays covered:
// it kills the CONDITIONALS_BOUNDARY mutant that loosens `>=` back to `>`.

import (
	"math"
	"testing"
)

func TestGkSubfluxR2_SettingIntRejectsUnrepresentableFloat(t *testing.T) {
	// float64(math.MaxInt) == 2^63, one past the int64 maximum (2^63-1).
	settings := map[string]any{"k": float64(math.MaxInt)}
	if got := SettingInt(settings, SettingKey("k"), 7); got != 7 {
		t.Errorf("SettingInt(float64(math.MaxInt)) = %d, want 7 (out-of-range float must return the default)", got)
	}
}
