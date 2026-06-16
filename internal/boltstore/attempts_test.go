package boltstore

import (
	"context"
	"math"
	"sort"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"pgregory.net/rapid"

	"github.com/cplieger/subflux/internal/api"
	boltkv "github.com/cplieger/subflux/internal/store/kv"
)

// This file covers the task-3.1 backoff behaviour: next_retry computation from
// BackoffParams (Requirement 2.1), the threshold-OR-future-retry rule with the
// max-attempts<=0 retry-only path (Requirement 2.2), and no-row-means-eligible
// (Requirement 2.4).

const (
	testMT   = api.MediaTypeEpisode
	testMID  = "tt0903747-s01e01"
	testLang = "en"
)

var testProv = api.ProviderNameOpenSubtitles

// readAttempt reads a single search_attempts row back via a View transaction.
// found is false when no row exists for the key.
func readAttempt(t *testing.T, db *DB, mt api.MediaType, mid, lang string, p api.ProviderID) (rec attemptRec, found bool) {
	t.Helper()
	err := db.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(bucketSearchAttempts)).Get(attemptKey(mt, mid, lang, p))
		if raw == nil {
			return nil
		}
		found = true
		return boltkv.Decode(raw, &rec)
	})
	if err != nil {
		t.Fatalf("readAttempt: %v", err)
	}
	return rec, found
}

// dueIndexLen counts the entries in ix_attempts_due (used to assert the due
// index is maintained alongside the primary write).
func dueIndexLen(t *testing.T, db *DB) int {
	t.Helper()
	var n int
	if err := db.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketIxAttemptsDue)).ForEach(func(_, _ []byte) error {
			n++
			return nil
		})
	}); err != nil {
		t.Fatalf("dueIndexLen: %v", err)
	}
	return n
}

func defaultParams() api.BackoffParams {
	return api.BackoffParams{
		InitialDelay: 5 * time.Minute,
		MaxDelay:     6 * time.Hour,
		Multiplier:   2.0,
	}
}

// TestRecordNoResult_newRow asserts a first no-result inserts a row with
// failures=1, next_retry = now + InitialDelay, maintains ix_attempts_due, and
// bumps the attempts counter (Requirement 2.1).
func TestRecordNoResult_newRow(t *testing.T) {
	db, _ := openTemp(t)
	bp := defaultParams()

	before := time.Now()
	if err := db.RecordNoResult(context.Background(), testMT, testMID, testLang, testProv, bp); err != nil {
		t.Fatalf("RecordNoResult: %v", err)
	}

	rec, found := readAttempt(t, db, testMT, testMID, testLang, testProv)
	if !found {
		t.Fatal("expected a search_attempts row after RecordNoResult")
	}
	if rec.Failures != 1 {
		t.Errorf("failures = %d, want 1", rec.Failures)
	}
	if rec.LastTried.Before(before) {
		t.Errorf("last_tried %v is before call start %v", rec.LastTried, before)
	}
	// next_retry - last_tried must equal InitialDelay exactly (both derive from
	// the same internal now on the insert path).
	if got := rec.NextRetry.Sub(rec.LastTried); got != bp.InitialDelay {
		t.Errorf("next_retry - last_tried = %v, want InitialDelay %v", got, bp.InitialDelay)
	}
	if n := dueIndexLen(t, db); n != 1 {
		t.Errorf("ix_attempts_due entries = %d, want 1", n)
	}

	var attempts int64
	_ = db.db.View(func(tx *bolt.Tx) error { attempts = readAttemptCount(tx); return nil })
	if attempts != 1 {
		t.Errorf("attempts counter = %d, want 1", attempts)
	}
}

