package syncsched_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

// code is the element type of syncsched.Codes.
type code = struct {
	Err  error
	Code string
}

// sentinels names every exported Err* var of the package; a new sentinel
// must be added here and to Codes or exempt.
var sentinels = map[string]error{
	"ErrSyncFailed":      syncsched.ErrSyncFailed,
	"ErrDifferentCut":    syncsched.ErrDifferentCut,
	"ErrSyncCapacity":    syncsched.ErrSyncCapacity,
	"ErrBatchTooLarge":   syncsched.ErrBatchTooLarge,
	"ErrPreviewCapacity": syncsched.ErrPreviewCapacity,
	"ErrPreviewRange":    syncsched.ErrPreviewRange,
}

// exempt lists sentinels deliberately left out of Codes, with the reason.
var exempt = map[string]string{
	"ErrSyncFailed":   "only Align returns it; only search calls Align",
	"ErrDifferentCut": "only Align returns it; jobs report the different_cut cause",
}

// Go cannot enumerate package vars by reflect, so the source is parsed.
func TestEverySentinelCoded(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				for _, id := range spec.(*ast.ValueSpec).Names {
					if id.IsExported() && strings.HasPrefix(id.Name, "Err") {
						declared = append(declared, id.Name)
					}
				}
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no exported Err* var; the parse is broken")
	}
	for _, name := range declared {
		v, ok := sentinels[name]
		if !ok {
			t.Errorf("%s is missing from this test's sentinels table", name)
			continue
		}
		_, isExempt := exempt[name]
		coded := slices.ContainsFunc(syncsched.Codes, func(c code) bool { return c.Err == v })
		if coded == isExempt {
			t.Errorf("%s: in Codes %v, exempt %v; want exactly one", name, coded, isExempt)
		}
	}
	for name := range sentinels {
		if !slices.Contains(declared, name) {
			t.Errorf("sentinels names %s, which the package does not declare", name)
		}
	}
	passed := slices.Concat(status.StartCodes, mediawrite.Codes)
	for _, c := range passed {
		if !slices.Contains(syncsched.Codes, c) {
			t.Errorf("%v (%s) is not passed on", c.Err, c.Code)
		}
	}
	if want := len(sentinels) - len(exempt) + len(passed); len(syncsched.Codes) != want {
		t.Errorf("Codes holds %d entries, want syncsched's %d plus the %d passed on", len(syncsched.Codes), len(sentinels)-len(exempt), len(passed))
	}
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
// exports and wants the code design section 10 and api's owner table give
// it; a caller bug (no method, an empty batch) is api's to refuse first.
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
		}, "file_changed"},
		"Propose: an unknown instance": {func(t *testing.T) error {
			r := newRig(t)
			f := r.subtitle(t, "Show S01E01.en.srt", payload)
			ref, err := mediawrite.DecodeRef(r.inst+1000, f.File.Name())
			if err != nil {
				t.Fatal(err)
			}
			return propose(r, syncsched.Target{File: ref, Expect: f.Expect}, nil)
		}, "param_invalid"},
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
		}, "body_invalid"},
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
