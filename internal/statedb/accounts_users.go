package statedb

import (
	"context"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
)

func userRowOf(u *access.User) userRow {
	return userRow{
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName,
		PasswordHash: u.PasswordHash.Reveal(), Role: string(u.Role), OIDCIssuer: u.OIDC.Issuer, OIDCSubject: u.OIDC.Subject,
		WebAuthnHandle: u.WebAuthnHandle, Enabled: u.Enabled,
	}
}

func userOf(id int64, r *userRow) *access.User {
	return &access.User{
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Username: r.Username, Email: r.Email, DisplayName: r.DisplayName,
		PasswordHash: access.NewHash(r.PasswordHash), Role: access.Role(r.Role),
		OIDC: access.OIDCIdentity{Issuer: r.OIDCIssuer, Subject: r.OIDCSubject}, WebAuthnHandle: r.WebAuthnHandle,
		ID: id, Enabled: r.Enabled,
	}
}

func (r authReader) user(id int64) (*access.User, bool, error) {
	row, ok, err := r.a.users.Get(r.r, id)
	if err != nil || !ok {
		return nil, false, err
	}
	return userOf(id, &row), true, nil
}

// userBy reads the user a unique users index entry names.
func (r authReader) userBy(ix *bolttable.Index[int64, userRow], key []byte) (*access.User, bool, error) {
	id, _, ok, err := ix.Lookup(r.r, key)
	if err != nil || !ok {
		return nil, false, err
	}
	return r.user(id)
}

func (r authReader) userByUsername(canonical string) (*access.User, bool, error) {
	return r.userBy(r.a.byUsername, appendText(nil, canonical))
}

func (r authReader) userByOIDC(id access.OIDCIdentity) (*access.User, bool, error) {
	if id.IsZero() {
		return nil, false, nil
	}
	return r.userBy(r.a.byOIDC, appendText(appendText(nil, id.Issuer), id.Subject))
}

func (r authReader) userByHandle(handle []byte) (*access.User, bool, error) {
	if len(handle) == 0 {
		return nil, false, nil
	}
	return r.userBy(r.a.byHandle, appendText(nil, string(handle)))
}

func (r authReader) users() ([]access.User, error) {
	out := []access.User{}
	err := r.a.users.Scan(r.r, bolttable.Range{}, func(id int64, row *userRow) (bool, error) {
		out = append(out, *userOf(id, row))
		return true, nil
	})
	return out, err
}

func (r authReader) userCount() (int, error) {
	n, err := r.a.userCount.Value(r.r)
	return int(n), err
}

// CreateUser assigns user.ID from the users sequence and stores the user.
func (x *accountTx) CreateUser(_ context.Context, user *access.User) error {
	id, err := newID(x.tx, x.s.a.users)
	if err != nil {
		return err
	}
	row := userRowOf(user)
	if err := x.s.a.users.Put(x.tx, id, &row); err != nil {
		return conflictErr(err)
	}
	set(x, &user.ID, id)
	return nil
}

// CreateInitialUser reads the users counter in this transaction, so two
// concurrent first sign-ups create one user (rule 5).
func (x *accountTx) CreateInitialUser(ctx context.Context, user *access.User) error {
	n, err := x.read().userCount()
	if err != nil {
		return err
	}
	if n > 0 {
		return account.ErrSetupComplete
	}
	return x.CreateUser(ctx, user)
}

func (x *accountTx) UserByID(_ context.Context, id int64) (*access.User, bool, error) {
	return x.read().user(id)
}

func (x *accountTx) UserByUsername(_ context.Context, canonical string) (*access.User, bool, error) {
	return x.read().userByUsername(canonical)
}

func (x *accountTx) UserByOIDC(_ context.Context, id access.OIDCIdentity) (*access.User, bool, error) {
	return x.read().userByOIDC(id)
}

