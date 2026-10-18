package status_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

// hookPersister is the reference persister with injectable failures; a nil
// hook delegates.
type hookPersister struct {
	*statustest.MemPersister
	insert func(context.Context, status.Job) error
	put    func(context.Context, status.Job) error
	mu     sync.Mutex
}

func newHook() *hookPersister { return &hookPersister{MemPersister: statustest.NewMemPersister()} }

func (h *hookPersister) setPut(f func(context.Context, status.Job) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.put = f
}

func (h *hookPersister) InsertJob(ctx context.Context, j status.Job, l status.JobLimits) (status.Job, bool, error) {
	if h.insert != nil {
		if err := h.insert(ctx, j); err != nil {
			return status.Job{}, false, err
		}
	}
	return h.MemPersister.InsertJob(ctx, j, l)
}

func (h *hookPersister) PutJob(ctx context.Context, j status.Job) error {
	h.mu.Lock()
	put := h.put
	h.mu.Unlock()
	if put != nil {
		if err := put(ctx, j); err != nil {
			return err
		}
	}
	return h.MemPersister.PutJob(ctx, j)
}

func open(t *testing.T, p status.Persister) *status.Store {
	t.Helper()
	s, err := status.New(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func begin(t *testing.T, s *status.Store, owner int64, key *status.JobKey, stop func()) status.Job {
	t.Helper()
	j, replayed, err := s.Begin(t.Context(), status.Job{Kind: status.Scan, Owner: owner, Key: key, Scope: "series/1"}, stop)
	if err != nil || replayed {
		t.Fatalf("Begin: replayed %v, %v", replayed, err)
	}
	return j
}

func get(t *testing.T, s *status.Store, id status.JobID) status.Job {
	t.Helper()
	j, ok, err := s.Get(t.Context(), id)
	if err != nil || !ok {
		t.Fatalf("Get(%s) = %v, %v", id, ok, err)
	}
	return j
}

func finish(s *status.Store, id status.JobID, o status.Outcome) {
	s.Update(id, func(j *status.Job) { j.State, j.Outcome = status.Done, o })
}

func TestCancellableDerived(t *testing.T) {
	s := open(t, statustest.NewMemPersister())
	stopped := 0
	j := begin(t, s, 7, nil, func() { stopped++ })
	if !j.Cancellable {
		t.Fatal("a job begun with a stop handle is not cancellable")
	}
	for i, fn := range []func(*status.Job){
		func(j *status.Job) { j.State, j.Done, j.Total = status.Running, 1, 3 },
		func(j *status.Job) { j.Done, j.Phase = 2, "matching" },
		func(j *status.Job) { j.Done = 3 },
		func(j *status.Job) { j.Cancellable = false },
	} {
		s.Update(j.ID, fn)
		if !get(t, s, j.ID).Cancellable {
			t.Errorf("after update %d the job lost Cancellable while its stop is set", i)
		}
	}
	if err := s.Cancel(j.ID); err != nil || stopped != 1 {
		t.Fatalf("Cancel = %v, stop called %d times; want nil and once", err, stopped)
	}
	finish(s, j.ID, status.Cancelled)
	done := get(t, s, j.ID)
	if done.Cancellable || done.State != status.Done || done.Outcome != status.Cancelled || done.Ended.IsZero() {
		t.Errorf("ended job %+v; want Done, Cancelled, Ended set, not cancellable", done)
	}
	if err := s.Cancel(j.ID); !errors.Is(err, status.ErrNotCancellable) {
		t.Errorf("Cancel of a Done job = %v, want ErrNotCancellable", err)
	}
	if stopped != 1 {
		t.Errorf("stop called %d times, want once", stopped)
	}

	t.Run("interrupted_by_new", func(t *testing.T) {
		mem := statustest.NewMemPersister()
		rec := status.Job{ID: "0123456789abcdef0123456789abcdef", Kind: status.Sync, Owner: 7, State: status.Running, Cancellable: true, Rev: 4}
		if _, _, err := mem.InsertJob(t.Context(), rec, status.DefaultLimits); err != nil {
			t.Fatal(err)
		}
		s := open(t, mem)
		got := get(t, s, rec.ID)
		if got.State != status.Done || got.Outcome != status.Interrupted || got.Cancellable {
			t.Errorf("rewritten record %+v; want Done, Interrupted, not cancellable", got)
		}
		if stored, _, _ := mem.GetJob(t.Context(), rec.ID); stored.Outcome != status.Interrupted || stored.Cancellable || stored.Rev <= rec.Rev {
			t.Errorf("stored record %+v; want Interrupted, not cancellable, rev above %d", stored, rec.Rev)
		}
		if err := s.Cancel(rec.ID); !errors.Is(err, status.ErrNotCancellable) {
			t.Errorf("Cancel of an interrupted record = %v, want ErrNotCancellable", err)
		}
	})
	t.Run("no_stop_handle", func(t *testing.T) {
		j := begin(t, s, 7, nil, nil)
		if j.Cancellable {
			t.Error("a job with no stop handle is cancellable")
		}
		if err := s.Cancel(j.ID); !errors.Is(err, status.ErrNotCancellable) {
			t.Errorf("Cancel = %v, want ErrNotCancellable", err)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		if err := s.Cancel("ffffffffffffffffffffffffffffffff"); !errors.Is(err, status.ErrUnknownJob) {
			t.Errorf("Cancel of an unknown id = %v, want ErrUnknownJob", err)
		}
	})
}

func TestUpdateRestoresFixedFields(t *testing.T) {
	s := open(t, statustest.NewMemPersister())
	key := &status.JobKey{Key: "k", Fingerprint: [32]byte{1}}
	j := begin(t, s, 7, key, nil)
	s.Update(j.ID, func(c *status.Job) {
		c.ID, c.Kind, c.Scope, c.Owner, c.Started, c.Rev = "x", status.Backup, "other", 8, time.Time{}, 99
		c.Key.Key = "stolen"
		c.Outcome, c.Ended = status.Succeeded, time.Now()
	})
	got := get(t, s, j.ID)
	if got.ID != j.ID || got.Kind != j.Kind || got.Scope != j.Scope || got.Owner != j.Owner || !got.Started.Equal(j.Started) {
		t.Errorf("fixed fields changed: %+v, began as %+v", got, j)
	}
	if got.Key == nil || got.Key.Key != "k" {
		t.Errorf("Key %+v; want k kept", got.Key)
	}
	if got.Rev != j.Rev+1 {
		t.Errorf("Rev %d, want %d", got.Rev, j.Rev+1)
	}
	if got.Outcome != status.None || !got.Ended.IsZero() {
		t.Errorf("an unfinished job holds Outcome %v, Ended %v", got.Outcome, got.Ended)
	}
	if _, ok, err := s.Keyed(t.Context(), 7, *key); err != nil || !ok {
		t.Errorf("Keyed after an update mutating Key: %v %v", ok, err)
	}
}

func TestDoneNeedsOutcome(t *testing.T) {
	rec := capture.Default(t)
	s := open(t, statustest.NewMemPersister())
	j := begin(t, s, 7, nil, nil)
	finish(s, j.ID, status.None)
	if got := get(t, s, j.ID); got.Outcome != status.Failed {
		t.Errorf("Done with no outcome reads %v, want Failed", got.Outcome)
	}
	if rec.CountLevel(slog.LevelError, "no valid outcome") != 1 {
		t.Errorf("logged %q, want one ERROR", rec.Messages())
	}
	s.Update(j.ID, func(c *status.Job) { c.State, c.Outcome = status.Running, status.None })
	if got := get(t, s, j.ID); got.State != status.Done || got.Outcome != status.Failed {
		t.Errorf("an update after Done changed the job to %v %v", got.State, got.Outcome)
	}
	k := begin(t, s, 7, nil, nil)
	s.Update(k.ID, func(c *status.Job) { c.State = 9 })
	if got := get(t, s, k.ID); got.State != status.Queued {
		t.Errorf("an invalid state was stored: %v", got.State)
	}
	if rec.CountLevel(slog.LevelError, "invalid state") != 1 {
		t.Errorf("logged %q, want an ERROR for the invalid state", rec.Messages())
	}
}

func TestLiveRetentionKeepsUndurableJob(t *testing.T) {
	for _, mode := range []string{"blocked", "failed"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHook()
				s := open(t, h)
				key := status.JobKey{Key: "first", Fingerprint: [32]byte{1}}
				first := begin(t, s, 7, &key, nil)
				release := make(chan struct{})
				h.setPut(func(ctx context.Context, j status.Job) error {
					if j.ID != first.ID {
						return nil
					}
					if mode == "failed" {
						return errors.New("disk full")
					}
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				go finish(s, first.ID, status.Succeeded)
				synctest.Wait()
				for range 51 {
					time.Sleep(time.Second)
					j := begin(t, s, 8, nil, nil)
					finish(s, j.ID, status.Succeeded)
				}
				if got := get(t, s, first.ID); got.State != status.Done {
					t.Errorf("Get answers %v, want Done", got.State)
				}
				if got, ok, err := s.Keyed(t.Context(), 7, key); err != nil || !ok || got.State != status.Done {
					t.Errorf("Keyed answers %v %v %v, want Done", got.State, ok, err)
				}
				snap, _ := s.Snapshot()
				if n := len(snap.Jobs); n != 51 {
					t.Errorf("%d live jobs, want 50 durable plus the undurable one", n)
				}
				close(release)
				synctest.Wait()
			})
		})
	}
}

func TestLiveRetentionBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := open(t, statustest.NewMemPersister())
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)
		go func() { done <- s.Run(ctx) }()
		var ids []status.JobID
		for range 51 {
			time.Sleep(time.Second)
			j := begin(t, s, 7, nil, nil)
			finish(s, j.ID, status.Succeeded)
			ids = append(ids, j.ID)
		}
		running := begin(t, s, 7, nil, nil)
		snap, _ := s.Snapshot()
		if len(snap.Jobs) != 51 {
			t.Errorf("%d live jobs, want 50 ended and one running", len(snap.Jobs))
		}
		if _, ok, _ := s.Get(t.Context(), ids[0]); !ok {
			t.Error("the evicted job is not in the stored records")
		}
		for _, j := range snap.Jobs {
			if j.ID == ids[0] {
				t.Error("the oldest-ended of 51 stayed live")
			}
		}
		time.Sleep(liveAge + time.Minute)
		snap, _ = s.Snapshot()
		if len(snap.Jobs) != 1 || snap.Jobs[0].ID != running.ID {
			t.Errorf("after 15 minutes the live jobs are %d, want only the running one", len(snap.Jobs))
		}
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run = %v", err)
		}
	})
}

