package mediawrite

import (
	"cmp"
	"context"

	"github.com/cplieger/go/keylock"
	"github.com/cplieger/subflux/internal/statedb"
)

// leaseKey is an instance (kind instanceLease) or a file path within one
// root (kind pathLease).
type leaseKey struct {
	root string
	path string
	inst statedb.InstanceID
	kind uint8
}

const (
	instanceLease uint8 = iota + 1
	pathLease
)

// compare orders instance keys before path keys, then by every field, so it
// is a strict total order consistent with ==.
func (leaseKey) compare(a, b leaseKey) int {
	return cmp.Or(
		cmp.Compare(a.kind, b.kind),
		cmp.Compare(a.inst, b.inst),
		cmp.Compare(a.root, b.root),
		cmp.Compare(a.path, b.path),
	)
}

func newLeases() *keylock.Set[leaseKey] { return keylock.New(leaseKey{}.compare) }

// lease takes inst's key, then every path of rels under root in the set's
// order. Holding the instance key first is what keeps two operations from
// waiting on each other: no operation takes an instance key while holding
// a path.
func (w *Writer) lease(ctx context.Context, inst statedb.InstanceID, root string, rels ...string) (unlock func(), err error) {
	unlockInst, err := w.leases.Lock(ctx, leaseKey{kind: instanceLease, inst: inst})
	if err != nil {
		return nil, err
	}
	unlockPaths, err := w.lockPaths(ctx, root, rels...)
	if err != nil {
		unlockInst()
		return nil, err
	}
	return func() {
		unlockPaths()
		unlockInst()
	}, nil
}

// lockPaths takes the path leases of rels under root; the caller holds the
// instance lease they belong to.
func (w *Writer) lockPaths(ctx context.Context, root string, rels ...string) (unlock func(), err error) {
	keys := make([]leaseKey, 0, len(rels))
	for _, rel := range rels {
		keys = append(keys, leaseKey{kind: pathLease, root: root, path: rel})
	}
	return w.leases.LockAll(ctx, keys)
}
