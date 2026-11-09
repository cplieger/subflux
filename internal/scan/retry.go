package scan

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/cplieger/subflux/internal/mediasource"
)

// retryRoot is a root of the retry set: re-read as a one-root pass once due,
// and left out of every batch until then.
type retryRoot struct {
	due, at time.Time
	err     *mediasource.SourceError
	backoff time.Duration
}

// retryLater puts ref in the retry set after a failed read, or doubles its
// wait when it was there, from one minute up to 30.
func (l *loop) retryLater(ref mediasource.NativeRef, se *mediasource.SourceError, now time.Time) {
	r := l.retry[ref]
	r.backoff = min(max(2*r.backoff, retryMin), retryMax)
	r.due, r.at, r.err = now.Add(r.backoff), now, se
	l.retry[ref] = r
}

// deferUnread puts a root a batch left unread in the retry set at the first
// wait; a root already there keeps its own.
func (l *loop) deferUnread(ref mediasource.NativeRef, se *mediasource.SourceError, now time.Time) {
	if _, ok := l.retry[ref]; !ok {
		l.retry[ref] = retryRoot{due: now.Add(retryMin), at: now, backoff: retryMin, err: se}
	}
}

// dueRetries lists the retry set's roots due at now, in ref order.
func (l *loop) dueRetries(now time.Time) []mediasource.NativeRef {
	var out []mediasource.NativeRef
	for ref, r := range l.retry {
		if !now.Before(r.due) {
			out = append(out, ref)
		}
	}
	slices.SortFunc(out, compareRefs)
	return out
}

// sortedRefs lists m's keys in ref order.
func sortedRefs[V any](m map[mediasource.NativeRef]V) []mediasource.NativeRef {
	return slices.SortedFunc(maps.Keys(m), compareRefs)
}

func compareRefs(a, b mediasource.NativeRef) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), strings.Compare(string(a.ID), string(b.ID)))
}

// waiting reports whether ref is in the retry set and not yet due.
func (l *loop) waiting(ref mediasource.NativeRef, now time.Time) bool {
	r, ok := l.retry[ref]
	return ok && now.Before(r.due)
}
