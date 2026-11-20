package syncsched_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/go/httpwire/httpwiretest"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

func TestOwnerSentinels(t *testing.T) {
	httpwiretest.Sentinels(t, syncsched.Owner)
}

// refusing is a persister whose inserts fail with err.
type refusing struct {
	*statustest.MemPersister
	err error
}

func (p *refusing) InsertJob(context.Context, status.Job, status.JobLimits) (status.Job, bool, error) {
	return status.Job{}, false, p.err
}

// TestCodesCoverFailureTable drives each failure arm of the api-called
// exports and wants the code its sentinel maps to; a caller bug (no method,
// an empty batch) is api's to refuse first.
func TestCodesCoverFailureTable(t *testing.T) {
	type arm struct {
		run  func(t *testing.T) error
		code string
	}
	propose := func(r *rig, f syncsched.Target, key *status.JobKey) error {
		_, err := r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: owner, File: f.File, Expect: f.Expect, Method: timing.MethodReference, Key: key})
		return err
	}
	resync := func(r *rig, key *status.JobKey, files ...syncsched.Target) error {
		_, err := r.a.Resync(t.Context(), syncsched.ResyncRequest{Owner: owner, Files: files, Method: timing.MethodReference, Key: key})
		return err
	}
	withPersister := func(err error) rigOption {
		return func(s *rigSetup) { s.persister = &refusing{MemPersister: statustest.NewMemPersister(), err: err} }
	}
	payload := srt(episode(60, 30))
	arms := map[string]arm{
		"Propose: a changed file": {func(t *testing.T) error {
			r := newRig(t)
			f := r.subtitle(t, "Show S01E01.en.srt", payload)
			r.subtitle(t, "Show S01E01.en.srt", srt(episode(61, 30)))
			return propose(r, f, nil)
		}, "stale_version"},
		"Propose: an unknown instance": {func(t *testing.T) error {
			r := newRig(t)
			f := r.subtitle(t, "Show S01E01.en.srt", payload)
			ref, err := mediawrite.DecodeRef(r.inst+1000, f.File.Name())
			if err != nil {
				t.Fatal(err)
			}
			return propose(r, syncsched.Target{File: ref, Expect: f.Expect}, nil)
		}, "not_found"},
		"Propose: an unreadable subtitle": {func(t *testing.T) error {
			r := newRig(t)
			return propose(r, r.subtitle(t, "Show S01E01.en.srt", []byte("no cues")), nil)
		}, "format_unsupported"},
		"Propose: a root unavailable": {func(t *testing.T) error {
			r := newRig(t, func(s *rigSetup) { s.empty = true })
			ref, err := mediawrite.DecodeRef(r.inst, "Show S01E01.en.srt")
			if err != nil {
				t.Fatal(err)
			}
			return propose(r, syncsched.Target{File: ref, Expect: mediawrite.Expectation{SHA256: [32]byte{1}}}, nil)
		}, "media_root_unavailable"},
		"Propose: outside every root": {func(t *testing.T) error {
			r := newRig(t, func(s *rigSetup) {
				s.roots = func(string) []string { return []string{filepath.Join(t.TempDir(), "elsewhere")} }
			})
			return propose(r, r.subtitle(t, "Show S01E01.en.srt", payload), nil)
		}, "outside_root"},
		"Propose: a reused key": {func(t *testing.T) error {
			r := newRig(t)
			f := r.subtitle(t, "Show S01E01.en.srt", payload)
			if err := propose(r, f, &status.JobKey{Key: "k", Fingerprint: [32]byte{1}}); err != nil {
				t.Fatal(err)
			}
			return propose(r, f, &status.JobKey{Key: "k", Fingerprint: [32]byte{2}})
		}, "idempotency_key_reused"},
		"Propose: job records full": {func(t *testing.T) error {
			r := newRig(t, withPersister(status.ErrJobCapacity))
			return propose(r, r.subtitle(t, "Show S01E01.en.srt", payload), nil)
		}, "job_capacity"},
		"Propose: a job not recorded": {func(t *testing.T) error {
			r := newRig(t, withPersister(errors.New("disk full")))
			return propose(r, r.subtitle(t, "Show S01E01.en.srt", payload), nil)
		}, "job_unrecorded"},
		"Propose: the sync bound": {func(t *testing.T) error {
			r := newRig(t)
			f := hung(t, r)
			for range syncsched.MaxQueuedPerOwner {
				if err := resync(r, nil, f); err != nil {
					t.Fatal(err)
				}
			}
			return propose(r, f, nil)
		}, "sync_capacity"},
		"Resync: too many files": {func(t *testing.T) error {
			r := newRig(t)
			f := r.subtitle(t, "Show S01E01.en.srt", payload)
			return resync(r, nil, slices.Repeat([]syncsched.Target{f}, syncsched.MaxBatch+1)...)
		}, "bad_body"},
		"Resync: a reused key": {func(t *testing.T) error {
			r := newRig(t)
			f := r.subtitle(t, "Show S01E01.en.srt", payload)
			if err := resync(r, &status.JobKey{Key: "k", Fingerprint: [32]byte{1}}, f); err != nil {
				t.Fatal(err)
			}
			return resync(r, &status.JobKey{Key: "k", Fingerprint: [32]byte{2}}, f)
		}, "idempotency_key_reused"},
		"Resync: a job not recorded": {func(t *testing.T) error {
			r := newRig(t, withPersister(errors.New("disk full")))
			return resync(r, nil, r.subtitle(t, "Show S01E01.en.srt", payload))
		}, "job_unrecorded"},
		"Resync: the sync bound": {func(t *testing.T) error {
			r := newRig(t)
			f := hung(t, r)
			for range syncsched.MaxQueuedPerOwner {
				if err := resync(r, nil, f); err != nil {
					t.Fatal(err)
				}
			}
			return resync(r, nil, f)
		}, "sync_capacity"},
		"Preview: out of range": {func(t *testing.T) error {
			_, _, _, err := newRig(t).a.Preview(t.Context(), 1, -time.Second)
			return err
		}, "preview_range"},
		"Preview: at capacity": {func(t *testing.T) error {
			r := newRig(t)
			ffmpegtest.UseFor(t, ffmpegtest.Preview, "hang")
			for range syncsched.MaxPreviews {
				rc, _, _, err := r.a.Preview(t.Context(), r.inst, 0)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = rc.Close() })
			}
			_, _, _, err := r.a.Preview(t.Context(), r.inst, 0)
			return err
		}, "preview_capacity"},
		"Preview: a root unavailable": {func(t *testing.T) error {
			r := newRig(t, func(s *rigSetup) { s.empty = true })
			_, err := previewOf(t, r, r.inst)
			return err
		}, "media_root_unavailable"},
		"Preview: outside every root": {func(t *testing.T) error {
			r := newRig(t, func(s *rigSetup) {
				s.roots = func(string) []string { return []string{filepath.Join(t.TempDir(), "elsewhere")} }
			})
			_, err := previewOf(t, r, r.inst)
			return err
		}, "outside_root"},
	}
	for name, a := range arms {
		t.Run(name, func(t *testing.T) {
			err := a.run(t)
			if got, ok := codeOf(err); !ok || got != a.code {
				t.Errorf("%v answers code %q (coded %v), want %q", err, got, ok, a.code)
			}
		})
	}
}