const liveAge = 15 * time.Minute

func TestKeyedRefusesReusedKey(t *testing.T) {
	mem := statustest.NewMemPersister()
	s := open(t, mem)
	k := status.JobKey{Key: "K", Fingerprint: [32]byte{1}}
	other := status.JobKey{Key: "K", Fingerprint: [32]byte{2}}
	j := begin(t, s, 7, &k, nil)
	if _, _, err := s.Keyed(t.Context(), 7, other); !errors.Is(err, status.ErrKeyReused) {
		t.Errorf("Keyed of a live job with another fingerprint = %v, want ErrKeyReused", err)
	}
	if got, ok, err := s.Keyed(t.Context(), 7, k); err != nil || !ok || got.ID != j.ID {
		t.Errorf("Keyed with the fingerprint = %s %v %v, want %s", got.ID, ok, err, j.ID)
	}
	if _, ok, err := s.Keyed(t.Context(), 8, k); err != nil || ok {
		t.Errorf("another owner's Keyed = %v %v, want not found", ok, err)
	}
	if got, replayed, err := s.Begin(t.Context(), status.Job{Kind: status.Scan, Owner: 7, Key: &k}, nil); err != nil || !replayed || got.ID != j.ID {
		t.Errorf("a keyed resend = %s replayed %v, %v; want %s replayed", got.ID, replayed, err, j.ID)
	}
	if _, _, err := s.Begin(t.Context(), status.Job{Kind: status.Scan, Owner: 7, Key: &other}, nil); !errors.Is(err, status.ErrKeyReused) {
		t.Errorf("Begin with another fingerprint = %v, want ErrKeyReused", err)
	}
	finish(s, j.ID, status.Succeeded)

	t.Run("stored", func(t *testing.T) {
		s := open(t, mem)
		if _, _, err := s.Keyed(t.Context(), 7, other); !errors.Is(err, status.ErrKeyReused) {
			t.Errorf("Keyed of a stored job with another fingerprint = %v, want ErrKeyReused", err)
		}
		if got, ok, err := s.Keyed(t.Context(), 7, k); err != nil || !ok || got.State != status.Done {
			t.Errorf("Keyed of the stored job = %v %v %v, want Done", got.State, ok, err)
		}
	})
	t.Run("concurrent", func(t *testing.T) {
		s := open(t, statustest.NewMemPersister())
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, key := range []status.JobKey{k, other} {
			wg.Go(func() {
				_, _, err := s.Begin(context.WithoutCancel(t.Context()), status.Job{Kind: status.Scan, Owner: 7, Key: &key}, nil)
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
			}
		}
		if snap, _ := s.Snapshot(); ok != 1 || reused != 1 || len(snap.Jobs) != 1 {
			t.Errorf("%d started, %d ErrKeyReused, %d live; want one each", ok, reused, len(snap.Jobs))
		}
	})
}

func TestKeyedDoesNotReplayCapacityEvictedLiveJob(t *testing.T) {
	mem := statustest.NewMemPersister()
	s := open(t, mem)
	k := status.JobKey{Key: "K", Fingerprint: [32]byte{1}}
	keyed := begin(t, s, 7, &k, nil)
	finish(s, keyed.ID, status.Succeeded)
	for range status.DefaultLimits.PerOwner {
		begin(t, s, 7, nil, nil)
	}
	if _, ok, err := mem.JobByKey(t.Context(), 7, k.Key); err != nil || ok {
		t.Fatalf("the persister still holds the keyed record: %v %v", ok, err)
	}
	if _, ok, err := s.Get(t.Context(), keyed.ID); err != nil || !ok {
		t.Fatalf("the evicted job left the live view: %v %v", ok, err)
	}
	for name, fp := range map[string][32]byte{"same": k.Fingerprint, "different": {2}} {
		t.Run(name, func(t *testing.T) {
			if j, ok, err := s.Keyed(t.Context(), 7, status.JobKey{Key: k.Key, Fingerprint: fp}); err != nil || ok {
				t.Errorf("Keyed of the evicted key = %q found=%v err=%v; want not found", j.ID, ok, err)
			}
		})
	}
}

func TestBeginInsertFailureNeverRuns(t *testing.T) {
	for _, tc := range []struct {
		insert    error
		want      error
		name      string
		owner     int64
		errorLogs int
	}{
		{name: "storage_fault", insert: errors.New("bolt: closed"), want: status.ErrJobUnrecorded, owner: 7, errorLogs: 1},
		{name: "user_capacity", insert: fmt.Errorf("full: %w", status.ErrJobCapacity), want: status.ErrJobCapacity, owner: 7},
		{name: "system_capacity", insert: status.ErrJobCapacity, want: status.ErrJobCapacity, owner: status.SystemOwner, errorLogs: 1},
		{name: "key_reused", insert: status.ErrKeyReused, want: status.ErrKeyReused, owner: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := capture.Default(t)
			h := newHook()
			h.insert = func(context.Context, status.Job) error { return tc.insert }
			s := open(t, h)
			_, seq := s.Snapshot()
			stopped := false
			_, _, err := s.Begin(t.Context(), status.Job{Kind: status.Scan, Owner: tc.owner}, func() { stopped = true })
			if !errors.Is(err, tc.want) {
				t.Errorf("Begin = %v, want %v", err, tc.want)
			}
			if snap, now := s.Snapshot(); len(snap.Jobs) != 0 || now != seq || stopped {
				t.Errorf("a refused job is live (%d jobs, seq %d to %d, stop called %v)", len(snap.Jobs), seq, now, stopped)
			}
			if n := rec.CountLevel(slog.LevelError, "status:"); n != tc.errorLogs {
				t.Errorf("logged %q, want %d ERROR", rec.Messages(), tc.errorLogs)
			}
		})
	}
	t.Run("invalid_kind", func(t *testing.T) {
		mem := statustest.NewMemPersister()
		s := open(t, mem)
		if _, _, err := s.Begin(t.Context(), status.Job{Owner: 7}, nil); !errors.Is(err, status.ErrJobUnrecorded) {
			t.Errorf("Begin of the zero Kind = %v, want ErrJobUnrecorded", err)
		}
		if recs, _ := mem.UnfinishedJobs(t.Context()); len(recs) != 0 {
			t.Errorf("a refused job was stored: %v", recs)
		}
	})
}

