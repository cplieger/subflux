package statedb

import (
	"errors"
	"math"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/access/account"
)

func testLogEntry(class account.LogClass, at time.Time) *account.LogEntry {
	kind := account.EventKeyMinted
	if class == account.LogClassSession {
		kind = account.EventSignedIn
	}
	return &account.LogEntry{
		At: at, Class: class, IP: netip.MustParseAddr("192.0.2.1"),
		Event: account.Event{At: at, Kind: kind, UserID: 1, SessionIDs: []string{"s1"}},
	}
}

func mustAppendLog(t testing.TB, a *AccountStore, e *account.LogEntry) uint64 {
	t.Helper()
	if err := a.AppendLog(t.Context(), e); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	return e.Event.Seq
}

// appendLogBulk appends n change entries in one Update, so a cost test
// reaches a full class without n commits.
func appendLogBulk(t testing.TB, a *AccountStore, n int) {
	t.Helper()
	ctx := t.Context()
	if err := a.Update(ctx, func(tx account.Tx) error {
		for range n {
			if err := tx.AppendLog(ctx, testLogEntry(account.LogClassChange, accountsAt)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("bulk AppendLog: %v", err)
	}
}

func logSeqs(t *testing.T, a *AccountStore) []uint64 {
	t.Helper()
	got, err := a.ListLog(t.Context(), account.LogRange{Limit: 100_000})
	if err != nil {
		t.Fatalf("ListLog: %v", err)
	}
	seqs := make([]uint64, len(got))
	for i := range got {
		seqs[i] = got[i].Event.Seq
	}
	return seqs
}

func logStats(t *testing.T, a *AccountStore) []account.LogClassStats {
	t.Helper()
	st, err := a.LogStats(t.Context())
	if err != nil {
		t.Fatalf("LogStats: %v", err)
	}
	return st
}

func mustPruneLog(t *testing.T, a *AccountStore, p account.LogPrune) int {
	t.Helper()
	n, err := a.PruneLog(t.Context(), p)
	if err != nil {
		t.Fatalf("PruneLog(%+v): %v", p, err)
	}
	return n
}

// countLogReads swaps logRead for counters of each kind until the test ends.
func countLogReads(t testing.TB) (indexKeys, counters *atomic.Int64) {
	t.Helper()
	indexKeys, counters = new(atomic.Int64), new(atomic.Int64)
	old := logRead
	logRead = func(k logReadKind) {
		switch k {
		case logReadIndexKey:
			indexKeys.Add(1)
		case logReadCounter:
			counters.Add(1)
		}
	}
	t.Cleanup(func() { logRead = old })
	return indexKeys, counters
}

func TestLogKeepTrimIsCounted(t *testing.T) {
	a := newStore(t).Accounts()
	appendLogBulk(t, a, 10_000)
	mustAppendLog(t, a, testLogEntry(account.LogClassChange, accountsAt))
	keys, counters := countLogReads(t)
	trim := account.LogPrune{Class: account.LogClassChange, Keep: 10_000, Limit: 10_000}
	if n := mustPruneLog(t, a, trim); n != 1 {
		t.Errorf("a Keep 10000 trim of 10001 removed %d, want 1", n)
	}
	if k, c := keys.Load(), counters.Load(); k > 2 || c != 1 {
		t.Errorf("the trim read %d class index keys and %d counters, want at most 2 and 1", k, c)
	}
	keys.Store(0)
	counters.Store(0)
	if n := mustPruneLog(t, a, trim); n != 0 {
		t.Errorf("a trim at the cap removed %d, want 0", n)
	}
	if k, c := keys.Load(), counters.Load(); k != 0 || c != 1 {
		t.Errorf("a trim at the cap read %d class index keys and %d counters, want 0 and 1", k, c)
	}
}

func TestLogEvictedIsPrimary(t *testing.T) {
	path := tempPath(t)
	s := mustOpen(t, path, productionConfig())
	a := s.Accounts()
	ctx := t.Context()
	for range 5 {
		mustAppendLog(t, a, testLogEntry(account.LogClassChange, accountsAt))
	}
	evicted := func(t *testing.T, a *AccountStore) uint64 {
		t.Helper()
		st := logStats(t, a)
		if len(st) != 1 {
			t.Fatalf("LogStats %+v, want the change class alone", st)
		}
		return st[0].Evicted
	}
	t.Run("keep prunes raise it", func(t *testing.T) {
		mustPruneLog(t, a, account.LogPrune{Class: account.LogClassChange, Keep: 4, Limit: 10})
		mustPruneLog(t, a, account.LogPrune{Class: account.LogClassChange, Keep: 2, Limit: 10})
		if e := evicted(t, a); e != 3 {
			t.Errorf("Evicted %d after removing 1 then 2, want 3", e)
		}
	})
	t.Run("a before prune leaves it", func(t *testing.T) {
		mustAppendLog(t, a, testLogEntry(account.LogClassChange, accountsAt.Add(-time.Hour)))
		if n := mustPruneLog(t, a, account.LogPrune{Class: account.LogClassChange, Before: accountsAt, Limit: 10}); n != 1 {
			t.Fatalf("the Before prune removed %d, want the older entry", n)
		}
		if e := evicted(t, a); e != 3 {
			t.Errorf("Evicted %d after a Before prune, want 3", e)
		}
	})
	t.Run("a rolled-back prune leaves it", func(t *testing.T) {
		errRollback := errors.New("roll back")
		err := a.Update(ctx, func(tx account.Tx) error {
			_, err := tx.PruneLog(ctx, account.LogPrune{Class: account.LogClassChange, Keep: 1, Limit: 10})
			return errors.Join(err, errRollback)
		})
		if !errors.Is(err, errRollback) {
			t.Fatalf("Update = %v, want the callback's error", err)
		}
		if e := evicted(t, a); e != 3 {
			t.Errorf("Evicted %d after a rolled-back prune, want 3", e)
		}
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = mustOpen(t, path, productionConfig())
	a = s.Accounts()
	t.Run("a reopen keeps it", func(t *testing.T) {
		if e := evicted(t, a); e != 3 {
			t.Errorf("Evicted %d after a reopen, want 3", e)
		}
	})
	t.Run("verify and repair leave it", func(t *testing.T) {
		activate(t, s, Activation{})
		for _, o := range []InspectOptions{{Verify: true}, {Repair: true}} {
			in := inspect(t, s, o)
			if len(in.Mismatches) != 0 {
				t.Errorf("Inspect(%+v) mismatches %+v, want none", o, in.Mismatches)
			}
			rows := map[string]int{}
			for _, ns := range in.Namespaces {
				for _, tb := range ns.Tables {
					rows[ns.Name+"."+tb.Name] = tb.Rows
				}
			}
			if rows["auth.security_log"] != 2 || rows["auth.log_classes"] != 1 {
				t.Errorf("Inspect(%+v) rows %v, want 2 security_log rows and 1 log_classes row", o, rows)
			}
		}
		if e := evicted(t, a); e != 3 {
			t.Errorf("Evicted %d after Inspect(Repair), want 3", e)
		}
	})
}

func TestLogBeforePruneSteppedClock(t *testing.T) {
	a := newStore(t).Accounts()
	noon := accountsAt
	var seqs []uint64
	for _, at := range []time.Time{noon, noon.Add(-time.Hour), noon.Add(time.Hour), noon.Add(-30 * time.Minute)} {
		seqs = append(seqs, mustAppendLog(t, a, testLogEntry(account.LogClassChange, at)))
	}
	before := account.LogPrune{Class: account.LogClassChange, Before: noon.Add(30 * time.Minute), Limit: 2}
	if n := mustPruneLog(t, a, before); n != 2 {
		t.Errorf("a Before 12:30 prune with Limit 2 removed %d, want 2", n)
	}
	if got, want := logSeqs(t, a), []uint64{seqs[2], seqs[3]}; !slices.Equal(got, want) {
		t.Errorf("after the Limit 2 prune the log holds %v, want %v: 12:00 and 11:00 gone, lowest Seq first", got, want)
	}
	before.Limit = 10
	if n := mustPruneLog(t, a, before); n != 1 {
		t.Errorf("the second Before 12:30 prune removed %d, want the 11:30 entry after the 13:00 one", n)
	}
	if got, want := logSeqs(t, a), []uint64{seqs[2]}; !slices.Equal(got, want) {
		t.Errorf("after both prunes the log holds %v, want %v", got, want)
	}
}

func TestLogListRange(t *testing.T) {
	a := newStore(t).Accounts()
	for range 5 {
		mustAppendLog(t, a, testLogEntry(account.LogClassChange, accountsAt))
	}
	for _, tc := range []struct {
		name string
		r    account.LogRange
		want []uint64
	}{
		{"limit", account.LogRange{Limit: 2}, []uint64{1, 2}},
		{"limit descending", account.LogRange{Limit: 2, Descending: true}, []uint64{5, 4}},
		{"after alone", account.LogRange{AfterSeq: 1, Limit: 10}, []uint64{2, 3, 4, 5}},
		{"before alone", account.LogRange{BeforeSeq: 3, Limit: 10}, []uint64{1, 2}},
		{"both bounds", account.LogRange{AfterSeq: 1, BeforeSeq: 4, Limit: 10}, []uint64{2, 3}},
		{"both bounds descending", account.LogRange{AfterSeq: 1, BeforeSeq: 4, Limit: 10, Descending: true}, []uint64{3, 2}},
		{"empty", account.LogRange{AfterSeq: 3, BeforeSeq: 4, Limit: 10}, []uint64{}},
		{"after the largest id", account.LogRange{AfterSeq: maxID, Limit: 10}, []uint64{}},
		{"after the largest uint64 does not wrap", account.LogRange{AfterSeq: math.MaxUint64, Limit: 10}, []uint64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.ListLog(t.Context(), tc.r)
			if err != nil {
				t.Fatalf("ListLog: %v", err)
			}
			seqs := make([]uint64, len(got))
			for i := range got {
				seqs[i] = got[i].Event.Seq
			}
			if !slices.Equal(seqs, tc.want) {
				t.Errorf("ListLog(%+v) = %v, want %v", tc.r, seqs, tc.want)
			}
		})
	}
	t.Run("limit below one", func(t *testing.T) {
		if _, err := a.ListLog(t.Context(), account.LogRange{}); !errors.Is(err, errLogLimit) {
			t.Errorf("ListLog with Limit 0 = %v, want errLogLimit", err)
		}
	})
}

func TestLogPruneRefusesBadPrune(t *testing.T) {
	a := newStore(t).Accounts()
	for range 3 {
		mustAppendLog(t, a, testLogEntry(account.LogClassChange, accountsAt))
	}
	wantSeqs, wantStats := logSeqs(t, a), logStats(t, a)
	for _, tc := range []struct {
		name string
		p    account.LogPrune
		want error
	}{
		{"both criteria", account.LogPrune{Class: account.LogClassChange, Keep: 1, Before: accountsAt.Add(time.Hour), Limit: 10}, errLogCriterion},
		{"neither criterion", account.LogPrune{Class: account.LogClassChange, Limit: 10}, errLogCriterion},
		{"negative keep", account.LogPrune{Class: account.LogClassChange, Keep: -1, Limit: 10}, errLogCriterion},
		{"limit zero", account.LogPrune{Class: account.LogClassChange, Keep: 1}, errLogLimit},
		{"unknown class", account.LogPrune{Class: "other", Keep: 1, Limit: 10}, errLogClass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if n, err := a.PruneLog(t.Context(), tc.p); n != 0 || !errors.Is(err, tc.want) {
				t.Errorf("PruneLog(%+v) = %d, %v; want 0, %v", tc.p, n, err, tc.want)
			}
			if got := logSeqs(t, a); !slices.Equal(got, wantSeqs) {
				t.Errorf("the refused prune left %v, want %v", got, wantSeqs)
			}
			if got := logStats(t, a); !slices.Equal(got, wantStats) {
				t.Errorf("the refused prune left LogStats %+v, want %+v", got, wantStats)
			}
		})
	}
}

func TestAppendLogRefusesUnknownClass(t *testing.T) {
	a := newStore(t).Accounts()
	e := testLogEntry("other", accountsAt)
	if err := a.AppendLog(t.Context(), e); !errors.Is(err, errLogClass) || e.Event.Seq != 0 {
		t.Errorf("AppendLog of class other = %v, Seq %d; want errLogClass, 0", err, e.Event.Seq)
	}
	if got, st := logSeqs(t, a), logStats(t, a); len(got) != 0 || len(st) != 0 {
		t.Errorf("after the refused append the log holds %v and LogStats %+v, want neither", got, st)
	}
}

func TestLogSeqNeverReusedAfterPruneAndReopen(t *testing.T) {
	path := tempPath(t)
	s := mustOpen(t, path, productionConfig())
	a := s.Accounts()
	var last uint64
	for range 3 {
		last = mustAppendLog(t, a, testLogEntry(account.LogClassChange, accountsAt))
	}
	mustPruneLog(t, a, account.LogPrune{Class: account.LogClassChange, Keep: 1, Limit: 10})
	mustPruneLog(t, a, account.LogPrune{Class: account.LogClassChange, Before: accountsAt.Add(time.Hour), Limit: 10})
	if got := logSeqs(t, a); len(got) != 0 {
		t.Fatalf("after pruning every entry the log holds %v", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	a = mustOpen(t, path, productionConfig()).Accounts()
	if seq := mustAppendLog(t, a, testLogEntry(account.LogClassSession, accountsAt)); seq <= last {
		t.Errorf("after the prunes and a reopen the next append got Seq %d, want above %d", seq, last)
	}
}

func TestLogStatsAfterAgePrune(t *testing.T) {
	a := newStore(t).Accounts()
	for range 3 {
		mustAppendLog(t, a, testLogEntry(account.LogClassChange, accountsAt))
	}
	mustPruneLog(t, a, account.LogPrune{Class: account.LogClassChange, Keep: 1, Limit: 10})
	mustPruneLog(t, a, account.LogPrune{Class: account.LogClassChange, Before: accountsAt.Add(time.Hour), Limit: 10})
	want := []account.LogClassStats{{Class: account.LogClassChange, Count: 0, Evicted: 2}}
	if got := logStats(t, a); !slices.Equal(got, want) {
		t.Errorf("LogStats after every entry aged out = %+v, want %+v", got, want)
	}
}

func TestLogStatsListsOnlyClassesThatHeldAnEntry(t *testing.T) {
	path := tempPath(t)
	s := mustOpen(t, path, productionConfig())
	a := s.Accounts()
	for _, c := range account.LogClasses() {
		mustPruneLog(t, a, account.LogPrune{Class: c, Keep: 10, Limit: 10})
		mustPruneLog(t, a, account.LogPrune{Class: c, Before: accountsAt.Add(time.Hour), Limit: 10})
	}
	if got := logStats(t, a); len(got) != 0 {
		t.Errorf("LogStats after prunes of an empty log = %+v, want none", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	a = mustOpen(t, path, productionConfig()).Accounts()
	if got := logStats(t, a); len(got) != 0 {
		t.Errorf("LogStats after prunes of an empty log and a reopen = %+v, want none", got)
	}
	mustAppendLog(t, a, testLogEntry(account.LogClassChange, accountsAt))
	for _, c := range account.LogClasses() {
		mustPruneLog(t, a, account.LogPrune{Class: c, Keep: 10, Limit: 10})
	}
	want := []account.LogClassStats{{Class: account.LogClassChange, Count: 1, Evicted: 0}}
	if got := logStats(t, a); !slices.Equal(got, want) {
		t.Errorf("LogStats after Keep prunes beside one change entry = %+v, want %+v", got, want)
	}
}
