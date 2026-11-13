package statedb

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
)

// firstRunFault fails the first run of each call whose context carries
// failThis, as a commit failure would, so bbolt's db.Batch reruns that
// call alone (go.etcd.io/bbolt v1.5.0 db.go:1054). A call that ran before
// a failing sibling reruns in the regrouped batch instead; both reruns
// start from the rolled-back run's caller state.
type firstRunFault struct {
	mu    sync.Mutex
	fired map[*callToken]bool
}

func (f *firstRunFault) hook(ctx context.Context) error {
	tok, _ := ctx.Value(callKey{}).(*callToken)
	if ctx.Value(failThis{}) == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fired[tok] {
		return nil
	}
	f.fired[tok] = true
	return errInjected
}

func (f *firstRunFault) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.fired)
}

// rerunStore opens the store at path with every call markFailing marks
// committing on its rerun.
func rerunStore(t *testing.T, path string) (*Store, *firstRunFault) {
	t.Helper()
	f := &firstRunFault{fired: map[*callToken]bool{}}
	cfg := productionConfig()
	cfg.opts.CommitFault = f.hook
	return mustOpen(t, path, cfg), f
}

func TestCleanupRerunNamesEachRefOnce(t *testing.T) {
	path := tempPath(t)
	seed := mustOpen(t, path, productionConfig())
	expired, _ := expiredSessions(t, seed.Accounts())
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	s, f := rerunStore(t, path)
	refs, err := s.Accounts().CleanupExpiredSessions(markFailing(t.Context()), accountsAt.Add(90*time.Minute), cleanupTimeouts, 10)
	if err != nil || f.count() != 1 {
		t.Fatalf("cleanup %v, %d first runs failed; want the rerun to commit", err, f.count())
	}
	slices.SortFunc(refs, func(x, y access.SessionRef) int { return strings.Compare(x.ID, y.ID) })
	if !slices.Equal(refs, expired) {
		t.Errorf("refs %v, want each expired session once: %v", refs, expired)
	}
}

// seeded is what seedOwned stored: u holds keys, a passkey, the sessions
// "keep" and "drop", and two invites of subject u, issued by key 5 and
// expiring at accountsAt; other holds one key; one failure record is named
// "gone".
type seeded struct {
	u, other *access.User
	keys     []int64
	passkey  int64
}

func seedOwned(t *testing.T, a *AccountStore) seeded {
	t.Helper()
	ctx := t.Context()
	sd := seeded{u: mustCreateUser(t, a, "ada"), other: mustCreateUser(t, a, "bob")}
	for i, owner := range []int64{sd.u.ID, sd.other.ID, sd.u.ID} {
		k := &access.Key{KeyHash: access.NewHash("seed-key-" + string(rune('a'+i))), UserID: owner}
		if err := a.CreateAPIKey(ctx, k); err != nil {
			t.Fatal(err)
		}
		if owner == sd.u.ID {
			sd.keys = append(sd.keys, k.ID)
		}
	}
	cred := &access.PasskeyCredential{UserID: sd.u.ID, CredentialID: []byte("seed-cred"), RawFlags: 1}
	if err := a.CreatePasskey(ctx, cred); err != nil {
		t.Fatal(err)
	}
	sd.passkey = cred.ID
	u := sd.u
	mustCreateSession(t, a, testSession(u.ID, "keep", accountsAt))
	mustCreateSession(t, a, testSession(u.ID, "drop", accountsAt))
	for _, h := range []string{"seed-tok-a", "seed-tok-b"} {
		tok := testToken(h, access.PurposeInvite, accountsAt)
		tok.Subject, tok.IssuedByKey = u.ID, 5
		mustCreateToken(t, a, tok)
	}
	if err := a.PutAuthFailure(ctx, account.FailureCount{Name: "gone", Method: access.MethodPassword, Count: 1}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		mustAppendLog(t, a, testLogEntry(account.LogClassChange, accountsAt))
	}
	return sd
}

// lineageKey is a key minted by the key whose path is [11 12].
func lineageKey(owner int64) *access.Key {
	return &access.Key{KeyHash: access.NewHash("minted"), UserID: owner, LineageIDs: []int64{11, 12}}
}

func TestBatchRerunKeepsCommittedRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, a *AccountStore, marked context.Context, sd seeded)
	}{
		{"CreateUser", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) {
			u := &access.User{Username: "carol"}
			if err := a.CreateUser(marked, u); err != nil {
				t.Fatal(err)
			}
			if got, ok, err := a.UserByUsername(t.Context(), "carol"); err != nil || !ok || got.ID != u.ID {
				t.Errorf("stored user %+v, found %v, %v; want ID %d", got, ok, err, u.ID)
			}
		}},
		{"CreateAPIKey", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			k := lineageKey(sd.u.ID)
			if err := a.CreateAPIKey(marked, k); err != nil {
				t.Fatal(err)
			}
			wantLineage(t, a, k)
		}},
		{"CreateAPIKey in an Update reusing its record", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			k := lineageKey(sd.u.ID)
			if err := a.Update(marked, func(tx account.Tx) error { return tx.CreateAPIKey(marked, k) }); err != nil {
				t.Fatal(err)
			}
			wantLineage(t, a, k)
		}},
		{"CreatePasskey", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			cred := &access.PasskeyCredential{UserID: sd.u.ID, CredentialID: []byte("c1"), RawFlags: 1}
			if err := a.CreatePasskey(marked, cred); err != nil {
				t.Fatal(err)
			}
			got, err := a.PasskeysByUserID(t.Context(), sd.u.ID)
			if err != nil || len(got) != 2 || got[1].ID != cred.ID {
				t.Errorf("passkeys %+v, %v; want the seeded one, then ID %d", got, err, cred.ID)
			}
		}},
		{"CreateKeyAttribution", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			rec := &access.KeyAttribution{UserID: sd.u.ID, Action: access.AttributionCreated}
			if err := a.CreateKeyAttribution(marked, rec); err != nil {
				t.Fatal(err)
			}
			got, err := a.ListKeyAttributions(t.Context())
			if err != nil || len(got) != 1 || got[0].ID != rec.ID {
				t.Errorf("attributions %+v, %v; want one of ID %d", got, err, rec.ID)
			}
		}},
		{"ConsumeOneTimeToken", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			tok := testToken("live", access.PurposeRecovery, accountsAt.Add(time.Hour))
			mustCreateToken(t, a, tok)
			got, found, err := a.ConsumeOneTimeToken(marked, tok.Hash, accountsAt)
			if err != nil || !found || !got.Hash.Equal(tok.Hash) {
				t.Errorf("ConsumeOneTimeToken = found %v, %v; want the live token", found, err)
			}
		}},
		{"DeleteUserAPIKeys", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			ids, err := a.DeleteUserAPIKeys(marked, sd.u.ID)
			if err != nil || !slices.Equal(ids, sd.keys) {
				t.Errorf("DeleteUserAPIKeys = %v, %v; want %v", ids, err, sd.keys)
			}
		}},
		{"DeleteUserPasskeys", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			ids, err := a.DeleteUserPasskeys(marked, sd.u.ID)
			if err != nil || !slices.Equal(ids, []int64{sd.passkey}) {
				t.Errorf("DeleteUserPasskeys = %v, %v; want [%d]", ids, err, sd.passkey)
			}
		}},
		{"DeleteUserSessions", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			ids, err := a.DeleteUserSessions(marked, sd.u.ID, "keep")
			if err != nil || !slices.Equal(ids, []string{"drop"}) {
				t.Errorf("DeleteUserSessions = %v, %v; want [drop]", ids, err)
			}
		}},
		{"DeleteOneTimeTokens", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			if n, err := a.DeleteOneTimeTokens(marked, access.PurposeInvite, sd.u.ID); err != nil || n != 2 {
				t.Errorf("DeleteOneTimeTokens = %d, %v; want 2", n, err)
			}
		}},
		{"DeleteOneTimeTokensIssuedBy", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			if n, err := a.DeleteOneTimeTokensIssuedBy(marked, []int64{5}); err != nil || n != 2 {
				t.Errorf("DeleteOneTimeTokensIssuedBy = %d, %v; want 2", n, err)
			}
		}},
		{"PruneAuthFailures", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			if n, err := a.PruneAuthFailures(marked, nil); err != nil || n != 1 {
				t.Errorf("PruneAuthFailures = %d, %v; want 1", n, err)
			}
		}},
		{"CleanupExpiredOneTimeTokens", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) {
			if n, err := a.CleanupExpiredOneTimeTokens(marked, accountsAt); err != nil || n != 2 {
				t.Errorf("CleanupExpiredOneTimeTokens = %d, %v; want 2", n, err)
			}
		}},
		{"AppendLog", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) {
			e := testLogEntry(account.LogClassSession, accountsAt)
			if err := a.AppendLog(marked, e); err != nil {
				t.Fatal(err)
			}
			if got := logSeqs(t, a); len(got) != 4 || got[3] != e.Event.Seq {
				t.Errorf("stored Seqs %v, want the seeded three, then the returned %d", got, e.Event.Seq)
			}
		}},
		{"AppendLog in an Update reusing its entry", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) {
			e := testLogEntry(account.LogClassSession, accountsAt)
			if err := a.Update(marked, func(tx account.Tx) error { return tx.AppendLog(marked, e) }); err != nil {
				t.Fatal(err)
			}
			if got := logSeqs(t, a); len(got) != 4 || got[3] != e.Event.Seq {
				t.Errorf("stored Seqs %v, want the seeded three, then the returned %d", got, e.Event.Seq)
			}
		}},
		{"PruneLog", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) {
			if n, err := a.PruneLog(marked, account.LogPrune{Class: account.LogClassChange, Keep: 1, Limit: 10}); err != nil || n != 2 {
				t.Errorf("PruneLog = %d, %v; want 2", n, err)
			}
			if st := logStats(t, a); len(st) != 1 || st[0].Evicted != 2 || st[0].Count != 1 {
				t.Errorf("LogStats %+v, want Count 1 and Evicted 2: the committed run's prune alone", st)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := rerunStore(t, tempPath(t))
			a := s.Accounts()
			sd := seedOwned(t, a)
			tc.run(t, a, markFailing(t.Context()), sd)
			if n := f.count(); n != 1 {
				t.Errorf("%d calls had a run rolled back, want the marked one", n)
			}
		})
	}
}

