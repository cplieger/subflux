package statedb

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
)

// key merges id's key row with its use stamp.
func (r authReader) key(id int64, row *keyRow) (*access.Key, error) {
	use, _, err := r.a.keyUse.Get(r.r, id)
	if err != nil {
		return nil, err
	}
	return &access.Key{
		CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, LastUsedAt: use.LastUsedAt, KeyHash: access.NewHash(row.KeyHash),
		KeyPrefix: row.KeyPrefix, KeySuffix: row.KeySuffix, Label: row.Label, Scopes: row.Scopes, LineageIDs: row.LineageIDs,
		ID: id, UserID: row.UserID,
	}, nil
}

func (r authReader) keyByID(id int64) (*access.Key, bool, error) {
	row, ok, err := r.a.keys.Get(r.r, id)
	if err != nil || !ok {
		return nil, false, err
	}
	k, err := r.key(id, &row)
	return k, err == nil, err
}

func (r authReader) keyByHash(hash access.Hash) (*access.Key, bool, error) {
	id, _, ok, err := r.a.keyByHash.Lookup(r.r, appendText(nil, hash.Reveal()))
	if err != nil || !ok {
		return nil, false, err
	}
	return r.keyByID(id)
}

// keyIDsOf lists userID's key IDs through the key_user index.
func (r authReader) keyIDsOf(userID int64) ([]int64, error) {
	ids := []int64{}
	err := r.a.keysByUser.Scan(r.r, bolttable.Exact(idKeys.AppendKey(nil, userID)), func(_ []byte, id int64, _ []byte) (bool, error) {
		ids = append(ids, id)
		return true, nil
	})
	return ids, err
}

// keysOf lists userID's keys in ID order, through the key_user index.
func (r authReader) keysOf(userID int64) ([]access.Key, error) {
	ids, err := r.keyIDsOf(userID)
	if err != nil {
		return nil, err
	}
	out := make([]access.Key, 0, len(ids))
	for _, id := range ids {
		k, ok, err := r.keyByID(id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: key_user names key %d, which is not stored", bolttable.ErrCorrupt, id)
		}
		out = append(out, *k)
	}
	return out, nil
}

func (r authReader) allKeys() ([]access.Key, error) {
	out := []access.Key{}
	err := r.a.keys.Scan(r.r, bolttable.Range{}, func(id int64, row *keyRow) (bool, error) {
		k, err := r.key(id, row)
		if err != nil {
			return false, err
		}
		out = append(out, *k)
		return true, nil
	})
	return out, err
}

// CreateAPIKey assigns key.ID from the keys sequence and appends it to
// key.LineageIDs, so a stored key's path ends with its own ID (rule 17).
func (x *accountTx) CreateAPIKey(_ context.Context, key *access.Key) error {
	if key.KeyHash.IsZero() {
		return errNoHash
	}
	id, err := newID(x.tx, x.s.a.keys)
	if err != nil {
		return err
	}
	lineage := append(slices.Clone(key.LineageIDs), id)
	row := keyRow{
		CreatedAt: key.CreatedAt, ExpiresAt: key.ExpiresAt, KeyHash: key.KeyHash.Reveal(), KeyPrefix: key.KeyPrefix,
		KeySuffix: key.KeySuffix, Label: key.Label, Scopes: key.Scopes, LineageIDs: lineage, UserID: key.UserID,
	}
	if err := x.s.a.keys.Put(x.tx, id, &row); err != nil {
		return conflictErr(err)
	}
	if err := x.s.a.keyUse.Put(x.tx, id, &keyUseRow{LastUsedAt: key.LastUsedAt}); err != nil {
		return err
	}
	set(x, &key.ID, id)
	set(x, &key.LineageIDs, lineage)
	return nil
}

func (x *accountTx) APIKeyByHash(_ context.Context, hash access.Hash) (*access.Key, bool, error) {
	return x.read().keyByHash(hash)
}

func (x *accountTx) ListAPIKeysByUserID(_ context.Context, userID int64) ([]access.Key, error) {
	return x.read().keysOf(userID)
}

func (x *accountTx) ListAPIKeys(context.Context) ([]access.Key, error) { return x.read().allKeys() }

// DeleteAPIKey deletes ref's key when ref.UserID owns it (rule 6).
func (x *accountTx) DeleteAPIKey(_ context.Context, ref access.KeyRef) error {
	row, ok, err := x.s.a.keys.Get(x.tx.Read(), ref.ID)
	if err != nil {
		return err
	}
	if !ok || row.UserID != ref.UserID {
		return account.ErrNotFound
	}
	_, err = x.s.a.keys.Delete(x.tx, ref.ID)
	return err
}

// DeleteUserAPIKeys deletes every key of userID and returns their IDs.
func (x *accountTx) DeleteUserAPIKeys(_ context.Context, userID int64) ([]int64, error) {
	ids, err := x.read().keyIDsOf(userID)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := x.s.a.keys.Delete(x.tx, id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// TouchAPIKey raises keyID's LastUsedAt to at when at is later, and writes
// nothing for a key that does not exist (rules 19 and 21).
func (x *accountTx) TouchAPIKey(_ context.Context, keyID int64, at time.Time) error {
	use, ok, err := x.s.a.keyUse.Get(x.tx.Read(), keyID)
	if err != nil || !ok || !at.After(use.LastUsedAt) {
		return err
	}
	return x.s.a.keyUse.Put(x.tx, keyID, &keyUseRow{LastUsedAt: at})
}

// CreateAPIKey assigns key.ID and appends it to key.LineageIDs once the key
// is stored.
func (a *AccountStore) CreateAPIKey(ctx context.Context, key *access.Key) error {
	return a.single(ctx, "create key", func(x *accountTx) error { return x.CreateAPIKey(ctx, key) })
}

// APIKeyByHash reads the key holding hash, its use stamp merged.
func (a *AccountStore) APIKeyByHash(ctx context.Context, hash access.Hash) (*access.Key, bool, error) {
	return viewFound(ctx, a, func(r authReader) (*access.Key, bool, error) { return r.keyByHash(hash) })
}

// ListAPIKeysByUserID lists userID's keys in ID order.
func (a *AccountStore) ListAPIKeysByUserID(ctx context.Context, userID int64) ([]access.Key, error) {
	return viewList(ctx, a, func(r authReader) ([]access.Key, error) { return r.keysOf(userID) })
}

// ListAPIKeys lists every user's keys in ID order.
func (a *AccountStore) ListAPIKeys(ctx context.Context) ([]access.Key, error) {
	return viewList(ctx, a, authReader.allKeys)
}

// DeleteAPIKey deletes ref's key, or answers account.ErrNotFound.
func (a *AccountStore) DeleteAPIKey(ctx context.Context, ref access.KeyRef) error {
	return a.single(ctx, "delete key", func(x *accountTx) error { return x.DeleteAPIKey(ctx, ref) })
}

// DeleteUserAPIKeys deletes userID's keys and returns their IDs.
func (a *AccountStore) DeleteUserAPIKeys(ctx context.Context, userID int64) ([]int64, error) {
	return singleValue(ctx, a, "delete user keys", func(x *accountTx) ([]int64, error) { return x.DeleteUserAPIKeys(ctx, userID) })
}

// TouchAPIKey raises keyID's LastUsedAt to at when at is later (rule 21).
func (a *AccountStore) TouchAPIKey(ctx context.Context, keyID int64, at time.Time) error {
	return a.single(ctx, "touch key", func(x *accountTx) error { return x.TouchAPIKey(ctx, keyID, at) })
}