func TestBeginAssignsIdentity(t *testing.T) {
	mem := statustest.NewMemPersister()
	s := open(t, mem)
	changed := s.Changed()
	j, _, err := s.Begin(t.Context(), status.Job{Kind: status.Scan, Owner: 7, ID: "mine", Rev: 9, State: status.Done, Outcome: status.Failed, Done: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.ID.MarshalText(); err != nil || j.Rev != 1 || j.State != status.Queued || j.Outcome != status.None || j.Done != 0 || j.Started.IsZero() {
		t.Errorf("begun job %+v; want a fresh id, Rev 1, Queued, no outcome, Started set", j)
	}
	if stored, ok, _ := mem.GetJob(t.Context(), j.ID); !ok || stored.State != status.Queued {
		t.Errorf("stored before runnable: %v %v", ok, stored.State)
	}
	select {
	case <-changed:
	default:
		t.Error("Begin did not signal Changed")
	}
	k, _, _ := s.Begin(t.Context(), status.Job{Kind: status.Scan, Owner: 7}, nil)
	if k.ID == j.ID {
		t.Error("two jobs share an id")
	}
}

func TestOwnerOfBypass(t *testing.T) {
	if got := status.OwnerOf(0, true); got != status.BypassOwner {
		t.Errorf("OwnerOf(0, true) = %d, want BypassOwner", got)
	}
	if got := status.OwnerOf(5, false); got != 5 {
		t.Errorf("OwnerOf(5, false) = %d, want 5", got)
	}
	if status.BypassOwner == status.SystemOwner {
		t.Error("the bypass principal's owner reads as the system's")
	}
}

func TestKeyedSystemJobUnrecorded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		owner int64
	}{
		{"system_owner", "k", status.SystemOwner},
		{"empty_key", "", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := capture.Default(t)
			mem := statustest.NewMemPersister()
			s := open(t, mem)
			_, _, err := s.Begin(t.Context(), status.Job{Kind: status.Backup, Owner: tc.owner, Key: &status.JobKey{Key: tc.key}}, nil)
			if !errors.Is(err, status.ErrJobUnrecorded) {
				t.Errorf("Begin = %v, want ErrJobUnrecorded", err)
			}
			if recs, _ := mem.UnfinishedJobs(t.Context()); len(recs) != 0 {
				t.Errorf("stored %d records, want none", len(recs))
			}
			if rec.CountLevel(slog.LevelError, "status:") != 1 {
				t.Errorf("logged %q, want one ERROR", rec.Messages())
			}
		})
	}
}

