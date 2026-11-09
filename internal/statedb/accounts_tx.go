package statedb

import (
	"slices"

	"github.com/cplieger/bolttable"
)

// accountTx is the account.Tx one Update callback receives, valid only
// until that callback returns.
type accountTx struct {
	s     *Store
	tx    *bolttable.Tx
	edits *callerEdits
}

func (x *accountTx) read() authReader { return authReader{a: x.s.a, r: x.tx.Read()} }

// callerEdits records each value a call's runs assign into its caller's
// records. db.Batch may rerun a call after rolling a run back, so each run
// first restores what the previous run assigned, and a failed call restores
// all of it: the caller keeps only the committed run's IDs and lineage.
type callerEdits struct{ restore []func() }

// set assigns v to *dst for the caller, undone if this run does not commit.
func set[T any](x *accountTx, dst *T, v T) {
	old := *dst
	x.edits.restore = append(x.edits.restore, func() { *dst = old })
	*dst = v
}

func (e *callerEdits) revert() {
	for _, undo := range slices.Backward(e.restore) {
		undo()
	}
	e.restore = e.restore[:0]
}

// authReader reads auth's rows as access records, each a fresh decoded
// value the caller owns (rule 2).
type authReader struct {
	a *authTables
	r *bolttable.ReadTx
}
