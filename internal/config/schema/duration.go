package schema

import (
	"strconv"
	"time"
)

// formatDuration converts a time.Duration to a human-friendly config string.
// Prefers the largest clean unit (M > D > h > m > s). Sub-second precision
// is truncated to whole seconds; callers should only pass durations that
// are meaningful at second granularity or above.
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	hours := int(d.Hours())
	months := int(d / (730 * time.Hour))
	if months > 0 && d == time.Duration(months)*730*time.Hour {
		return strconv.Itoa(months) + "M"
	}
	if hours >= 24 && hours%24 == 0 {
		days := hours / 24
		return strconv.Itoa(days) + "D"
	}
	if hours > 0 && d == time.Duration(hours)*time.Hour {
		return strconv.Itoa(hours) + "h"
	}
	mins := int(d.Minutes())
	if mins > 0 && d == time.Duration(mins)*time.Minute {
		return strconv.Itoa(mins) + "m"
	}
	secs := int(d.Seconds())
	return strconv.Itoa(secs) + "s"
}
