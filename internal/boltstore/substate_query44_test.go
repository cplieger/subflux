package boltstore

import (
	"context"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/cplieger/subflux/internal/api"
	boltkv "github.com/cplieger/subflux/internal/store/kv"
)

// This file covers task-4.4: GetState (filter by type/language/provider,
// literal-escaped contains-match title search, 1000-row default cap,
// media_imported DESC then id DESC ordering with a tie, and shallow + deep
// pagination), Stats (O(1) maintained counters across inserts and deletes),
// GetManualLocks (one entry per locked triple with count, ordered by
// media_type then media_id), and HistoryMediaIDs (distinct ids, type filter,
// literal-escaped prefix). Requirements 8.4, 15.1, 15.2, 15.3, 15.5, 18.1,
// 18.4.

// putStateRow inserts a fully-specified subtitle_state row through the putState
// chokepoint (which allocates the be64 key, maintains ix_state_triple /
// ix_state_imported / ix_state_video, and bumps the downloads counter). The
// caller controls every field EXCEPT the surrogate id, which is allocated via
// NextSequence so the row's key and the id-DESC tiebreak stay recoverable. It
// returns the allocated id. This is the test seam that lets the ordering and
// tie cases pin an explicit media_imported (SaveDownload always stamps now).
func putStateRow(t *testing.T, db *DB, mt api.MediaType, mid, lang string, sr stateRec) int64 {
	t.Helper()
	var id int64
	if err := db.db.Update(func(tx *bolt.Tx) error {
		sb := tx.Bucket([]byte(bucketSubtitleState))
		seq, _, err := boltkv.NextID(sb)
		if err != nil {
			return err
		}
		sr.ID = int64(seq) //nolint:gosec // G115: surrogate id from NextSequence
		id = sr.ID
		return putState(tx, mt, mid, lang, &sr)
	}); err != nil {
		t.Fatalf("putStateRow(%s/%s/%s): %v", mt, mid, lang, err)
	}
	return id
}

// deleteStateRow removes a row through the deleteState chokepoint (which
// removes every index entry and decrements the downloads counter), used to
// exercise the Stats counter on the delete path.
func deleteStateRow(t *testing.T, db *DB, mt api.MediaType, mid, lang string, id int64) {
	t.Helper()
	if err := db.db.Update(func(tx *bolt.Tx) error {
		_, err := deleteState(tx, mt, mid, lang, id)
		return err
	}); err != nil {
		t.Fatalf("deleteStateRow: %v", err)
	}
}

