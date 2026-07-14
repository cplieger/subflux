// Package storetest provides a shared, engine-agnostic behavioral contract
// suite for api.Store implementations. It depends ONLY on internal/api, so it
// can be run against any concrete engine (the legacy SQLite store.DB and the
// bbolt boltstore.DB) without importing either, proving behavioral parity at
// the api.Store seam (Requirement 14.1).
//
// # What this suite is
//
// Suite asserts EXACT observable values, not merely "does not error". The
// substantive behavioural expectations were promoted here from the formerly
// SQLite-coupled store_contract_test.go and store_maint_reconcile_test.go so a
// single suite pins every resolved finding from the bbolt-store-rewrite design
// review (Requirement 14.2):
//
//   - the three ReconcileState branches (video gone; subtitle gone with a
//     sibling still present; all subtitles gone),
//   - non-destructive ClearManualLock (rows preserved, lock cleared),
//   - backoff cleared on SaveDownload,
//   - DeleteStateByPaths orphan cleanup (rows + backoff + coverage),
//   - GetState filter / search / limit / offset,
//   - both CleanupDrift branches (removed languages/providers, Requirement 7.7;
//     adaptive disabled, Requirement 7.8).
//
// Because it asserts exact values it requires a REAL persisting store: a no-op
// fake (testsupport.NopStore) that discards writes cannot satisfy it, and the
// boltstore package proves the suite catches a regression by running one
// promoted invariant (AssertClearManualLockNonDestructive) against a
// deliberately broken stub and asserting the assertion fails.
//
// # Filesystem dependency
//
// ReconcileState classifies each row against the real filesystem (both engines
// default their stat oracle to os.Stat), so the reconcile cases create real
// video/subtitle files under t.TempDir() and reference those paths. This keeps
// the suite engine-agnostic: it drives reconcile entirely through SaveDownload
// + RecordNoResult + RecordScanState and real files, with no engine-specific
// stat injection.
package storetest

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

const (
	langEng = "eng"
	langFra = "fra"
	provOS  = api.ProviderID("opensubtitles")

	codecSubrip = "subrip"
)

// TB is the subset of *testing.T the reusable behavioural assertions need. Real
// callers pass *testing.T; the broken-stub self-check (in the boltstore
// package) passes a recorder that captures whether a failure was reported,
// proving the suite catches a non-conforming store without failing the parent
// test. *testing.T satisfies TB.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Suite runs the engine-agnostic behavioral contract against any api.Store
// produced by newStore. Each promoted finding is a NAMED subtest so a failure
// names the exact behaviour that regressed.
func Suite(t *testing.T, newStore func(t *testing.T) api.Store) {
	t.Helper()

	t.Run("RecordNoResult_then_BackedOff_eligibility", func(t *testing.T) {
		t.Parallel()
		testBackoffEligibility(t, newStore(t))
	})

	t.Run("SaveDownload_auto_CurrentScore_DownloadedRefs", func(t *testing.T) {
		t.Parallel()
		testSaveAutoRoundtrip(t, newStore(t))
	})

	t.Run("SaveDownload_auto_upsert_preserves_media_imported", func(t *testing.T) {
		t.Parallel()
		testAutoUpsertPreservesImported(t, newStore(t))
	})

	t.Run("SaveDownload_clears_backoff", func(t *testing.T) {
		t.Parallel()
		testSaveClearsBackoff(t, newStore(t))
	})

	t.Run("ClearManualLock_nondestructive", func(t *testing.T) {
		t.Parallel()
		AssertClearManualLockNonDestructive(t, newStore(t))
	})

	t.Run("ManualLock_count_paths_next_number", func(t *testing.T) {
		t.Parallel()
		testManualOrdinals(t, newStore(t))
	})

	t.Run("Variant_dimension_independent_state_locks_ordinals", func(t *testing.T) {
		t.Parallel()
		testVariantIndependence(t, newStore(t))
	})

	t.Run("PollTimestamp_roundtrip_and_absent_zero", func(t *testing.T) {
		t.Parallel()
		testPollTimestamp(t, newStore(t))
	})

	t.Run("SyncOffset_roundtrip", func(t *testing.T) {
		t.Parallel()
		testSyncOffset(t, newStore(t))
	})

	t.Run("Coverage_files_roundtrip_and_diff", func(t *testing.T) {
		t.Parallel()
		testCoverageFiles(t, newStore(t))
	})

	t.Run("ScanState_roundtrip_and_RecentlyScanned", func(t *testing.T) {
		t.Parallel()
		testScanState(t, newStore(t))
	})

	t.Run("GetState_filter_search_limit_offset", func(t *testing.T) {
		t.Parallel()
		testGetStateQuery(t, newStore(t))
	})

	t.Run("DeleteStateByPaths_orphan_cleanup", func(t *testing.T) {
		t.Parallel()
		testDeleteStateByPaths(t, newStore(t))
	})

	t.Run("CleanupDrift_removed_languages_and_providers", func(t *testing.T) {
		t.Parallel()
		testCleanupDriftRemoved(t, newStore(t))
	})

	t.Run("CleanupDrift_adaptive_disabled_clears_all", func(t *testing.T) {
		t.Parallel()
		testCleanupDriftAdaptiveDisabled(t, newStore(t))
	})

	t.Run("Reconcile_video_gone_deletes_row_orphans_backoff_scanstate", func(t *testing.T) {
		t.Parallel()
		testReconcileVideoGone(t, newStore(t))
	})

	t.Run("Reconcile_subtitle_gone_sibling_present_preserves_lock", func(t *testing.T) {
		t.Parallel()
		testReconcileSubGoneSiblingPresent(t, newStore(t))
	})

	t.Run("Reconcile_all_subtitles_gone_resets_auto_deletes_manual_clears_backoff", func(t *testing.T) {
		t.Parallel()
		testReconcileAllSubsGone(t, newStore(t))
	})
}

// defaultBackoff is the backoff window used by the backoff cases. The 1h
// initial delay guarantees a freshly recorded attempt's next_retry is in the
// future, so a recorded provider is observably backed off regardless of the
// max-attempts threshold.
func defaultBackoff() api.BackoffParams {
	return api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}
}

