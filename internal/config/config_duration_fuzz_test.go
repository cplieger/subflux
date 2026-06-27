package config

import "testing"

// FuzzParseDuration feeds arbitrary strings through the extended duration
// parser. Beyond "never panics", any successful parse of an extended unit
// (D/M/Y) must be non-negative: parseMul rejects negative, non-finite, and
// overflowing values, so a negative result would mean that guard was bypassed
// (these durations drive scan intervals and backoff timers).
func FuzzParseDuration(f *testing.F) {
	f.Add("5m")
	f.Add("1h")
	f.Add("7D")
	f.Add("3M")
	f.Add("1Y")
	f.Add("")
	f.Add("invalid")
	f.Add("-3M")
	f.Add("0D")

	f.Fuzz(func(t *testing.T, s string) {
		d, err := ParseDuration(s)
		if err != nil {
			return
		}
		if n := len(s); n > 0 {
			switch s[n-1] {
			case 'D', 'M', 'Y':
				if d < 0 {
					t.Errorf("ParseDuration(%q) = %v, want non-negative for extended unit", s, d)
				}
			}
		}
	})
}
