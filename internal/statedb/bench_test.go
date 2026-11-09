package statedb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/access/ratelimit"
	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// The production scale design section 13 benchmarks at: 63,000 items under
// three targets, one root large enough for a full 500-row page.
const (
	benchItems     = 63_000
	benchBigRoot   = 1_000
	benchRootItems = 100
	benchTargets   = "default:\n  - tag: en\n  - tag: fr\n  - tag: de\n"
)

// benchLibrary is a store holding benchItems episodes, each in its own
// instance, observed by one complete pass of sonarr under three targets.
func benchLibrary(b *testing.B) (*Store, language.Policy) {
	b.Helper()
	p := pol(b, benchTargets)
	s, err := open(filepath.Join(b.TempDir(), "subflux.bolt"), new(productionConfig()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	ctx := b.Context()
	if err := s.Activate(ctx, Activation{Sources: []media.SourceID{"sonarr"}, Languages: p}); err != nil {
		b.Fatal(err)
	}
	pass, err := s.BeginPass(ctx, "sonarr", PassScope{})
	if err != nil {
		b.Fatal(err)
	}
	for root, left := 0, benchItems; left > 0; root++ {
		n := benchRootItems
		if root == 0 {
			n = benchBigRoot
		}
		items := make([]mediasource.Item, 0, n)
		for i := range n {
			season, ep := 1+i/100, 1+i%100
			items = append(items, episode(fmt.Sprintf("r%d-e%d", root, i), season, ep, fmt.Sprintf("/tv/%d/S%02dE%02d.mkv", root, season, ep)))
		}
		if err := s.ObserveItems(ctx, "sonarr", pass, series(fmt.Sprintf("r%d", root), uint64(10_000+root)), items); err != nil {
			b.Fatal(err)
		}
		left -= n
	}
	if _, err := s.FinishPass(ctx, "sonarr", pass, true, mediasource.Cursor{}, nil); err != nil {
		b.Fatal(err)
	}
	return s, p
}

func bigRoot(b *testing.B) media.RootKey {
	b.Helper()
	k, ok := media.Choose(media.Episode, media.ExternalIDs{TVDB: 10_000}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1})
	if !ok {
		b.Fatal("no key for the big root")
	}
	return k.Root()
}

func BenchmarkItemsPage(b *testing.B) {
	s, _ := benchLibrary(b)
	root := bigRoot(b)
	b.ReportAllocs()
	for b.Loop() {
		rows, _, err := s.Items(b.Context(), root, "", maxListLimit)
		if err != nil || len(rows) != maxListLimit {
			b.Fatalf("Items: %d rows, %v; want %d", len(rows), err, maxListLimit)
		}
	}
}

func BenchmarkSince20000Changes(b *testing.B) {
	s, _ := benchLibrary(b)
	from := headRev(b, s)
	const changes = 20_000
	var wg sync.WaitGroup
	errs := make(chan error, changes)
	sem := make(chan struct{}, 256)
	for i := range changes {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			k, _ := media.Choose(media.Episode, media.ExternalIDs{TVDB: uint64(10_001 + i%620)}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1 + i/620})
			errs <- s.db.Batch(b.Context(), func(tx *bolttable.Tx) error {
				return s.t.locks.Put(tx, lockKey{Key: k, Tag: mustTag(b, "en"), Variant: language.Standard}, &lockRow{At: time.Unix(int64(i), 0).UTC()})
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			b.Fatal(err)
		}
	}
	epoch := s.db.Epoch()
	b.ReportAllocs()
	for b.Loop() {
		d, err := s.Since(b.Context(), epoch, from)
		if err != nil || d.Full || len(d.Roots) != 620 {
			b.Fatalf("Since: full %v, %d roots, %v; want 620 roots", d.Full, len(d.Roots), err)
		}
	}
}

func BenchmarkActivatePolicyChange(b *testing.B) {
	s, p := benchLibrary(b)
	policies := []language.Policy{pol(b, "default:\n  - tag: en\n  - tag: fr\n  - tag: es\n"), p}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if err := s.Activate(b.Context(), Activation{Sources: []media.SourceID{"sonarr"}, Languages: policies[i%2]}); err != nil {
			b.Fatal(err)
		}
		i++
	}
}

