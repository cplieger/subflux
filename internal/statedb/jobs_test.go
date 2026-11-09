package statedb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

// small keeps Total above PerOwner, as DefaultLimits does, at sizes a case
// fills in a few writes; the contract runs DefaultLimits.
var small = status.JobLimits{PerOwner: 4, Total: 6}

var jobAt = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

type jobMaker struct{ n int }

// job is a fresh record of owner, Done and ended at ended when ended is
// not zero, else Running; a key gets a fingerprint unique to the record.
func (m *jobMaker) job(owner int64, key string, ended time.Time) status.Job {
	m.n++
	j := status.Job{
		ID: status.JobID(fmt.Sprintf("%032x", m.n)), Kind: status.Scan, Owner: owner,
		State: status.Running, Rev: 1, Started: jobAt.Add(-time.Hour),
	}
	if key != "" {
		j.Key = &status.JobKey{Key: key, Fingerprint: sha256.Sum256([]byte(j.ID))}
	}
	if !ended.IsZero() {
		j.State, j.Outcome, j.Ended = status.Done, status.Succeeded, ended
	}
	return j
}

func insertJob(t *testing.T, s *Store, j status.Job, lim status.JobLimits) status.Job {
	t.Helper()
	got, replayed, err := s.InsertJob(t.Context(), j, lim)
	if err != nil || replayed || got.ID != j.ID {
		t.Fatalf("InsertJob(%s owner %d) = %s replayed %v, %v; want it stored", j.ID, j.Owner, got.ID, replayed, err)
	}
	return got
}

func refuseJob(t *testing.T, s *Store, j status.Job, lim status.JobLimits, want error) {
	t.Helper()
	before := jobCount(t, s)
	_, _, err := s.InsertJob(t.Context(), j, lim)
	if err == nil || (want != nil && !errors.Is(err, want)) {
		t.Fatalf("InsertJob(%s owner %d) = %v, want %v", j.ID, j.Owner, err, want)
	}
	if want == nil && (errors.Is(err, status.ErrJobCapacity) || errors.Is(err, status.ErrKeyReused)) {
		t.Errorf("InsertJob(%s) = %v, want a plain error", j.ID, err)
	}
	if n := jobCount(t, s); n != before {
		t.Errorf("a refused insert changed the record count from %d to %d", before, n)
	}
}

func hasJob(t *testing.T, s *Store, id status.JobID) bool {
	t.Helper()
	_, found, err := s.GetJob(t.Context(), id)
	if err != nil {
		t.Fatalf("GetJob(%s): %v", id, err)
	}
	return found
}

