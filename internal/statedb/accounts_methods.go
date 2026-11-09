package statedb

import (
	"context"
	"errors"

	"github.com/cplieger/access/account"
)

// AuthMethodState is table 2B's one row: the sign-in configuration the
// committed settings publish. The method guard judges PasswordLogin,
// OIDCIssuer and RPID; OIDCIssuer is empty while OIDC is off, RPID while
// passkeys are off.
type AuthMethodState struct {
	OIDCIssuer, RPID string
	SignIn           SignInKeys
	PasswordLogin    bool
}

// SignInKeys are the other sign-in settings platform-auth's security-log
// entries compare. OIDCClientSecret is the secret's SHA-256, zero when it is
// empty, never the secret.
type SignInKeys struct {
	OIDCClientID, OIDCRedirectURL     string
	OIDCAllowedGroups, PasskeyOrigins SetDigest
	OIDCClientSecret                  [32]byte
	OIDCAutoProvision, DisableAuth    bool
}

// SetDigest is a list setting compared as a set: Count distinct values and
// the SHA-256 of them sorted, each preceded by its byte length as a
// big-endian uint32.
type SetDigest struct {
	Count  int
	SHA256 [32]byte
}

// MethodGuard runs inside Activate's or ActivateAuth's Update before the
// record is written: before is the stored row, the zero value when stored is
// false. A non-nil error aborts the Update, writing nothing, and is returned
// as it is.
type MethodGuard func(ctx context.Context, tx account.Tx, before AuthMethodState, stored bool, next AuthMethodState) error

var (
	errForeignTx = errors.New("statedb: MethodStateOf needs this package's account transaction or store")
	errNoGuard   = errors.New("statedb: an auth method state needs its guard")
)

func methodRowOf(m *AuthMethodState) methodStateRow {
	digest := func(d SetDigest) setDigestRow { return setDigestRow{Count: d.Count, SHA256: d.SHA256} }
	return methodStateRow{
		OIDCIssuer: m.OIDCIssuer, RPID: m.RPID, PasswordLogin: m.PasswordLogin,
		SignIn: signInKeyRow{
			OIDCClientID: m.SignIn.OIDCClientID, OIDCRedirectURL: m.SignIn.OIDCRedirectURL,
			OIDCAllowedGroups: digest(m.SignIn.OIDCAllowedGroups), PasskeyOrigins: digest(m.SignIn.PasskeyOrigins),
			OIDCClientSecret: m.SignIn.OIDCClientSecret, OIDCAutoProvision: m.SignIn.OIDCAutoProvision, DisableAuth: m.SignIn.DisableAuth,
		},
	}
}

func methodStateOf(r *methodStateRow) AuthMethodState {
	digest := func(d setDigestRow) SetDigest { return SetDigest{Count: d.Count, SHA256: d.SHA256} }
	return AuthMethodState{
		OIDCIssuer: r.OIDCIssuer, RPID: r.RPID, PasswordLogin: r.PasswordLogin,
		SignIn: SignInKeys{
			OIDCClientID: r.SignIn.OIDCClientID, OIDCRedirectURL: r.SignIn.OIDCRedirectURL,
			OIDCAllowedGroups: digest(r.SignIn.OIDCAllowedGroups), PasskeyOrigins: digest(r.SignIn.PasskeyOrigins),
			OIDCClientSecret: r.SignIn.OIDCClientSecret, OIDCAutoProvision: r.SignIn.OIDCAutoProvision, DisableAuth: r.SignIn.DisableAuth,
		},
	}
}

func (r authReader) methodState() (AuthMethodState, bool, error) {
	row, ok, err := r.a.methodState.Get(r.r, constKey{})
	if err != nil || !ok {
		return AuthMethodState{}, false, err
	}
	return methodStateOf(&row), true, nil
}

// MethodStateOf reads table 2B's row through tx, which must be an
// accountTx or the AccountStore of this package; any other tx is an error.
func MethodStateOf(ctx context.Context, tx account.Tx) (AuthMethodState, bool, error) {
	switch t := tx.(type) {
	case *accountTx:
		return t.read().methodState()
	case *AccountStore:
		return viewFound(ctx, t, authReader.methodState)
	}
	return AuthMethodState{}, false, errForeignTx
}

// guardAndWrite is design 5.4's auth step inside x: when next differs from
// the stored row, guard judges both records whole in x, then the row is
// written; an equal row writes nothing and calls no guard.
func guardAndWrite(ctx context.Context, x *accountTx, next *AuthMethodState, guard MethodGuard) error {
	before, stored, err := x.read().methodState()
	if err != nil {
		return err
	}
	if stored && before == *next {
		return nil
	}
	if err := guard(ctx, x, before, stored, *next); err != nil {
		return err
	}
	row := methodRowOf(next)
	return x.s.a.methodState.Put(x.tx, constKey{}, &row)
}

// ActivateAuth is the auth half of a boot that is not Active (design 5.4):
// one Update that calls guard and writes auth as Activate's does with
// Activation.Auth set, and writes no source, coverage or digest row and
// installs no activation, so the first Activate after Open still recomputes
// coverage. An auth equal to table 2B's row commits nothing.
func (s *Store) ActivateAuth(ctx context.Context, auth AuthMethodState, guard MethodGuard) error { //nolint:gocritic // hugeParam: records travel as values, as MethodGuard's do
	if guard == nil {
		return errNoGuard
	}
	before, stored, err := viewFound(ctx, s.Accounts(), authReader.methodState)
	if err != nil {
		return err
	}
	if stored && before == auth {
		return nil
	}
	return s.accountWrite(ctx, "activate auth", s.db.Update, func(x *accountTx) error {
		return guardAndWrite(ctx, x, &auth, guard)
	})
}
