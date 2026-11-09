package statedb

import (
	"context"
	"crypto/rand"

	"github.com/cplieger/bolttable"
)

// AuthSecrets are the two keys account.Config needs: the failure-record
// HMAC key and the device-cookie key.
type AuthSecrets struct{ FailureKey, DeviceKey [32]byte }

// AuthSecrets reads the keys, creating them from crypto/rand on the first
// call, inside one Update: a rerun or two concurrent first calls store one
// value and both return it. They live in auth, so a core reset keeps them.
func (s *Store) AuthSecrets(ctx context.Context) (AuthSecrets, error) {
	var out AuthSecrets
	err := s.write(ctx, "auth secrets", s.db.Update, func(tx *bolttable.Tx) error {
		row, ok, err := s.a.secrets.Get(tx.Read(), constKey{})
		if err != nil {
			return err
		}
		if !ok {
			// rand.Read never fails: https://pkg.go.dev/crypto/rand#Read (Go 1.24).
			_, _ = rand.Read(row.FailureKey[:])
			_, _ = rand.Read(row.DeviceKey[:])
			if err := s.a.secrets.Put(tx, constKey{}, &row); err != nil {
				return err
			}
		}
		out = AuthSecrets{FailureKey: row.FailureKey, DeviceKey: row.DeviceKey}
		return nil
	})
	if err != nil {
		return AuthSecrets{}, err
	}
	return out, nil
}
