package api

import (
	"strings"
	"testing"
)

// TestValidRequestID_length_boundaries pins the inclusive [1,64] length window
// of validRequestID: 1 and 64 chars are accepted, 0 and 65 are rejected. The
// allowed character class is covered by FuzzValidRequestID; this isolates the
// length limits so a boundary slip (< vs <=, > vs >=) is caught.
func TestValidRequestID_length_boundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"single char valid", "a", true},
		{"len 64 valid", strings.Repeat("a", 64), true},
		{"empty invalid", "", false},
		{"len 65 invalid", strings.Repeat("a", 65), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := validRequestID(tc.in); got != tc.want {
				t.Errorf("validRequestID(len=%d) = %v, want %v", len(tc.in), got, tc.want)
			}
		})
	}
}
