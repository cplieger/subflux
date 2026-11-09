package statedb

import (
	"context"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
)

func failureOf(ref account.FailureRef, r *failureRow) account.FailureCount {
	return account.FailureCount{LockedAt: r.LockedAt, FailureRef: ref, Count: r.Count, Locked: r.Locked, Unreported: r.Unreported}
}

func (r authReader) failure(ref account.FailureRef) (account.FailureCount, bool, error) {
	row, ok, err := r.a.failures.Get(r.r, ref)
	if err != nil || !ok {
		return account.FailureCount{}, false, err
	}
	return failureOf(ref, &row), true, nil
}

// unreported lists the records with Unreported set, through the sparse
// unreported index.
func (r authReader) unreported() ([]account.FailureCount, error) {
	out := []account.FailureCount{}
	err := r.a.unreported.Scan(r.r, bolttable.Range{}, func(_ []byte, ref account.FailureRef, _ []byte) (bool, error) {
		f, ok, err := r.failure(ref)
		if ok {
			out = append(out, f)
		}
		return err == nil, err
	})
	return out, err
}

func (x *accountTx) AuthFailure(_ context.Context, ref account.FailureRef) (account.FailureCount, bool, error) {
	return x.read().failure(ref)
}

// PutAuthFailure upserts f's record.
func (x *accountTx) PutAuthFailure(_ context.Context, f account.FailureCount) error { //nolint:gocritic // hugeParam: account.FailureStore's signature
	row := failureRow{LockedAt: f.LockedAt, Count: f.Count, Locked: f.Locked, Unreported: f.Unreported}
	return x.s.a.failures.Put(x.tx, f.FailureRef, &row)
}

func (x *accountTx) UnreportedLocks(context.Context) ([]account.FailureCount, error) {
	return x.read().unreported()
}

// DeleteAuthFailures deletes name's records of each listed method in both
// trust contexts, skipping absent ones.
func (x *accountTx) DeleteAuthFailures(_ context.Context, name string, methods []access.Method) error {
	for _, m := range methods {
		for _, trusted := range []bool{false, true} {
			if _, err := x.s.a.failures.Delete(x.tx, account.FailureRef{Name: name, Method: m, Trusted: trusted}); err != nil {
				return err
			}
		}
	}
	return nil
}

// PruneAuthFailures deletes every record whose Name is not in keep and
// returns how many it deleted.
func (x *accountTx) PruneAuthFailures(_ context.Context, keep []string) (int64, error) {
	kept := make(map[string]bool, len(keep))
	for _, name := range keep {
		kept[name] = true
	}
	var drop []account.FailureRef
	if err := x.s.a.failures.Scan(x.tx.Read(), bolttable.Range{}, func(ref account.FailureRef, _ *failureRow) (bool, error) {
		if !kept[ref.Name] {
			drop = append(drop, ref)
		}
		return true, nil
	}); err != nil {
		return 0, err
	}
	for _, ref := range drop {
		if _, err := x.s.a.failures.Delete(x.tx, ref); err != nil {
			return 0, err
		}
	}
	return int64(len(drop)), nil
}

// AuthFailure reads one failure record.
func (a *AccountStore) AuthFailure(ctx context.Context, ref account.FailureRef) (account.FailureCount, bool, error) {
	return viewFound(ctx, a, func(r authReader) (account.FailureCount, bool, error) { return r.failure(ref) })
}

// PutAuthFailure upserts f's record.
func (a *AccountStore) PutAuthFailure(ctx context.Context, f account.FailureCount) error { //nolint:gocritic // hugeParam: account.FailureStore's signature
	return a.single(ctx, "put failure", func(x *accountTx) error { return x.PutAuthFailure(ctx, f) })
}

// UnreportedLocks lists the records with Unreported set.
func (a *AccountStore) UnreportedLocks(ctx context.Context) ([]account.FailureCount, error) {
	return viewList(ctx, a, authReader.unreported)
}

// DeleteAuthFailures deletes name's records of each listed method in both trust contexts.
func (a *AccountStore) DeleteAuthFailures(ctx context.Context, name string, methods []access.Method) error {
	return a.single(ctx, "delete failures", func(x *accountTx) error { return x.DeleteAuthFailures(ctx, name, methods) })
}

// PruneAuthFailures deletes every record whose Name is not in keep and returns how many.
func (a *AccountStore) PruneAuthFailures(ctx context.Context, keep []string) (int64, error) {
	return singleValue(ctx, a, "prune failures", func(x *accountTx) (int64, error) { return x.PruneAuthFailures(ctx, keep) })
}