func BenchmarkSingleRowCommit(b *testing.B) {
	s, _ := benchLibrary(b)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		if err := s.PutCredentialDisable(b.Context(), CredentialDisable{At: time.Unix(int64(i), 0).UTC(), Provider: "opensubtitles", Reason: "rejected"}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkViewDuringBackup reports the slowest Item read while a Backup
// runs beside a writer committing in a loop.
func BenchmarkViewDuringBackup(b *testing.B) {
	s, _ := benchLibrary(b)
	k, _ := media.Choose(media.Episode, media.ExternalIDs{TVDB: 10_001}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1})
	dir := b.TempDir()
	var slowest time.Duration
	n := 0
	for b.Loop() {
		n++
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.PutCredentialDisable(b.Context(), CredentialDisable{At: time.Unix(int64(i), 0).UTC(), Provider: "p", Reason: "r"}); err != nil {
					b.Error(err)
					return
				}
			}
		})
		done := make(chan error, 1)
		go func() { done <- s.Backup(b.Context(), filepath.Join(dir, fmt.Sprintf("b%d.bolt", n))) }()
	reads:
		for {
			select {
			case err := <-done:
				if err != nil {
					b.Fatal(err)
				}
				break reads
			default:
			}
			start := time.Now()
			if _, _, found, err := s.Item(b.Context(), k); err != nil || !found {
				b.Fatalf("Item: %v, %v", found, err)
			}
			slowest = max(slowest, time.Since(start))
		}
		close(stop)
		wg.Wait()
	}
	b.ReportMetric(float64(slowest.Microseconds()), "max-view-µs")
}

// The largest realistic root: a long-running series of 2,000 episodes, one
// file each, under three targets in all three variants.
const (
	recountItems   = 2_000
	recountTargets = "default:\n  - tag: en\n    variants: [standard, hi, forced]\n  - tag: fr\n    variants: [standard, hi, forced]\n  - tag: de\n    variants: [standard, hi, forced]\n"
)