// writeFile creates a real file so ReconcileState's os.Stat oracle classifies
// the path as present. Used by the reconcile cases to stand up the on-disk
// state both engines inspect.
func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

// mustSaveDownload saves rec via s.SaveDownload or fails the test, labelling the
// failure with name so a fatal during a multi-step reconcile setup identifies
// which save failed. It uses context.Background() (every caller's ctx).
func mustSaveDownload(t *testing.T, s api.Store, name string, rec *api.DownloadRecord) {
	t.Helper()
	if err := s.SaveDownload(context.Background(), rec); err != nil {
		t.Fatalf("SaveDownload(%s): %v", name, err)
	}
}

// testBackoffEligibility asserts the no-row-means-eligible rule and that a
// recorded attempt becomes backed off (Requirements 2.1, 2.2, 2.4).
func testBackoffEligibility(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()

	// No row recorded yet: the provider is immediately eligible.
	backed, err := s.BackedOffProviders(ctx, api.MediaTypeMovie, "tt-bo-1", langEng, 5)
	if err != nil {
		t.Fatalf("BackedOffProviders (fresh): %v", err)
	}
	if len(backed) != 0 {
		t.Fatalf("BackedOffProviders (fresh) = %v, want empty (no row means eligible)", backed)
	}

	if rerr := s.RecordNoResult(ctx, api.MediaTypeMovie, "tt-bo-1", langEng, provOS, defaultBackoff()); rerr != nil {
		t.Fatalf("RecordNoResult: %v", rerr)
	}

	// next_retry is in the future, so the provider is backed off under the
	// threshold path (maxAttempts=5) and the retry-only path (maxAttempts=0).
	for _, maxAttempts := range []int{5, 0} {
		backed, err = s.BackedOffProviders(ctx, api.MediaTypeMovie, "tt-bo-1", langEng, maxAttempts)
		if err != nil {
			t.Fatalf("BackedOffProviders(max=%d): %v", maxAttempts, err)
		}
		if len(backed) != 1 || backed[0] != provOS {
			t.Fatalf("BackedOffProviders(max=%d) = %v, want [%s]", maxAttempts, backed, provOS)
		}
	}

	// A different, unrecorded provider for the same triple stays eligible.
	if got := containsProvider(backed, api.ProviderID("other")); got {
		t.Fatalf("unrecorded provider reported backed off: %v", backed)
	}
}

func containsProvider(list []api.ProviderID, p api.ProviderID) bool {
	return slices.Contains(list, p)
}

// testSaveAutoRoundtrip asserts an auto download is retrievable via CurrentScore
// (exact score + found) and DownloadedRefs (Requirements 3.4, 3.5).
func testSaveAutoRoundtrip(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	rec := &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt-rt-1",
		Language: langEng, ProviderName: provOS,
		ReleaseName: "Movie.2024.eng.srt", Score: 85,
		Path: "/media/rt/movie.eng.srt",
	}
	if err := s.SaveDownload(ctx, rec); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	score, _, found, err := s.CurrentScore(ctx, api.MediaTypeMovie, "tt-rt-1", langEng, api.VariantStandard)
	if err != nil {
		t.Fatalf("CurrentScore: %v", err)
	}
	if !found || score != 85 {
		t.Fatalf("CurrentScore = (score=%d, found=%v), want (85, true)", score, found)
	}

	refs, err := s.DownloadedRefs(ctx, api.MediaTypeMovie, "tt-rt-1", langEng)
	if err != nil {
		t.Fatalf("DownloadedRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("DownloadedRefs = %d entries, want 1", len(refs))
	}
	if refs[0].ReleaseName != "Movie.2024.eng.srt" || refs[0].Provider != provOS {
		t.Fatalf("DownloadedRefs[0] = %+v, want {ReleaseName:Movie.2024.eng.srt Provider:%s}", refs[0], provOS)
	}

	// An auto-only triple is not manually locked.
	locked, err := s.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt-rt-1", langEng, "")
	if err != nil {
		t.Fatalf("IsManuallyLocked: %v", err)
	}
	if locked {
		t.Fatalf("IsManuallyLocked = true for an auto-only triple, want false")
	}
}

// testAutoUpsertPreservesImported asserts saving a second auto download for a
// triple updates it in place AND preserves the original media_imported
// (Requirement 3.1).
func testAutoUpsertPreservesImported(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	mid := "tt-up-1"
	first := &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: mid, Language: langEng,
		ProviderName: provOS, ReleaseName: "First", Score: 50, Path: "/m/up.eng.srt",
	}
	if err := s.SaveDownload(ctx, first); err != nil {
		t.Fatalf("SaveDownload(first): %v", err)
	}
	_, imported1, found1, err := s.CurrentScore(ctx, api.MediaTypeMovie, mid, langEng, api.VariantStandard)
	if err != nil || !found1 {
		t.Fatalf("CurrentScore(first) = found=%v err=%v, want found", found1, err)
	}

	second := &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: mid, Language: langEng,
		ProviderName: provOS, ReleaseName: "Second", Score: 90, Path: "/m/up.eng.srt",
	}
	if serr := s.SaveDownload(ctx, second); serr != nil {
		t.Fatalf("SaveDownload(second): %v", serr)
	}

	score2, imported2, found2, err := s.CurrentScore(ctx, api.MediaTypeMovie, mid, langEng, api.VariantStandard)
	if err != nil || !found2 {
		t.Fatalf("CurrentScore(second) = found=%v err=%v, want found", found2, err)
	}
	if score2 != 90 {
		t.Fatalf("CurrentScore(second) score = %d, want 90 (upgraded in place)", score2)
	}
	if !imported2.Equal(imported1) {
		t.Fatalf("media_imported changed on upgrade: %v -> %v, want preserved", imported1, imported2)
	}

	// The upgrade is in place: still exactly one row for the triple.
	entries, err := s.GetState(ctx, &api.StateQuery{MediaType: api.MediaTypeMovie})
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("GetState = %d rows after auto upgrade, want 1 (updated in place)", len(entries))
	}
}

