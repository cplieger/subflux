package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"

	"github.com/cplieger/access/account"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/statedb"
)

// MethodRecord is the auth_method_state c publishes: password_login, the
// issuer while oidc_enabled, the RP ID passkey_origins names, and SignIn's
// keys as set whatever oidc_enabled says, the client secret as its SHA-256
// only.
func MethodRecord(cfg Config) statedb.AuthMethodState {
	c := cfg.get()
	st := statedb.AuthMethodState{
		PasswordLogin: c.passwordLogin,
		RPID:          c.rpID,
		SignIn: statedb.SignInKeys{
			OIDCClientID:      c.clientID,
			OIDCRedirectURL:   c.redirect,
			OIDCAllowedGroups: setDigest(c.groups),
			PasskeyOrigins:    setDigest(c.origins),
			OIDCAutoProvision: c.autoProvision,
			DisableAuth:       c.disableAuth,
		},
	}
	if c.oidcEnabled {
		st.OIDCIssuer = c.issuer
	}
	if c.secret != "" {
		st.SignIn.OIDCClientSecret = sha256.Sum256([]byte(c.secret))
	}
	return st
}

// setDigest is statedb.SetDigest's rule over values: the distinct ones,
// sorted, each preceded by its length as a big-endian uint32, so a reorder
// or a duplicate changes nothing.
func setDigest(values []string) statedb.SetDigest {
	set := slices.Compact(slices.Sorted(slices.Values(values)))
	h := sha256.New()
	for _, v := range set {
		h.Write(binary.BigEndian.AppendUint32(nil, uint32(len(v)))) //nolint:gosec // a settings value is bounded by the 1 MiB file
		h.Write([]byte(v))
	}
	d := statedb.SetDigest{Count: len(set)}
	h.Sum(d.SHA256[:0])
	return d
}

// MethodGuard is activate's statedb.MethodGuard, at boot and on every Save.
// Inside the Update it refuses next with *formschema.RefusedError, one field
// error at the first key whose change takes a method away, when an enabled
// user with a usable method under before has none under next; before is the
// stored record, or MethodRecord of the section in force when none is
// stored. Any other error, a store error included, is returned as it is.
func (s *Service) MethodGuard() statedb.MethodGuard {
	return func(ctx context.Context, tx account.Tx, before statedb.AuthMethodState, stored bool, next statedb.AuthMethodState) error {
		if !stored {
			before = MethodRecord(s.config())
		}
		return s.guardMethods(ctx, tx, &before, &next)
	}
}

// PreCheckMethods is prepare's advisory count of next against the stored
// record, read through the store with no write. A read error answers no
// field error: MethodGuard decides inside the Update.
func (s *Service) PreCheckMethods(ctx context.Context, next Config) []formschema.FieldError {
	before, stored, err := statedb.MethodStateOf(ctx, s.deps.Store)
	if err != nil {
		return nil
	}
	if !stored {
		before = MethodRecord(s.config())
	}
	rec := MethodRecord(next)
	if refused, ok := errors.AsType[*formschema.RefusedError](s.guardMethods(ctx, s.deps.Store, &before, &rec)); ok {
		return refused.Fields
	}
	return nil
}

// guardMethods is the one count of MethodGuard and PreCheckMethods, read
// through tx. A change that takes no method away reads no user.
func (s *Service) guardMethods(ctx context.Context, tx account.Tx, before, next *statedb.AuthMethodState) error {
	key, ok := firstRemoval(before, next)
	if !ok {
		return nil
	}
	users, err := tx.ListUsers(ctx)
	if err != nil {
		return err
	}
	stranded := 0
	for i := range users {
		if !users[i].Enabled {
			continue
		}
		had, err := s.signsIn(ctx, tx, users[i].ID, before)
		if err != nil {
			return err
		}
		if !had {
			continue
		}
		has, err := s.signsIn(ctx, tx, users[i].ID, next)
		if err != nil {
			return err
		}
		if !has {
			stranded++
		}
	}
	if stranded == 0 {
		return nil
	}
	return &formschema.RefusedError{Fields: []formschema.FieldError{{Path: SectionKey + "." + key, Message: strandedMessage(stranded)}}}
}

// firstRemoval is the first key, in the order password_login, oidc_enabled,
// oidc_issuer, passkey_origins, whose change from before to next takes a
// method away; false when none does, since a change that only adds a method
// cannot strand a user.
func firstRemoval(before, next *statedb.AuthMethodState) (string, bool) {
	switch {
	case before.PasswordLogin && !next.PasswordLogin:
		return keyPasswordLogin, true
	case before.OIDCIssuer != "" && next.OIDCIssuer == "":
		return keyOIDCEnabled, true
	case before.OIDCIssuer != "" && next.OIDCIssuer != before.OIDCIssuer:
		return keyOIDCIssuer, true
	case before.RPID != "" && next.RPID != before.RPID:
		return keyPasskeyOrigins, true
	}
	return "", false
}

// signsIn reports whether user id keeps a usable method under st: a
// password, a passkey under its RP ID, or a link of its issuer. The state
// carries what methodUsable answers, so the count runs without it.
func (s *Service) signsIn(ctx context.Context, tx account.Tx, id int64, st *statedb.AuthMethodState) (bool, error) {
	r := s.rules()
	r.DisablePasswordLogin, r.MethodUsable = !st.PasswordLogin, nil
	av, err := s.account.UsableMethods(ctx, tx, id, account.MethodState{RPID: st.RPID, OIDCIssuer: st.OIDCIssuer, Rules: r})
	if err != nil {
		return false, err
	}
	return av.HasPassword || av.PasskeyCount > 0 || av.OIDCEnabled, nil
}

func strandedMessage(n int) string {
	if n == 1 {
		return "would leave 1 user with no way to sign in"
	}
	return fmt.Sprintf("would leave %d users with no way to sign in", n)
}
