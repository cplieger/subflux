package defaults

import (
	"testing"
	"time"
)

// gk_subflux_u19_case is one FormatDuration expectation.
type gk_subflux_u19_case struct {
	name string
	want string
	in   time.Duration
}

// gk_subflux_u19_cases hardcodes inputs whose expected output depends on the
// exact comparison/arithmetic operators in FormatDuration, so each surviving
// gremlins mutant in defaults.go changes at least one expectation. Each row
// documents which mutant lines it pins.
var gk_subflux_u19_cases = []gk_subflux_u19_case{
	// d == 0 guard (line 59). The zero case documents the guard; the 59:7
	// CONDITIONALS_NEGATION mutant (`d != 0`) is actually killed by every
	// non-zero row below, since `d != 0` would return "0s" early for them.
	{name: "zero", in: 0, want: "0s"},

	// Months branch (lines 63-64). 730h is exactly one month: months==1 and
	// d == months*730h. Kills 63:18 (d / 730h -> *), 64:12 NEGATION
	// (months>0 -> months<=0), 64:21 (d == ... -> d != ...), 64:45 and 64:49
	// (the *730*time.Hour products -> /). Any of those makes the branch not
	// fire, yielding "730h" instead of "1M".
	{name: "one_month_exact", in: 730 * time.Hour, want: "1M"},
	// 731h: months==1 but d != months*730h, so the months branch is skipped
	// and "731h" results. Complements 64:21 — the != flip would wrongly emit
	// "1M" here.
	{name: "month_plus_one_hour", in: 731 * time.Hour, want: "731h"},

	// Days branch (lines 67-68). 24h: hours==24 exactly. Kills 67:11 BOUNDARY
	// (>= vs >, differ at 24 -> "24h") and 67:11 NEGATION (>= vs <).
	{name: "one_day_exact", in: 24 * time.Hour, want: "1D"},
	// 48h: hours==48, a multiple of 24 where +,-,*,/ all differ from % and
	// 48/24==2. Kills 67:25 (hours%24 -> other op), 67:29 (== 0 -> != 0) and
	// 68:17 (hours/24 -> other op); each changes the result away from "2D".
	{name: "two_days_exact", in: 48 * time.Hour, want: "2D"},
	// 25h: hours not a multiple of 24, so the days branch is skipped and
	// "25h" results. Complements 67:29 — the != flip would wrongly emit "1D".
	{name: "day_plus_one_hour", in: 25 * time.Hour, want: "25h"},
	// 30m: hours==0 so the days branch must NOT fire. Complements 67:11
	// NEGATION — `hours < 24 && 0%24==0` would wrongly emit "0D".
	{name: "thirty_minutes", in: 30 * time.Minute, want: "30m"},

	// Hours branch (line 71). 1h: hours==1 and d == hours*time.Hour. Kills
	// 71:11 NEGATION (hours>0 -> hours<=0), 71:20 (d == ... -> d != ...) and
	// 71:43 (*time.Hour -> /); each yields "60m" instead of "1h".
	{name: "one_hour_exact", in: time.Hour, want: "1h"},
	// 90m: hours==1 but d != hours*time.Hour, so the hours branch is skipped
	// and "90m" results. Complements 71:20 — the != flip would wrongly emit
	// "1h".
	{name: "ninety_minutes", in: 90 * time.Minute, want: "90m"},

	// Minutes branch (line 75). 1m: mins==1 and d == mins*time.Minute. Kills
	// 75:10 NEGATION (mins>0 -> mins<=0), 75:19 (d == ... -> d != ...) and
	// 75:41 (*time.Minute -> /); each yields "60s" instead of "1m".
	{name: "one_minute_exact", in: time.Minute, want: "1m"},
	// 90s: mins==1 but d != mins*time.Minute, so the minutes branch is skipped
	// and "90s" results. Complements 75:19 — the != flip would wrongly emit
	// "1m".
	{name: "ninety_seconds", in: 90 * time.Second, want: "90s"},
	// 30s: mins==0 so the minutes branch must NOT fire; falls through to the
	// seconds tail. Complements 75:10 NEGATION (the mins<=0 path).
	{name: "thirty_seconds", in: 30 * time.Second, want: "30s"},
}

// TestGkSubfluxU19FormatDuration pins FormatDuration's exact output for inputs
// that lie on the boundaries the surviving mutants perturb.
func TestGkSubfluxU19FormatDuration(t *testing.T) {
	for _, tc := range gk_subflux_u19_cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatDuration(tc.in)
			if got != tc.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
