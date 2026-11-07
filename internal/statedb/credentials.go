package statedb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cplieger/bolttable"
)

// CredentialDisable is a provider's disabled credential: the Argon2
// fingerprint of the credential refused, why, and when.
type CredentialDisable struct {
	At          time.Time
	Provider    string
	Fingerprint string
	Reason      string
}

var errNoProvider = errors.New("statedb: a credential-disable record names no provider")

// CredentialDisable returns provider's record; found is false when the
// provider has none.
func (s *Store) CredentialDisable(ctx context.Context, provider string) (CredentialDisable, bool, error) {
	var d CredentialDisable
	var found bool
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		row, ok, err := s.t.credentials.Get(r, provider)
		if ok {
			d, found = CredentialDisable{Provider: provider, Fingerprint: row.Fingerprint, Reason: row.Reason, At: row.At}, true
		}
		return err
	})
	if err != nil {
		return CredentialDisable{}, false, fmt.Errorf("statedb: credential disable: %w", err)
	}
	return d, found, nil
}

// PutCredentialDisable stores d, replacing its provider's record; an empty
// Provider is refused, writing nothing.
func (s *Store) PutCredentialDisable(ctx context.Context, d CredentialDisable) error {
	if d.Provider == "" {
		return errNoProvider
	}
	row := credentialRow{At: d.At.UTC().Round(0), Fingerprint: d.Fingerprint, Reason: d.Reason}
	err := s.write(ctx, "put credential disable", s.db.Update, func(tx *bolttable.Tx) error {
		return s.t.credentials.Put(tx, d.Provider, &row)
	})
	if err != nil {
		return fmt.Errorf("statedb: put credential disable: %w", err)
	}
	return nil
}

// DeleteCredentialDisable deletes provider's record; none stored is nil.
func (s *Store) DeleteCredentialDisable(ctx context.Context, provider string) error {
	if provider == "" {
		return errNoProvider
	}
	err := s.write(ctx, "delete credential disable", s.db.Update, func(tx *bolttable.Tx) error {
		_, err := s.t.credentials.Delete(tx, provider)
		return err
	})
	if err != nil {
		return fmt.Errorf("statedb: delete credential disable: %w", err)
	}
	return nil
}