// wantLineage checks k came back, and is stored, as its minting path plus
// its own ID (rule 17).
func wantLineage(t *testing.T, a *AccountStore, k *access.Key) {
	t.Helper()
	want := []int64{11, 12, k.ID}
	if !slices.Equal(k.LineageIDs, want) {
		t.Errorf("returned lineage %v, want %v", k.LineageIDs, want)
	}
	got, ok, err := a.APIKeyByHash(t.Context(), k.KeyHash)
	if err != nil || !ok || !slices.Equal(got.LineageIDs, want) {
		t.Errorf("stored lineage %v (found %v, %v), want %v", got.LineageIDs, ok, err, want)
	}
}

// authRows counts the rows of the auth tables a seeded call could change.
func authRows(t *testing.T, s *Store) []int {
	t.Helper()
	return []int{
		countRows(t, s, s.a.users, nil), countRows(t, s, s.a.keys, nil), countRows(t, s, s.a.passkeys, nil),
		countRows(t, s, s.a.sessions, nil), countRows(t, s, s.a.tokens, nil), countRows(t, s, s.a.failures, nil),
		countRows(t, s, s.a.attributions, nil), countRows(t, s, s.a.log, nil), countRows(t, s, s.a.logClasses, nil),
	}
}

func TestFailedWriteReturnsNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) error
	}{
		{"CreateUser keeps the caller's ID", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) error {
			u := &access.User{Username: "carol", ID: 99}
			err := a.CreateUser(marked, u)
			if u.ID != 99 {
				t.Errorf("user ID %d after the failed create, want the caller's 99", u.ID)
			}
			return err
		}},
		{"CreateAPIKey", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) error {
			k := lineageKey(sd.u.ID)
			err := a.CreateAPIKey(marked, k)
			wantUntouched(t, k)
			return err
		}},
		{"CreateAPIKey in an Update", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) error {
			k := lineageKey(sd.u.ID)
			err := a.Update(marked, func(tx account.Tx) error { return tx.CreateAPIKey(marked, k) })
			wantUntouched(t, k)
			return err
		}},
		{"CreatePasskey", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) error {
			cred := &access.PasskeyCredential{UserID: sd.u.ID, CredentialID: []byte("c1")}
			err := a.CreatePasskey(marked, cred)
			if cred.ID != 0 {
				t.Errorf("passkey ID %d after the failed create, want 0", cred.ID)
			}
			return err
		}},
		{"CreateKeyAttribution", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) error {
			rec := &access.KeyAttribution{UserID: sd.u.ID, Action: access.AttributionCreated}
			err := a.CreateKeyAttribution(marked, rec)
			if rec.ID != 0 {
				t.Errorf("attribution ID %d after the failed create, want 0", rec.ID)
			}
			return err
		}},
		{"ConsumeOneTimeToken", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) error {
			_, found, err := a.ConsumeOneTimeToken(marked, access.NewHash("seed-tok-a"), accountsAt.Add(-time.Hour))
			if found {
				t.Error("the failed consume answered found")
			}
			return err
		}},
		{"DeleteUserAPIKeys", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) error {
			ids, err := a.DeleteUserAPIKeys(marked, sd.u.ID)
			wantNone(t, ids)
			return err
		}},
		{"DeleteUserPasskeys", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) error {
			ids, err := a.DeleteUserPasskeys(marked, sd.u.ID)
			wantNone(t, ids)
			return err
		}},
		{"DeleteUserSessions", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) error {
			ids, err := a.DeleteUserSessions(marked, sd.u.ID, "keep")
			wantNone(t, ids)
			return err
		}},
		{"DeleteOneTimeTokens", func(t *testing.T, a *AccountStore, marked context.Context, sd seeded) error {
			n, err := a.DeleteOneTimeTokens(marked, access.PurposeInvite, sd.u.ID)
			wantZero(t, n)
			return err
		}},
		{"DeleteOneTimeTokensIssuedBy", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) error {
			n, err := a.DeleteOneTimeTokensIssuedBy(marked, []int64{5})
			wantZero(t, n)
			return err
		}},
		{"PruneAuthFailures", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) error {
			n, err := a.PruneAuthFailures(marked, nil)
			wantZero(t, n)
			return err
		}},
		{"CleanupExpiredSessions", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) error {
			refs, err := a.CleanupExpiredSessions(marked, accountsAt.Add(2*time.Hour), cleanupTimeouts, 10)
			wantNone(t, refs)
			return err
		}},
		{"CleanupExpiredOneTimeTokens", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) error {
			n, err := a.CleanupExpiredOneTimeTokens(marked, accountsAt)
			wantZero(t, n)
			return err
		}},
		{"AppendLog keeps the caller's Seq", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) error {
			e := testLogEntry(account.LogClassSession, accountsAt)
			e.Event.Seq = 99
			err := a.AppendLog(marked, e)
			if e.Event.Seq != 99 {
				t.Errorf("Seq %d after the failed append, want the caller's 99", e.Event.Seq)
			}
			return err
		}},
		{"PruneLog", func(t *testing.T, a *AccountStore, marked context.Context, _ seeded) error {
			n, err := a.PruneLog(marked, account.LogPrune{Class: account.LogClassChange, Keep: 1, Limit: 10})
			wantZero(t, n)
			if st := logStats(t, a); len(st) != 1 || st[0].Evicted != 0 {
				t.Errorf("LogStats %+v after the failed prune, want Evicted 0", st)
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := faultyStore(t, errInjected)
			a := s.Accounts()
			sd := seedOwned(t, a)
			before := authRows(t, s)
			if err := tc.run(t, a, markFailing(t.Context()), sd); !errors.Is(err, bolttable.ErrCommit) {
				t.Errorf("err %v, want ErrCommit", err)
			}
			if rows := authRows(t, s); !slices.Equal(rows, before) {
				t.Errorf("auth rows %v after the failed call, want %v", rows, before)
			}
		})
	}
}

func wantUntouched(t *testing.T, k *access.Key) {
	t.Helper()
	if k.ID != 0 || !slices.Equal(k.LineageIDs, []int64{11, 12}) {
		t.Errorf("key ID %d, lineage %v after the failed create; want 0 and the caller's [11 12]", k.ID, k.LineageIDs)
	}
}

func wantNone[T any](t *testing.T, got []T) {
	t.Helper()
	if got != nil {
		t.Errorf("the failed call answered %v, want nil", got)
	}
}

func wantZero[T int | int64](t *testing.T, n T) {
	t.Helper()
	if n != 0 {
		t.Errorf("the failed call answered %d, want 0", n)
	}
}