func TestJobEnumsText(t *testing.T) {
	type codec interface {
		MarshalText() ([]byte, error)
	}
	cases := []struct {
		parse func([]byte) error
		v     codec
		name  string
	}{
		{name: "scan", v: status.Scan},
		{name: "import", v: status.Import},
		{name: "item_scan", v: status.ItemScan},
		{name: "download", v: status.Download},
		{name: "sync", v: status.Sync},
		{name: "backup", v: status.Backup},
		{name: "queued", v: status.Queued},
		{name: "running", v: status.Running},
		{name: "done", v: status.Done},
		{name: "none", v: status.None},
		{name: "succeeded", v: status.Succeeded},
		{name: "failed", v: status.Failed},
		{name: "cancelled", v: status.Cancelled},
		{name: "partial", v: status.Partial},
		{name: "interrupted", v: status.Interrupted},
		{name: "ok", v: status.OK},
		{name: "degraded", v: status.Degraded},
		{name: "disabled", v: status.Disabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := tc.v.MarshalText()
			if err != nil || string(b) != tc.name {
				t.Fatalf("MarshalText = %q, %v; want %q", b, err, tc.name)
			}
			var back codec
			switch tc.v.(type) {
			case status.Kind:
				var p status.Kind
				err, back = p.UnmarshalText(b), p
			case status.State:
				var p status.State
				err, back = p.UnmarshalText(b), p
			case status.Outcome:
				var p status.Outcome
				err, back = p.UnmarshalText(b), p
			case status.Health:
				var p status.Health
				err, back = p.UnmarshalText(b), p
			}
			if err != nil || back != tc.v {
				t.Errorf("UnmarshalText(%q) = %v, %v; want %v", b, back, err, tc.v)
			}
		})
	}
	var k status.Kind
	var st status.State
	var o status.Outcome
	var h status.Health
	for _, bad := range []string{"", "Scan", "SCAN", "scan ", "item-scan", "0", "1"} {
		if k.UnmarshalText([]byte(bad)) == nil || st.UnmarshalText([]byte(bad)) == nil || o.UnmarshalText([]byte(bad)) == nil || h.UnmarshalText([]byte(bad)) == nil {
			t.Errorf("%q was accepted by one of the codecs", bad)
		}
	}
	for _, v := range []codec{status.Kind(0), status.State(0), status.Health(0), status.Kind(7), status.State(4), status.Outcome(6), status.Health(4)} {
		if b, err := v.MarshalText(); err == nil {
			t.Errorf("%#v marshalled as %q, want an error", v, b)
		}
	}
}

func TestJobIDText(t *testing.T) {
	s := open(t, statustest.NewMemPersister())
	j := begin(t, s, 7, nil, nil)
	b, err := j.ID.MarshalText()
	if err != nil || len(b) != 32 {
		t.Fatalf("MarshalText = %q, %v", b, err)
	}
	var back status.JobID
	if err := back.UnmarshalText(b); err != nil || back != j.ID {
		t.Errorf("round trip = %q, %v; want %q", back, err, j.ID)
	}
	for _, bad := range []string{
		"", string(b[:31]), string(b) + "0", "0123456789ABCDEF0123456789ABCDEF", "0123456789abcdef0123456789abcdeg",
	} {
		var id status.JobID
		if err := id.UnmarshalText([]byte(bad)); err == nil {
			t.Errorf("UnmarshalText(%q) accepted", bad)
		}
		if _, err := status.JobID(bad).MarshalText(); err == nil {
			t.Errorf("MarshalText(%q) accepted", bad)
		}
	}
}