// testSaveClearsBackoff asserts SaveDownload clears the triple's backoff in the
// same operation (Requirement 3.3): success clears adaptive backoff.
func testSaveClearsBackoff(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	mid := "tt-cb-1"

	if err := s.RecordNoResult(ctx, api.MediaTypeMovie, mid, langEng, provOS, defaultBackoff()); err != nil {
		t.Fatalf("RecordNoResult: %v", err)
	}
	_, attempts, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats (before save): %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts before save = %d, want 1", attempts)
	}

	if serr := s.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: mid, Language: langEng,
		ProviderName: provOS, ReleaseName: "Got.It", Score: 70, Path: "/m/cb.eng.srt",
	}); serr != nil {
		t.Fatalf("SaveDownload: %v", serr)
	}

	downloads, attempts, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats (after save): %v", err)
	}
	if downloads != 1 {
		t.Fatalf("downloads after save = %d, want 1", downloads)
	}
	if attempts != 0 {
		t.Fatalf("attempts after save = %d, want 0 (backoff cleared on success)", attempts)
	}
}

// AssertClearManualLockNonDestructive asserts ClearManualLock is a NON-destructive
// flag flip: the lock is cleared but the rows are preserved and stay visible to
// GetState and DownloadedRefs (Requirement 4.3). It is exported and written
// against TB so the boltstore package can run it against a deliberately broken
// (destructive) stub and assert the assertion fails — proving the suite catches
// the regression.
func AssertClearManualLockNonDestructive(t TB, s api.Store) {
	t.Helper()
	ctx := context.Background()
	mid := "tt-cl-1"

	if err := s.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: mid, Language: langEng,
		ProviderName: provOS, ReleaseName: "Manual.Pick", Score: 100,
		Path: "/m/cl.fr.1.srt",
		Meta: &api.DownloadMeta{Manual: true},
	}); err != nil {
		t.Fatalf("SaveDownload(manual): %v", err)
	}

	locked, err := s.IsManuallyLocked(ctx, api.MediaTypeMovie, mid, langEng, api.VariantStandard)
	if err != nil {
		t.Fatalf("IsManuallyLocked (before clear): %v", err)
	}
	if !locked {
		t.Fatalf("IsManuallyLocked before clear = false, want true (manual row present)")
	}

	before, err := s.GetState(ctx, &api.StateQuery{MediaType: api.MediaTypeMovie})
	if err != nil {
		t.Fatalf("GetState (before clear): %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("GetState before clear = %d rows, want 1", len(before))
	}

	if cerr := s.ClearManualLock(ctx, api.MediaTypeMovie, mid, langEng, api.VariantStandard); cerr != nil {
		t.Fatalf("ClearManualLock: %v", cerr)
	}

	locked, err = s.IsManuallyLocked(ctx, api.MediaTypeMovie, mid, langEng, api.VariantStandard)
	if err != nil {
		t.Fatalf("IsManuallyLocked (after clear): %v", err)
	}
	if locked {
		t.Errorf("IsManuallyLocked after clear = true, want false (lock should be cleared)")
	}

	// Non-destructive: the row is preserved (now auto) and still visible.
	after, err := s.GetState(ctx, &api.StateQuery{MediaType: api.MediaTypeMovie})
	if err != nil {
		t.Fatalf("GetState (after clear): %v", err)
	}
	if len(after) != 1 {
		t.Errorf("GetState after clear = %d rows, want 1 (rows preserved, not deleted)", len(after))
	} else if after[0].Manual {
		t.Errorf("row after clear Manual = true, want false (flipped to auto)")
	}

	refs, err := s.DownloadedRefs(ctx, api.MediaTypeMovie, mid, langEng)
	if err != nil {
		t.Fatalf("DownloadedRefs (after clear): %v", err)
	}
	if len(refs) != 1 || refs[0].ReleaseName != "Manual.Pick" {
		t.Errorf("DownloadedRefs after clear = %+v, want one ref {Manual.Pick}", refs)
	}
}

// testManualOrdinals asserts manual count, the lock-list entry, and the next
// manual ordinal derived from the path (Requirements 4.4, 15.5, 15.6).
func testManualOrdinals(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	mid := "tt-mo-1"

	if n := s.NextManualNumber(ctx, api.MediaTypeMovie, mid, langFra, api.VariantStandard); n != 1 {
		t.Fatalf("NextManualNumber (empty) = %d, want 1", n)
	}

	if err := s.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: mid, Language: langFra,
		ProviderName: provOS, ReleaseName: "M1", Score: 100,
		Path: "/m/movie.fra.1.srt", Meta: &api.DownloadMeta{Manual: true},
	}); err != nil {
		t.Fatalf("SaveDownload(manual 1): %v", err)
	}

	count, err := s.ManualDownloadCount(ctx, api.MediaTypeMovie, mid, langFra, api.VariantStandard)
	if err != nil {
		t.Fatalf("ManualDownloadCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("ManualDownloadCount = %d, want 1", count)
	}

	paths, err := s.ManualSubtitlePaths(ctx, api.MediaTypeMovie, mid, langFra, api.VariantStandard)
	if err != nil {
		t.Fatalf("ManualSubtitlePaths: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/m/movie.fra.1.srt" {
		t.Fatalf("ManualSubtitlePaths = %v, want [/m/movie.fra.1.srt]", paths)
	}

	// The next ordinal is one greater than the highest stored ordinal (parsed
	// from the path), so a second manual download lands at .2. The manual
	// filename embeds the triple's language token (movie.<lang>.N.srt), which
	// both engines rely on to locate the ordinal.
	if n := s.NextManualNumber(ctx, api.MediaTypeMovie, mid, langFra, api.VariantStandard); n != 2 {
		t.Fatalf("NextManualNumber (after .1) = %d, want 2", n)
	}

	locks, err := s.GetManualLocks(ctx)
	if err != nil {
		t.Fatalf("GetManualLocks: %v", err)
	}
	if len(locks) != 1 || locks[0].MediaID != mid || locks[0].Language != langFra ||
		locks[0].Variant != api.VariantStandard || locks[0].Count != 1 {
		t.Fatalf("GetManualLocks = %+v, want one entry {%s, %s, standard, count=1}", locks, mid, langFra)
	}
}

// testVariantIndependence asserts the variant key dimension: state rows,
// scores, locks, and manual ordinals of one (media, language) are tracked
// independently per variant, an empty variant means any/all variants on the
// lock reads, and ClearManualLock("") clears every variant's lock. The three
// phases (row/score independence, lock scoping, lock listing + clear-all)
// share seeded state, so they run in order on one store.
func testVariantIndependence(t *testing.T, s api.Store) {
	t.Helper()
	mid := "tt-var-1"
	assertVariantRowIndependence(t, s, mid)
	assertVariantLockScoping(t, s, mid)
	assertVariantLockListAndClear(t, s, mid)
}

// assertVariantRowIndependence seeds one auto row per variant and asserts the
// rows and their scores stay independent per quad.
func assertVariantRowIndependence(t *testing.T, s api.Store, mid string) {
	t.Helper()
	ctx := context.Background()

	// One auto row per variant: same language, different variants.
	if err := s.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: mid, Language: langFra,
		Variant: api.VariantStandard, ProviderName: provOS,
		ReleaseName: "Std", Score: 85, Path: "/m/var.fra.srt",
	}); err != nil {
		t.Fatalf("SaveDownload(standard): %v", err)
	}
	if err := s.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: mid, Language: langFra,
		Variant: api.VariantForced, ProviderName: provOS,
		ReleaseName: "Frc", Score: 60, Path: "/m/var.fra.forced.srt",
	}); err != nil {
		t.Fatalf("SaveDownload(forced): %v", err)
	}

	// Two distinct rows: the forced save must not overwrite the standard row.
	entries, err := s.GetState(ctx, &api.StateQuery{MediaType: api.MediaTypeMovie})
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("GetState = %d rows after standard+forced saves, want 2 (independent quads)", len(entries))
	}
	variants := map[api.Variant]bool{}
	for i := range entries {
		variants[entries[i].Variant] = true
	}
	if !variants[api.VariantStandard] || !variants[api.VariantForced] {
		t.Fatalf("GetState variants = %v, want standard and forced exposed", variants)
	}

	assertVariantScores(t, s, mid)
}

