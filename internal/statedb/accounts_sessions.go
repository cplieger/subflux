package statedb

import (
	"context"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
)

func sessionRowOf(s *access.Session) sessionRow {
	return sessionRow{
		CreatedAt: s.CreatedAt, AuthTime: s.AuthTime, TokenHash: s.TokenHash.Reveal(), AuthMethod: string(s.AuthMethod),
		Kind: string(s.Kind), UserAgent: s.UserAgent, Label: s.Label, IssuedByKey: s.IssuedByKey,
		IdleCap: int64(s.IdleCap), AbsoluteCap: int64(s.AbsoluteCap),
	}
}

// session merges ref's session row with its activity row.
func (r authReader) session(ref access.SessionRef, row *sessionRow) (*access.Session, error) {
	act, _, err := r.a.activity.Get(r.r, ref)
	if err != nil {
		return nil, err
	}
	return &access.Session{
		CreatedAt: row.CreatedAt, LastActivity: act.LastActivity, AuthTime: row.AuthTime, ID: ref.ID,
		TokenHash: access.NewHash(row.TokenHash), AuthMethod: access.Method(row.AuthMethod), Kind: access.SessionKind(row.Kind),
		UserAgent: row.UserAgent, Label: row.Label, LastIP: act.LastIP, UserID: ref.UserID, IssuedByKey: row.IssuedByKey,
		IdleCap: time.Duration(row.IdleCap), AbsoluteCap: time.Duration(row.AbsoluteCap),
	}, nil
}

func (r authReader) sessionRef(tokenHash access.Hash) (access.SessionRef, bool, error) {
	ref, _, ok, err := r.a.sessionByHash.Lookup(r.r, appendText(nil, tokenHash.Reveal()))
	return ref, ok, err
}

func (r authReader) sessionByHash(tokenHash access.Hash) (*access.Session, bool, error) {
	ref, ok, err := r.sessionRef(tokenHash)
	if err != nil || !ok {
		return nil, false, err
	}
	row, ok, err := r.a.sessions.Get(r.r, ref)
	if err != nil || !ok {
		return nil, false, err
	}
	sess, err := r.session(ref, &row)
	return sess, err == nil, err
}

func (r authReader) sessionsOf(userID int64) ([]access.Session, error) {
	out := []access.Session{}
	err := r.a.sessions.Scan(r.r, bolttable.Range{Prefix: idKeys.AppendKey(nil, userID)}, func(ref access.SessionRef, row *sessionRow) (bool, error) {
		sess, err := r.session(ref, row)
		if err != nil {
			return false, err
		}
		out = append(out, *sess)
		return true, nil
	})
	return out, err
}

// CreateSession stores sess and its activity, refusing a stored session of
// the same user and ID; the session_id index refuses one of another user.
func (x *accountTx) CreateSession(_ context.Context, sess *access.Session) error {
	switch {
	case sess.TokenHash.IsZero():
		return errNoHash
	case sess.ID == "":
		return errNoSessionID
	case sess.UserID < 1:
		return errNoUser
	}
	ref := access.SessionRef{UserID: sess.UserID, ID: sess.ID}
	if _, ok, err := x.s.a.sessions.Get(x.tx.Read(), ref); err != nil || ok {
		return existsErr(ok, err)
	}
	row := sessionRowOf(sess)
	if err := x.s.a.sessions.Put(x.tx, ref, &row); err != nil {
		return conflictErr(err)
	}
	return x.s.a.activity.Put(x.tx, ref, &activityRow{LastActivity: sess.LastActivity, LastIP: sess.LastIP})
}

// existsErr is a create's answer to its read-first: the read's error, or
// errRowExists when the row is there.
func existsErr(exists bool, err error) error {
	if err != nil {
		return err
	}
	if exists {
		return errRowExists
	}
	return nil
}

func (x *accountTx) SessionByHash(_ context.Context, tokenHash access.Hash) (*access.Session, bool, error) {
	return x.read().sessionByHash(tokenHash)
}

func (x *accountTx) SessionsByUserID(_ context.Context, userID int64) ([]access.Session, error) {
	return x.read().sessionsOf(userID)
}

// UpdateSessionActivity writes act only when act.At is after the stored
// LastActivity (rule 21), and nothing for a hash no session has (rule 19).
func (x *accountTx) UpdateSessionActivity(_ context.Context, tokenHash access.Hash, act access.SessionActivity) error {
	ref, ok, err := x.read().sessionRef(tokenHash)
	if err != nil || !ok {
		return err
	}
	return x.raiseActivity(ref, activityRow{LastActivity: act.At, LastIP: act.IP}, notBefore)
}

// notBefore and time.Time.After are raiseActivity's two keep rules: an
// activity update loses a tie to the stored stamp, a rotation wins it.
func notBefore(stored, next time.Time) bool { return !next.After(stored) }

// raiseActivity stores next as ref's activity unless keep(stored, next)
// holds for the stamps, so no write lowers a session's stamp (rule 21).
func (x *accountTx) raiseActivity(ref access.SessionRef, next activityRow, keep func(stored, next time.Time) bool) error {
	stored, _, err := x.s.a.activity.Get(x.tx.Read(), ref)
	if err != nil || keep(stored.LastActivity, next.LastActivity) {
		return err
	}
	return x.s.a.activity.Put(x.tx, ref, &next)
}