// recountLibrary is a store holding one root of n episodes, each with an
// English file, every other one locked on en, under recountTargets.
func recountLibrary(b *testing.B, tvdb uint64, n int) (*Store, InstanceID) {
	b.Helper()
	s, err := open(filepath.Join(b.TempDir(), "subflux.bolt"), new(productionConfig()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	ctx := b.Context()
	if err := s.Activate(ctx, Activation{Sources: []media.SourceID{"sonarr"}, Languages: pol(b, "default:\n  - tag: en\n")}); err != nil {
		b.Fatal(err)
	}
	pass, err := s.BeginPass(ctx, "sonarr", PassScope{})
	if err != nil {
		b.Fatal(err)
	}
	items := make([]mediasource.Item, 0, n)
	for i := range n {
		season, ep := 1+i/100, 1+i%100
		items = append(items, episode(fmt.Sprintf("e%d", i), season, ep, fmt.Sprintf("/tv/long/S%02dE%02d.mkv", season, ep)))
	}
	if err := s.ObserveItems(ctx, "sonarr", pass, series("long", tvdb), items); err != nil {
		b.Fatal(err)
	}
	var first InstanceID
	if err := s.db.Update(ctx, func(tx *bolttable.Tx) error {
		return s.t.instances.Scan(tx.Read(), bolttable.Range{}, func(id InstanceID, v *instanceRow) (bool, error) {
			if first == 0 {
				first = id
			}
			err := s.t.local.Put(tx, id, &localRow{Files: []localFileRow{{Name: "x.en.srt", Tag: rowTag{t: mustTag(b, "en")}}}})
			if err == nil && id%2 == 0 {
				err = s.t.locks.Put(tx, lockKey{Key: v.Items[0].k, Tag: mustTag(b, "en"), Variant: language.Standard}, &lockRow{At: time.Unix(1, 0).UTC()})
			}
			return err == nil, err
		})
	}); err != nil {
		b.Fatal(err)
	}
	if err := s.Activate(ctx, Activation{Sources: []media.SourceID{"sonarr"}, Languages: pol(b, recountTargets)}); err != nil {
		b.Fatal(err)
	}
	return s, first
}

// BenchmarkRecountLargestRoot measures what CR-UX-1's recount adds to a
// commit: one lock toggle in the 2,000-episode root against the same toggle
// in a one-episode root, and the recount alone.
func BenchmarkRecountLargestRoot(b *testing.B) {
	toggle := func(b *testing.B, n int) {
		s, id := recountLibrary(b, 20_000, n)
		en := mustTag(b, "en")
		b.ReportAllocs()
		locked := false
		for b.Loop() {
			locked = !locked
			if err := s.SetLock(b.Context(), id, en, language.Standard, locked); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Run("lock_toggle_2000_items", func(b *testing.B) { toggle(b, recountItems) })
	b.Run("lock_toggle_1_item", func(b *testing.B) { toggle(b, 1) })
	b.Run("recount_2000_items", func(b *testing.B) {
		s, _ := recountLibrary(b, 20_000, recountItems)
		root := epKeyB(b, 20_000).Root()
		b.ReportAllocs()
		for b.Loop() {
			if err := s.db.View(b.Context(), func(r *bolttable.ReadTx) error {
				row, err := s.countRoot(r, root)
				if err == nil && len(row.Pairs) != 9 {
					err = fmt.Errorf("%d pairs, want 9", len(row.Pairs))
				}
				return err
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func epKeyB(b *testing.B, tvdb uint64) media.Key {
	b.Helper()
	k, ok := media.Choose(media.Episode, media.ExternalIDs{TVDB: tvdb}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1})
	if !ok {
		b.Fatal("no key")
	}
	return k
}

func benchAccounts(b *testing.B) (*Store, *AccountStore) {
	b.Helper()
	s, err := open(filepath.Join(b.TempDir(), "subflux.bolt"), new(productionConfig()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s, s.Accounts()
}

func benchCommits(b *testing.B, s *Store) uint64 {
	b.Helper()
	st, err := s.db.Stats(b.Context())
	if err != nil {
		b.Fatal(err)
	}
	return st.Commits
}

// countParams is OWASP's cheapest Argon2id row, which access's own
// BenchmarkCountCommit derives at behind a four-derivation budget, so the
// two sets of figures compare.
var countParams = access.Argon2Params{Memory: 7168, Iterations: 5, Parallelism: 1, SaltLength: 16, KeyLength: 32}

// timedAccounts adds up the wall time every Update spends.
type timedAccounts struct {
	*AccountStore
	nanos atomic.Int64
}

func (t *timedAccounts) Update(ctx context.Context, fn func(tx account.Tx) error) error {
	start := time.Now()
	err := t.AccountStore.Update(ctx, fn)
	t.nanos.Add(int64(time.Since(start)))
	return err
}

// countService is an account.Service over store whose limiter admits every
// attempt, with the password hash it gives the users it signs in.
func countService(b *testing.B, store *timedAccounts) (*account.Service, access.Hash) {
	b.Helper()
	ctx := b.Context()
	budget, err := access.NewHashBudget(4 * access.HashCost(countParams))
	if err != nil {
		b.Fatal(err)
	}
	h, err := access.NewHasher(access.HasherConfig{Params: countParams, Budget: budget, Wait: 10 * time.Minute})
	if err != nil {
		b.Fatal(err)
	}
	hash, err := h.Hash(ctx, "correct horse battery staple")
	if err != nil {
		b.Fatal(err)
	}
	authn, err := access.New(store.AccountStore, access.Config{})
	if err != nil {
		b.Fatal(err)
	}
	lc := ratelimit.DefaultConfig()
	wide := ratelimit.Rate{Limit: 1 << 30, Window: time.Hour}
	lc.IP, lc.Account, lc.Device, lc.Request = wide, wide, wide, wide
	lc.Devices = access.NewEphemeralSealer()
	lim, err := ratelimit.New(ctx, lc)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = lim.Shutdown(context.Background()) })
	svc, err := account.New(ctx, account.Config{
		Store:         store,
		Policy:        account.RolePolicy{},
		Authenticator: authn,
		Hasher:        h,
		Limiter:       lim,
		Rules:         func() account.Rules { return account.Rules{} },
		Logger:        slog.New(slog.DiscardHandler),
		FailureKey:    make([]byte, 32),
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = svc.Shutdown(context.Background()) })
	return svc, hash
}

// BenchmarkCountCommit is one burst of concurrent wrong-password
// PasswordLogin attempts per iteration against a fresh user, so no burst
// reaches the failure cap. It reports each attempt's mean wall time, the
// share of it spent inside Update (a failed attempt's one Update is its
// count commit), that time itself and the durable commits per attempt.
func BenchmarkCountCommit(b *testing.B) {
	for _, callers := range []int{1, 16} {
		b.Run(fmt.Sprintf("%d_concurrent", callers), func(b *testing.B) {
			s, a := benchAccounts(b)
			timed := &timedAccounts{AccountStore: a}
			svc, hash := countService(b, timed)
			ctx := b.Context()
			var spent atomic.Int64
			var inUpdate time.Duration
			var commits uint64
			attempts := 0
			for b.Loop() {
				b.StopTimer()
				name := fmt.Sprintf("user%d", attempts)
				if err := a.CreateUser(ctx, &access.User{Username: name, Role: access.RoleUser, Enabled: true, PasswordHash: hash, CreatedAt: time.Now()}); err != nil {
					b.Fatal(err)
				}
				fromCommits, fromNanos := benchCommits(b, s), timed.nanos.Load()
				b.StartTimer()
				var wg sync.WaitGroup
				for i := range callers {
					ip := netip.AddrFrom4([4]byte{10, byte(attempts >> 8), byte(attempts), byte(i)})
					wg.Go(func() {
						start := time.Now()
						_, err := svc.PasswordLogin(ctx, account.Client{UserAgent: "bench", IP: ip, Peer: ip},
							account.Credentials{Username: name, Password: access.NewSecret("wrong password")})
						spent.Add(int64(time.Since(start)))
						if !errors.Is(err, account.ErrInvalidCredentials) {
							b.Errorf("PasswordLogin = %v, want ErrInvalidCredentials", err)
						}
					})
				}
				wg.Wait()
				b.StopTimer()
				inUpdate += time.Duration(timed.nanos.Load() - fromNanos)
				commits += benchCommits(b, s) - fromCommits
				attempts += callers
				b.StartTimer()
			}
			b.ReportMetric(float64(spent.Load())/float64(attempts)/float64(time.Millisecond), "ms/attempt")
			b.ReportMetric(float64(inUpdate)/float64(spent.Load()), "commit-share")
			b.ReportMetric(float64(inUpdate)/float64(attempts)/float64(time.Millisecond), "commit-ms/attempt")
			b.ReportMetric(float64(commits)/float64(attempts), "commits/attempt")
		})
	}
}

// boundUsers is the user count rule 15's bound is taken at, the 1,000
// access's BenchmarkPruneAtBound uses.
const boundUsers = 1000

func failureName(name string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(name))) }

// failuresAtBound is a store at rule 15's bound for boundUsers users: two
// records of each user's name and 2 x account.MaxUntrustedCounts records of
// names that are no user's. It returns the users' failure names, which a
// prune keeps.
func failuresAtBound(b *testing.B) (*AccountStore, []string) {
	b.Helper()
	_, a := benchAccounts(b)
	ctx := b.Context()
	keep := make([]string, boundUsers)
	for i := range keep {
		keep[i] = failureName(fmt.Sprintf("user%d", i))
	}
	if err := a.Update(ctx, func(tx account.Tx) error {
		for i := range boundUsers {
			name := fmt.Sprintf("user%d", i)
			if err := tx.CreateUser(ctx, &access.User{Username: name, Role: access.RoleUser, Enabled: true, CreatedAt: time.Unix(1, 0).UTC()}); err != nil {
				return err
			}
			for _, trusted := range []bool{false, true} {
				ref := account.FailureRef{Name: keep[i], Method: access.MethodPassword, Trusted: trusted}
				if err := tx.PutAuthFailure(ctx, account.FailureCount{FailureRef: ref, Count: 1}); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	fillOthers(b, a)
	return a, keep
}

func fillOthers(b *testing.B, a *AccountStore) {
	b.Helper()
	ctx := b.Context()
	if err := a.Update(ctx, func(tx account.Tx) error {
		for i := range 2 * account.MaxUntrustedCounts {
			ref := account.FailureRef{Name: failureName(fmt.Sprintf("other%d", i)), Method: access.MethodPassword}
			if err := tx.PutAuthFailure(ctx, account.FailureCount{FailureRef: ref, Count: 1}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkPublishFailures is one count commit, an Update holding one
// PutAuthFailure, while the store holds rule 15's bound of failure records.
func BenchmarkPublishFailures(b *testing.B) {
	a, keep := failuresAtBound(b)
	ctx := b.Context()
	ref := account.FailureRef{Name: keep[0], Method: access.MethodPassword}
	n := 1
	for b.Loop() {
		n++
		if err := a.Update(ctx, func(tx account.Tx) error {
			return tx.PutAuthFailure(ctx, account.FailureCount{FailureRef: ref, Count: n})
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPruneFailures is account's failure-record sweep as one Update
// (ListUsers, UnreportedLocks, PruneAuthFailures of every name that is no
// user's) over rule 15's bound.
func BenchmarkPruneFailures(b *testing.B) {
	a, keep := failuresAtBound(b)
	ctx := b.Context()
	refill := false
	for b.Loop() {
		if refill {
			b.StopTimer()
			fillOthers(b, a)
			b.StartTimer()
		}
		refill = true
		var removed int64
		if err := a.Update(ctx, func(tx account.Tx) error {
			if _, err := tx.ListUsers(ctx); err != nil {
				return err
			}
			if _, err := tx.UnreportedLocks(ctx); err != nil {
				return err
			}
			n, err := tx.PruneAuthFailures(ctx, keep)
			removed = n
			return err
		}); err != nil {
			b.Fatal(err)
		}
		if removed != 2*account.MaxUntrustedCounts {
			b.Fatalf("pruned %d records, want %d", removed, 2*account.MaxUntrustedCounts)
		}
	}
}

// BenchmarkSignInBurst is one burst per iteration of the stamps 16
// concurrent sign-ins write: a session's activity, a key's use, a passkey's
// sign count and a failure count each. Every one is silent, so the burst
// must reserve no revision (AC1).
func BenchmarkSignInBurst(b *testing.B) {
	const signIns = 16
	s, a := benchAccounts(b)
	ctx := b.Context()
	type signIn struct {
		session access.Hash
		cred    []byte
		key     int64
		user    int64
	}
	var ins []signIn
	at := time.Unix(1, 0).UTC()
	for i := range signIns {
		u := &access.User{Username: fmt.Sprintf("user%d", i), Role: access.RoleUser, Enabled: true, CreatedAt: at}
		if err := a.CreateUser(ctx, u); err != nil {
			b.Fatal(err)
		}
		in := signIn{session: access.NewHash(fmt.Sprintf("session-%d", i)), cred: fmt.Appendf(nil, "cred-%d", i), user: u.ID}
		sess := &access.Session{CreatedAt: at, LastActivity: at, AuthTime: at, ID: fmt.Sprintf("s%d", i), TokenHash: in.session, AuthMethod: access.MethodPassword, UserID: u.ID}
		key := &access.Key{CreatedAt: at, KeyHash: access.NewHash(fmt.Sprintf("key-%d", i)), UserID: u.ID}
		err := errors.Join(a.CreateSession(ctx, sess), a.CreateAPIKey(ctx, key),
			a.CreatePasskey(ctx, &access.PasskeyCredential{CreatedAt: at, UserID: u.ID, CredentialID: in.cred, RawFlags: 1}))
		if err != nil {
			b.Fatal(err)
		}
		in.key = key.ID
		ins = append(ins, in)
	}
	head := headRev(b, s)
	n := 0
	for b.Loop() {
		n++
		stamp := at.Add(time.Duration(n) * time.Second)
		var wg sync.WaitGroup
		for i, in := range ins {
			wg.Go(func() {
				err := errors.Join(
					a.UpdateSessionActivity(ctx, in.session, access.SessionActivity{At: stamp, IP: "192.0.2.1"}),
					a.TouchAPIKey(ctx, in.key, stamp),
					a.UpdatePasskeyAfterLogin(ctx, in.user, in.cred, access.PasskeyLoginUpdate{At: stamp, SignCount: uint32(n), RawFlags: 5}),
					a.PutAuthFailure(ctx, account.FailureCount{Name: failureName(fmt.Sprintf("user%d", i)), Method: access.MethodPassword, Count: n}),
				)
				if err != nil {
					b.Error(err)
				}
			})
		}
		wg.Wait()
	}
	if rev := headRev(b, s); rev != head {
		b.Errorf("%d bursts moved the head from %d to %d", n, head, rev)
	}
}
