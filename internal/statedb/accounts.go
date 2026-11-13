package statedb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
)

// AccountStore is v7's account.Store over the store's auth namespace
// (design section 9). Every single-call write and every Update runs through
// db.Batch, so concurrent sign-ins share one commit; reads run in one View.
type AccountStore struct{ s *Store }

// Accounts returns the account store over s's auth namespace.
func (s *Store) Accounts() *AccountStore { return &AccountStore{s: s} }

// Update runs fn in one db.Batch transaction. db.Batch may run fn more than
// once; only its last run's writes commit, and only the committed run's
// assignments into the records fn passes to tx stay (rule 5).
func (a *AccountStore) Update(ctx context.Context, fn func(tx account.Tx) error) error {
	return a.single(ctx, "accounts update", func(x *accountTx) error { return fn(x) })
}

// single runs fn as one single-call write through db.Batch.
func (a *AccountStore) single(ctx context.Context, op string, fn func(*accountTx) error) error {
	return a.s.accountWrite(ctx, op, a.s.db.Batch, fn)
}

// accountWrite is the one path a write handing out an accountTx takes: each
// run starts from the caller's records as they were before the call, and a
// call that does not commit leaves them so.
func (s *Store) accountWrite(ctx context.Context, op string, run runner, fn func(*accountTx) error) error {
	e := &callerEdits{}
	committed := false
	defer func() {
		if !committed {
			e.revert()
		}
	}()
	err := s.write(ctx, op, run, func(tx *bolttable.Tx) error {
		e.revert()
		return fn(&accountTx{s: s, tx: tx, edits: e})
	})
	committed = err == nil
	return err
}

// singleValue runs fn as one single-call write and returns the value of the
// run that committed, or the zero value with the call's error.
func singleValue[T any](ctx context.Context, a *AccountStore, op string, fn func(*accountTx) (T, error)) (T, error) {
	var v T
	err := a.single(ctx, op, func(x *accountTx) (err error) {
		v, err = fn(x)
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return v, nil
}

func (a *AccountStore) reader(ctx context.Context, read func(authReader) error) error {
	return a.s.db.View(ctx, func(r *bolttable.ReadTx) error { return read(authReader{a: a.s.a, r: r}) })
}

// viewFound answers one by-key read in its own View.
func viewFound[T any](ctx context.Context, a *AccountStore, read func(authReader) (T, bool, error)) (v T, found bool, err error) {
	err = a.reader(ctx, func(r authReader) (err error) {
		v, found, err = read(r)
		return err
	})
	return v, found, err
}

// viewList answers one list read in its own View.
func viewList[T any](ctx context.Context, a *AccountStore, read func(authReader) (T, error)) (v T, err error) {
	err = a.reader(ctx, func(r authReader) (err error) {
		v, err = read(r)
		return err
	})
	return v, err
}

// The backend errors: a caller fault the account package has no sentinel
// for (rule 12). account answers each as access.ErrAuthBackend.
var (
	errNoHash         = errors.New("statedb: the record has no hash")
	errNoSessionID    = errors.New("statedb: the session has no id")
	errNoCredentialID = errors.New("statedb: the passkey has no credential id")
	errNoUser         = errors.New("statedb: the record names no user")
	errRowExists      = errors.New("statedb: the record is already stored")
	errIDRange        = errors.New("statedb: the next id is above 2^53 - 1")
	errRotationRef    = errors.New("statedb: a rotation's next session changes its id or user")
	errCleanupLimit   = errors.New("statedb: a cleanup limit below 1")
	errCleanupTimeout = errors.New("statedb: a cleanup with a timeout not above zero")
)

// drawID is the sequence an account ID or log Seq is drawn from; a test
// swaps it to reach the 2^53 - 1 bound of rules 17 and 22 without that many
// draws.
var drawID = func(tx *bolttable.Tx, next func(*bolttable.Tx) (uint64, error)) (uint64, error) { return next(tx) }

// newSeq assigns the next number of t's sequence, which a delete never
// lowers and a rollback returns, so no committed number is assigned twice
// (rules 17 and 22).
func newSeq[K, V any](tx *bolttable.Tx, t *bolttable.Table[K, V]) (uint64, error) {
	n, err := drawID(tx, t.NextID)
	if err != nil {
		return 0, err
	}
	if n == 0 || n > maxID {
		return 0, fmt.Errorf("%w: %d", errIDRange, n)
	}
	return n, nil
}

// newID is newSeq as the int64 ID an account record carries.
func newID[K, V any](tx *bolttable.Tx, t *bolttable.Table[K, V]) (int64, error) {
	n, err := newSeq(tx, t)
	return int64(n), err //nolint:gosec // G115: newSeq bounds n to 2^53 - 1
}

// conflictErr maps a unique-index conflict to the sentinel its index answers
// (rule 4); a conflict on any other index is a backend error.
func conflictErr(err error) error {
	var c *bolttable.ConflictError
	if !errors.As(err, &c) {
		return err
	}
	switch c.Index {
	case ixUsername:
		return fmt.Errorf("%w: %w", account.ErrUsernameTaken, err)
	case ixOIDC:
		return fmt.Errorf("%w: %w", account.ErrOIDCIdentityTaken, err)
	case ixCredential:
		return fmt.Errorf("%w: %w", account.ErrDuplicateCredential, err)
	}
	return fmt.Errorf("statedb: %w", err)
}

// CleanupExpiredSessions deletes, in one db.Batch, at most limit of the
// sessions access.ValidateSession refuses at now, in key order, and returns
// the ref of each it deleted (rule 14).
func (a *AccountStore) CleanupExpiredSessions(ctx context.Context, now time.Time, timeouts access.SessionTimeouts, limit int) ([]access.SessionRef, error) {
	switch {
	case limit < 1:
		return nil, errCleanupLimit
	case timeouts.Idle <= 0 || timeouts.Absolute <= 0:
		return nil, errCleanupTimeout
	}
	return singleValue(ctx, a, "cleanup sessions", func(x *accountTx) ([]access.SessionRef, error) {
		return x.deleteExpiredSessions(now, timeouts, limit)
	})
}

// deleteExpiredSessions deletes at most limit sessions ValidateSession
// refuses at now and returns their refs.
func (x *accountTx) deleteExpiredSessions(now time.Time, timeouts access.SessionTimeouts, limit int) ([]access.SessionRef, error) {
	refs := []access.SessionRef{}
	err := x.s.a.sessions.Scan(x.tx.Read(), bolttable.Range{}, func(ref access.SessionRef, row *sessionRow) (bool, error) {
		sess, err := x.read().session(ref, row)
		if err != nil {
			return false, err
		}
		if access.ValidateSession(sess, timeouts, now) != nil {
			refs = append(refs, ref)
		}
		return len(refs) < limit, nil
	})
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if _, err := x.s.a.sessions.Delete(x.tx, ref); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

// CleanupExpiredOneTimeTokens deletes every token whose ExpiresAt is not
// after now and returns how many it deleted.
func (a *AccountStore) CleanupExpiredOneTimeTokens(ctx context.Context, now time.Time) (int64, error) {
	n, err := singleValue(ctx, a, "cleanup tokens", func(x *accountTx) (int, error) {
		return x.deleteTokensIn(x.s.a.tokensByExpiry, bolttable.Range{To: append(appendTime(nil, now), 0)})
	})
	return int64(n), err
}