// assertVariantScores asserts CurrentScore answers per quad after the
// standard(85)/forced(60) seeding, and reports not-found for the unseeded
// hi variant.
func assertVariantScores(t *testing.T, s api.Store, mid string) {
	t.Helper()
	ctx := context.Background()

	if score, _, found, serr := s.CurrentScore(ctx, api.MediaTypeMovie, mid, langFra, api.VariantStandard); serr != nil || !found || score != 85 {
		t.Fatalf("CurrentScore(standard) = (%d, %v, %v), want (85, true, nil)", score, found, serr)
	}
	if score, _, found, serr := s.CurrentScore(ctx, api.MediaTypeMovie, mid, langFra, api.VariantForced); serr != nil || !found || score != 60 {
		t.Fatalf("CurrentScore(forced) = (%d, %v, %v), want (60, true, nil)", score, found, serr)
	}
	if _, _, found, serr := s.CurrentScore(ctx, api.MediaTypeMovie, mid, langFra, api.VariantHI); serr != nil || found {
		t.Fatalf("CurrentScore(hi) found = %v (err %v), want false (no hi row)", found, serr)
	}
}

// assertVariantLockScoping saves a manual forced download and asserts the lock
// and the manual ordinal stay scoped to the forced quad.
func assertVariantLockScoping(t *testing.T, s api.Store, mid string) {
	t.Helper()
	ctx := context.Background()

	// A manual forced download locks ONLY the forced quad.
	if err := s.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: mid, Language: langFra,
		Variant: api.VariantForced, ProviderName: provOS,
		ReleaseName: "Frc.Manual", Score: 100, Path: "/m/var.fra.forced.1.srt",
		Meta: &api.DownloadMeta{Manual: true},
	}); err != nil {
		t.Fatalf("SaveDownload(manual forced): %v", err)
	}
	if locked, lerr := s.IsManuallyLocked(ctx, api.MediaTypeMovie, mid, langFra, api.VariantForced); lerr != nil || !locked {
		t.Fatalf("IsManuallyLocked(forced) = (%v, %v), want (true, nil)", locked, lerr)
	}
	if locked, lerr := s.IsManuallyLocked(ctx, api.MediaTypeMovie, mid, langFra, api.VariantStandard); lerr != nil || locked {
		t.Fatalf("IsManuallyLocked(standard) = (%v, %v), want (false, nil): forced lock must not leak", locked, lerr)
	}
	if locked, lerr := s.IsManuallyLocked(ctx, api.MediaTypeMovie, mid, langFra, ""); lerr != nil || !locked {
		t.Fatalf("IsManuallyLocked(any) = (%v, %v), want (true, nil)", locked, lerr)
	}

	// Manual ordinals advance per quad.
	if n := s.NextManualNumber(ctx, api.MediaTypeMovie, mid, langFra, api.VariantForced); n != 2 {
		t.Fatalf("NextManualNumber(forced) = %d, want 2 (one forced manual at .1)", n)
	}
	if n := s.NextManualNumber(ctx, api.MediaTypeMovie, mid, langFra, api.VariantStandard); n != 1 {
		t.Fatalf("NextManualNumber(standard) = %d, want 1 (forced ordinal must not leak)", n)
	}
}

// assertVariantLockListAndClear asserts the lock list carries the variant and
// that ClearManualLock("") clears every variant's lock for the language.
func assertVariantLockListAndClear(t *testing.T, s api.Store, mid string) {
	t.Helper()
	ctx := context.Background()

	locks, err := s.GetManualLocks(ctx)
	if err != nil {
		t.Fatalf("GetManualLocks: %v", err)
	}
	if len(locks) != 1 || locks[0].Variant != api.VariantForced || locks[0].Count != 1 {
		t.Fatalf("GetManualLocks = %+v, want one forced entry with count=1", locks)
	}

	// ClearManualLock("") clears every variant's lock for the language.
	if err := s.ClearManualLock(ctx, api.MediaTypeMovie, mid, langFra, ""); err != nil {
		t.Fatalf("ClearManualLock(all variants): %v", err)
	}
	if locked, lerr := s.IsManuallyLocked(ctx, api.MediaTypeMovie, mid, langFra, ""); lerr != nil || locked {
		t.Fatalf("IsManuallyLocked(any) after clear-all = (%v, %v), want (false, nil)", locked, lerr)
	}
}

