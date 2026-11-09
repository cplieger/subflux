package statedb

import (
	"context"

	"github.com/cplieger/access"
	"github.com/cplieger/bolttable"
)

func (r authReader) attributions() ([]access.KeyAttribution, error) {
	out := []access.KeyAttribution{}
	err := r.a.attributions.Scan(r.r, bolttable.Range{}, func(id int64, row *attributionRow) (bool, error) {
		out = append(out, access.KeyAttribution{
			At: row.At, Action: access.AttributionAction(row.Action), KeyLineage: row.KeyLineage, ID: id, UserID: row.UserID,
		})
		return true, nil
	})
	return out, err
}

// CreateKeyAttribution assigns a.ID from the key_attributions sequence, so
// ascending ID is append order (rule 17), and appends the record.
func (x *accountTx) CreateKeyAttribution(_ context.Context, a *access.KeyAttribution) error {
	id, err := newID(x.tx, x.s.a.attributions)
	if err != nil {
		return err
	}
	row := attributionRow{At: a.At, Action: string(a.Action), KeyLineage: a.KeyLineage, UserID: a.UserID}
	if err := x.s.a.attributions.Put(x.tx, id, &row); err != nil {
		return err
	}
	set(x, &a.ID, id)
	return nil
}

func (x *accountTx) ListKeyAttributions(context.Context) ([]access.KeyAttribution, error) {
	return x.read().attributions()
}

// CreateKeyAttribution assigns a.ID once the record is stored.
func (a *AccountStore) CreateKeyAttribution(ctx context.Context, rec *access.KeyAttribution) error {
	return a.single(ctx, "create key attribution", func(x *accountTx) error { return x.CreateKeyAttribution(ctx, rec) })
}

// ListKeyAttributions returns every record in ascending ID order.
func (a *AccountStore) ListKeyAttributions(ctx context.Context) ([]access.KeyAttribution, error) {
	return viewList(ctx, a, authReader.attributions)
}
