package boltstore

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	bolt "go.etcd.io/bbolt"
)

// This file covers the task-4.1 SaveDownload behaviour: auto-row upsert
// preserving media_imported (Requirement 3.1), first-save inserting with now
// (Requirement 3.2), backoff cleared on save (Requirement 3.3), manual append
// with the ordinal taken verbatim from rec.Path (Requirement 4.5), and the
// auto-vs-manual row distinction.

// readTripleRows reads every subtitle_state row under a triple back through the
// shared collectTripleRows helper in a View transaction.
func readTripleRows(t *testing.T, db *DB, mt api.MediaType, mid, lang string) []stateRec {
	t.Helper()
	var rows []stateRec
	if err := db.db.View(func(tx *bolt.Tx) error {
		var err error
		rows, err = collectTripleRows(tx, mt, mid, lang)
		return err
	}); err != nil {
		t.Fatalf("readTripleRows: %v", err)
	}
	return rows
}

// autoRows / manualRows partition a triple's rows by the manual flag.
func partitionRows(rows []stateRec) (auto, manual []stateRec) {
	for _, r := range rows {
		if r.Manual {
			manual = append(manual, r)
		} else {
			auto = append(auto, r)
		}
	}
	return auto, manual
}

func autoRec(provider api.ProviderID, release, path string, score int) *api.DownloadRecord {
	return &api.DownloadRecord{
		MediaType:    testMT,
		MediaID:      testMID,
		Language:     testLang,
		ProviderName: provider,
		ReleaseName:  release,
		Path:         path,
		Score:        score,
		Meta: &api.DownloadMeta{
			Title:     "Test Title",
			VideoPath: "/media/test.mkv",
			Manual:    false,
		},
	}
}