// TestRecordNoResult_incrementsExponential asserts repeated no-results
// increment failures and grow next_retry by the exponential formula until the
// max-delay clamp (Requirement 2.1).
func TestRecordNoResult_incrementsExponential(t *testing.T) {
	db, _ := openTemp(t)
	bp := api.BackoffParams{InitialDelay: 60 * time.Second, MaxDelay: 600 * time.Second, Multiplier: 2.0}

	// Expected delay (= next_retry - last_tried) for the k-th call:
	//   k==1 -> InitialDelay
	//   k>=2 -> min(MaxDelay, InitialDelay*mult^(k-1)) truncated to whole seconds
	want := []time.Duration{
		60 * time.Second,  // insert
		120 * time.Second, // 60*2^1
		240 * time.Second, // 60*2^2
		480 * time.Second, // 60*2^3
		600 * time.Second, // 60*2^4=960 clamped to 600
		600 * time.Second, // clamped
	}
	for k := 1; k <= len(want); k++ {
		if err := db.RecordNoResult(context.Background(), testMT, testMID, testLang, testProv, bp); err != nil {
			t.Fatalf("RecordNoResult call %d: %v", k, err)
		}
		rec, found := readAttempt(t, db, testMT, testMID, testLang, testProv)
		if !found {
			t.Fatalf("call %d: row missing", k)
		}
		if rec.Failures != k {
			t.Errorf("call %d: failures = %d, want %d", k, rec.Failures, k)
		}
		if got := rec.NextRetry.Sub(rec.LastTried); got != want[k-1] {
			t.Errorf("call %d: next_retry-last_tried = %v, want %v", k, got, want[k-1])
		}
	}
	// All increments stay on one row, so the due index and counter stay at 1.
	if n := dueIndexLen(t, db); n != 1 {
		t.Errorf("ix_attempts_due entries = %d, want 1", n)
	}
}

// TestRecordNoResult_nextRetryFormula is a property test: after k successive
// no-results, the stored failures equal k and (next_retry - last_tried) equals
// the backoff formula's delay for the final call, including the integer-second
// truncation the old SQLite upsert applied (Requirement 2.1).
func TestRecordNoResult_nextRetryFormula(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db := openPropDB(rt)

		initialSec := rapid.IntRange(1, 120).Draw(rt, "initialSec")
		maxSec := rapid.IntRange(1, 7200).Draw(rt, "maxSec")
		mult := rapid.SampledFrom([]float64{1.0, 1.5, 2.0, 3.0}).Draw(rt, "multiplier")
		bp := api.BackoffParams{
			InitialDelay: time.Duration(initialSec) * time.Second,
			MaxDelay:     time.Duration(maxSec) * time.Second,
			Multiplier:   mult,
		}
		k := rapid.IntRange(1, 6).Draw(rt, "calls")

		for i := 0; i < k; i++ {
			if err := db.RecordNoResult(context.Background(), testMT, testMID, testLang, testProv, bp); err != nil {
				rt.Fatalf("RecordNoResult: %v", err)
			}
		}

		var rec attemptRec
		found := false
		if err := db.db.View(func(tx *bolt.Tx) error {
			raw := tx.Bucket([]byte(bucketSearchAttempts)).Get(attemptKey(testMT, testMID, testLang, testProv))
			if raw == nil {
				return nil
			}
			found = true
			return boltkv.Decode(raw, &rec)
		}); err != nil {
			rt.Fatalf("read row: %v", err)
		}
		if !found {
			rt.Fatal("row missing after RecordNoResult")
		}
		if rec.Failures != k {
			rt.Fatalf("failures = %d, want %d", rec.Failures, k)
		}

		var wantDelay time.Duration
		if k == 1 {
			wantDelay = bp.InitialDelay // insert path: full duration
		} else {
			delaySec := bp.InitialDelay.Seconds() * math.Pow(bp.Multiplier, float64(k-1))
			if ms := bp.MaxDelay.Seconds(); delaySec > ms {
				delaySec = ms
			}
			wantDelay = time.Duration(int64(delaySec)) * time.Second
		}
		if got := rec.NextRetry.Sub(rec.LastTried); got != wantDelay {
			rt.Fatalf("next_retry-last_tried = %v, want %v (k=%d, bp=%+v)", got, wantDelay, k, bp)
		}
	})
}

// TestBackedOffProviders_noRowEligible asserts a triple with no recorded
// attempts reports no backed-off providers (Requirement 2.4).
func TestBackedOffProviders_noRowEligible(t *testing.T) {
	db, _ := openTemp(t)
	got, err := db.BackedOffProviders(context.Background(), testMT, testMID, testLang, 3)
	if err != nil {
		t.Fatalf("BackedOffProviders: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none (no row means eligible)", got)
	}
}

