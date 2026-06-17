package defaults

import (
	"testing"
	"time"
)

// gk_subflux_u19_case is one FormatDuration expectation.
type gk_subflux_u19_case struct {
	name string
	in   time.Duration
	want string
}

// gk_subflux_u19_cases hardcodes inputs whose expected output depends on the
// exact comparison/arithmetic operators in FormatDuration, so each surviving
// gremlins mutant in defaults.go changes at least one expectation. Each row
// documents which mutant lines it pins.
var gk_subflux_u19_cases = []gk_subflux_u19_case{
	// d == 0 guard (line 59). The zero case documents the guard; the 59:7
	// CONDITIONALS_NEGATION mutant (`d != 0`) is actually killed by every
	// non-zero row below, since `d != 0` would return "0s" early for them.
	{"zero", 0, "0s"},

	// Months branch (lines 63-64). 730h is exactly one month: months==1 and
	// d == months*730h. Kills 63:18 (d / 730h -> *), 64:12 NEGATION
	// (months>0 -> months<=0), 64:21 (d == ... -> d != ...), 64:45 and 64:49
	// (the *730*time.Hour products -> /). Any of those makes the branch not
	// fire, yielding "730h" instead of "1M".
	{"one_month_exact", 730 * time.Hour, "1M"},
	// 731h: months==1 but d != months*730h, so the months branch is skipped
	// and "731h" results. Complements 64:21 — the != flip would wrongly emit
	// "1M" here.
	{"month_plus_one_hour", 731 * time.Hour, "731h"},

	// Days branch (lines 67-68). 24h: hours==24 exactly. Kills 67:11 BOUNDARY
	// (>= vs >, differ at 24 -> "24h") and 67:11 NEGATION (>= vs <).
	{"one_day_exact", 24 * time.Hour, "1D"},
	// 48h: hours==48, a multiple of 24 where +,-,*,/ all differ from % and
	// 48/24==2. Kills 67:25 (hours%24 -> other op), 67:29 (== 0 -> != 0) and
	// 68:17 (hours/24 -> other op); each changes the result away from "2D".
	{"two_days_exact", 48 * time.Hour, "2D"},
	// 25h: hours not a multiple of 24, so the days branch is skipped and
	// "25h" results. Complements 67:29 — the != flip would wrongly emit "1D".
	{"day_plus_one_hour", 25 * time.Hour, "25h"},
	// 30m: hours==0 so the days branch must NOT fire. Complements 67:11
	// NEGATION — `hours < 24 && 0%24==0` would wrongly emit "0D".
	{"thirty_minutes", 30 * time.Minute, "30m"},

	// Hours branch (line 71). 1h: hours==1 and d == hours*time.Hour. Kills
	// 71:11 NEGATION (hours>0 -> hours<=0), 71:20 (d == ... -> d != ...) and
	// 71:43 (*time.Hour -> /); each yields "60m" instead of "1h".
	{"one_hour_exact", time.Hour, "1h"},
	// 90m: hours==1 but d != hours*time.Hour, so the hours branch is skipped
	// and "90m" results. Complements 71:20 — the != flip would wrongly emit
	// "1h".
	{"ninety_minutes", 90 * time.Minute, "90m"},

	// Minutes branch (line 75). 1m: mins==1 and d == mins*time.Minute. Kills
	// 75:10 NEGATION (mins>0 -> mins<=0), 75:19 (d == ... -> d != ...) and
	// 75:41 (*time.Minute -> /); each yields "60s" instead of "1m".
	{"one_minute_exact", time.Minute, "1m"},
	// 90s: mins==1 but d != mins*time.Minute, so the minutes branch is skipped
	// and "90s" results. Complements 75:19 — the != flip would wrongly emit
	// "1m".
	{"ninety_seconds", 90 * time.Second, "90s"},
	// 30s: mins==0 so the minutes branch must NOT fire; falls through to the
	// seconds tail. Complements 75:10 NEGATION (the mins<=0 path).
	{"thirty_seconds", 30 * time.Second, "30s"},
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