func (x *accountTx) UserByWebAuthnHandle(_ context.Context, handle []byte) (*access.User, bool, error) {
	return x.read().userByHandle(handle)
}

func (x *accountTx) ListUsers(context.Context) ([]access.User, error) { return x.read().users() }

func (x *accountTx) UpdateUser(_ context.Context, user *access.User) error {
	_, ok, err := x.s.a.users.Get(x.tx.Read(), user.ID)
	if err != nil {
		return err
	}
	if !ok {
		return account.ErrNotFound
	}
	row := userRowOf(user)
	return conflictErr(x.s.a.users.Put(x.tx, user.ID, &row))
}

// DeleteUser deletes the user; the cascades take its sessions, passkeys,
// keys, the tokens whose Subject it is and its key attributions with it.
func (x *accountTx) DeleteUser(_ context.Context, id int64) error {
	ok, err := x.s.a.users.Delete(x.tx, id)
	if err != nil {
		return err
	}
	if !ok {
		return account.ErrNotFound
	}
	return nil
}

func (x *accountTx) UserCount(context.Context) (int, error) { return x.read().userCount() }

// CreateUser assigns user.ID once the user is stored.
func (a *AccountStore) CreateUser(ctx context.Context, user *access.User) error {
	return a.createUser(ctx, user, (*accountTx).CreateUser)
}

// CreateInitialUser creates user while no user exists, else
// account.ErrSetupComplete.
func (a *AccountStore) CreateInitialUser(ctx context.Context, user *access.User) error {
	return a.createUser(ctx, user, (*accountTx).CreateInitialUser)
}

func (a *AccountStore) createUser(ctx context.Context, user *access.User, create func(*accountTx, context.Context, *access.User) error) error {
	return a.single(ctx, "create user", func(x *accountTx) error { return create(x, ctx, user) })
}

// UserByID reads one user in its own View.
func (a *AccountStore) UserByID(ctx context.Context, id int64) (*access.User, bool, error) {
	return viewFound(ctx, a, func(r authReader) (*access.User, bool, error) { return r.user(id) })
}

// UserByUsername reads the user holding canonical exactly (rule 3).
func (a *AccountStore) UserByUsername(ctx context.Context, canonical string) (*access.User, bool, error) {
	return viewFound(ctx, a, func(r authReader) (*access.User, bool, error) { return r.userByUsername(canonical) })
}

// UserByOIDC reads the user linked to id; the zero identity matches no user.
func (a *AccountStore) UserByOIDC(ctx context.Context, id access.OIDCIdentity) (*access.User, bool, error) {
	return viewFound(ctx, a, func(r authReader) (*access.User, bool, error) { return r.userByOIDC(id) })
}

// UserByWebAuthnHandle reads the user holding handle; an unknown and an empty handle answer alike (rule 9).
func (a *AccountStore) UserByWebAuthnHandle(ctx context.Context, handle []byte) (*access.User, bool, error) {
	return viewFound(ctx, a, func(r authReader) (*access.User, bool, error) { return r.userByHandle(handle) })
}

// ListUsers lists every user in ID order.
func (a *AccountStore) ListUsers(ctx context.Context) ([]access.User, error) {
	return viewList(ctx, a, authReader.users)
}

// UpdateUser rewrites a stored user, or answers account.ErrNotFound.
func (a *AccountStore) UpdateUser(ctx context.Context, user *access.User) error {
	return a.single(ctx, "update user", func(x *accountTx) error { return x.UpdateUser(ctx, user) })
}

// DeleteUser deletes the user with its cascades in one write, or answers account.ErrNotFound.
func (a *AccountStore) DeleteUser(ctx context.Context, id int64) error {
	return a.single(ctx, "delete user", func(x *accountTx) error { return x.DeleteUser(ctx, id) })
}

// UserCount reads the users counter.
func (a *AccountStore) UserCount(ctx context.Context) (int, error) {
	return viewList(ctx, a, authReader.userCount)
}