// TestBackedOffProviders_futureRetry asserts a provider whose next_retry is in
// the future is backed off even when its failure count is below the threshold
// (Requirement 2.2).
func TestBackedOffProviders_futureRetry(t *testing.T) {
	db, _ := openTemp(t)
	// A fresh no-result sets next_retry well into the future (InitialDelay 5m).
	if err := db.RecordNoResult(context.Background(), testMT, testMID, testLang, testProv, defaultParams()); err != nil {
		t.Fatalf("RecordNoResult: %v", err)
	}
	got, err := db.BackedOffProviders(context.Background(), testMT, testMID, testLang, 100)
	if err != nil {
		t.Fatalf("BackedOffProviders: %v", err)
	}
	if len(got) != 1 || got[0] != testProv {
		t.Errorf("got %v, want [%s] (future next_retry)", got, testProv)
	}
}

// TestBackedOffProviders_thresholdReached asserts a provider whose failures
// reach maxAttempts is backed off even when next_retry is already in the past
// (Requirement 2.2).
func TestBackedOffProviders_thresholdReached(t *testing.T) {
	db, _ := openTemp(t)
	// Insert a row directly with a past next_retry and a high failure count.
	putAttemptRow(t, db, testMT, testMID, testLang, testProv, attemptRec{
		LastTried: time.Now().Add(-2 * time.Hour),
		NextRetry: time.Now().Add(-time.Hour), // past: retry check alone would NOT back off
		Failures:  5,
	})

	// maxAttempts=5, failures=5 -> threshold reached, backed off.
	got, err := db.BackedOffProviders(context.Background(), testMT, testMID, testLang, 5)
	if err != nil {
		t.Fatalf("BackedOffProviders: %v", err)
	}
	if len(got) != 1 || got[0] != testProv {
		t.Errorf("got %v, want [%s] (threshold reached)", got, testProv)
	}

	// maxAttempts=6 -> below threshold AND past retry -> eligible.
	got, err = db.BackedOffProviders(context.Background(), testMT, testMID, testLang, 6)
	if err != nil {
		t.Fatalf("BackedOffProviders: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none (failures below threshold, retry in past)", got)
	}
}

// TestBackedOffProviders_maxAttemptsZeroRetryOnly asserts that when maxAttempts
// is zero or negative the threshold check is disabled and only the next_retry
// check applies (Requirement 2.2).
func TestBackedOffProviders_maxAttemptsZeroRetryOnly(t *testing.T) {
	db, _ := openTemp(t)
	// High failure count but next_retry already in the past.
	putAttemptRow(t, db, testMT, testMID, testLang, testProv, attemptRec{
		LastTried: time.Now().Add(-2 * time.Hour),
		NextRetry: time.Now().Add(-time.Hour),
		Failures:  99,
	})

	for _, maxAttempts := range []int{0, -1} {
		got, err := db.BackedOffProviders(context.Background(), testMT, testMID, testLang, maxAttempts)
		if err != nil {
			t.Fatalf("BackedOffProviders(maxAttempts=%d): %v", maxAttempts, err)
		}
		if len(got) != 0 {
			t.Errorf("maxAttempts=%d: got %v, want none (retry-only check, retry in past)", maxAttempts, got)
		}
	}

	// Same row but with a future next_retry -> backed off under retry-only.
	putAttemptRow(t, db, testMT, testMID, testLang, testProv, attemptRec{
		LastTried: time.Now(),
		NextRetry: time.Now().Add(time.Hour),
		Failures:  99,
	})
	got, err := db.BackedOffProviders(context.Background(), testMT, testMID, testLang, 0)
	if err != nil {
		t.Fatalf("BackedOffProviders: %v", err)
	}
	if len(got) != 1 || got[0] != testProv {
		t.Errorf("got %v, want [%s] (future retry, retry-only)", got, testProv)
	}
}

