package statedb

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cplieger/access/accesstest"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
)

// durabilityFault is design 9's BeforePublish: arming it makes the next
// write call claim the fault through its call token, and bolttable asks the
// hook on every run of that call, so every rerun db.Batch makes fails too.
type durabilityFault struct {
	claimed atomic.Pointer[callToken]
	armed   atomic.Bool
}

func (f *durabilityFault) hook(ctx context.Context) error {
	tok, _ := ctx.Value(callKey{}).(*callToken)
	if tok == nil {
		return errors.New("write call without a token")
	}
	if f.armed.CompareAndSwap(true, false) {
		f.claimed.Store(tok)
	}
	if f.claimed.Load() == tok {
		return errInjected
	}
	return nil
}

// contractPaths remembers each contract store's file, so Reopen can open a
// fresh Store over it.
var contractPaths sync.Map

func contractStore(t *testing.T, cfg config) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	s := mustOpen(t, path, cfg)
	contractPaths.Store(s, path)
	return s
}

func faultyAccounts(t *testing.T) (*Store, *durabilityFault) {
	t.Helper()
	f := &durabilityFault{}
	cfg := productionConfig()
	cfg.opts.CommitFault = f.hook
	return contractStore(t, cfg), f
}

func accountContract() accesstest.ContractConfig {
	return accesstest.ContractConfig{
		NewFaulty: func(t *testing.T) (account.Store, accesstest.DurabilityFaults) {
			s, f := faultyAccounts(t)
			return s.Accounts(), accesstest.DurabilityFaults{
				BeforePublish: func() { f.armed.Store(true) },
				Publishes: func() int {
					st, err := s.db.Stats(context.Background())
					if err != nil {
						t.Errorf("Stats: %v", err)
					}
					return int(st.Commits)
				},
			}
		},
		Reopen: func(t *testing.T, old account.Store) account.Store {
			s := old.(*AccountStore).s
			path, ok := contractPaths.Load(s)
			if !ok {
				t.Fatal("Reopen of a store contractStore did not make")
			}
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			next := mustOpen(t, path.(string), productionConfig())
			contractPaths.Store(next, path)
			return next.Accounts()
		},
	}
}

func TestStoreContract(t *testing.T) {
	accesstest.StoreContract(t, func(t *testing.T) account.Store {
		return contractStore(t, productionConfig()).Accounts()
	}, accountContract())
}

func TestWriteHealthCountsContractFault(t *testing.T) {
	s, f := faultyAccounts(t)
	f.armed.Store(true)
	err := s.Accounts().PutAuthFailure(t.Context(), account.FailureCount{Name: "n", Method: "password", Count: 1})
	if !errors.Is(err, bolttable.ErrCommit) || !errors.Is(err, errInjected) {
		t.Fatalf("PutAuthFailure under an armed fault: %v, want the injected commit failure", err)
	}
	if h := s.WriteHealth(); h.Failures != 1 || !errors.Is(h.Last, errInjected) {
		t.Errorf("WriteHealth %+v, want the fault counted once", h)
	}
	if _, ok, err := s.Accounts().AuthFailure(t.Context(), account.FailureRef{Name: "n", Method: "password"}); err != nil || ok {
		t.Errorf("record after the failed write: found %v, error %v; want none", ok, err)
	}
}