// testPollTimestamp asserts a poll cursor round-trips and an absent key returns
// the zero time without error (Requirements 6.2, 6.3).
func testPollTimestamp(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()

	// Absent key: zero time, no error.
	got, err := s.GetPollTimestamp(ctx, api.PollKeyRadarr)
	if err != nil {
		t.Fatalf("GetPollTimestamp (absent): %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("GetPollTimestamp (absent) = %v, want zero time", got)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if serr := s.SetPollTimestamp(ctx, api.PollKeySonarr, now); serr != nil {
		t.Fatalf("SetPollTimestamp: %v", serr)
	}
	got, err = s.GetPollTimestamp(ctx, api.PollKeySonarr)
	if err != nil {
		t.Fatalf("GetPollTimestamp: %v", err)
	}
	if !got.Equal(now) {
		t.Fatalf("GetPollTimestamp = %v, want %v", got, now)
	}
}

// testSyncOffset asserts a sync offset round-trips by path (Requirement 6.1).
//
// The offset is associated with a known subtitle file: the legacy SQLite store
// carries offset_ms ON the subtitle_files row (a SetSyncOffset for an unknown
// path is a no-op there), whereas the bbolt store keeps an independent
// sync_offsets bucket keyed by path. Recording the subtitle file first is the
// behaviour-preserving common precondition both engines satisfy; the bbolt
// engine's standalone bucket is a (lenient) superset of the old behaviour.
func testSyncOffset(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	const path = "/media/show/episode.eng.srt"
	if _, err := s.RecordSubtitleFiles(ctx, api.MediaTypeEpisode, "tvdb-so-1", []api.SubtitleFile{
		{Language: langEng, Variant: api.VariantStandard, Source: api.SourceExternal, Path: path, Codec: codecSubrip},
	}); err != nil {
		t.Fatalf("RecordSubtitleFiles: %v", err)
	}
	if err := s.SetSyncOffset(ctx, path, 1500); err != nil {
		t.Fatalf("SetSyncOffset: %v", err)
	}
	got, err := s.GetSyncOffset(ctx, path)
	if err != nil {
		t.Fatalf("GetSyncOffset: %v", err)
	}
	if got != 1500 {
		t.Fatalf("GetSyncOffset = %d, want 1500", got)
	}
}

// testCoverageFiles asserts the subtitle-file inventory diff-sync: first record
// reports changed and is retrievable; re-recording the same set reports no
// change; the total counter is exact (Requirements 5.1, 5.2, 5.5).
func testCoverageFiles(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	files := []api.SubtitleFile{
		{Language: langEng, Variant: api.VariantStandard, Source: api.SourceExternal, Path: "/m/cov.eng.srt", Codec: codecSubrip},
	}

	changed, err := s.RecordSubtitleFiles(ctx, api.MediaTypeMovie, "tt-cov-1", files)
	if err != nil {
		t.Fatalf("RecordSubtitleFiles (first): %v", err)
	}
	if !changed {
		t.Fatalf("RecordSubtitleFiles (first) changed = false, want true")
	}

	got, err := s.GetSubtitleFiles(ctx, api.MediaTypeMovie, "tt-cov-1")
	if err != nil {
		t.Fatalf("GetSubtitleFiles: %v", err)
	}
	if len(got) != 1 || got[0].Language != langEng || got[0].Path != "/m/cov.eng.srt" {
		t.Fatalf("GetSubtitleFiles = %+v, want one eng row at /m/cov.eng.srt", got)
	}

	total, err := s.TotalSubtitleFiles(ctx)
	if err != nil {
		t.Fatalf("TotalSubtitleFiles: %v", err)
	}
	if total != 1 {
		t.Fatalf("TotalSubtitleFiles = %d, want 1", total)
	}

	// Re-recording the identical set is a no-op: nothing changed.
	changed, err = s.RecordSubtitleFiles(ctx, api.MediaTypeMovie, "tt-cov-1", files)
	if err != nil {
		t.Fatalf("RecordSubtitleFiles (second): %v", err)
	}
	if changed {
		t.Fatalf("RecordSubtitleFiles (identical second) changed = true, want false")
	}
}

// testScanState asserts scan-state upsert + retrieval and the inclusive
// RecentlyScanned cutoff (Requirements 5.2, 5.3, 5.4).
func testScanState(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	mid := "tvdb-ss-1-s01e01"

	before := time.Now().Add(-time.Hour)
	if err := s.RecordScanState(ctx, &api.ScanRecord{
		MediaType: api.MediaTypeEpisode, MediaID: mid,
		Title: "ScanShow", AudioLang: langEng, Season: 1, Episode: 1,
	}); err != nil {
		t.Fatalf("RecordScanState: %v", err)
	}

	states, err := s.GetScanStates(ctx, api.MediaTypeEpisode, "tvdb-ss-1")
	if err != nil {
		t.Fatalf("GetScanStates: %v", err)
	}
	if len(states) != 1 || states[0].Title != "ScanShow" {
		t.Fatalf("GetScanStates = %+v, want one row titled ScanShow", states)
	}

	// A cutoff in the past includes the just-recorded item.
	recent, err := s.RecentlyScanned(ctx, before)
	if err != nil {
		t.Fatalf("RecentlyScanned (past cutoff): %v", err)
	}
	if !recent[mid] {
		t.Fatalf("RecentlyScanned(past cutoff) missing %q; got %v", mid, recent)
	}

	// A cutoff in the future excludes it.
	recent, err = s.RecentlyScanned(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("RecentlyScanned (future cutoff): %v", err)
	}
	if recent[mid] {
		t.Fatalf("RecentlyScanned(future cutoff) unexpectedly included %q", mid)
	}
}

// saveAuto is a small helper for the query case: it persists one auto download
// row for a distinct triple with the given title/provider, so GetState filters
// have material to match.
func saveAuto(t *testing.T, s api.Store, mt api.MediaType, mid, lang string, prov api.ProviderID, title string, score int) {
	t.Helper()
	if err := s.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: mt, MediaID: mid, Language: lang, ProviderName: prov,
		ReleaseName: title + ".rel", Score: score, Path: "/m/" + mid + "." + lang + ".srt",
		Meta: &api.DownloadMeta{Title: title},
	}); err != nil {
		t.Fatalf("SaveDownload(%s/%s): %v", mid, lang, err)
	}
}

