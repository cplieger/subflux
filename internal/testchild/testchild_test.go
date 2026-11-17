package testchild

import "testing"

func TestGORACE(t *testing.T) {
	for _, tt := range []struct{ name, inherited, want string }{
		{"none inherited", "", "atexit_sleep_ms=0"},
		{"inherited options kept", "halt_on_error=1", "halt_on_error=1 atexit_sleep_ms=0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GORACE", tt.inherited)
			if got := GORACE(); got != tt.want {
				t.Errorf("GORACE() with GORACE=%q = %q, want %q", tt.inherited, got, tt.want)
			}
		})
	}
}
