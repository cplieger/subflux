package statedb

import (
	"context"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
)

func passkeyRowOf(c *access.PasskeyCredential) passkeyRow {
	return passkeyRow{
		CreatedAt: c.CreatedAt, Discoverable: c.Discoverable, AttestationType: c.AttestationType, AttestationFormat: c.AttestationFormat,
		RPID: c.RPID, Transport: c.Transport, Name: c.Name, Attachment: string(c.Attachment),
		CredentialID: c.CredentialID, PublicKey: c.PublicKey, AAGUID: c.AAGUID, RawAttestation: c.RawAttestation,
	}
}

// passkey merges ref's passkey row with its login stamps, decoding the flag
// booleans from the stored octet (rule 8).
func (r authReader) passkey(ref access.PasskeyRef, row *passkeyRow) (access.PasskeyCredential, error) {
	use, _, err := r.a.passkeyUse.Get(r.r, ref)
	if err != nil {
		return access.PasskeyCredential{}, err
	}
	c := access.PasskeyCredential{
		CreatedAt: row.CreatedAt, LastUsedAt: use.At, Discoverable: row.Discoverable, AttestationType: row.AttestationType,
		AttestationFormat: row.AttestationFormat, RPID: row.RPID, Transport: row.Transport, Name: row.Name,
		Attachment: access.AuthenticatorAttachment(row.Attachment), CredentialID: row.CredentialID, PublicKey: row.PublicKey,
		AAGUID: row.AAGUID, RawAttestation: row.RawAttestation, ID: ref.ID, UserID: ref.UserID,
		SignCount: use.SignCount, RawFlags: use.RawFlags, CloneWarning: use.CloneWarning,
	}
	c.UserPresent, c.UserVerified, c.BackupEligible, c.BackupState = access.DecodePasskeyFlags(use.RawFlags)
	return c, nil
}

func (r authReader) passkeysOf(userID int64) ([]access.PasskeyCredential, error) {
	out := []access.PasskeyCredential{}
	err := r.a.passkeys.Scan(r.r, bolttable.Range{Prefix: idKeys.AppendKey(nil, userID)}, func(ref access.PasskeyRef, row *passkeyRow) (bool, error) {
		c, err := r.passkey(ref, row)
		if err != nil {
			return false, err
		}
		out = append(out, c)
		return true, nil
	})
	return out, err
}

// CreatePasskey assigns cred.ID from the passkeys sequence and stores the
// credential with its custody fields; a credential ID any user registered is
// account.ErrDuplicateCredential.
func (x *accountTx) CreatePasskey(_ context.Context, cred *access.PasskeyCredential) error {
	switch {
	case len(cred.CredentialID) == 0:
		return errNoCredentialID
	case cred.UserID < 1:
		return errNoUser
	}
	id, err := newID(x.tx, x.s.a.passkeys)
	if err != nil {
		return err
	}
	ref := access.PasskeyRef{UserID: cred.UserID, ID: id}
	row := passkeyRowOf(cred)
	if err := x.s.a.passkeys.Put(x.tx, ref, &row); err != nil {
		return conflictErr(err)
	}
	use := passkeyUseRow{At: cred.LastUsedAt, SignCount: cred.SignCount, RawFlags: cred.RawFlags, CloneWarning: cred.CloneWarning}
	if err := x.s.a.passkeyUse.Put(x.tx, ref, &use); err != nil {
		return err
	}
	set(x, &cred.ID, id)
	return nil
}

func (x *accountTx) PasskeysByUserID(_ context.Context, userID int64) ([]access.PasskeyCredential, error) {
	return x.read().passkeysOf(userID)
}

