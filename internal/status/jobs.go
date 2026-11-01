package status

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
)

// JobID is a job's opaque id: the 32 lowercase hex digits Begin assigns.
type JobID string

// Kind is what a job does.
type Kind uint8

// The job kinds; the zero Kind is none of them.
const (
	Scan Kind = iota + 1
	Import
	ItemScan
	Download
	Sync
	Backup
)

// State is where a job is in its life.
type State uint8

// The job states; the zero State is none of them.
const (
	Queued State = iota + 1
	Running
	Done
)

// Outcome is how a Done job ended; it is None until then.
type Outcome uint8

// The outcomes.
const (
	None Outcome = iota
	Succeeded
	Failed
	Cancelled
	Partial
	Interrupted
)

// The sentinels the job calls return.
var (
	ErrJobUnrecorded  = errors.New("status: job not recorded")
	ErrKeyReused      = errors.New("status: idempotency key reused with another request")
	ErrJobCapacity    = errors.New("status: job record capacity reached")
	ErrNotCancellable = errors.New("status: job is not cancellable")
	ErrUnknownJob     = errors.New("status: unknown job")
)

// JobKey is an Idempotency-Key as its owner sent it; the owner is Job.Owner.
type JobKey struct {
	Key         string
	Fingerprint [32]byte
}

// Job is one unit of work as observers and the persister see it.
type Job struct {
	Started time.Time
	Ended   time.Time
	Key     *JobKey
	ID      JobID
	Scope   string
	Phase   string
	Cause   string
	// Owner is the starting principal's user ID, BypassOwner for the bypass
	// principal, SystemOwner only for scheduled work (OwnerOf).
	Owner       int64
	Done, Total int
	Rev         uint64
	Kind        Kind
	State       State
	Outcome     Outcome
	// Cancellable is derived by Update: a stop handle and State != Done.
	Cancellable bool
}

type liveJob struct {
	stop func()
	job  Job
	// durable is false from a job's Done transition until its terminal
	// record is stored; retention never evicts such a job.
	durable  bool
	saving   bool
	attempts int
}

