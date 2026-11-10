package mediarefresh

import (
	"context"
	"time"
)

// A sink's retry delays, for a failed feed read, an outage and a root's own
// failure alike.
const (
	retryMin = 30 * time.Second
	retryMax = 10 * time.Minute
)

// backoff is a sink's retry delay, retryMin doubling to retryMax. Its zero
// value starts at retryMin.
type backoff struct{ last time.Duration }

// next is the delay before the next retry.
func (b *backoff) next() time.Duration {
	b.last = min(max(2*b.last, retryMin), retryMax)
	return b.last
}

// reset starts the delays over at retryMin.
func (b *backoff) reset() { b.last = 0 }

// sleep waits d; false when ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
