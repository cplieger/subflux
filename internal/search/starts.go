package search

import (
	"cmp"
	"context"

	"github.com/cplieger/go/keylock"
	"github.com/cplieger/subflux/internal/status"
)

// Starts is the process-wide download-start coordinator: app makes one and
// every engine shares it, so a same-key Download resend that crosses an
// engine swap waits for its original and finds its job.
type Starts struct {
	keys *keylock.Set[startKey]
}

type startKey struct {
	key   string
	owner int64
}

// NewStarts makes the coordinator app passes every engine.
func NewStarts() *Starts {
	return &Starts{keys: keylock.New(func(a, b startKey) int {
		return cmp.Or(cmp.Compare(a.owner, b.owner), cmp.Compare(a.key, b.key))
	})}
}

// lock holds owner's idem key until unlock; with no key there is nothing to
// hold. Its error is ctx's.
func (s *Starts) lock(ctx context.Context, owner int64, idem *status.JobKey) (unlock func(), err error) {
	if idem == nil {
		return func() {}, nil
	}
	return s.keys.Lock(ctx, startKey{owner: owner, key: idem.Key})
}