func jobCount(t *testing.T, s *Store) int {
	t.Helper()
	n := 0
	if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) error {
		return s.t.jobs.Scan(r, bolttable.Range{}, func(status.JobID, *jobRow) (bool, error) {
			n++
			return true, nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func hasKey(t *testing.T, s *Store, owner int64, key string) bool {
	t.Helper()
	_, found, err := s.JobByKey(t.Context(), owner, key)
	if err != nil {
		t.Fatalf("JobByKey(%d, %q): %v", owner, key, err)
	}
	return found
}

func TestPersisterContract(t *testing.T) {
	statustest.PersisterContract(t, func(t *testing.T) status.Persister { return newStore(t) })
}

func TestTotalNeverRefusesSystemOwner(t *testing.T) {
	s, m := newStore(t), &jobMaker{}
	for _, owner := range []int64{1, 2} {
		for range small.Total / 2 {
			insertJob(t, s, m.job(owner, "", time.Time{}), small)
		}
	}
	insertJob(t, s, m.job(status.SystemOwner, "", time.Time{}), small)
	refuseJob(t, s, m.job(3, "", time.Time{}), small, status.ErrJobCapacity)
}

func TestFullBoundEvictsOldestEnded(t *testing.T) {
	const o7, o8 = int64(7), int64(8)
	at := func(h, m int) time.Time { return time.Date(2026, 3, 1, h, m, 0, 0, time.UTC) }

	// ownerFull fills o7 to PerOwner: a keyed record ended at 10:00, a
	// keyless one ended at 11:00, the rest unfinished.
	ownerFull := func(t *testing.T) (s *Store, m *jobMaker, keyed, keyless status.Job) {
		s, m = newStore(t), &jobMaker{}
		keyed = insertJob(t, s, m.job(o7, "a", at(10, 0)), small)
		keyless = insertJob(t, s, m.job(o7, "", at(11, 0)), small)
		for range small.PerOwner - 2 {
			insertJob(t, s, m.job(o7, "", time.Time{}), small)
		}
		return s, m, keyed, keyless
	}

	t.Run("owner bound evicts its oldest ended keyed record", func(t *testing.T) {
		s, m, keyed, keyless := ownerFull(t)
		fresh := insertJob(t, s, m.job(o7, "", time.Time{}), small)
		if hasJob(t, s, keyed.ID) || hasKey(t, s, o7, "a") {
			t.Error("the 10:00 keyed record or its key entry survived the start")
		}
		if !hasJob(t, s, keyless.ID) || !hasJob(t, s, fresh.ID) {
			t.Error("the start deleted more than the oldest ended record")
		}
	})

	t.Run("owner bound with every record unfinished refuses", func(t *testing.T) {
		s, m := newStore(t), &jobMaker{}
		for range small.PerOwner {
			insertJob(t, s, m.job(o7, "", time.Time{}), small)
		}
		refuseJob(t, s, m.job(o7, "", time.Time{}), small, status.ErrJobCapacity)
		refuseJob(t, s, m.job(o7, "next", time.Time{}), small, status.ErrJobCapacity)
	})

	t.Run("total bound evicts any user's oldest ended record", func(t *testing.T) {
		s, m := newStore(t), &jobMaker{}
		keyed8 := insertJob(t, s, m.job(o8, "b", at(9, 0)), small)
		keyless7 := insertJob(t, s, m.job(o7, "", at(10, 0)), small)
		for range small.Total/2 - 1 {
			insertJob(t, s, m.job(o7, "", time.Time{}), small)
			insertJob(t, s, m.job(o8, "", time.Time{}), small)
		}
		insertJob(t, s, m.job(o7, "", time.Time{}), small)
		if hasJob(t, s, keyed8.ID) || hasKey(t, s, o8, "b") || !hasJob(t, s, keyless7.ID) {
			t.Fatal("o7's start at Total did not delete exactly o8's 09:00 keyed record")
		}
		insertJob(t, s, m.job(o8, "", time.Time{}), small)
		if hasJob(t, s, keyless7.ID) {
			t.Error("o8's start at Total kept o7's 10:00 keyless record")
		}
		if n := jobCount(t, s); n != small.Total {
			t.Errorf("%d records stored, want Total %d", n, small.Total)
		}
	})

	t.Run("total bound with every user record unfinished refuses", func(t *testing.T) {
		s, m := newStore(t), &jobMaker{}
		for range small.Total / 2 {
			insertJob(t, s, m.job(o7, "", time.Time{}), small)
			insertJob(t, s, m.job(o8, "", time.Time{}), small)
		}
		insertJob(t, s, m.job(status.SystemOwner, "", at(8, 0)), small)
		refuseJob(t, s, m.job(9, "", time.Time{}), small, status.ErrJobCapacity)
		refuseJob(t, s, m.job(status.BypassOwner, "k", time.Time{}), small, status.ErrJobCapacity)
	})

	t.Run("keyed resend at a full table replays without evicting", func(t *testing.T) {
		s, m, keyed, keyless := ownerFull(t)
		resend := m.job(o7, "a", time.Time{})
		resend.Key.Fingerprint = keyed.Key.Fingerprint
		got, replayed, err := s.InsertJob(t.Context(), resend, small)
		if err != nil || !replayed || got.ID != keyed.ID || got.State != status.Done {
			t.Fatalf("resend = %s %v replayed %v, %v; want the stored %s", got.ID, got.State, replayed, err, keyed.ID)
		}
		refuseJob(t, s, m.job(o7, "a", time.Time{}), small, status.ErrKeyReused)
		if !hasJob(t, s, keyless.ID) || hasJob(t, s, resend.ID) {
			t.Error("a replay or a reused key evicted a record or stored one")
		}
	})

	t.Run("system bound evicts its own oldest ended record", func(t *testing.T) {
		s, m := newStore(t), &jobMaker{}
		user := insertJob(t, s, m.job(o7, "", at(8, 0)), small)
		var sys []status.Job
		for i := range small.PerOwner {
			sys = append(sys, insertJob(t, s, m.job(status.SystemOwner, "", at(9, i)), small))
		}
		insertJob(t, s, m.job(status.SystemOwner, "", time.Time{}), small)
		if hasJob(t, s, sys[0].ID) || !hasJob(t, s, sys[1].ID) || !hasJob(t, s, user.ID) {
			t.Error("the system start did not delete exactly its own oldest ended record")
		}
	})

	t.Run("an evicted key's resend is a new start", func(t *testing.T) {
		s, m, keyed, keyless := ownerFull(t)
		insertJob(t, s, m.job(o7, "", time.Time{}), small)
		again := m.job(o7, "a", time.Time{})
		again.Key.Fingerprint = keyed.Key.Fingerprint
		insertJob(t, s, again, small)
		if hasJob(t, s, keyless.ID) {
			t.Error("the new start made no room at the full owner bound")
		}
		if got, found, err := s.JobByKey(t.Context(), o7, "a"); err != nil || !found || got.ID != again.ID {
			t.Errorf("JobByKey(o7, a) = %s %v %v; want the new start %s", got.ID, found, err, again.ID)
		}
	})
}

func TestInsertExistingIDAndPutAbsentID(t *testing.T) {
	s, m := newStore(t), &jobMaker{}
	one := status.JobLimits{PerOwner: 1, Total: 10}
	stored := insertJob(t, s, m.job(7, "", time.Time{}), small)
	refuseJob(t, s, stored, small, nil)

	put := func(name string, j status.Job) {
		j.Rev++
		if err := s.PutJob(t.Context(), j); err == nil || errors.Is(err, status.ErrJobCapacity) {
			t.Errorf("PutJob of %s = %v, want a plain error", name, err)
		}
		if hasJob(t, s, j.ID) {
			t.Errorf("PutJob of %s stored it", name)
		}
	}
	put("a never stored id", m.job(7, "", time.Time{}))

	evicted := insertJob(t, s, m.job(8, "", jobAt), one)
	insertJob(t, s, m.job(8, "", time.Time{}), one)
	put("an id a bound evicted", evicted)

	swept := insertJob(t, s, m.job(9, "", jobAt), small)
	if n, err := s.DeleteJobsEndedBefore(t.Context(), jobAt.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("DeleteJobsEndedBefore = %d, %v; want 1", n, err)
	}
	put("an id the sweep deleted", swept)
}

func TestJobsEndedOrderByInstant(t *testing.T) {
	noon := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	before := noon.Add(-500 * time.Millisecond)
	two := status.JobLimits{PerOwner: 2, Total: 10}

	t.Run("the bound evicts the earlier instant", func(t *testing.T) {
		s, m := newStore(t), &jobMaker{}
		atNoon := insertJob(t, s, m.job(status.SystemOwner, "", noon), two)
		early := insertJob(t, s, m.job(status.SystemOwner, "", before), two)
		insertJob(t, s, m.job(status.SystemOwner, "", time.Time{}), two)
		if hasJob(t, s, early.ID) || !hasJob(t, s, atNoon.ID) {
			t.Error("the system bound did not evict the record ended at 11:59:59.5 first")
		}
	})

	for _, tc := range []struct {
		cut        time.Time
		name       string
		keepAtNoon bool
	}{
		{name: "a cut past noon deletes both", cut: noon.Add(100 * time.Millisecond)},
		{name: "a cut at noon keeps the record ended at noon", cut: noon, keepAtNoon: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, m := newStore(t), &jobMaker{}
			atNoon := insertJob(t, s, m.job(7, "", noon), small)
			early := insertJob(t, s, m.job(7, "", before), small)
			want := 2
			if tc.keepAtNoon {
				want = 1
			}
			if n, err := s.DeleteJobsEndedBefore(t.Context(), tc.cut); err != nil || n != want {
				t.Errorf("DeleteJobsEndedBefore(%s) = %d, %v; want %d", tc.cut.Format(time.RFC3339Nano), n, err, want)
			}
			if hasJob(t, s, early.ID) {
				t.Error("the record ended at 11:59:59.5 survived")
			}
			if hasJob(t, s, atNoon.ID) != tc.keepAtNoon {
				t.Errorf("the record ended at noon kept = %v, want %v", !tc.keepAtNoon, tc.keepAtNoon)
			}
		})
	}
}

func TestJobsAC14(t *testing.T) {
	t.Run("a bypass start is stored and counts against Total", func(t *testing.T) {
		s, m := newStore(t), &jobMaker{}
		for range small.Total - 1 {
			insertJob(t, s, m.job(7, "", time.Time{}), status.JobLimits{PerOwner: small.Total, Total: small.Total})
		}
		insertJob(t, s, m.job(status.BypassOwner, "k", time.Time{}), small)
		refuseJob(t, s, m.job(status.BypassOwner, "", time.Time{}), small, status.ErrJobCapacity)
	})

	t.Run("a stale revision is refused", func(t *testing.T) {
		s, m := newStore(t), &jobMaker{}
		j := insertJob(t, s, m.job(7, "", time.Time{}), small)
		j.Rev, j.Phase = 3, "fetch"
		if err := s.PutJob(t.Context(), j); err != nil {
			t.Fatalf("PutJob rev 3: %v", err)
		}
		for _, rev := range []uint64{3, 2} {
			stale := j
			stale.Rev, stale.Phase = rev, "stale"
			if err := s.PutJob(t.Context(), stale); err == nil {
				t.Errorf("PutJob rev %d over the stored 3 succeeded", rev)
			}
		}
		if got, _, err := s.GetJob(t.Context(), j.ID); err != nil || got.Rev != 3 || got.Phase != "fetch" {
			t.Errorf("stored rev %d phase %q, %v; want rev 3 phase fetch", got.Rev, got.Phase, err)
		}
	})

	t.Run("an owner below BypassOwner is refused", func(t *testing.T) {
		s, m := newStore(t), &jobMaker{}
		refuseJob(t, s, m.job(status.BypassOwner-1, "", time.Time{}), small, nil)
	})
}

func TestInsertJobRefusals(t *testing.T) {
	s, m := newStore(t), &jobMaker{}
	for _, lim := range []status.JobLimits{{PerOwner: 0, Total: 1}, {PerOwner: 1, Total: 0}} {
		refuseJob(t, s, m.job(7, "", time.Time{}), lim, nil)
	}
	unended := m.job(7, "", time.Time{})
	unended.State, unended.Outcome = status.Done, status.Succeeded
	refuseJob(t, s, unended, small, nil)
	refuseJob(t, s, status.Job{ID: "x", Owner: 7, Rev: 1, State: status.Running}, small, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := s.InsertJob(ctx, m.job(7, "", time.Time{}), small); !errors.Is(err, context.Canceled) {
		t.Errorf("InsertJob on a done context = %v, want context.Canceled", err)
	}
}

func TestJobRoundTrip(t *testing.T) {
	s := newStore(t)
	j := status.Job{
		ID: "00000000000000000000000000000abc", Started: jobAt, Ended: jobAt.Add(90 * time.Second),
		Key:   &status.JobKey{Key: "idem", Fingerprint: sha256.Sum256([]byte("body"))},
		Scope: "library", Source: "sonarr", Phase: "fetch", Cause: "manual", Owner: 7, Done: 2, Total: 5, Rev: 4,
		Kind: status.Download, State: status.Done, Outcome: status.Partial, Cancellable: true,
	}
	insertJob(t, s, j, small)
	byID, found, err := s.GetJob(t.Context(), j.ID)
	if err != nil || !found {
		t.Fatalf("GetJob = %v %v", found, err)
	}
	byKey, found, err := s.JobByKey(t.Context(), 7, "idem")
	if err != nil || !found {
		t.Fatalf("JobByKey = %v %v", found, err)
	}
	for name, got := range map[string]status.Job{"GetJob": byID, "JobByKey": byKey} {
		if !got.Started.Equal(j.Started) || !got.Ended.Equal(j.Ended) || got.Key == nil || *got.Key != *j.Key {
			t.Errorf("%s times or key = %v %v %+v; want %v %v %+v", name, got.Started, got.Ended, got.Key, j.Started, j.Ended, j.Key)
		}
		got.Started, got.Ended, got.Key = j.Started, j.Ended, j.Key
		if got != j {
			t.Errorf("%s = %+v, want %+v", name, got, j)
		}
	}
	if _, found, err := s.JobByKey(t.Context(), 8, "idem"); err != nil || found {
		t.Errorf("another owner's JobByKey = %v %v; want absent", found, err)
	}
}

func TestUnfinishedJobsBound(t *testing.T) {
	s, m := newStore(t), &jobMaker{}
	most := status.DefaultLimits.Total + status.DefaultLimits.PerOwner
	put := func(n int, state status.State) {
		update(t, s, func(tx *bolttable.Tx) error {
			for range n {
				j := m.job(int64(m.n%50+1), "", jobAt)
				j.State = state
				row := jobRowOf(&j)
				if err := s.t.jobs.Put(tx, j.ID, &row); err != nil {
					return err
				}
			}
			return nil
		})
	}
	put(most-1, status.Done)
	put(1, status.Running)
	got, err := s.UnfinishedJobs(t.Context())
	if err != nil || len(got) != 1 || got[0].State != status.Running {
		t.Fatalf("UnfinishedJobs over %d records = %d jobs, %v; want the one running", most, len(got), err)
	}
	put(1, status.Done)
	if _, err := s.UnfinishedJobs(t.Context()); !errors.Is(err, errTooManyJobs) {
		t.Errorf("UnfinishedJobs over %d records = %v, want errTooManyJobs", most+1, err)
	}
}
