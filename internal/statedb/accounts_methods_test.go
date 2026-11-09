package statedb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesstest"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

// coreSnapshot renders the activation, source, item, coverage and counts
// rows as their stored JSON, so two snapshots are equal exactly when those
// rows are.
func coreSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	var b strings.Builder
	err := s.db.View(t.Context(), func(r *bolttable.ReadTx) error {
		dump := func(name string, scan func(func(k, v any)) error) error {
			b.WriteString(name + ":\n")
			return scan(func(k, v any) {
				val, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&b, "  %v %s\n", k, val)
			})
		}
		for _, d := range []struct {
			name string
			scan func(func(k, v any)) error
		}{
			{"activation", scanAll(r, s.t.activation)},
			{"roots", scanAll(r, s.t.roots)},
			{"sightings", scanAll(r, s.t.sightings)},
			{"items", scanAll(r, s.t.items)},
			{"instances", scanAll(r, s.t.instances)},
			{"coverage", scanAll(r, s.t.coverage)},
			{"counts", scanAll(r, s.t.counts)},
		} {
			if err := dump(d.name, d.scan); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func scanAll[K, V any](r *bolttable.ReadTx, tb *bolttable.Table[K, V]) func(func(k, v any)) error {
	return func(emit func(k, v any)) error {
		return tb.Scan(r, bolttable.Range{}, func(k K, v *V) (bool, error) { emit(k, *v); return true, nil })
	}
}

// guardCall is one MethodGuard call as the guard saw it.
type guardCall struct {
	before, next AuthMethodState
	stored       bool
}

// recordingGuard records each call and creates the user name in the
// transaction it is given, reading it back there, unless refuse is set.
type recordingGuard struct {
	refuse error
	name   string
	calls  []guardCall
}

func (g *recordingGuard) guard(t *testing.T) MethodGuard {
	return func(ctx context.Context, tx account.Tx, before AuthMethodState, stored bool, next AuthMethodState) error {
		g.calls = append(g.calls, guardCall{before: before, next: next, stored: stored})
		if g.refuse != nil {
			return g.refuse
		}
		if err := tx.CreateUser(ctx, &access.User{Username: g.name}); err != nil {
			return err
		}
		if _, ok, err := tx.UserByUsername(ctx, g.name); err != nil || !ok {
			t.Errorf("the guard's tx does not read the user it created: found %v, %v", ok, err)
		}
		return nil
	}
}

func methodState(password bool, issuer string) AuthMethodState {
	return AuthMethodState{
		PasswordLogin: password, OIDCIssuer: issuer, RPID: "subflux.example",
		SignIn: SignInKeys{OIDCClientID: "subflux", PasskeyOrigins: SetDigest{Count: 1, SHA256: [32]byte{1}}},
	}
}

func storedMethodState(t *testing.T, s *Store) (AuthMethodState, bool) {
	t.Helper()
	st, ok, err := MethodStateOf(t.Context(), s.Accounts())
	if err != nil {
		t.Fatalf("MethodStateOf: %v", err)
	}
	return st, ok
}

func commits(t *testing.T, s *Store) uint64 {
	t.Helper()
	st, err := s.db.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return st.Commits
}

func TestActivateWritesMethodState(t *testing.T) {
	s := mustOpen(t, tempPath(t), productionConfig())
	en := policyOf(t, "en")
	srcs := []media.SourceID{"sonarr"}
	first := methodState(true, "")
	g := &recordingGuard{name: "created-by-guard"}
	before := commits(t, s)
	activate(t, s, Activation{Sources: srcs, Languages: en, Auth: &first, Guard: g.guard(t)})
	if got := commits(t, s) - before; got != 1 {
		t.Errorf("the first Activate made %d commits, want one Update", got)
	}
	if len(g.calls) != 1 || g.calls[0].stored || g.calls[0].before != (AuthMethodState{}) || g.calls[0].next != first {
		t.Fatalf("guard calls %+v, want one with nothing stored and next the record", g.calls)
	}
	if st, ok := storedMethodState(t, s); !ok || st != first {
		t.Fatalf("stored %+v (found %v), want %+v", st, ok, first)
	}
	if _, ok, err := s.Accounts().UserByUsername(t.Context(), "created-by-guard"); err != nil || !ok {
		t.Errorf("the guard's user committed with the record: found %v, %v", ok, err)
	}

	t.Run("an equal row under an equal digest writes nothing", func(t *testing.T) {
		g.calls = nil
		before := commits(t, s)
		same := first
		activate(t, s, Activation{Sources: srcs, Languages: en, Auth: &same, Guard: g.guard(t)})
		if got := commits(t, s) - before; got != 0 || len(g.calls) != 0 {
			t.Errorf("%d commits and %d guard calls, want none", got, len(g.calls))
		}
	})
	t.Run("a changed sign-in key under an equal digest is written", func(t *testing.T) {
		g.calls, g.name = nil, "second"
		next := first
		next.SignIn.OIDCRedirectURL = "https://subflux.example/callback"
		before := commits(t, s)
		activate(t, s, Activation{Sources: srcs, Languages: en, Auth: &next, Guard: g.guard(t)})
		if got := commits(t, s) - before; got != 1 {
			t.Errorf("%d commits, want one", got)
		}
		if len(g.calls) != 1 || !g.calls[0].stored || g.calls[0].before != first || g.calls[0].next != next {
			t.Errorf("guard calls %+v, want both records whole", g.calls)
		}
		if st, _ := storedMethodState(t, s); st != next {
			t.Errorf("stored %+v, want %+v", st, next)
		}
		first = next
	})
	t.Run("a guard refusal writes nothing and keeps the activation", func(t *testing.T) {
		refused := errors.New("the last sign-in method")
		g.calls, g.refuse = nil, refused
		fr := policyOf(t, "fr")
		off := methodState(false, "")
		before := commits(t, s)
		err := s.Activate(t.Context(), Activation{Sources: []media.SourceID{"radarr"}, Languages: fr, Auth: &off, Guard: g.guard(t)})
		if err != refused { //nolint:errorlint // the guard's error is returned as it is
			t.Fatalf("Activate = %v, want the guard's error itself", err)
		}
		if got := commits(t, s) - before; got != 0 {
			t.Errorf("%d commits after the refusal, want none", got)
		}
		if st, _ := storedMethodState(t, s); st != first {
			t.Errorf("stored %+v after the refusal, want %+v", st, first)
		}
		if !samePolicy(s.Policy(), en) {
			t.Error("the refused activation's policy is in force")
		}
		if row, _ := getRow(t, s, s.t.activation, constKey{}); len(row.Sources) != 1 || row.Sources[0] != "sonarr" {
			t.Errorf("activation row sources %v after the refusal, want [sonarr]", row.Sources)
		}
	})
	t.Run("an Auth without a Guard is refused", func(t *testing.T) {
		if err := s.Activate(t.Context(), Activation{Sources: srcs, Languages: en, Auth: &first}); !errors.Is(err, errNoGuard) {
			t.Errorf("Activate = %v, want errNoGuard", err)
		}
	})
}

func TestMethodStateOf(t *testing.T) {
	s := mustOpen(t, tempPath(t), productionConfig())
	want := methodState(true, "https://idp.example")
	if err := s.ActivateAuth(t.Context(), want, func(context.Context, account.Tx, AuthMethodState, bool, AuthMethodState) error { return nil }); err != nil {
		t.Fatal(err)
	}
	t.Run("through the store", func(t *testing.T) {
		if got, ok := storedMethodState(t, s); !ok || got != want {
			t.Errorf("MethodStateOf = %+v, %v; want %+v", got, ok, want)
		}
	})
	t.Run("through its transaction", func(t *testing.T) {
		err := s.Accounts().Update(t.Context(), func(tx account.Tx) error {
			got, ok, err := MethodStateOf(t.Context(), tx)
			if err == nil && (!ok || got != want) {
				t.Errorf("MethodStateOf = %+v, %v; want %+v", got, ok, want)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("through another store's transaction", func(t *testing.T) {
		err := accesstest.NewMemStore().Update(t.Context(), func(tx account.Tx) error {
			_, _, err := MethodStateOf(t.Context(), tx)
			return err
		})
		if !errors.Is(err, errForeignTx) {
			t.Errorf("MethodStateOf over a MemStore tx = %v, want errForeignTx", err)
		}
	})
}

func TestActivateAuthWritesOnlyMethodState(t *testing.T) {
	path := tempPath(t)
	s := covLib(t, path, policyOf(t, "en"))
	showEpisode(t, s, "sonarr", epPath)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = mustOpen(t, path, productionConfig())
	coreBefore := coreSnapshot(t, s)
	g := &recordingGuard{name: "boot-guard"}
	auth := methodState(true, "")
	before := commits(t, s)
	if err := s.ActivateAuth(t.Context(), auth, g.guard(t)); err != nil {
		t.Fatal(err)
	}
	if got := commits(t, s) - before; got != 1 {
		t.Errorf("%d commits, want one Update", got)
	}
	if len(g.calls) != 1 || g.calls[0].stored || g.calls[0].next != auth {
		t.Errorf("guard calls %+v, want one with nothing stored", g.calls)
	}
	if st, ok := storedMethodState(t, s); !ok || st != auth {
		t.Errorf("stored %+v (found %v), want %+v", st, ok, auth)
	}
	if after := coreSnapshot(t, s); after != coreBefore {
		t.Errorf("core rows after ActivateAuth:\n%s\nbefore:\n%s", after, coreBefore)
	}
	if !samePolicy(s.Policy(), language.Policy{}) {
		t.Error("ActivateAuth installed a policy")
	}
	t.Run("the next Activate recomputes coverage", func(t *testing.T) {
		if s.active.Load() != nil {
			t.Fatal("ActivateAuth installed an activation, so the next Activate would skip its recompute")
		}
	})
	t.Run("a stored row is handed to the guard", func(t *testing.T) {
		g.calls, g.name = nil, "boot-guard-2"
		next := methodState(false, "https://idp.example")
		if err := s.ActivateAuth(t.Context(), next, g.guard(t)); err != nil {
			t.Fatal(err)
		}
		if len(g.calls) != 1 || !g.calls[0].stored || g.calls[0].before != auth {
			t.Errorf("guard calls %+v, want the stored row", g.calls)
		}
		auth = next
	})
	t.Run("an equal row writes nothing", func(t *testing.T) {
		g.calls = nil
		before := commits(t, s)
		if err := s.ActivateAuth(t.Context(), auth, g.guard(t)); err != nil {
			t.Fatal(err)
		}
		if n := commits(t, s); n != before || len(g.calls) != 0 {
			t.Errorf("%d commits (was %d) and %d guard calls; want no commit and no guard call", n, before, len(g.calls))
		}
		if st, _ := storedMethodState(t, s); st != auth {
			t.Errorf("stored %+v, want %+v", st, auth)
		}
	})
	t.Run("a guard error writes nothing", func(t *testing.T) {
		refused := errors.New("refused")
		g.calls, g.refuse = nil, refused
		if err := s.ActivateAuth(t.Context(), methodState(true, "https://other.example"), g.guard(t)); err != refused { //nolint:errorlint // returned as it is
			t.Fatalf("ActivateAuth = %v, want the guard's error itself", err)
		}
		if st, _ := storedMethodState(t, s); st != auth {
			t.Errorf("stored %+v after the refusal, want %+v", st, auth)
		}
	})
	t.Run("a nil guard is refused", func(t *testing.T) {
		if err := s.ActivateAuth(t.Context(), auth, nil); !errors.Is(err, errNoGuard) {
			t.Errorf("ActivateAuth = %v, want errNoGuard", err)
		}
	})
}
