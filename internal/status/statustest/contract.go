package statustest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/status"
)

// PersisterContract runs the behaviour status relies on against a fresh
// persister from open per case, at DefaultLimits.
func PersisterContract(t *testing.T, open func(t *testing.T) status.Persister) {
	t.Helper()
	cases := []struct {
		run  func(t *testing.T, c *contract)
		name string
	}{
		{endedKeyedRecordsMakeRoom, "EndedKeyedRecordsMakeRoom"},
		{endedKeylessRecordMakesRoom, "EndedKeylessRecordMakesRoom"},
		{unfinishedRefuses, "UnfinishedRefuses"},
		{totalEvictsAnyUsersOldestEnded, "TotalEvictsAnyUsersOldestEnded"},
		{totalUnfinishedRefuses, "TotalUnfinishedRefuses"},
		{systemOwnerOutsideTotal, "SystemOwnerOutsideTotal"},
		{systemOwnerEvictsItsOwnOldest, "SystemOwnerEvictsItsOwnOldest"},
		{systemOwnerUnfinishedRefuses, "SystemOwnerUnfinishedRefuses"},
		{keyedResendAtFullTableReplays, "KeyedResendAtFullTableReplays"},
		{evictedKeyStartsAgain, "EvictedKeyStartsAgain"},
		{concurrentInsertsStoreExactlyTheBound, "ConcurrentInsertsStoreExactlyTheBound"},
		{concurrentSameKeyOneWins, "ConcurrentSameKeyOneWins"},
		{insertExistingIDFails, "InsertExistingIDFails"},
		{putJobRevisions, "PutJobRevisions"},
		{unfinishedJobs, "UnfinishedJobs"},
		{deleteJobsEndedBefore, "DeleteJobsEndedBefore"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, &contract{p: open(t), base: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
		})
	}
}

type contract struct {
	base time.Time
	p    status.Persister
	n    atomic.Int64
}

// job is a fresh record of owner, ended at base plus its sequence number in
// minutes when ended is true.
func (c *contract) job(owner int64, key string, ended bool) status.Job {
	n := c.n.Add(1)
	j := status.Job{
		ID:      status.JobID(fmt.Sprintf("%032x", n)),
		Kind:    status.Scan,
		Owner:   owner,
		State:   status.Running,
		Rev:     1,
		Started: c.base.Add(time.Duration(n) * time.Minute),
	}
	if key != "" {
		j.Key = &status.JobKey{Key: key, Fingerprint: sha256.Sum256([]byte(j.ID))}
	}
	if ended {
		j.State, j.Outcome, j.Ended = status.Done, status.Succeeded, j.Started
	}
	return j
}

func (c *contract) insert(t *testing.T, j status.Job) status.Job { //nolint:gocritic // hugeParam: InsertJob takes the Job by value
	t.Helper()
	got, replayed, err := c.p.InsertJob(t.Context(), j, status.DefaultLimits)
	if err != nil || replayed {
		t.Fatalf("InsertJob(%s owner %d): replayed %v, %v; want stored", j.ID, j.Owner, replayed, err)
	}
	return got
}

func (c *contract) fill(t *testing.T, owner int64, n int, keyed, ended bool) []status.Job {
	t.Helper()
	out := make([]status.Job, 0, n)
	for i := range n {
		key := ""
		if keyed {
			key = fmt.Sprintf("k-%d-%d", owner, i)
		}
		out = append(out, c.insert(t, c.job(owner, key, ended)))
	}
	return out
}

func (c *contract) present(t *testing.T, id status.JobID) bool {
	t.Helper()
	_, ok, err := c.p.GetJob(t.Context(), id)
	if err != nil {
		t.Fatalf("GetJob(%s): %v", id, err)
	}
	return ok
}