// TestSaveDownload_autoInsertSetsNow asserts the first auto save for a triple
// inserts exactly one auto row, with media_imported set to ~now and a surrogate
// id allocated (Requirement 3.2).
func TestSaveDownload_autoInsertSetsNow(t *testing.T) {
	db, _ := openTemp(t)
	before := time.Now()

	if err := db.SaveDownload(context.Background(), autoRec(testProv, "Release.A", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	auto, manual := partitionRows(readTripleRows(t, db, testMT, testMID, testLang))
	if len(manual) != 0 {
		t.Errorf("manual rows = %d, want 0", len(manual))
	}
	if len(auto) != 1 {
		t.Fatalf("auto rows = %d, want 1", len(auto))
	}
	r := auto[0]
	if r.ID == 0 {
		t.Error("surrogate id not allocated (id == 0)")
	}
	if r.MediaImported.Before(before) || r.MediaImported.After(time.Now()) {
		t.Errorf("media_imported = %v, want within [%v, now]", r.MediaImported, before)
	}
	if r.Score != 80 || r.Provider != testProv || r.ReleaseName != "Release.A" {
		t.Errorf("row fields = %+v, want score=80 provider=%s release=Release.A", r, testProv)
	}
}

// TestSaveDownload_autoUpgradePreservesImported asserts a second auto save for
// the same triple updates the row in place (same id), overwrites the mutable
// download fields, and preserves the original media_imported (Requirement 3.1).
func TestSaveDownload_autoUpgradePreservesImported(t *testing.T) {
	db, _ := openTemp(t)

	if err := db.SaveDownload(context.Background(), autoRec(testProv, "Release.A", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("first SaveDownload: %v", err)
	}
	first := readTripleRows(t, db, testMT, testMID, testLang)
	if len(first) != 1 {
		t.Fatalf("after first save: rows = %d, want 1", len(first))
	}
	origID := first[0].ID
	origImported := first[0].MediaImported

	// A later upgrade with a different provider/score/release.
	if err := db.SaveDownload(context.Background(), autoRec(api.ProviderNameSubDL, "Release.B", "/media/test.fr.srt", 95)); err != nil {
		t.Fatalf("second SaveDownload: %v", err)
	}

	auto, _ := partitionRows(readTripleRows(t, db, testMT, testMID, testLang))
	if len(auto) != 1 {
		t.Fatalf("after upgrade: auto rows = %d, want 1 (update in place, not insert)", len(auto))
	}
	r := auto[0]
	if r.ID != origID {
		t.Errorf("id = %d, want preserved %d (update in place)", r.ID, origID)
	}
	if !r.MediaImported.Equal(origImported) {
		t.Errorf("media_imported = %v, want preserved %v", r.MediaImported, origImported)
	}
	if r.Score != 95 || r.Provider != api.ProviderNameSubDL || r.ReleaseName != "Release.B" {
		t.Errorf("mutable fields not upgraded: %+v", r)
	}
}

// TestSaveDownload_clearsBackoff asserts that saving a download clears all
// search_attempts rows (and their due-index entries) for the triple within the
// same transaction (Requirement 3.3).
func TestSaveDownload_clearsBackoff(t *testing.T) {
	db, _ := openTemp(t)

	// Seed backoff rows for two providers on the triple, plus an unrelated
	// triple that must survive.
	future := time.Now().Add(time.Hour)
	putAttemptRow(t, db, testMT, testMID, testLang, testProv, attemptRec{NextRetry: future, Failures: 2})
	putAttemptRow(t, db, testMT, testMID, testLang, api.ProviderNameSubDL, attemptRec{NextRetry: future, Failures: 1})
	putAttemptRow(t, db, testMT, "other-id", testLang, testProv, attemptRec{NextRetry: future, Failures: 1})

	if err := db.SaveDownload(context.Background(), autoRec(testProv, "Release.A", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	// The triple's backoff is gone.
	backed, err := db.BackedOffProviders(context.Background(), testMT, testMID, testLang, 0)
	if err != nil {
		t.Fatalf("BackedOffProviders: %v", err)
	}
	if len(backed) != 0 {
		t.Errorf("triple backoff after save = %v, want none (cleared on success)", backed)
	}

	// The unrelated triple's backoff survives.
	other, err := db.BackedOffProviders(context.Background(), testMT, "other-id", testLang, 0)
	if err != nil {
		t.Fatalf("BackedOffProviders(other): %v", err)
	}
	if len(other) != 1 {
		t.Errorf("unrelated triple backoff = %v, want preserved", other)
	}

	// The due index and attempts counter reflect only the surviving row.
	if n := dueIndexLen(t, db); n != 1 {
		t.Errorf("ix_attempts_due entries = %d, want 1 (only the unrelated triple)", n)
	}
	var attempts int64
	_ = db.db.View(func(tx *bolt.Tx) error { attempts = readAttemptCount(tx); return nil })
	if attempts != 1 {
		t.Errorf("attempts counter = %d, want 1", attempts)
	}
}

// TestSaveDownload_manualAppendsWithPathOrdinal asserts manual saves always
// append new rows (the lock), store rec.Path verbatim, and that the stored
// row's ordinal equals the ordinal encoded in rec.Path, even under interleaved
// manual saves for the same triple (Requirements 4.1, 4.5).
func TestSaveDownload_manualAppendsWithPathOrdinal(t *testing.T) {
	db, _ := openTemp(t)

	manualRec := func(path string, ordinal int) *api.DownloadRecord {
		return &api.DownloadRecord{
			MediaType:    testMT,
			MediaID:      testMID,
			Language:     testLang,
			ProviderName: testProv,
			ReleaseName:  "Manual.Release",
			Path:         path,
			Score:        70,
			Meta:         &api.DownloadMeta{Title: "Test", VideoPath: "/media/test.mkv", Manual: true},
		}
	}

	// Two interleaved manual downloads with distinct ordinals in their paths.
	paths := []struct {
		path    string
		ordinal int
	}{
		{"/media/test.fr.1.srt", 1},
		{"/media/test.fr.2.srt", 2},
	}
	for _, p := range paths {
		if err := db.SaveDownload(context.Background(), manualRec(p.path, p.ordinal)); err != nil {
			t.Fatalf("SaveDownload(%s): %v", p.path, err)
		}
	}

	auto, manual := partitionRows(readTripleRows(t, db, testMT, testMID, testLang))
	if len(auto) != 0 {
		t.Errorf("auto rows = %d, want 0 (manual saves only)", len(auto))
	}
	if len(manual) != 2 {
		t.Fatalf("manual rows = %d, want 2 (each manual save appends)", len(manual))
	}

	// Each stored row's path ordinal must equal the ordinal in the path the
	// handler supplied (stored verbatim).
	byOrdinal := map[int]string{}
	for _, r := range manual {
		ord, ok := parseManualOrdinal(r.Path)
		if !ok {
			t.Errorf("stored manual path %q has no parseable ordinal", r.Path)
			continue
		}
		byOrdinal[ord] = r.Path
	}
	for _, p := range paths {
		if got := byOrdinal[p.ordinal]; got != p.path {
			t.Errorf("ordinal %d: stored path = %q, want %q", p.ordinal, got, p.path)
		}
	}
}

// TestSaveDownload_manualDoesNotTouchAutoRow asserts a manual save appends
// alongside an existing auto row without modifying or deleting it, so auto and
// manual rows coexist for the same triple (Requirement 4.1).
func TestSaveDownload_manualDoesNotTouchAutoRow(t *testing.T) {
	db, _ := openTemp(t)

	if err := db.SaveDownload(context.Background(), autoRec(testProv, "Auto.Release", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("auto SaveDownload: %v", err)
	}
	autoBefore, _ := partitionRows(readTripleRows(t, db, testMT, testMID, testLang))
	autoID := autoBefore[0].ID

	manual := &api.DownloadRecord{
		MediaType: testMT, MediaID: testMID, Language: testLang,
		ProviderName: api.ProviderNameSubDL, ReleaseName: "Manual.Release",
		Path: "/media/test.fr.1.srt", Score: 50,
		Meta: &api.DownloadMeta{Manual: true},
	}
	if err := db.SaveDownload(context.Background(), manual); err != nil {
		t.Fatalf("manual SaveDownload: %v", err)
	}

	auto, manualRows := partitionRows(readTripleRows(t, db, testMT, testMID, testLang))
	if len(auto) != 1 || auto[0].ID != autoID {
		t.Errorf("auto row changed: %+v, want preserved id %d", auto, autoID)
	}
	if auto[0].ReleaseName != "Auto.Release" || auto[0].Score != 80 {
		t.Errorf("auto row mutated by manual save: %+v", auto[0])
	}
	if len(manualRows) != 1 {
		t.Errorf("manual rows = %d, want 1", len(manualRows))
	}
}

// TestSaveDownload_indexAndCounterMaintained asserts the surrogate id, the
// ix_state_triple projection, and the downloads counter are all maintained by
// the putState chokepoint after a save (Requirement 8.1, 18.x projection).
func TestSaveDownload_indexAndCounterMaintained(t *testing.T) {
	db, _ := openTemp(t)

	if err := db.SaveDownload(context.Background(), autoRec(testProv, "Release.A", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	rows := readTripleRows(t, db, testMT, testMID, testLang)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	wantID := rows[0].ID

	err := db.db.View(func(tx *bolt.Tx) error {
		// downloads counter == 1.
		if got := readDownloadCount(tx); got != 1 {
			t.Errorf("downloads counter = %d, want 1", got)
		}
		// ix_state_triple has exactly one entry whose projection matches the row.
		idx := tx.Bucket([]byte(bucketIxStateTriple))
		prefix := triplePrefix(testMT, testMID, testLang)
		n := 0
		c := idx.Cursor()
		for k, v := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = c.Next() {
			n++
			id, ok := stateTripleKeyID(k)
			if !ok || id != wantID {
				t.Errorf("triple index key id = (%d, ok=%v), want %d", id, ok, wantID)
			}
			manual, score, provider, ok := decodeStateProjection(v)
			if !ok {
				t.Errorf("projection decode failed for %v", v)
			}
			if manual || score != 80 || provider != testProv {
				t.Errorf("projection = (manual=%v, score=%d, provider=%s), want (false, 80, %s)",
					manual, score, provider, testProv)
			}
		}
		if n != 1 {
			t.Errorf("ix_state_triple entries = %d, want 1", n)
		}
		// ix_state_imported and ix_state_video each have one entry.
		if got := indexLen(tx, bucketIxStateImported); got != 1 {
			t.Errorf("ix_state_imported entries = %d, want 1", got)
		}
		if got := indexLen(tx, bucketIxStateVideo); got != 1 {
			t.Errorf("ix_state_video entries = %d, want 1", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
}

// hasPrefix is a tiny local alias to avoid importing bytes solely for the test
// cursor walk above.
func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}

// indexLen counts entries in a named index bucket.
func indexLen(tx *bolt.Tx, bucket string) int {
	n := 0
	_ = tx.Bucket([]byte(bucket)).ForEach(func(_, _ []byte) error { n++; return nil })
	return n
}

// TestParseManualOrdinal exercises the ordinal extraction across the path forms
// the handler produces (standard, HI, forced variants) and the no-ordinal case.
func TestParseManualOrdinal(t *testing.T) {
	cases := []struct {
		path string
		want int
		ok   bool
	}{
		{"/media/movie.fr.2.srt", 2, true},
		{"/media/movie.fr.hi.3.srt", 3, true},
		{"/media/movie.fr.forced.10.srt", 10, true},
		{"movie.en.1.srt", 1, true},
		{"/media/movie.fr.9.srt", 9, true},  // ordinal at the upper digit boundary ('9')
		{"123.srt", 123, true},              // all-digit basename: the scan must stop at index 0, not read s[-1]
		{"/media/movie.fr.srt", 0, false},   // auto file, no ordinal
		{"/media/movie.fr", 0, false},
	}
	for _, c := range cases {
		got, ok := parseManualOrdinal(c.path)
		if got != c.want || ok != c.ok {
			t.Errorf("parseManualOrdinal(%q) = (%d, %v), want (%d, %v)", c.path, got, ok, c.want, c.ok)
		}
	}
}
