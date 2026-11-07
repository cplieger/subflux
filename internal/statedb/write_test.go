package statedb

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/media"
)

var errInjected = errors.New("injected commit fault")

// commitFault fails every run of the one write call whose context carries
// failThis, the way section 9's durability fault claims a call through its
// token.
type commitFault struct {
	claimed atomic.Pointer[callToken]
	err     error
}

type failThis struct{}

func markFailing(ctx context.Context) context.Context {
	return context.WithValue(ctx, failThis{}, true)
}

func (f *commitFault) hook(ctx context.Context) error {
	tok, _ := ctx.Value(callKey{}).(*callToken)
	if tok == nil {
		return errors.New("write call without a token")
	}
	if ctx.Value(failThis{}) != nil {
		f.claimed.CompareAndSwap(nil, tok)
	}
	if f.claimed.Load() == tok {
		return f.err
	}
	return nil
}

func faultyStore(t *testing.T, err error) *Store {
	t.Helper()
	f := &commitFault{err: err}
	cfg := productionConfig()
	cfg.opts.CommitFault = f.hook
	return mustOpen(t, filepath.Join(t.TempDir(), "subflux.bolt"), cfg)
}

func putCursor(s *Store, src string) func(*bolttable.Tx) error {
	return func(tx *bolttable.Tx) error {
		return s.t.cursors.Put(tx, media.SourceID(src), &cursorRow{Cursor: []byte(src)})
	}
}

func TestWriteHealth(t *testing.T) {
	s := faultyStore(t, errInjected)
	ctx := t.Context()
	if got := s.WriteHealth(); got != (WriteHealth{}) {
		t.Fatalf("WriteHealth before any write: %+v", got)
	}

	err := s.write(markFailing(ctx), "fail", s.db.Update, putCursor(s, "a"))
	if !errors.Is(err, bolttable.ErrCommit) || !errors.Is(err, errInjected) {
		t.Fatalf("faulted write: %v, want ErrCommit and the injected error", err)
	}
	failed := s.WriteHealth()
	if failed.Failures != 1 || !errors.Is(failed.Last, errInjected) || failed.LastFailed.IsZero() || !failed.LastOK.IsZero() {
		t.Errorf("after one failed commit: %+v", failed)
	}

	if err := s.write(ctx, "ok", s.db.Update, putCursor(s, "b")); err != nil {
		t.Fatalf("write: %v", err)
	}
	ok := s.WriteHealth()
	if ok.Failures != 1 || !errors.Is(ok.Last, errInjected) || ok.LastFailed != failed.LastFailed || !ok.LastOK.After(ok.LastFailed) {
		t.Errorf("after recovery: %+v; want the failure kept and LastOK after it", ok)
	}

	t.Run("refusal_changes_nothing", func(t *testing.T) {
		refused := errors.New("refused")
		err := s.write(ctx, "refuse", s.db.Update, func(*bolttable.Tx) error { return refused })
		if !errors.Is(err, refused) {
			t.Fatalf("refusal: %v", err)
		}
		if got := s.WriteHealth(); got != ok {
			t.Errorf("after a refusal: %+v, want %+v", got, ok)
		}
	})
	t.Run("conflict_changes_nothing", func(t *testing.T) {
		putInst := func(id InstanceID) func(*bolttable.Tx) error {
			return func(tx *bolttable.Tx) error {
				return s.t.instances.Put(tx, id, &instanceRow{Owner: "src", VideoPath: "/tv/show/s01e01.mkv"})
			}
		}
		if err := s.write(ctx, "inst", s.db.Update, putInst(1)); err != nil {
			t.Fatalf("first instance: %v", err)
		}
		before := s.WriteHealth()
		var conflict *bolttable.ConflictError
		if err := s.write(ctx, "inst", s.db.Update, putInst(2)); !errors.As(err, &conflict) {
			t.Fatalf("second instance at the same path: %v, want *ConflictError", err)
		}
		if got := s.WriteHealth(); got != before {
			t.Errorf("after a conflict: %+v, want %+v", got, before)
		}
	})
}

func TestWriteBatchFaultFailsOnlyItsCall(t *testing.T) {
	s := faultyStore(t, errInjected)
	ctx := t.Context()
	var wg sync.WaitGroup
	errs := make([]error, 9)
	for i := range errs {
		callCtx := ctx
		if i == 0 {
			callCtx = markFailing(ctx)
		}
		wg.Go(func() { errs[i] = s.write(callCtx, "batch", s.db.Batch, putCursor(s, string(rune('a'+i)))) })
	}
	wg.Wait()
	if !errors.Is(errs[0], bolttable.ErrCommit) || !errors.Is(errs[0], errInjected) {
		t.Errorf("claimed call: %v, want the injected commit failure", errs[0])
	}
	for i, err := range errs[1:] {
		if err != nil {
			t.Errorf("batch call %d beside the claimed one: %v", i+1, err)
		}
	}
	if got := s.WriteHealth().Failures; got != 1 {
		t.Errorf("Failures %d, want the claimed call counted once", got)
	}
}

func TestWriteCommitFailureLogs(t *testing.T) {
	cases := []struct{ text, cause string }{
		{"file resize error: write /config/subflux.bolt: no space left on device", "ENOSPC"},
		{"file resize error: open /config/subflux.bolt: read-only file system", "EROFS"},
		{"fdatasync /config/subflux.bolt: input/output error", "EIO"},
		{"bbolt: tx closed", "other"},
	}
	for _, tc := range cases {
		t.Run(tc.cause, func(t *testing.T) {
			s := faultyStore(t, errors.New(tc.text))
			rec := capture.Default(t)
			if err := s.write(markFailing(t.Context()), "put cursor", s.db.Update, putCursor(s, "a")); !errors.Is(err, bolttable.ErrCommit) {
				t.Fatalf("write: %v, want ErrCommit", err)
			}
			if n := rec.CountLevel(slog.LevelError, "did not commit"); n != 1 {
				t.Fatalf("%d ERROR records, want 1", n)
			}
			if v, _ := rec.AttrValue("did not commit", "op"); v != "put cursor" {
				t.Errorf("op %q, want put cursor", v)
			}
			if v, _ := rec.AttrValue("did not commit", "cause"); v != tc.cause {
				t.Errorf("cause %q, want %s", v, tc.cause)
			}
		})
	}
	t.Run("refusal_logs_nothing", func(t *testing.T) {
		s := faultyStore(t, errInjected)
		rec := capture.Default(t)
		_ = s.write(t.Context(), "refuse", s.db.Update, func(*bolttable.Tx) error { return errors.New("refused") })
		if rec.Len() != 0 {
			t.Errorf("a refusal logged %v", rec.Messages())
		}
	})
}