// TestBackedOffProviders_prefixIsolation asserts the triple scan does not bleed
// across media-id or language component boundaries (Requirement 8.3 applied to
// the backoff scan): a row for "tt1" must not surface when querying "tt12", and
// a row under language "fr" must not surface when querying "f".
func TestBackedOffProviders_prefixIsolation(t *testing.T) {
	db, _ := openTemp(t)
	future := time.Now().Add(time.Hour)
	putAttemptRow(t, db, api.MediaTypeMovie, "tt1", "en", testProv, attemptRec{NextRetry: future, Failures: 1})
	putAttemptRow(t, db, api.MediaTypeMovie, "tt12", "en", testProv, attemptRec{NextRetry: future, Failures: 1})
	putAttemptRow(t, db, api.MediaTypeMovie, "tt1", "fr", testProv, attemptRec{NextRetry: future, Failures: 1})

	// Querying tt1/en must return only the tt1/en row, not tt12/en.
	got, err := db.BackedOffProviders(context.Background(), api.MediaTypeMovie, "tt1", "en", 0)
	if err != nil {
		t.Fatalf("BackedOffProviders: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("tt1/en: got %v, want exactly one provider (no tt12 bleed)", got)
	}

	// Querying language "f" must NOT match the "fr" row.
	got, err = db.BackedOffProviders(context.Background(), api.MediaTypeMovie, "tt1", "f", 0)
	if err != nil {
		t.Fatalf("BackedOffProviders: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tt1/f: got %v, want none (no 'fr' language bleed)", got)
	}
}

// providerSetPool is a small pool of distinct providers for the property test.
var providerSetPool = []api.ProviderID{
	api.ProviderNameOpenSubtitles,
	api.ProviderNameGestdown,
	api.ProviderNameSubDL,
}

// TestBackedOffProviders_predicate is a property test asserting the returned
// set equals the reference predicate (threshold OR future-retry, retry-only
// when maxAttempts<=0) over a randomly populated triple, including the no-row
// eligibility (Requirements 2.2, 2.4).
func TestBackedOffProviders_predicate(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db := openPropDB(rt)
		now := time.Now()

		type spec struct {
			failures int
			future   bool
			present  bool
		}
		specs := map[api.ProviderID]spec{}
		for _, p := range providerSetPool {
			present := rapid.Bool().Draw(rt, "present-"+string(p))
			s := spec{present: present}
			if present {
				s.failures = rapid.IntRange(0, 6).Draw(rt, "failures-"+string(p))
				s.future = rapid.Bool().Draw(rt, "future-"+string(p))
				nextRetry := now.Add(time.Hour)
				if !s.future {
					nextRetry = now.Add(-time.Hour)
				}
				putAttemptRow(t, db, testMT, testMID, testLang, p, attemptRec{
					LastTried: now,
					NextRetry: nextRetry,
					Failures:  s.failures,
				})
			}
			specs[p] = s
		}

		rawMax := rapid.IntRange(-2, 6).Draw(rt, "maxAttempts")
		got, err := db.BackedOffProviders(context.Background(), testMT, testMID, testLang, rawMax)
		if err != nil {
			rt.Fatalf("BackedOffProviders: %v", err)
		}

		// Reference predicate, mirroring the documented semantics.
		maxAttempts := rawMax
		if maxAttempts < 0 {
			maxAttempts = 0
		}
		var want []api.ProviderID
		for _, p := range providerSetPool {
			s := specs[p]
			if !s.present {
				continue // no row means eligible
			}
			backed := (maxAttempts > 0 && s.failures >= maxAttempts) || s.future
			if backed {
				want = append(want, p)
			}
		}

		if !sameProviderSet(got, want) {
			rt.Fatalf("BackedOffProviders(maxAttempts=%d) = %v, want %v (specs=%v)", rawMax, got, want, specs)
		}
	})
}

// putAttemptRow inserts a search_attempts row directly through the index
// chokepoint so tests can control failures/next_retry without depending on the
// internal clock.
func putAttemptRow(t *testing.T, db *DB, mt api.MediaType, mid, lang string, p api.ProviderID, rec attemptRec) {
	t.Helper()
	if err := db.db.Update(func(tx *bolt.Tx) error {
		return putAttempt(tx, mt, mid, lang, p, &rec)
	}); err != nil {
		t.Fatalf("putAttemptRow: %v", err)
	}
}

// sameProviderSet reports whether a and b contain the same providers regardless
// of order.
func sameProviderSet(a, b []api.ProviderID) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]api.ProviderID(nil), a...)
	bs := append([]api.ProviderID(nil), b...)
	sort.Slice(as, func(i, j int) bool { return as[i] < as[j] })
	sort.Slice(bs, func(i, j int) bool { return bs[i] < bs[j] })
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}