// stateIDs projects the surrogate ids from a GetState result, in result order,
// so ordering/pagination assertions compare ids rather than whole structs.
func stateIDs(entries []api.StateEntry) []int64 {
	out := make([]int64, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- GetState filters ---

// TestGetState_filtersByTypeLanguageProvider asserts each filter narrows the
// result set independently and that zero-value fields mean "no filter"
// (Requirement 15.5). Rows span two media types, two languages, and two
// providers so each filter has both a match and a non-match to exclude.
func TestGetState_filtersByTypeLanguageProvider(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// (type, mid, lang, provider) fixture, one row each.
	putStateRow(t, db, api.MediaTypeEpisode, "ep-fr-os", "fr", stateRec{Provider: api.ProviderNameOpenSubtitles, Title: "Ep FR OS"})
	putStateRow(t, db, api.MediaTypeEpisode, "ep-en-sd", "en", stateRec{Provider: api.ProviderNameSubDL, Title: "Ep EN SD"})
	putStateRow(t, db, api.MediaTypeMovie, "mv-fr-sd", "fr", stateRec{Provider: api.ProviderNameSubDL, Title: "Mv FR SD"})
	putStateRow(t, db, api.MediaTypeMovie, "mv-en-os", "en", stateRec{Provider: api.ProviderNameOpenSubtitles, Title: "Mv EN OS"})

	// No filter: all four rows.
	all, err := db.GetState(ctx, &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState(all): %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("unfiltered count = %d, want 4", len(all))
	}

	// MediaType filter.
	eps, err := db.GetState(ctx, &api.StateQuery{MediaType: api.MediaTypeEpisode})
	if err != nil {
		t.Fatalf("GetState(type): %v", err)
	}
	if len(eps) != 2 {
		t.Errorf("type=episode count = %d, want 2", len(eps))
	}
	for _, e := range eps {
		if e.MediaType != api.MediaTypeEpisode {
			t.Errorf("type filter leaked %s", e.MediaType)
		}
	}

	// Language filter.
	fr, err := db.GetState(ctx, &api.StateQuery{Language: "fr"})
	if err != nil {
		t.Fatalf("GetState(lang): %v", err)
	}
	if len(fr) != 2 {
		t.Errorf("lang=fr count = %d, want 2", len(fr))
	}
	for _, e := range fr {
		if e.Language != "fr" {
			t.Errorf("lang filter leaked %s", e.Language)
		}
	}

	// Provider filter.
	sd, err := db.GetState(ctx, &api.StateQuery{Provider: api.ProviderNameSubDL})
	if err != nil {
		t.Fatalf("GetState(provider): %v", err)
	}
	if len(sd) != 2 {
		t.Errorf("provider=subdl count = %d, want 2", len(sd))
	}
	for _, e := range sd {
		if e.Provider != api.ProviderNameSubDL {
			t.Errorf("provider filter leaked %s", e.Provider)
		}
	}

	// Combined filters narrow to exactly one row.
	combo, err := db.GetState(ctx, &api.StateQuery{
		MediaType: api.MediaTypeMovie, Language: "fr", Provider: api.ProviderNameSubDL,
	})
	if err != nil {
		t.Fatalf("GetState(combo): %v", err)
	}
	if len(combo) != 1 || combo[0].MediaID != "mv-fr-sd" {
		t.Errorf("combined filter = %+v, want exactly the mv-fr-sd row", combo)
	}
}

// TestGetState_titleSearchTreatsWildcardsLiterally asserts the title search is
// a case-insensitive CONTAINS match in which the user's %, _, and \ are matched
// literally, reproducing the old `title LIKE '%'||escape(search)||'%' ESCAPE
// '\'` (Requirements 8.4, 15.5). This is the property the SQL escape provided:
// a search term of "100%" must NOT behave like the SQL wildcard.
func TestGetState_titleSearchTreatsWildcardsLiterally(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	putStateRow(t, db, api.MediaTypeMovie, "m-pct", "en", stateRec{Title: "100% Complete"})
	putStateRow(t, db, api.MediaTypeMovie, "m-num", "en", stateRec{Title: "1000 Leagues"})
	putStateRow(t, db, api.MediaTypeMovie, "m-und", "en", stateRec{Title: "a_b matched"})
	putStateRow(t, db, api.MediaTypeMovie, "m-axb", "en", stateRec{Title: "axb other"})
	putStateRow(t, db, api.MediaTypeMovie, "m-bsl", "en", stateRec{Title: `back\slash`})

	cases := []struct {
		name     string
		search   string
		wantMIDs map[string]bool
	}{
		// '%' is literal: matches "100% Complete" but NOT "1000 Leagues"
		// (which a real SQL wildcard would match).
		{"percent literal", "100%", map[string]bool{"m-pct": true}},
		// '_' is literal: matches "a_b matched" but NOT "axb other".
		{"underscore literal", "a_b", map[string]bool{"m-und": true}},
		// backslash is literal.
		{"backslash literal", `back\slash`, map[string]bool{"m-bsl": true}},
		// plain contains, case-insensitive.
		{"case insensitive", "COMPLETE", map[string]bool{"m-pct": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := db.GetState(ctx, &api.StateQuery{Search: c.search})
			if err != nil {
				t.Fatalf("GetState(search=%q): %v", c.search, err)
			}
			if len(got) != len(c.wantMIDs) {
				t.Fatalf("search %q matched %d rows %v, want %d %v",
					c.search, len(got), stateMIDs(got), len(c.wantMIDs), c.wantMIDs)
			}
			for _, e := range got {
				if !c.wantMIDs[e.MediaID] {
					t.Errorf("search %q unexpectedly matched %q (%q)", c.search, e.MediaID, e.Title)
				}
			}
		})
	}
}

// stateMIDs projects media ids for diagnostic output.
func stateMIDs(entries []api.StateEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.MediaID
	}
	return out
}

// TestGetState_defaultThousandRowCap asserts that when no Limit is given
// (Limit <= 0) the result is capped at the default 1000 rows, matching the old
// store's hard cap that prevents unbounded allocation (Requirement 15.3). 1001
// rows are inserted in a single transaction for speed; exactly 1000 come back.
func TestGetState_defaultThousandRowCap(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	const total = defaultQueryLimit + 1
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.db.Update(func(tx *bolt.Tx) error {
		sb := tx.Bucket([]byte(bucketSubtitleState))
		for i := 0; i < total; i++ {
			seq, _, err := boltkv.NextID(sb)
			if err != nil {
				return err
			}
			sr := stateRec{
				ID:            int64(seq), //nolint:gosec // G115: surrogate id
				Title:         "Bulk",
				MediaImported: base.Add(time.Duration(i) * time.Second),
			}
			if err := putState(tx, api.MediaTypeMovie, "m-"+itoa(i), "en", &sr); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("bulk insert: %v", err)
	}

	got, err := db.GetState(ctx, &api.StateQuery{}) // Limit unset -> default cap
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(got) != defaultQueryLimit {
		t.Errorf("unlimited query returned %d rows, want the %d default cap", len(got), defaultQueryLimit)
	}
}

// itoa is a tiny allocation-free-enough integer formatter for the bulk fixture
// keys (avoids pulling strconv into the test for one call site).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestGetState_orderingMediaImportedDescThenIDDesc asserts results come back
// media_imported DESC, then surrogate-id DESC on a tie, exactly the old
// `ORDER BY media_imported DESC, id DESC` (Requirements 8.4, 15.1). Two rows
// share a media_imported so the id tiebreak is exercised: the later-inserted
// row (higher id) must precede the earlier one.
func TestGetState_orderingMediaImportedDescThenIDDesc(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	t1 := time.Date(2021, 6, 1, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour) // strictly newer

	// A is newest (t2). B and C tie at t1; C is inserted after B so id(C) > id(B).
	idA := putStateRow(t, db, api.MediaTypeMovie, "m-a", "en", stateRec{Title: "A", MediaImported: t2})
	idB := putStateRow(t, db, api.MediaTypeMovie, "m-b", "en", stateRec{Title: "B", MediaImported: t1})
	idC := putStateRow(t, db, api.MediaTypeMovie, "m-c", "en", stateRec{Title: "C", MediaImported: t1})

	got, err := db.GetState(ctx, &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	want := []int64{idA, idC, idB} // t2 first; then t1 tie broken by id DESC
	if gotIDs := stateIDs(got); !equalIDs(gotIDs, want) {
		t.Errorf("order = %v, want %v (media_imported DESC, id DESC; tie %d>%d)", gotIDs, want, idC, idB)
	}
}

// TestGetState_paginationShallowOffsetAndDeepPage asserts numeric Offset paging
// returns contiguous, non-overlapping slices of the media_imported-DESC order
// for both a shallow page and a deep page (Requirements 15.5, 18.4). Five rows
// with strictly descending import times give a deterministic full order.
func TestGetState_paginationShallowOffsetAndDeepPage(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	base := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]int64, 5)
	for i := 0; i < 5; i++ {
		// Later index -> newer import, so the DESC order is ids[4],ids[3],...,ids[0].
		ids[i] = putStateRow(t, db, api.MediaTypeMovie, "m-"+itoa(i), "en",
			stateRec{Title: "P", MediaImported: base.Add(time.Duration(i) * time.Hour)})
	}
	fullDesc := []int64{ids[4], ids[3], ids[2], ids[1], ids[0]}

	// Shallow page 1.
	p1, err := db.GetState(ctx, &api.StateQuery{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("GetState(page1): %v", err)
	}
	if want := fullDesc[0:2]; !equalIDs(stateIDs(p1), want) {
		t.Errorf("page1 = %v, want %v", stateIDs(p1), want)
	}

	// Shallow page 2.
	p2, err := db.GetState(ctx, &api.StateQuery{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("GetState(page2): %v", err)
	}
	if want := fullDesc[2:4]; !equalIDs(stateIDs(p2), want) {
		t.Errorf("page2 = %v, want %v", stateIDs(p2), want)
	}

	// Deep page: offset past the shallow pages returns the tail only.
	deep, err := db.GetState(ctx, &api.StateQuery{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("GetState(deep): %v", err)
	}
	if want := fullDesc[4:5]; !equalIDs(stateIDs(deep), want) {
		t.Errorf("deep page = %v, want %v", stateIDs(deep), want)
	}

	// Offset beyond the end returns nothing.
	empty, err := db.GetState(ctx, &api.StateQuery{Limit: 2, Offset: 99})
	if err != nil {
		t.Fatalf("GetState(beyond): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("offset beyond end = %v, want empty", stateIDs(empty))
	}
}

// --- Stats ---

// TestStats_countersTrackInsertsAndDeletes asserts Stats reports the maintained
// O(1) download and attempt counters, that an in-place auto upgrade does NOT
// double-count, and that both counters track deletes (Requirements 15.1, 18.1).
func TestStats_countersTrackInsertsAndDeletes(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// Fresh store: both counters zero.
	if d, a, err := db.Stats(ctx); err != nil || d != 0 || a != 0 {
		t.Fatalf("fresh Stats = (%d, %d, %v), want (0, 0, nil)", d, a, err)
	}

	// Auto download on T1 -> downloads = 1.
	if err := db.SaveDownload(ctx, autoRec(testProv, "R", "/m/t.fr.srt", 70)); err != nil {
		t.Fatalf("auto SaveDownload: %v", err)
	}
	// Manual download on the same triple -> downloads = 2 (manual always appends).
	if err := db.SaveDownload(ctx, manualRec(testProv, "R", "/m/t.fr.1.srt", 70)); err != nil {
		t.Fatalf("manual SaveDownload: %v", err)
	}
	// In-place auto upgrade -> still 2 (update, not insert).
	if err := db.SaveDownload(ctx, autoRec(api.ProviderNameSubDL, "R2", "/m/t.fr.srt", 95)); err != nil {
		t.Fatalf("upgrade SaveDownload: %v", err)
	}
	// Backoff on a DIFFERENT triple so SaveDownload above didn't clear it -> attempts = 1.
	if err := db.RecordNoResult(ctx, testMT, "other-id", testLang, testProv, defaultParams()); err != nil {
		t.Fatalf("RecordNoResult: %v", err)
	}

	d, a, err := db.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if d != 2 {
		t.Errorf("downloads = %d, want 2 (auto + manual; upgrade is in place)", d)
	}
	if a != 1 {
		t.Errorf("attempts = %d, want 1", a)
	}

	// Delete one state row -> downloads = 1.
	rows := readTripleRows(t, db, testMT, testMID, testLang)
	if len(rows) == 0 {
		t.Fatal("expected state rows on the T1 triple")
	}
	deleteStateRow(t, db, testMT, testMID, testLang, rows[0].ID)

	// A save on the other-id triple clears ITS backoff -> attempts = 0.
	if err := db.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: testMT, MediaID: "other-id", Language: testLang,
		ProviderName: testProv, ReleaseName: "R3", Path: "/m/o.en.srt", Score: 50,
		Meta: &api.DownloadMeta{},
	}); err != nil {
		t.Fatalf("other-id SaveDownload: %v", err)
	}

	d, a, err = db.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats after delete: %v", err)
	}
	if d != 2 {
		// T1 went 2->1 by the delete; the other-id auto insert added 1 -> 2.
		t.Errorf("downloads = %d, want 2 (one deleted, one inserted)", d)
	}
	if a != 0 {
		t.Errorf("attempts = %d, want 0 (cleared on the other-id save)", a)
	}
}

// --- GetManualLocks ---

// TestGetManualLocks_oneEntryPerLockedTripleOrdered asserts GetManualLocks
// returns one entry per triple that has at least one manual row, carrying the
// manual-row count, ordered by media_type then media_id; auto-only triples are
// excluded (Requirement 15.2). Mirrors the old GROUP BY ... ORDER BY
// media_type, media_id.
func TestGetManualLocks_oneEntryPerLockedTripleOrdered(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// episode/tt-b/en: two manual rows (count 2).
	putStateRow(t, db, api.MediaTypeEpisode, "tt-b", "en", stateRec{Manual: true, Path: "/m/b.en.1.srt"})
	putStateRow(t, db, api.MediaTypeEpisode, "tt-b", "en", stateRec{Manual: true, Path: "/m/b.en.2.srt"})
	// episode/tt-a/fr: one manual row.
	putStateRow(t, db, api.MediaTypeEpisode, "tt-a", "fr", stateRec{Manual: true, Path: "/m/a.fr.1.srt"})
	// movie/tt-a/en: one manual row.
	putStateRow(t, db, api.MediaTypeMovie, "tt-a", "en", stateRec{Manual: true, Path: "/m/a.en.1.srt"})
	// episode/tt-c/en: auto only -> must NOT appear.
	putStateRow(t, db, api.MediaTypeEpisode, "tt-c", "en", stateRec{Manual: false, Path: "/m/c.en.srt"})

	got, err := db.GetManualLocks(ctx)
	if err != nil {
		t.Fatalf("GetManualLocks: %v", err)
	}

	want := []api.ManualLockEntry{
		{MediaType: api.MediaTypeEpisode, MediaID: "tt-a", Language: "fr", Count: 1},
		{MediaType: api.MediaTypeEpisode, MediaID: "tt-b", Language: "en", Count: 2},
		{MediaType: api.MediaTypeMovie, MediaID: "tt-a", Language: "en", Count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("locks = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("lock[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestGetManualLocks_emptyWhenNoManualRows asserts a store with only auto rows
// reports no locks.
func TestGetManualLocks_emptyWhenNoManualRows(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	putStateRow(t, db, api.MediaTypeMovie, "m-1", "en", stateRec{Manual: false})
	got, err := db.GetManualLocks(ctx)
	if err != nil {
		t.Fatalf("GetManualLocks: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("locks = %+v, want none (no manual rows)", got)
	}
}

// --- HistoryMediaIDs ---

// TestHistoryMediaIDs_distinctTypeFilteredAndLiteralPrefix asserts the distinct
// media ids are returned per media_type, deduplicated across languages and
// auto/manual rows, with the optional prefix matched literally (Requirements
// 8.4, 15.3). Mirrors the old `SELECT DISTINCT media_id ... WHERE media_type=?
// [AND media_id LIKE escape(prefix)||'%' ESCAPE '\']`.
func TestHistoryMediaIDs_distinctTypeFilteredAndLiteralPrefix(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// tt-1 has two languages (and an auto+manual mix) -> still one distinct id.
	putStateRow(t, db, api.MediaTypeEpisode, "tt-1", "en", stateRec{Manual: false})
	putStateRow(t, db, api.MediaTypeEpisode, "tt-1", "fr", stateRec{Manual: true, Path: "/m/x.fr.1.srt"})
	putStateRow(t, db, api.MediaTypeEpisode, "tt-2", "en", stateRec{Manual: false})
	// A different media type must be excluded by the type filter.
	putStateRow(t, db, api.MediaTypeMovie, "tt-9", "en", stateRec{Manual: false})

	eps, err := db.HistoryMediaIDs(ctx, api.MediaTypeEpisode, "")
	if err != nil {
		t.Fatalf("HistoryMediaIDs(episode): %v", err)
	}
	if want := []string{"tt-1", "tt-2"}; !equalStrings(eps, want) {
		t.Errorf("episode ids = %v, want %v (distinct, ascending)", eps, want)
	}

	mv, err := db.HistoryMediaIDs(ctx, api.MediaTypeMovie, "")
	if err != nil {
		t.Fatalf("HistoryMediaIDs(movie): %v", err)
	}
	if want := []string{"tt-9"}; !equalStrings(mv, want) {
		t.Errorf("movie ids = %v, want %v", mv, want)
	}

	// Prefix filter.
	pref, err := db.HistoryMediaIDs(ctx, api.MediaTypeEpisode, "tt-1")
	if err != nil {
		t.Fatalf("HistoryMediaIDs(prefix): %v", err)
	}
	if want := []string{"tt-1"}; !equalStrings(pref, want) {
		t.Errorf("prefix tt-1 = %v, want %v", pref, want)
	}
}

// TestHistoryMediaIDs_prefixWildcardLiteral asserts the prefix's %/_ are matched
// literally, not as SQL wildcards (Requirement 8.4): a prefix of "tt%" matches
// only an id that literally starts with "tt%", never an arbitrary "tt"-prefixed id.
func TestHistoryMediaIDs_prefixWildcardLiteral(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	putStateRow(t, db, api.MediaTypeMovie, "tt%special", "en", stateRec{})
	putStateRow(t, db, api.MediaTypeMovie, "ttother", "en", stateRec{})

	got, err := db.HistoryMediaIDs(ctx, api.MediaTypeMovie, "tt%")
	if err != nil {
		t.Fatalf("HistoryMediaIDs: %v", err)
	}
	if want := []string{"tt%special"}; !equalStrings(got, want) {
		t.Errorf("prefix \"tt%%\" = %v, want %v (%% is literal, must not match ttother)", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