func (c *contract) refused(t *testing.T, j status.Job, want error) { //nolint:gocritic // hugeParam: InsertJob takes the Job by value
	t.Helper()
	if _, _, err := c.p.InsertJob(t.Context(), j, status.DefaultLimits); !errors.Is(err, want) {
		t.Fatalf("InsertJob(owner %d) = %v, want %v", j.Owner, err, want)
	}
	if c.present(t, j.ID) {
		t.Errorf("a refused insert stored %s", j.ID)
	}
}

func (c *contract) unfinishedOf(t *testing.T, owner int64) int {
	t.Helper()
	all, err := c.p.UnfinishedJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := range all {
		if all[i].Owner == owner {
			n++
		}
	}
	return n
}

const owner7 = int64(7)

func endedKeyedRecordsMakeRoom(t *testing.T, c *contract) {
	old := c.fill(t, owner7, status.DefaultLimits.PerOwner, true, true)
	fresh := c.insert(t, c.job(owner7, "fresh", false))
	if c.present(t, old[0].ID) {
		t.Error("the oldest-ended keyed record survived a fresh-key start at the bound")
	}
	if !c.present(t, old[1].ID) || !c.present(t, fresh.ID) {
		t.Error("the start deleted more than the oldest-ended record")
	}
}

func endedKeylessRecordMakesRoom(t *testing.T, c *contract) {
	running := c.fill(t, owner7, status.DefaultLimits.PerOwner-1, true, false)
	ended := c.insert(t, c.job(owner7, "", true))
	c.insert(t, c.job(owner7, "", false))
	if c.present(t, ended.ID) {
		t.Error("the one ended keyless record survived a start at the bound")
	}
	if !c.present(t, running[0].ID) {
		t.Error("an unfinished record was deleted")
	}
}

func unfinishedRefuses(t *testing.T, c *contract) {
	c.fill(t, owner7, status.DefaultLimits.PerOwner/2, true, false)
	c.fill(t, owner7, status.DefaultLimits.PerOwner/2, false, false)
	c.refused(t, c.job(owner7, "", false), status.ErrJobCapacity)
	c.refused(t, c.job(owner7, "next", false), status.ErrJobCapacity)
	if n := c.unfinishedOf(t, owner7); n != status.DefaultLimits.PerOwner {
		t.Errorf("%d unfinished records after refusals, want %d", n, status.DefaultLimits.PerOwner)
	}
	c.insert(t, c.job(owner7+1, "", false))
}

// fillUsers stores Total records across eleven user owners, none at its own
// bound, the first ended when firstEnded is true.
func fillUsers(t *testing.T, c *contract, firstEnded bool) []status.Job {
	t.Helper()
	var all []status.Job
	per := status.DefaultLimits.Total/11 + 1
	for o := int64(1); len(all) < status.DefaultLimits.Total; o++ {
		n := min(per, status.DefaultLimits.Total-len(all))
		ended := firstEnded && len(all) == 0
		if ended {
			all = append(all, c.insert(t, c.job(o, "", true)))
			n--
		}
		all = append(all, c.fill(t, o, n, false, false)...)
	}
	return all
}

func totalEvictsAnyUsersOldestEnded(t *testing.T, c *contract) {
	all := fillUsers(t, c, true)
	c.insert(t, c.job(11, "", false))
	if c.present(t, all[0].ID) {
		t.Error("owner 11's start at the full Total kept owner 1's ended record")
	}
}

func totalUnfinishedRefuses(t *testing.T, c *contract) {
	fillUsers(t, c, false)
	c.refused(t, c.job(11, "", false), status.ErrJobCapacity)
	c.refused(t, c.job(status.BypassOwner, "k", false), status.ErrJobCapacity)
}

func systemOwnerOutsideTotal(t *testing.T, c *contract) {
	fillUsers(t, c, false)
	c.insert(t, c.job(status.SystemOwner, "", false))
}