// testGetStateQuery asserts GetState filtering (type/language/provider), the
// case-insensitive contains title search, and limit/offset pagination
// (Requirements 15.1, 15.2, 15.3, 15.5). Pagination is asserted by set-union so
// it is robust to engine-specific tie ordering on equal media_imported.
func testGetStateQuery(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()

	saveAuto(t, s, api.MediaTypeMovie, "ttq1", langEng, provOS, "Alpha", 10)
	saveAuto(t, s, api.MediaTypeMovie, "ttq2", langEng, provOS, "Beta", 20)
	saveAuto(t, s, api.MediaTypeMovie, "ttq3", langFra, provOS, "Gamma", 30)
	saveAuto(t, s, api.MediaTypeEpisode, "ttq4", langEng, api.ProviderID("subdl"), "Delta", 40)

	assertCount := func(name string, q *api.StateQuery, want int) []api.StateEntry {
		entries, err := s.GetState(ctx, q)
		if err != nil {
			t.Fatalf("GetState(%s): %v", name, err)
		}
		if len(entries) != want {
			t.Fatalf("GetState(%s) = %d rows, want %d", name, len(entries), want)
		}
		return entries
	}

	assertCount("all", &api.StateQuery{}, 4)
	assertCount("type=movie", &api.StateQuery{MediaType: api.MediaTypeMovie}, 3)
	assertCount("type=movie,lang=eng", &api.StateQuery{MediaType: api.MediaTypeMovie, Language: langEng}, 2)
	assertCount("provider=subdl", &api.StateQuery{Provider: api.ProviderID("subdl")}, 1)

	// Case-insensitive contains search on title.
	search := assertCount("search=alp", &api.StateQuery{Search: "alp"}, 1)
	if search[0].MediaID != "ttq1" {
		t.Fatalf("GetState(search=alp)[0].MediaID = %q, want ttq1", search[0].MediaID)
	}

	// Pagination: two disjoint pages of 2 cover all 4 distinct rows.
	page1 := assertCount("limit=2,offset=0", &api.StateQuery{Limit: 2, Offset: 0}, 2)
	page2 := assertCount("limit=2,offset=2", &api.StateQuery{Limit: 2, Offset: 2}, 2)
	seen := map[string]bool{}
	union := append(append([]api.StateEntry{}, page1...), page2...)
	for i := range union {
		e := &union[i]
		if seen[e.MediaID] {
			t.Fatalf("paginated pages overlap on media_id %q", e.MediaID)
		}
		seen[e.MediaID] = true
	}
	for _, want := range []string{"ttq1", "ttq2", "ttq3", "ttq4"} {
		if !seen[want] {
			t.Fatalf("paginated pages missing media_id %q; union = %v", want, seen)
		}
	}
}

// testDeleteStateByPaths asserts a video-path delete removes the state rows AND
// their orphaned coverage and backoff (Requirement 7.6).
func testDeleteStateByPaths(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	const video = "/media/del/movie.mkv"
	const mid = "tt-del-1"
	const subPath = "/media/del/movie.eng.srt"

	if err := s.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: mid, Language: langEng,
		ProviderName: provOS, ReleaseName: "Movie-GRP", Score: 80,
		Path: subPath,
		Meta: &api.DownloadMeta{VideoPath: video},
	}); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}
	if _, err := s.RecordSubtitleFiles(ctx, api.MediaTypeMovie, mid, []api.SubtitleFile{
		{Language: langEng, Variant: api.VariantStandard, Source: api.SourceExternal, Path: subPath, Codec: codecSubrip},
	}); err != nil {
		t.Fatalf("RecordSubtitleFiles: %v", err)
	}
	if err := s.RecordScanState(ctx, &api.ScanRecord{
		MediaType: api.MediaTypeMovie, MediaID: mid, Title: "Movie", AudioLang: langEng,
	}); err != nil {
		t.Fatalf("RecordScanState: %v", err)
	}
	if err := s.RecordNoResult(ctx, api.MediaTypeMovie, mid, langEng, provOS, defaultBackoff()); err != nil {
		t.Fatalf("RecordNoResult: %v", err)
	}

	result, err := s.DeleteStateByPaths(ctx, []string{video})
	if err != nil {
		t.Fatalf("DeleteStateByPaths: %v", err)
	}
	if len(result.Paths) != 1 || result.Paths[0] != subPath {
		t.Fatalf("DeleteStateByPaths paths = %v, want [%s]", result.Paths, subPath)
	}

	downloads, attempts, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if downloads != 0 {
		t.Fatalf("downloads after delete = %d, want 0", downloads)
	}
	if attempts != 0 {
		t.Fatalf("attempts after delete = %d, want 0 (triple backoff cleared)", attempts)
	}

	covg, err := s.GetSubtitleFiles(ctx, api.MediaTypeMovie, mid)
	if err != nil {
		t.Fatalf("GetSubtitleFiles: %v", err)
	}
	if len(covg) != 0 {
		t.Fatalf("GetSubtitleFiles after delete = %d, want 0 (orphan coverage cleaned)", len(covg))
	}
	scans, err := s.GetScanStates(ctx, api.MediaTypeMovie, mid)
	if err != nil {
		t.Fatalf("GetScanStates: %v", err)
	}
	if len(scans) != 0 {
		t.Fatalf("GetScanStates after delete = %d, want 0 (orphan scan_state cleaned)", len(scans))
	}
}