// Begin records job and makes it live: it assigns ID, Rev 1 and Queued and
// inserts the record before returning, so the caller runs the work only
// after a nil error with replayed false. A keyed resend of the same request
// returns the existing job, its live state while live, with replayed true. A
// SystemOwner job with a Key is ErrJobUnrecorded, writing nothing.
// ErrKeyReused and ErrJobCapacity pass through; any other failure is
// ErrJobUnrecorded.
func (s *Store) Begin(ctx context.Context, job Job, stop func()) (_ Job, replayed bool, _ error) { //nolint:gocritic // hugeParam: job is the caller's template, filled in and returned
	if !validKind(job.Kind) {
		slog.Error("status: refused a job of an invalid kind", "kind", int(job.Kind))
		return Job{}, false, ErrJobUnrecorded
	}
	if job.Key != nil {
		if job.Owner == SystemOwner || job.Key.Key == "" {
			slog.Error("status: refused a keyed job with no owner or an empty key", "owner", job.Owner)
			return Job{}, false, ErrJobUnrecorded
		}
		job.Key = &JobKey{Key: job.Key.Key, Fingerprint: job.Key.Fingerprint}
	}
	job.ID = newJobID()
	job.Rev = 1
	job.State = Queued
	job.Outcome = None
	job.Done = 0
	job.Phase, job.Cause = "", ""
	job.Started = time.Now()
	job.Ended = time.Time{}
	job.Cancellable = stop != nil

	stored, replay, err := s.p.InsertJob(ctx, job, DefaultLimits)
	switch {
	case errors.Is(err, ErrKeyReused):
		return Job{}, false, ErrKeyReused
	case errors.Is(err, ErrJobCapacity):
		if job.Owner == SystemOwner {
			slog.Error("status: the system's job records are full of unfinished jobs", "kind", job.Kind.String())
		}
		return Job{}, false, ErrJobCapacity
	case err != nil:
		slog.Error("status: could not record a job", "kind", job.Kind.String(), "error", err)
		return Job{}, false, ErrJobUnrecorded
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if replay {
		if l, ok := s.live[stored.ID]; ok {
			return cloneJob(l.job), true, nil
		}
		return stored, true, nil
	}
	s.live[job.ID] = &liveJob{job: job, stop: stop, durable: true}
	s.bump()
	return cloneJob(job), false, nil
}

// Keyed is the job owner started under k.Key while the persister holds its
// record, read as the live job when it is live. A job under that key with
// another fingerprint is ErrKeyReused; a failed read is ErrJobUnrecorded.
func (s *Store) Keyed(ctx context.Context, owner int64, k JobKey) (Job, bool, error) {
	j, ok, err := s.p.JobByKey(ctx, owner, k.Key)
	if err != nil {
		slog.Error("status: could not read a keyed job", "error", err)
		return Job{}, false, ErrJobUnrecorded
	}
	if !ok {
		return Job{}, false, nil
	}
	if j.Key == nil || j.Key.Fingerprint != k.Fingerprint {
		return Job{}, false, ErrKeyReused
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.live[j.ID]; ok {
		return cloneJob(l.job), true, nil
	}
	return j, true, nil
}

// Get answers the live job, then the stored record.
func (s *Store) Get(ctx context.Context, id JobID) (Job, bool, error) {
	s.mu.Lock()
	if l, ok := s.live[id]; ok {
		j := cloneJob(l.job)
		s.mu.Unlock()
		return j, true, nil
	}
	s.mu.Unlock()
	j, ok, err := s.p.GetJob(ctx, id)
	if err != nil {
		return Job{}, false, fmt.Errorf("status: read job: %w", err)
	}
	return j, ok, nil
}

// Update applies fn to a copy of the live job and keeps what callers may not
// change: ID, Kind, Scope, Owner, Key, Started, Rev and Cancellable, which is
// derived as a stop handle and State != Done. A transition to Done needs an
// Outcome other than None (else Failed, with ERROR), sets Ended, drops the
// stop handle and is final; it stores the terminal record before returning,
// retried by Run on failure. An unknown or Done job is left alone.
func (s *Store) Update(id JobID, fn func(*Job)) {
	s.mu.Lock()
	l, ok := s.live[id]
	if !ok || l.job.State == Done {
		s.mu.Unlock()
		return
	}
	prev := l.job
	next := cloneJob(prev)
	fn(&next)
	next.ID, next.Kind, next.Scope, next.Owner, next.Key, next.Started = prev.ID, prev.Kind, prev.Scope, prev.Owner, prev.Key, prev.Started
	next.Rev = prev.Rev + 1
	if !validState(next.State) {
		slog.Error("status: an update set an invalid state", "job", string(id), "state", int(next.State))
		next.State = prev.State
	}
	terminal := next.State == Done
	if terminal {
		if next.Outcome == None || next.Outcome > Interrupted {
			slog.Error("status: a job ended with no valid outcome", "job", string(id), "outcome", int(next.Outcome))
			next.Outcome = Failed
		}
		next.Ended = time.Now()
		l.stop = nil
		l.durable = false
		l.attempts = 0
		s.unsaved[id] = true
	} else {
		next.Outcome = None
		next.Ended = time.Time{}
	}
	next.Cancellable = l.stop != nil && !terminal
	l.job = next
	s.bump()
	s.mu.Unlock()
	if terminal {
		s.persist(id)
	}
}

// Cancel calls a live job's stop handle; the job's own work then ends it.
// A Done job, or one started without a handle, is ErrNotCancellable; a job
// that is not live is ErrUnknownJob.
func (s *Store) Cancel(id JobID) error {
	s.mu.Lock()
	l, ok := s.live[id]
	if !ok {
		s.mu.Unlock()
		return ErrUnknownJob
	}
	stop := l.stop
	cancellable := l.job.Cancellable
	s.mu.Unlock()
	if !cancellable || stop == nil {
		return ErrNotCancellable
	}
	stop()
	return nil
}

// liveJobs lists the live jobs, newest started first, ties by ID; s.mu is
// held.
func (s *Store) liveJobs() []Job {
	jobs := make([]Job, 0, len(s.live))
	for _, l := range s.live {
		jobs = append(jobs, cloneJob(l.job))
	}
	slices.SortFunc(jobs, func(a, b Job) int {
		if c := b.Started.Compare(a.Started); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return jobs
}

func cloneJob(j Job) Job { //nolint:gocritic // hugeParam: the copy is the result
	if j.Key != nil {
		k := *j.Key
		j.Key = &k
	}
	return j
}

func newJobID() JobID {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return JobID(hex.EncodeToString(b[:]))
}

func validKind(k Kind) bool    { return k >= Scan && k <= Backup }
func validState(st State) bool { return st >= Queued && st <= Done }

var (
	kindNames    = [...]string{Scan: "scan", Import: "import", ItemScan: "item_scan", Download: "download", Sync: "sync", Backup: "backup"}
	stateNames   = [...]string{Queued: "queued", Running: "running", Done: "done"}
	outcomeNames = [...]string{None: "none", Succeeded: "succeeded", Failed: "failed", Cancelled: "cancelled", Partial: "partial", Interrupted: "interrupted"}
	healthNames  = [...]string{OK: "ok", Degraded: "degraded", Disabled: "disabled"}
)

// String is k's text form, or "kind(<n>)" for a value no constant names.
func (k Kind) String() string { return nameOf(kindNames[:], k, "kind") }

// String is st's text form, or "state(<n>)" for a value no constant names.
func (st State) String() string { return nameOf(stateNames[:], st, "state") }

// String is o's text form, or "outcome(<n>)" for a value no constant names.
func (o Outcome) String() string { return nameOf(outcomeNames[:], o, "outcome") }

// MarshalText writes the kind's snake_case name; a value no constant names
// is an error.
func (k Kind) MarshalText() ([]byte, error) { return marshalName(kindNames[:], k, "kind") }

// UnmarshalText reads a kind's snake_case name, refusing every other text.
func (k *Kind) UnmarshalText(b []byte) error { return unmarshalName(kindNames[:], b, k, "kind") }

// MarshalText writes the state's snake_case name; a value no constant names
// is an error.
func (st State) MarshalText() ([]byte, error) { return marshalName(stateNames[:], st, "state") }

// UnmarshalText reads a state's snake_case name, refusing every other text.
func (st *State) UnmarshalText(b []byte) error { return unmarshalName(stateNames[:], b, st, "state") }

// MarshalText writes the outcome's snake_case name, None as "none"; a value
// no constant names is an error.
func (o Outcome) MarshalText() ([]byte, error) { return marshalName(outcomeNames[:], o, "outcome") }

// UnmarshalText reads an outcome's snake_case name, refusing every other
// text.
func (o *Outcome) UnmarshalText(b []byte) error {
	return unmarshalName(outcomeNames[:], b, o, "outcome")
}

// MarshalText writes the health's snake_case name; a value no constant names
// is an error.
func (h Health) MarshalText() ([]byte, error) { return marshalName(healthNames[:], h, "health") }

// UnmarshalText reads a health's snake_case name, refusing every other text.
func (h *Health) UnmarshalText(b []byte) error { return unmarshalName(healthNames[:], b, h, "health") }

// MarshalText writes the id as is; anything but 32 lowercase hex digits is an
// error, so no id Begin did not assign reaches the wire.
func (id JobID) MarshalText() ([]byte, error) {
	if !validJobID(string(id)) {
		return nil, errors.New("status: invalid job id")
	}
	return []byte(id), nil
}

// UnmarshalText reads 32 lowercase hex digits, refusing every other text.
func (id *JobID) UnmarshalText(b []byte) error {
	if !validJobID(string(b)) {
		return errors.New("status: invalid job id")
	}
	*id = JobID(b)
	return nil
}

func validJobID(s string) bool {
	return len(s) == 32 && !strings.ContainsFunc(s, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	})
}

func nameOf[T ~uint8](names []string, v T, what string) string {
	if int(v) < len(names) && names[v] != "" {
		return names[v]
	}
	return what + "(" + strconv.Itoa(int(v)) + ")"
}

func marshalName[T ~uint8](names []string, v T, what string) ([]byte, error) {
	if int(v) < len(names) && names[v] != "" {
		return []byte(names[v]), nil
	}
	return nil, fmt.Errorf("status: no %s named %d", what, v)
}

func unmarshalName[T ~uint8](names []string, b []byte, v *T, what string) error {
	if i := slices.Index(names, string(b)); i >= 0 && names[i] != "" {
		*v = T(i)
		return nil
	}
	return fmt.Errorf("status: unknown %s", what)
}