func systemOwnerEvictsItsOwnOldest(t *testing.T, c *contract) {
	user := c.insert(t, c.job(owner7, "", true))
	sys := c.fill(t, status.SystemOwner, status.DefaultLimits.PerOwner, false, true)
	c.insert(t, c.job(status.SystemOwner, "", false))
	if c.present(t, sys[0].ID) || !c.present(t, sys[1].ID) {
		t.Error("the 1,001st system insert did not delete exactly its own oldest ended record")
	}
	if !c.present(t, user.ID) {
		t.Error("a system insert deleted a user's older ended record")
	}
}

func systemOwnerUnfinishedRefuses(t *testing.T, c *contract) {
	c.fill(t, status.SystemOwner, status.DefaultLimits.PerOwner, false, false)
	c.refused(t, c.job(status.SystemOwner, "", false), status.ErrJobCapacity)
}

func keyedResendAtFullTableReplays(t *testing.T, c *contract) {
	ended := c.insert(t, c.job(owner7, "", true))
	keyed := c.insert(t, c.job(owner7, "k", false))
	c.fill(t, owner7, status.DefaultLimits.PerOwner-2, false, false)
	resend := c.job(owner7, "k", false)
	resend.Key.Fingerprint = keyed.Key.Fingerprint
	got, replayed, err := c.p.InsertJob(t.Context(), resend, status.DefaultLimits)
	if err != nil || !replayed || got.ID != keyed.ID {
		t.Fatalf("resend = %s replayed %v, %v; want %s replayed", got.ID, replayed, err, keyed.ID)
	}
	other := c.job(owner7, "k", false)
	if _, _, err := c.p.InsertJob(t.Context(), other, status.DefaultLimits); !errors.Is(err, status.ErrKeyReused) {
		t.Errorf("another fingerprint = %v, want ErrKeyReused", err)
	}
	if !c.present(t, ended.ID) {
		t.Error("a replay or a reused key evicted the ended record")
	}
	if c.present(t, resend.ID) || c.present(t, other.ID) {
		t.Error("a replay or a reused key stored a record")
	}
}

func evictedKeyStartsAgain(t *testing.T, c *contract) {
	first := c.insert(t, c.job(owner7, "k", true))
	running := c.fill(t, owner7, status.DefaultLimits.PerOwner-1, false, false)
	c.insert(t, c.job(owner7, "", false))
	if c.present(t, first.ID) {
		t.Fatal("the ended keyed record survived a start at the bound")
	}
	done := running[0]
	done.State, done.Outcome, done.Ended, done.Rev = status.Done, status.Succeeded, done.Started, 2
	if err := c.p.PutJob(t.Context(), done); err != nil {
		t.Fatal(err)
	}
	again := c.job(owner7, "k", false)
	again.Key.Fingerprint = first.Key.Fingerprint
	got, replayed, err := c.p.InsertJob(t.Context(), again, status.DefaultLimits)
	if err != nil || replayed || got.ID != again.ID {
		t.Errorf("an evicted key's resend = %s replayed %v, %v; want the new job %s stored", got.ID, replayed, err, again.ID)
	}
}

func concurrentInsertsStoreExactlyTheBound(t *testing.T, c *contract) {
	const room, racers = 10, 32
	c.fill(t, owner7, status.DefaultLimits.PerOwner-room, false, false)
	var wg sync.WaitGroup
	var stored, refused atomic.Int32
	for i := range racers {
		key := ""
		if i%2 == 0 {
			key = fmt.Sprintf("race-%d", i)
		}
		j := c.job(owner7, key, false)
		wg.Go(func() {
			_, _, err := c.p.InsertJob(context.WithoutCancel(t.Context()), j, status.DefaultLimits)
			switch {
			case err == nil:
				stored.Add(1)
			case errors.Is(err, status.ErrJobCapacity):
				refused.Add(1)
			default:
				t.Errorf("InsertJob: %v", err)
			}
		})
	}
	wg.Wait()
	if stored.Load() != room || refused.Load() != racers-room {
		t.Errorf("%d stored, %d refused; want %d and %d", stored.Load(), refused.Load(), room, racers-room)
	}
	if n := c.unfinishedOf(t, owner7); n != status.DefaultLimits.PerOwner {
		t.Errorf("%d records stored, want exactly %d", n, status.DefaultLimits.PerOwner)
	}
}