// testCleanupDriftRemoved asserts config-drift cleanup deletes only the backoff
// rows for removed languages, then for removed providers (Requirement 7.7).
func testCleanupDriftRemoved(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	const mid = "tt-drift-1"

	// Two languages (eng, fra) and two providers (os, subdl) under one media id.
	if err := s.RecordNoResult(ctx, api.MediaTypeMovie, mid, langEng, provOS, defaultBackoff()); err != nil {
		t.Fatalf("RecordNoResult(eng,os): %v", err)
	}
	if err := s.RecordNoResult(ctx, api.MediaTypeMovie, mid, langFra, provOS, defaultBackoff()); err != nil {
		t.Fatalf("RecordNoResult(fra,os): %v", err)
	}
	if err := s.RecordNoResult(ctx, api.MediaTypeMovie, mid, langEng, api.ProviderID("subdl"), defaultBackoff()); err != nil {
		t.Fatalf("RecordNoResult(eng,subdl): %v", err)
	}
	if _, attempts, _ := s.Stats(ctx); attempts != 3 {
		t.Fatalf("attempts before drift = %d, want 3", attempts)
	}

	// Remove language fra: only the (eng-or-fra)=fra rows go.
	if err := s.CleanupDrift(ctx, api.ConfigDrift{RemovedLanguages: []string{langFra}}); err != nil {
		t.Fatalf("CleanupDrift(removed lang fra): %v", err)
	}
	remaining, err := s.GetBackoffByPrefix(ctx, api.MediaTypeMovie, mid)
	if err != nil {
		t.Fatalf("GetBackoffByPrefix: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("after removing lang fra: %d backoff rows, want 2", len(remaining))
	}
	for _, e := range remaining {
		if e.Language == langFra {
			t.Fatalf("backoff row for removed language fra still present: %+v", e)
		}
	}

	// Remove provider subdl: the (eng,subdl) row goes, leaving (eng,os).
	if cerr := s.CleanupDrift(ctx, api.ConfigDrift{RemovedProviders: []api.ProviderID{api.ProviderID("subdl")}}); cerr != nil {
		t.Fatalf("CleanupDrift(removed provider subdl): %v", cerr)
	}
	remaining, err = s.GetBackoffByPrefix(ctx, api.MediaTypeMovie, mid)
	if err != nil {
		t.Fatalf("GetBackoffByPrefix: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("after removing provider subdl: %d backoff rows, want 1", len(remaining))
	}
	if remaining[0].Language != langEng || remaining[0].Provider != provOS {
		t.Fatalf("surviving backoff row = %+v, want {lang=eng, provider=%s}", remaining[0], provOS)
	}
}

// testCleanupDriftAdaptiveDisabled asserts disabling adaptive search clears ALL
// backoff (Requirement 7.8).
func testCleanupDriftAdaptiveDisabled(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()

	for _, mid := range []string{"tt-ad-1", "tt-ad-2"} {
		if err := s.RecordNoResult(ctx, api.MediaTypeMovie, mid, langEng, provOS, defaultBackoff()); err != nil {
			t.Fatalf("RecordNoResult(%s): %v", mid, err)
		}
	}
	if _, attempts, _ := s.Stats(ctx); attempts != 2 {
		t.Fatalf("attempts before drift = %d, want 2", attempts)
	}

	if err := s.CleanupDrift(ctx, api.ConfigDrift{AdaptiveDisabled: true}); err != nil {
		t.Fatalf("CleanupDrift(adaptive disabled): %v", err)
	}
	if _, attempts, err := s.Stats(ctx); err != nil {
		t.Fatalf("Stats: %v", err)
	} else if attempts != 0 {
		t.Fatalf("attempts after adaptive-disabled drift = %d, want 0 (all cleared)", attempts)
	}
}

// testReconcileVideoGone asserts the VIDEO-GONE reconcile branch: a row whose
// video file no longer exists is deleted along with its orphaned subtitle file,
// the triple's backoff, and the media's orphaned scan_state; a row whose video
// still exists is preserved (Requirement 7.1).
func testReconcileVideoGone(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	// Present control: video + subtitle both exist on disk.
	okVideo := filepath.Join(dir, "ok.mkv")
	okSub := filepath.Join(dir, "ok.eng.srt")
	writeFile(t, okVideo)
	writeFile(t, okSub)
	mustSaveDownload(t, s, "ok", &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt-ok", Language: langEng,
		ProviderName: provOS, ReleaseName: "OK", Score: 100, Path: okSub,
		Meta: &api.DownloadMeta{VideoPath: okVideo},
	})

	// Gone item: neither video nor subtitle exists on disk.
	goneVideo := filepath.Join(dir, "gone.mkv")
	goneSub := filepath.Join(dir, "gone.eng.srt")
	mustSaveDownload(t, s, "gone", &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt-gone", Language: langEng,
		ProviderName: provOS, ReleaseName: "Gone", Score: 100, Path: goneSub,
		Meta: &api.DownloadMeta{VideoPath: goneVideo},
	})
	if _, err := s.RecordSubtitleFiles(ctx, api.MediaTypeMovie, "tt-gone", []api.SubtitleFile{
		{Language: langEng, Variant: api.VariantStandard, Source: api.SourceExternal, Path: goneSub, Codec: codecSubrip},
	}); err != nil {
		t.Fatalf("RecordSubtitleFiles(gone): %v", err)
	}
	if err := s.RecordScanState(ctx, &api.ScanRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt-gone", Title: "Gone", AudioLang: langEng,
	}); err != nil {
		t.Fatalf("RecordScanState(gone): %v", err)
	}
	if err := s.RecordNoResult(ctx, api.MediaTypeMovie, "tt-gone", langEng, provOS, defaultBackoff()); err != nil {
		t.Fatalf("RecordNoResult(gone): %v", err)
	}

	result, err := s.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	if len(result.Deleted.Paths) != 1 || result.Deleted.Paths[0] != goneSub {
		t.Fatalf("Deleted.Paths = %v, want [%s]", result.Deleted.Paths, goneSub)
	}
	if result.ResetCount != 0 {
		t.Fatalf("ResetCount = %d, want 0 (video-gone deletes, not resets)", result.ResetCount)
	}

	// Only the present control row survives.
	downloads, attempts, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if downloads != 1 {
		t.Fatalf("downloads after reconcile = %d, want 1 (present row preserved)", downloads)
	}
	if attempts != 0 {
		t.Fatalf("attempts after reconcile = %d, want 0 (gone triple backoff cleared)", attempts)
	}

	// The gone media's orphaned coverage and scan_state are cleaned; the
	// present media keeps no coverage rows of its own (none recorded) — assert
	// the gone media specifically.
	goneScans, err := s.GetScanStates(ctx, api.MediaTypeMovie, "tt-gone")
	if err != nil {
		t.Fatalf("GetScanStates(gone): %v", err)
	}
	if len(goneScans) != 0 {
		t.Fatalf("GetScanStates(gone) = %d, want 0 (orphaned scan_state cleaned)", len(goneScans))
	}
	goneFiles, err := s.GetSubtitleFiles(ctx, api.MediaTypeMovie, "tt-gone")
	if err != nil {
		t.Fatalf("GetSubtitleFiles(gone): %v", err)
	}
	if len(goneFiles) != 0 {
		t.Fatalf("GetSubtitleFiles(gone) = %d, want 0 (orphaned coverage cleaned)", len(goneFiles))
	}
}