// UpdatePasskeyAfterLogin writes upd's custody fields together, for a
// credential of userID only (rules 6 and 8).
func (x *accountTx) UpdatePasskeyAfterLogin(_ context.Context, userID int64, credID []byte, upd access.PasskeyLoginUpdate) error {
	ref, _, ok, err := x.s.a.byCredential.Lookup(x.tx.Read(), appendText(nil, string(credID)))
	if err != nil {
		return err
	}
	if !ok || ref.UserID != userID {
		return account.ErrNotFound
	}
	use := passkeyUseRow{At: upd.At, SignCount: upd.SignCount, RawFlags: upd.RawFlags, CloneWarning: upd.CloneWarning}
	return x.s.a.passkeyUse.Put(x.tx, ref, &use)
}

func (x *accountTx) RenamePasskey(_ context.Context, ref access.PasskeyRef, name string) error {
	row, ok, err := x.s.a.passkeys.Get(x.tx.Read(), ref)
	if err != nil {
		return err
	}
	if !ok {
		return account.ErrNotFound
	}
	row.Name = name
	return x.s.a.passkeys.Put(x.tx, ref, &row)
}

func (x *accountTx) DeletePasskey(_ context.Context, ref access.PasskeyRef) error {
	ok, err := x.s.a.passkeys.Delete(x.tx, ref)
	if err != nil {
		return err
	}
	if !ok {
		return account.ErrNotFound
	}
	return nil
}

// DeleteUserPasskeys deletes every passkey of userID and returns their IDs.
func (x *accountTx) DeleteUserPasskeys(_ context.Context, userID int64) ([]int64, error) {
	var refs []access.PasskeyRef
	err := x.s.a.passkeys.Scan(x.tx.Read(), bolttable.Range{Prefix: idKeys.AppendKey(nil, userID)}, func(ref access.PasskeyRef, _ *passkeyRow) (bool, error) {
		refs = append(refs, ref)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(refs))
	for _, ref := range refs {
		if _, err := x.s.a.passkeys.Delete(x.tx, ref); err != nil {
			return nil, err
		}
		ids = append(ids, ref.ID)
	}
	return ids, nil
}

// CreatePasskey assigns cred.ID once the credential is stored.
func (a *AccountStore) CreatePasskey(ctx context.Context, cred *access.PasskeyCredential) error {
	return a.single(ctx, "create passkey", func(x *accountTx) error { return x.CreatePasskey(ctx, cred) })
}

// PasskeysByUserID lists userID's passkeys, their login stamps merged.
func (a *AccountStore) PasskeysByUserID(ctx context.Context, userID int64) ([]access.PasskeyCredential, error) {
	return viewList(ctx, a, func(r authReader) ([]access.PasskeyCredential, error) { return r.passkeysOf(userID) })
}

// UpdatePasskeyAfterLogin writes a login's custody fields for userID's credential (rule 8).
func (a *AccountStore) UpdatePasskeyAfterLogin(ctx context.Context, userID int64, credID []byte, upd access.PasskeyLoginUpdate) error {
	return a.single(ctx, "passkey login", func(x *accountTx) error { return x.UpdatePasskeyAfterLogin(ctx, userID, credID, upd) })
}

// RenamePasskey renames ref's passkey, or answers account.ErrNotFound.
func (a *AccountStore) RenamePasskey(ctx context.Context, ref access.PasskeyRef, name string) error {
	return a.single(ctx, "rename passkey", func(x *accountTx) error { return x.RenamePasskey(ctx, ref, name) })
}

// DeletePasskey deletes ref's passkey, or answers account.ErrNotFound.
func (a *AccountStore) DeletePasskey(ctx context.Context, ref access.PasskeyRef) error {
	return a.single(ctx, "delete passkey", func(x *accountTx) error { return x.DeletePasskey(ctx, ref) })
}

// DeleteUserPasskeys deletes userID's passkeys and returns their IDs.
func (a *AccountStore) DeleteUserPasskeys(ctx context.Context, userID int64) ([]int64, error) {
	return singleValue(ctx, a, "delete user passkeys", func(x *accountTx) ([]int64, error) { return x.DeleteUserPasskeys(ctx, userID) })
}