func concurrentSameKeyOneWins(t *testing.T, c *contract) {
	a, b := c.job(owner7, "k", false), c.job(owner7, "k", false)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, j := range []*status.Job{&a, &b} {
		wg.Go(func() {
			_, _, err := c.p.InsertJob(context.WithoutCancel(t.Context()), *j, status.DefaultLimits)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	var ok, reused int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, status.ErrKeyReused):
			reused++
		default:
			t.Errorf("InsertJob: %v", err)
		}
	}
	if ok != 1 || reused != 1 {
		t.Errorf("%d stored, %d ErrKeyReused; want one each", ok, reused)
	}
}

func insertExistingIDFails(t *testing.T, c *contract) {
	j := c.insert(t, c.job(owner7, "", false))
	j.Key = nil
	if _, _, err := c.p.InsertJob(t.Context(), j, status.DefaultLimits); err == nil {
		t.Error("inserting an existing id succeeded")
	}
}

func putJobRevisions(t *testing.T, c *contract) {
	j := c.insert(t, c.job(owner7, "k", false))
	j.State, j.Outcome, j.Ended, j.Rev = status.Done, status.Failed, j.Started, 3
	if err := c.p.PutJob(t.Context(), j); err != nil {
		t.Fatalf("PutJob rev 3: %v", err)
	}
	stale := j
	stale.State, stale.Outcome = status.Running, status.None
	for _, rev := range []uint64{3, 2} {
		stale.Rev = rev
		if err := c.p.PutJob(t.Context(), stale); err == nil {
			t.Errorf("PutJob rev %d over stored rev 3 succeeded", rev)
		}
	}
	got, ok, err := c.p.GetJob(t.Context(), j.ID)
	if err != nil || !ok || got.Rev != 3 || got.State != status.Done {
		t.Errorf("stored %+v %v %v; want rev 3 Done", got, ok, err)
	}
	if byKey, ok, err := c.p.JobByKey(t.Context(), owner7, "k"); err != nil || !ok || byKey.ID != j.ID {
		t.Errorf("JobByKey after PutJob = %s %v %v", byKey.ID, ok, err)
	}
	if err := c.p.PutJob(t.Context(), c.job(owner7, "", false)); err == nil {
		t.Error("PutJob of an absent id succeeded")
	}
}

func unfinishedJobs(t *testing.T, c *contract) {
	running := c.insert(t, c.job(owner7, "", false))
	c.insert(t, c.job(owner7, "", true))
	got, err := c.p.UnfinishedJobs(t.Context())
	if err != nil || len(got) != 1 || got[0].ID != running.ID {
		t.Errorf("UnfinishedJobs = %v, %v; want only %s", got, err, running.ID)
	}
}

func deleteJobsEndedBefore(t *testing.T, c *contract) {
	old := c.insert(t, c.job(owner7, "old", true))
	recent := c.insert(t, c.job(owner7, "", true))
	running := c.insert(t, c.job(owner7, "", false))
	n, err := c.p.DeleteJobsEndedBefore(t.Context(), recent.Ended)
	if err != nil || n != 1 {
		t.Fatalf("DeleteJobsEndedBefore = %d, %v; want 1", n, err)
	}
	if c.present(t, old.ID) || !c.present(t, recent.ID) || !c.present(t, running.ID) {
		t.Error("the sweep deleted other than the record ended before the cut")
	}
	if _, ok, err := c.p.JobByKey(t.Context(), owner7, "old"); err != nil || ok {
		t.Errorf("a swept record's key still answers: %v %v", ok, err)
	}
}
