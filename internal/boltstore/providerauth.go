package boltstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cplieger/subflux/internal/store/kv"
	"github.com/cplieger/subflux/internal/subflux"
	bolt "go.etcd.io/bbolt"
)

// errProviderAuthBucket reports a store opened without its provider_auth
// bucket, which Open bootstraps; reaching it means the file was not opened
// through Open.
var errProviderAuthBucket = errors.New("boltstore: provider_auth bucket not found")

// ProviderAuthRecords returns every stored provider credential-failure record.
// The key is the provider id and overrides the value's own provider field. An
// undecodable record is skipped with a warning and then deleted.
func (d *DB) ProviderAuthRecords(_ context.Context) ([]subflux.ProviderAuthRecord, error) {
	var out []subflux.ProviderAuthRecord
	corrupt := map[string][]byte{}
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketProviderAuth))
		if b == nil {
			return errProviderAuthBucket
		}
		return b.ForEach(func(k, v []byte) error {
			var rec subflux.ProviderAuthRecord
			skip, err := decodeRecord(bucketDecodeMode(bucketProviderAuth), bucketProviderAuth, k, v, &rec)
			if err != nil {
				return err
			}
			if skip {
				corrupt[string(k)] = bytes.Clone(v)
				return nil
			}
			rec.Provider = subflux.ProviderID(k)
			out = append(out, rec)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if len(corrupt) > 0 {
		if err := d.deleteCorruptProviderAuth(corrupt); err != nil {
			slog.Warn("undecodable provider credential records not deleted", "error", err)
		}
	}
	return out, nil
}

// deleteCorruptProviderAuth deletes each key whose value is still the
// undecodable one read, so a record written since then survives.
func (d *DB) deleteCorruptProviderAuth(corrupt map[string][]byte) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketProviderAuth))
		if b == nil {
			return errProviderAuthBucket
		}
		for k, v := range corrupt {
			if !bytes.Equal(b.Get([]byte(k)), v) {
				continue
			}
			if err := b.Delete([]byte(k)); err != nil {
				return fmt.Errorf("delete provider auth record %s: %w", k, err)
			}
		}
		return nil
	})
}

// PutProviderAuthRecord stores rec under its provider id, replacing any
// previous record for that provider.
func (d *DB) PutProviderAuthRecord(_ context.Context, rec *subflux.ProviderAuthRecord) error {
	if rec.Provider == "" {
		return errors.New("put provider auth record: empty provider")
	}
	data, err := kv.Encode(rec)
	if err != nil {
		return fmt.Errorf("put provider auth record %s: %w", rec.Provider, err)
	}
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketProviderAuth))
		if b == nil {
			return errProviderAuthBucket
		}
		return b.Put([]byte(rec.Provider), data)
	})
}

// DeleteProviderAuthRecord removes the provider's record. Deleting an absent
// record is not an error.
func (d *DB) DeleteProviderAuthRecord(_ context.Context, id subflux.ProviderID) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketProviderAuth))
		if b == nil {
			return errProviderAuthBucket
		}
		return b.Delete([]byte(id))
	})
}
