package statedb

import (
	"bytes"
	"context"
	"errors"
	"slices"

	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
)

var (
	errLogClass     = errors.New("statedb: a log class outside account.LogClasses")
	errLogLimit     = errors.New("statedb: a log limit below 1")
	errLogCriterion = errors.New("statedb: a log prune needs exactly one of a positive Keep and a non-zero Before")
)

// logReadKind is one read the log helpers report to logRead.
type logReadKind uint8

const (
	logReadIndexKey logReadKind = iota + 1 // one class index key a scan visited
	logReadCounter                         // one class counter read
)

// logRead receives each class index key and counter read the log helpers
// make; bolttable has no read hook, so a cost test swaps it to count them.
var logRead = func(logReadKind) {}

func knownClass(c account.LogClass) error {
	if !slices.Contains(account.LogClasses(), c) {
		return errLogClass
	}
	return nil
}

// AppendLog assigns e.Event.Seq from table 0x2C's sequence inside the one
// writer transaction, so Seq order is commit order and a rolled-back run
// returns its Seq, and stores a copy of e.
func (x *accountTx) AppendLog(_ context.Context, e *account.LogEntry) error {
	if err := knownClass(e.Class); err != nil {
		return err
	}
	if err := x.ensureLogClass(e.Class); err != nil {
		return err
	}
	seq, err := newSeq(x.tx, x.s.a.log)
	if err != nil {
		return err
	}
	row := logRowOf(e)
	if err := x.s.a.log.Put(x.tx, seq, &row); err != nil {
		return err
	}
	set(x, &e.Event.Seq, seq)
	return nil
}

// ensureLogClass creates class's log_classes row, Evicted 0, when absent,
// so LogStats lists every class that has ever held an entry.
func (x *accountTx) ensureLogClass(class account.LogClass) error {
	_, found, err := x.s.a.logClasses.Get(x.tx.Read(), class)
	if err != nil || found {
		return err
	}
	return x.s.a.logClasses.Put(x.tx, class, &logClassRow{})
}

func (r authReader) log(lr account.LogRange) ([]account.LogEntry, error) {
	if lr.Limit < 1 {
		return nil, errLogLimit
	}
	out := []account.LogEntry{}
	if lr.AfterSeq >= maxID {
		return out, nil
	}
	rg := bolttable.Range{From: logSeqKeys.AppendKey(nil, lr.AfterSeq+1), Reverse: lr.Descending}
	if lr.BeforeSeq != 0 {
		rg.To = logSeqKeys.AppendKey(nil, lr.BeforeSeq)
	}
	err := r.a.log.Scan(r.r, rg, func(seq uint64, row *logRow) (bool, error) {
		out = append(out, logEntryOf(seq, row))
		return len(out) < lr.Limit, nil
	})
	return out, err
}

func (r authReader) logStats() ([]account.LogClassStats, error) {
	out := []account.LogClassStats{}
	for _, class := range account.LogClasses() {
		row, found, err := r.a.logClasses.Get(r.r, class)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		n, err := r.logCount(class)
		if err != nil {
			return nil, err
		}
		out = append(out, account.LogClassStats{Class: class, Count: int(n), Evicted: row.Evicted})
	}
	return out, nil
}

func (r authReader) logCount(class account.LogClass) (int64, error) {
	logRead(logReadCounter)
	return r.a.logCounts[class].Value(r.r)
}

// classSeqs returns the Seqs of class's entries, lowest first, for which
// keep holds of the entry's projected At, at most limit of them.
func (r authReader) classSeqs(class account.LogClass, limit int, keep func(at []byte) bool) ([]uint64, error) {
	var seqs []uint64
	err := r.a.logByClass.Scan(r.r, bolttable.Range{Prefix: logClassKeys.AppendKey(nil, class)}, func(_ []byte, seq uint64, at []byte) (bool, error) {
		logRead(logReadIndexKey)
		if keep(at) {
			seqs = append(seqs, seq)
		}
		return len(seqs) < limit, nil
	})
	return seqs, err
}

// pruned returns the Seqs p removes, lowest first.
func (r authReader) pruned(p *account.LogPrune) ([]uint64, error) {
	if p.Keep == 0 {
		before := appendTime(nil, p.Before)
		return r.classSeqs(p.Class, p.Limit, func(at []byte) bool { return bytes.Compare(at, before) < 0 })
	}
	n, err := r.logCount(p.Class)
	if err != nil || n <= int64(p.Keep) {
		return nil, err
	}
	return r.classSeqs(p.Class, int(min(n-int64(p.Keep), int64(p.Limit))), func([]byte) bool { return true })
}

func checkPrune(p *account.LogPrune) error {
	switch {
	case p.Limit < 1:
		return errLogLimit
	case p.Keep < 0, (p.Keep > 0) == !p.Before.IsZero():
		return errLogCriterion
	}
	return knownClass(p.Class)
}

// PruneLog removes p.Class's entries lowest Seq first. A Keep prune reads
// the class's counter and visits only the index keys it deletes, adding
// their number to the class's Evicted; a prune that removes nothing writes
// nothing, so LogStats never lists a class that held no entry. A Before
// prune walks the class's index, comparing each projected At, because the
// service clock can step back, and leaves Evicted unchanged.
func (x *accountTx) PruneLog(_ context.Context, p account.LogPrune) (int, error) {
	if err := checkPrune(&p); err != nil {
		return 0, err
	}
	seqs, err := x.read().pruned(&p)
	if err != nil {
		return 0, err
	}
	for _, seq := range seqs {
		if _, err := x.s.a.log.Delete(x.tx, seq); err != nil {
			return 0, err
		}
	}
	if p.Keep > 0 && len(seqs) > 0 {
		row, _, err := x.s.a.logClasses.Get(x.tx.Read(), p.Class)
		if err != nil {
			return 0, err
		}
		row.Evicted += uint64(len(seqs))
		if err := x.s.a.logClasses.Put(x.tx, p.Class, &row); err != nil {
			return 0, err
		}
	}
	return len(seqs), nil
}

func (x *accountTx) ListLog(_ context.Context, r account.LogRange) ([]account.LogEntry, error) {
	return x.read().log(r)
}

func (x *accountTx) LogStats(context.Context) ([]account.LogClassStats, error) {
	return x.read().logStats()
}

// AppendLog stores e as one single-call write and assigns e.Event.Seq once
// it commits.
func (a *AccountStore) AppendLog(ctx context.Context, e *account.LogEntry) error {
	return a.single(ctx, "append log", func(x *accountTx) error { return x.AppendLog(ctx, e) })
}

// ListLog reads r's entries in one View.
func (a *AccountStore) ListLog(ctx context.Context, r account.LogRange) ([]account.LogEntry, error) {
	return viewList(ctx, a, func(rd authReader) ([]account.LogEntry, error) { return rd.log(r) })
}

// PruneLog applies p as one single-call write and returns how many entries
// the committed run removed.
func (a *AccountStore) PruneLog(ctx context.Context, p account.LogPrune) (int, error) {
	return singleValue(ctx, a, "prune log", func(x *accountTx) (int, error) { return x.PruneLog(ctx, p) })
}

// LogStats reads every class that has held an entry, in account.LogClasses
// order, in one View.
func (a *AccountStore) LogStats(ctx context.Context) ([]account.LogClassStats, error) {
	return viewList(ctx, a, authReader.logStats)
}
