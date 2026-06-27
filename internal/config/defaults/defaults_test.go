package defaults

import (
	"testing"
	"time"
)

// TestFormatDuration pins FormatDuration's output across every unit branch and
// the boundaries between them: a value that is an exact multiple of a unit is
// rendered in that unit (730h -> "1M", 24h -> "1D", 60s stays "1m"), while a
// value one step off the multiple falls through to the next-smaller unit
// (731h -> "731h", 25h -> "25h", 90m -> "90m"). Sub-second precision truncates
// to whole seconds.
func TestFormatDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		want  string
		input time.Duration
	}{
		{name: "zero", input: 0, want: "0s"},

		// Seconds tail.
		{name: "one second", input: time.Second, want: "1s"},
		{name: "forty-five seconds", input: 45 * time.Second, want: "45s"},
		{name: "thirty seconds", input: 30 * time.Second, want: "30s"},
		{name: "ninety seconds is not a clean minute", input: 90 * time.Second, want: "90s"},
		{name: "sub-second truncates to whole seconds", input: 1500 * time.Millisecond, want: "1s"},

		// Minutes branch.
		{name: "one minute", input: time.Minute, want: "1m"},
		{name: "five minutes", input: 5 * time.Minute, want: "5m"},
		{name: "thirty minutes", input: 30 * time.Minute, want: "30m"},
		{name: "ninety minutes is not a clean hour", input: 90 * time.Minute, want: "90m"},

		// Hours branch.
		{name: "one hour", input: time.Hour, want: "1h"},
		{name: "three hours", input: 3 * time.Hour, want: "3h"},
		{name: "twenty-five hours is not a clean day", input: 25 * time.Hour, want: "25h"},

		// Days branch (hours a multiple of 24, below one month).
		{name: "one day", input: 24 * time.Hour, want: "1D"},
		{name: "two days", input: 48 * time.Hour, want: "2D"},
		{name: "seven days", input: 7 * 24 * time.Hour, want: "7D"},
		{name: "thirty days", input: 30 * 24 * time.Hour, want: "30D"},
		{name: "sixty days", input: 60 * 24 * time.Hour, want: "60D"},
		{name: "ninety days", input: 90 * 24 * time.Hour, want: "90D"},

		// Months branch (exact multiples of 730h) and its boundary.
		{name: "one month is exactly 730h", input: 730 * time.Hour, want: "1M"},
		{name: "three months", input: 3 * 730 * time.Hour, want: "3M"},
		{name: "one hour over a month is not collapsed", input: 731 * time.Hour, want: "731h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := FormatDuration(tt.input); got != tt.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
