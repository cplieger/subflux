package subsync

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
)

// alignEvent represents a change in the rating derivative at a specific offset.
type alignEvent struct {
	offset int64
	delta  float64
}

// alignMergeSort uses sorted events for sparse offset ranges where a full
// bucket array would waste memory. It generates 4 events per reference/incorrect
// span pair (the breakpoints of the piecewise-linear rating derivative), sorts
// them by offset, then sweeps through to find the offset with the highest
// cumulative rating.
func alignMergeSort(ctx context.Context, ref, inc []TimeSpan, minOffset int64) int64 {
	capacity := min(int64(len(ref))*int64(len(inc))*4, maxAlignEvents)
	events := make([]alignEvent, 0, capacity)

outer:
	for _, r := range ref {
		for _, s := range inc {
			score := spanScore(r, s)
			if score == 0 {
				continue
			}
			if int64(len(events))+4 > maxAlignEvents {
				slog.Warn("alignment: event cap reached, truncating",
					"events", len(events), "cap", maxAlignEvents)
				break outer
			}
			events = append(events,
				alignEvent{r.Start - s.End, score},
				alignEvent{r.End - s.End, -score},
				alignEvent{r.Start - s.Start, -score},
				alignEvent{r.End - s.Start, score},
			)
		}
	}

	if ctx.Err() != nil {
		return 0
	}

	slices.SortFunc(events, func(a, b alignEvent) int {
		return cmp.Compare(a.offset, b.offset)
	})

	var derivative, rating, bestRating float64
	var bestOffset = minOffset

	for i := range events {
		derivative += events[i].delta

		// candidateOffset tracks the end of the interval where the peak
		// rating occurs. For non-last events, this is events[i+1].offset
		// (start of next interval = end of current). For the last event,
		// it's events[i].offset itself. This differs from bucket sort by
		// up to 1ms due to discrete vs event-based accumulation.
		var gap int64 = 1
		candidateOffset := events[i].offset
		if i+1 < len(events) {
			candidateOffset = events[i+1].offset
			gap = candidateOffset - events[i].offset
		}
		rating += derivative * float64(gap)
		if rating > bestRating {
			bestRating = rating
			bestOffset = candidateOffset
		}
	}

	slog.Debug("alignment complete (merge)",
		"offset_ms", bestOffset, "rating", bestRating, "events", len(events))
	return bestOffset
}