// testReconcileSubGoneSiblingPresent asserts the SUBTITLE-GONE-SIBLING-PRESENT
// branch: when one subtitle row's file is gone but the video exists AND another
// subtitle row for the same triple still has its file, only the missing row is
// deleted; the remaining row, the manual lock, the backoff, and media_imported
// are all preserved (Requirement 7.2).
func testReconcileSubGoneSiblingPresent(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	video := filepath.Join(dir, "movie.mkv")
	presentSub := filepath.Join(dir, "movie.fr.1.srt")
	missingSub := filepath.Join(dir, "movie.fr.2.srt")
	writeFile(t, video)
	writeFile(t, presentSub) // sibling on disk
	// missingSub is intentionally never created.

	// Two manual rows for the same triple: one present, one gone. Both manual so
	// deleting the gone one still leaves a manual lock behind.
	if err := s.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt-sib", Language: langFra,
		ProviderName: provOS, ReleaseName: "Present", Score: 100, Path: presentSub,
		Meta: &api.DownloadMeta{VideoPath: video, Manual: true},
	}); err != nil {
		t.Fatalf("SaveDownload(present): %v", err)
	}
	if err := s.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt-sib", Language: langFra,
		ProviderName: provOS, ReleaseName: "Missing", Score: 80, Path: missingSub,
		Meta: &api.DownloadMeta{VideoPath: video, Manual: true},
	}); err != nil {
		t.Fatalf("SaveDownload(missing): %v", err)
	}
	// Backoff recorded AFTER the saves (SaveDownload clears triple backoff).
	if err := s.RecordNoResult(ctx, api.MediaTypeMovie, "tt-sib", langFra, provOS, defaultBackoff()); err != nil {
		t.Fatalf("RecordNoResult: %v", err)
	}

	result, err := s.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	if len(result.Deleted.Paths) != 0 {
		t.Fatalf("Deleted.Paths = %v, want empty (sibling-present is not a video-gone delete)", result.Deleted.Paths)
	}
	if result.ResetCount != 0 {
		t.Fatalf("ResetCount = %d, want 0 (sibling present: no reset)", result.ResetCount)
	}

	// Only the missing row was removed; the present row remains, the lock holds,
	// and backoff is NOT cleared.
	downloads, attempts, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if downloads != 1 {
		t.Fatalf("downloads after reconcile = %d, want 1 (missing row deleted, present preserved)", downloads)
	}
	if attempts != 1 {
		t.Fatalf("attempts after reconcile = %d, want 1 (backoff preserved, not cleared)", attempts)
	}

	locked, err := s.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt-sib", langFra, api.VariantStandard)
	if err != nil {
		t.Fatalf("IsManuallyLocked: %v", err)
	}
	if !locked {
		t.Fatalf("IsManuallyLocked after reconcile = false, want true (lock preserved)")
	}

	entries, err := s.GetState(ctx, &api.StateQuery{MediaType: api.MediaTypeMovie})
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(entries) != 1 || entries[0].ReleaseName != "Present" {
		t.Fatalf("GetState = %+v, want one row 'Present'", entries)
	}
}

// testReconcileAllSubsGone asserts the ALL-SUBTITLES-GONE branch: when every
// subtitle for a triple is gone but the video exists, the auto rows are reset in
// place (path/score cleared), the manual rows are deleted, and the triple's
// backoff is cleared (Requirement 7.3).
func testReconcileAllSubsGone(t *testing.T, s api.Store) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	video := filepath.Join(dir, "movie.mkv")
	writeFile(t, video)                               // video present
	autoSub := filepath.Join(dir, "movie.fr.srt")     // never created
	manualSub := filepath.Join(dir, "movie.fr.1.srt") // never created

	mustSaveDownload(t, s, "auto", &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt-all", Language: langFra,
		ProviderName: provOS, ReleaseName: "Auto", Score: 100, Path: autoSub,
		Meta: &api.DownloadMeta{VideoPath: video},
	})
	mustSaveDownload(t, s, "manual", &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt-all", Language: langFra,
		ProviderName: provOS, ReleaseName: "Manual", Score: 200, Path: manualSub,
		Meta: &api.DownloadMeta{VideoPath: video, Manual: true},
	})
	if err := s.RecordNoResult(ctx, api.MediaTypeMovie, "tt-all", langFra, provOS, defaultBackoff()); err != nil {
		t.Fatalf("RecordNoResult: %v", err)
	}

	result, err := s.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	if result.ResetCount != 1 {
		t.Fatalf("ResetCount = %d, want 1 (one triple reset)", result.ResetCount)
	}
	if len(result.Deleted.Paths) != 0 {
		t.Fatalf("Deleted.Paths = %v, want empty (video present: reset, not video-gone delete)", result.Deleted.Paths)
	}

	downloads, attempts, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if downloads != 1 {
		t.Fatalf("downloads after reconcile = %d, want 1 (manual deleted, auto reset)", downloads)
	}
	if attempts != 0 {
		t.Fatalf("attempts after reconcile = %d, want 0 (backoff cleared for re-search)", attempts)
	}

	// The manual lock is gone (manual row deleted) and the surviving auto row is
	// reset for re-search.
	locked, err := s.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt-all", langFra, api.VariantStandard)
	if err != nil {
		t.Fatalf("IsManuallyLocked: %v", err)
	}
	if locked {
		t.Fatalf("IsManuallyLocked after reconcile = true, want false (manual row deleted)")
	}

	entries, err := s.GetState(ctx, &api.StateQuery{MediaType: api.MediaTypeMovie})
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("GetState = %d rows, want 1 (auto reset, manual deleted)", len(entries))
	}
	if entries[0].Manual {
		t.Fatalf("surviving row Manual = true, want false")
	}
	if entries[0].Path != "" || entries[0].Score != 0 {
		t.Fatalf("surviving row not reset: path=%q score=%d, want empty/0", entries[0].Path, entries[0].Score)
	}
}