// RotateSession writes rot.Next over the session (rot.UserID, rot.ID) when
// ExpectHash, if set, is the stored hash; the stored activity stays only
// when it is later than Next's (rule 21).
func (x *accountTx) RotateSession(_ context.Context, rot account.Rotation) error { //nolint:gocritic // hugeParam: account.SessionStore's signature
	ref := access.SessionRef{UserID: rot.UserID, ID: rot.ID}
	row, ok, err := x.s.a.sessions.Get(x.tx.Read(), ref)
	switch {
	case err != nil:
		return err
	case !ok, !rot.ExpectHash.IsZero() && !rot.ExpectHash.Equal(access.NewHash(row.TokenHash)):
		return account.ErrNotFound
	case rot.Next.ID != rot.ID || rot.Next.UserID != rot.UserID:
		return errRotationRef
	case rot.Next.TokenHash.IsZero():
		return errNoHash
	}
	next := sessionRowOf(&rot.Next)
	if err := x.s.a.sessions.Put(x.tx, ref, &next); err != nil {
		return conflictErr(err)
	}
	return x.raiseActivity(ref, activityRow{LastActivity: rot.Next.LastActivity, LastIP: rot.Next.LastIP}, time.Time.After)
}

func (x *accountTx) UpdateSessionLabel(_ context.Context, ref access.SessionRef, label string) error {
	row, ok, err := x.s.a.sessions.Get(x.tx.Read(), ref)
	if err != nil {
		return err
	}
	if !ok {
		return account.ErrNotFound
	}
	row.Label = label
	return x.s.a.sessions.Put(x.tx, ref, &row)
}

func (x *accountTx) DeleteSession(_ context.Context, ref access.SessionRef) error {
	ok, err := x.s.a.sessions.Delete(x.tx, ref)
	if err != nil {
		return err
	}
	if !ok {
		return account.ErrNotFound
	}
	return nil
}

func (x *accountTx) DeleteSessionByHash(_ context.Context, tokenHash access.Hash) error {
	ref, ok, err := x.read().sessionRef(tokenHash)
	if err != nil || !ok {
		return err
	}
	_, err = x.s.a.sessions.Delete(x.tx, ref)
	return err
}

// DeleteUserSessions deletes every session of userID but exceptID and
// returns the IDs it deleted.
func (x *accountTx) DeleteUserSessions(_ context.Context, userID int64, exceptID string) ([]string, error) {
	var refs []access.SessionRef
	err := x.s.a.sessions.Scan(x.tx.Read(), bolttable.Range{Prefix: idKeys.AppendKey(nil, userID)}, func(ref access.SessionRef, _ *sessionRow) (bool, error) {
		if ref.ID != exceptID {
			refs = append(refs, ref)
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, err := x.s.a.sessions.Delete(x.tx, ref); err != nil {
			return nil, err
		}
		ids = append(ids, ref.ID)
	}
	return ids, nil
}

// CreateSession stores sess with its activity.
func (a *AccountStore) CreateSession(ctx context.Context, sess *access.Session) error {
	return a.single(ctx, "create session", func(x *accountTx) error { return x.CreateSession(ctx, sess) })
}

// SessionByHash reads the session holding tokenHash, its activity merged.
func (a *AccountStore) SessionByHash(ctx context.Context, tokenHash access.Hash) (*access.Session, bool, error) {
	return viewFound(ctx, a, func(r authReader) (*access.Session, bool, error) { return r.sessionByHash(tokenHash) })
}

// SessionsByUserID lists userID's sessions, their activity merged.
func (a *AccountStore) SessionsByUserID(ctx context.Context, userID int64) ([]access.Session, error) {
	return viewList(ctx, a, func(r authReader) ([]access.Session, error) { return r.sessionsOf(userID) })
}

// UpdateSessionActivity raises a session's activity to act when act.At is later (rule 21).
func (a *AccountStore) UpdateSessionActivity(ctx context.Context, tokenHash access.Hash, act access.SessionActivity) error {
	return a.single(ctx, "session activity", func(x *accountTx) error { return x.UpdateSessionActivity(ctx, tokenHash, act) })
}

// RotateSession writes rot.Next over the session it names (rule 7).
func (a *AccountStore) RotateSession(ctx context.Context, rot account.Rotation) error { //nolint:gocritic // hugeParam: account.SessionStore's signature
	return a.single(ctx, "rotate session", func(x *accountTx) error { return x.RotateSession(ctx, rot) })
}

// UpdateSessionLabel relabels ref's session, or answers account.ErrNotFound.
func (a *AccountStore) UpdateSessionLabel(ctx context.Context, ref access.SessionRef, label string) error {
	return a.single(ctx, "session label", func(x *accountTx) error { return x.UpdateSessionLabel(ctx, ref, label) })
}

// DeleteSession deletes ref's session, or answers account.ErrNotFound.
func (a *AccountStore) DeleteSession(ctx context.Context, ref access.SessionRef) error {
	return a.single(ctx, "delete session", func(x *accountTx) error { return x.DeleteSession(ctx, ref) })
}

// DeleteSessionByHash deletes the session holding tokenHash, if any.
func (a *AccountStore) DeleteSessionByHash(ctx context.Context, tokenHash access.Hash) error {
	return a.single(ctx, "delete session", func(x *accountTx) error { return x.DeleteSessionByHash(ctx, tokenHash) })
}

// DeleteUserSessions deletes userID's sessions but exceptID and returns their IDs.
func (a *AccountStore) DeleteUserSessions(ctx context.Context, userID int64, exceptID string) ([]string, error) {
	return singleValue(ctx, a, "delete user sessions", func(x *accountTx) ([]string, error) { return x.DeleteUserSessions(ctx, userID, exceptID) })
}
