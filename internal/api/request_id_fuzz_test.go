package api

import "testing"

// FuzzValidRequestID exercises validRequestID with arbitrary byte sequences
// to verify it never panics and that accepted strings conform to the
// documented shape: 1..64 chars of [a-zA-Z0-9_-].
func FuzzValidRequestID(f *testing.F) {
	f.Add("abc-123_DEF")
	f.Add("")
	f.Add("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") // 65 chars
	f.Add("evil\ninjected")
	f.Add("café")
	f.Add("valid_ID-01")

	f.Fuzz(func(t *testing.T, s string) {
		ok := validRequestID(s)
		if !ok {
			return
		}
		// If accepted, verify the documented invariants hold.
		if len(s) < 1 || len(s) > 64 {
			t.Errorf("validRequestID(%q) = true but len=%d outside [1,64]", s, len(s))
		}
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= 'A' && r <= 'Z':
			case r >= '0' && r <= '9':
			case r == '-' || r == '_':
			default:
				t.Errorf("validRequestID(%q) = true but contains invalid rune %q", s, r)
			}
		}
	})
}
