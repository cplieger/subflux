package boltstore

import (
	"bytes"
	"context"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
	"pgregory.net/rapid"
)

// This file covers the task-5.1 subtitle_files domain: RecordSubtitleFiles
// diff-sync (insert/update/delete + changed report, Requirement 5.1),
// UpsertSubtitleFile / DeleteSubtitleFile single-row ops (Requirement 15.7),
// GetSubtitleFiles listing with the prefix semantics and the subtitle_state
// score/video_path join (Requirement 5.2), the O(1) TotalSubtitleFiles counter
// (Requirement 18.1), and per-media coverage derivable from KEYS without
// decoding values (Requirement 18.2).

const (
	covMT  = api.MediaTypeMovie
	vStd   = api.Variant("standard")
	vHI    = api.Variant("hi")
	srcExt = api.SourceExternal
	srcEmb = api.SourceEmbedded
)

// subFile is a terse api.SubtitleFile constructor for tests.
func subFile(lang string, variant api.Variant, source api.SubtitleSource, codec, path string) api.SubtitleFile {
	return api.SubtitleFile{Language: lang, Variant: variant, Source: source, Codec: codec, Path: path}
}

// listFiles is a context-free GetSubtitleFiles for tests.
func listFiles(t *testing.T, db *DB, mt api.MediaType, prefix string) []api.SubtitleEntry {
	t.Helper()
	rows, err := db.GetSubtitleFiles(context.Background(), mt, prefix)
	if err != nil {
		t.Fatalf("GetSubtitleFiles(%q, %q): %v", mt, prefix, err)
	}
	return rows
}

// totalFiles is a context-free TotalSubtitleFiles for tests.
func totalFiles(t *testing.T, db *DB) int {
	t.Helper()
	n, err := db.TotalSubtitleFiles(context.Background())
	if err != nil {
		t.Fatalf("TotalSubtitleFiles: %v", err)
	}
	return n
}

// countFileRowsRaw counts subtitle_files rows by a raw bucket walk, the
// ground-truth COUNT(*) the maintained counter must equal.
func countFileRowsRaw(t *testing.T, db *DB) int {
	t.Helper()
	n := 0
	if err := db.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketSubtitleFiles)).ForEach(func(_, _ []byte) error {
			n++
			return nil
		})
	}); err != nil {
		t.Fatalf("countFileRowsRaw: %v", err)
	}
	return n
}

// setFileOffset white-box writes a subtitle_files row's offset_ms via the same
// chokepoint production uses, so the offset-preservation tests have a non-zero
// offset to preserve (RecordSubtitleFiles/UpsertSubtitleFile insert at 0; the
// offset is set elsewhere, e.g. the sync path).
func setFileOffset(t *testing.T, db *DB, mt api.MediaType, mid, lang string, variant api.Variant, source api.SubtitleSource, path string, offset int64) {
	t.Helper()
	key := subtitleFileKey(mt, mid, lang, variant, source, path)
	if err := db.db.Update(func(tx *bolt.Tx) error {
		var fr fileRec
		if old := tx.Bucket([]byte(bucketSubtitleFiles)).Get(key); old != nil {
			_ = kv.Decode(old, &fr)
		}
		fr.OffsetMs = offset
		return putSubtitleFile(tx, key, &fr)
	}); err != nil {
		t.Fatalf("setFileOffset: %v", err)
	}
}

// --- RecordSubtitleFiles diff-sync ---

func TestRecordSubtitleFiles_inserts_and_lists(t *testing.T) {
	db, _ := openTemp(t)
	files := []api.SubtitleFile{
		subFile("en", vStd, srcExt, "srt", "/m/x.en.srt"),
		subFile("fr", vStd, srcExt, "srt", "/m/x.fr.srt"),
	}

	changed, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1", files)
	if err != nil {
		t.Fatalf("RecordSubtitleFiles: %v", err)
	}
	if !changed {
		t.Error("changed = false on first insert, want true")
	}

	rows := listFiles(t, db, covMT, "tmdb-1")
	if len(rows) != 2 {
		t.Fatalf("listed %d rows, want 2", len(rows))
	}
	// Key-derived fields must be exact.
	if rows[0].Language != "en" || rows[0].Path != "/m/x.en.srt" || rows[0].Source != "external" {
		t.Errorf("row[0] = %+v, want en/external/x.en.srt", rows[0])
	}
	if rows[1].Language != "fr" {
		t.Errorf("row[1].Language = %q, want fr", rows[1].Language)
	}
	if totalFiles(t, db) != 2 {
		t.Errorf("TotalSubtitleFiles = %d, want 2", totalFiles(t, db))
	}
}

func TestRecordSubtitleFiles_no_change_returns_false(t *testing.T) {
	db, _ := openTemp(t)
	files := []api.SubtitleFile{subFile("en", vStd, srcExt, "srt", "/m/x.en.srt")}

	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1", files); err != nil {
		t.Fatalf("first RecordSubtitleFiles: %v", err)
	}
	changed, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1", files)
	if err != nil {
		t.Fatalf("second RecordSubtitleFiles: %v", err)
	}
	if changed {
		t.Error("changed = true on identical set, want false")
	}
	if totalFiles(t, db) != 1 {
		t.Errorf("TotalSubtitleFiles = %d, want 1 (no duplicate insert)", totalFiles(t, db))
	}
}

func TestRecordSubtitleFiles_codec_change_updates_and_preserves_offset(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1",
		[]api.SubtitleFile{subFile("en", vStd, srcExt, "srt", "/m/x.en.srt")}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Give the row a non-zero offset that a codec update must preserve.
	setFileOffset(t, db, covMT, "tmdb-1", "en", vStd, srcExt, "/m/x.en.srt", 1500)

	changed, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1",
		[]api.SubtitleFile{subFile("en", vStd, srcExt, "ass", "/m/x.en.srt")})
	if err != nil {
		t.Fatalf("RecordSubtitleFiles: %v", err)
	}
	if !changed {
		t.Error("changed = false on codec change, want true")
	}
	rows := listFiles(t, db, covMT, "tmdb-1")
	if len(rows) != 1 {
		t.Fatalf("listed %d rows, want 1 (update, not insert)", len(rows))
	}
	if rows[0].Codec != "ass" {
		t.Errorf("codec = %q, want ass (updated)", rows[0].Codec)
	}
	if rows[0].OffsetMs != 1500 {
		t.Errorf("offset_ms = %d, want 1500 (preserved across codec update)", rows[0].OffsetMs)
	}
	if totalFiles(t, db) != 1 {
		t.Errorf("TotalSubtitleFiles = %d, want 1 (update keeps count)", totalFiles(t, db))
	}
}

func TestRecordSubtitleFiles_deletes_stale(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1", []api.SubtitleFile{
		subFile("en", vStd, srcExt, "srt", "/m/x.en.srt"),
		subFile("fr", vStd, srcExt, "srt", "/m/x.fr.srt"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Re-record with only the fr file: the en row must be deleted.
	changed, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1",
		[]api.SubtitleFile{subFile("fr", vStd, srcExt, "srt", "/m/x.fr.srt")})
	if err != nil {
		t.Fatalf("RecordSubtitleFiles: %v", err)
	}
	if !changed {
		t.Error("changed = false when a row was deleted, want true")
	}
	rows := listFiles(t, db, covMT, "tmdb-1")
	if len(rows) != 1 || rows[0].Language != "fr" {
		t.Fatalf("rows = %+v, want exactly the fr row", rows)
	}
	if totalFiles(t, db) != 1 {
		t.Errorf("TotalSubtitleFiles = %d, want 1 after stale delete", totalFiles(t, db))
	}
}

func TestRecordSubtitleFiles_empty_clears_existing(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1",
		[]api.SubtitleFile{subFile("en", vStd, srcExt, "srt", "/m/x.en.srt")}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	changed, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1", nil)
	if err != nil {
		t.Fatalf("RecordSubtitleFiles(nil): %v", err)
	}
	if !changed {
		t.Error("changed = false when clearing all rows, want true")
	}
	if rows := listFiles(t, db, covMT, "tmdb-1"); len(rows) != 0 {
		t.Errorf("rows = %d, want 0 after clear", len(rows))
	}
	if totalFiles(t, db) != 0 {
		t.Errorf("TotalSubtitleFiles = %d, want 0", totalFiles(t, db))
	}
}

func TestRecordSubtitleFiles_other_media_unaffected(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1",
		[]api.SubtitleFile{subFile("en", vStd, srcExt, "srt", "/m/a.en.srt")}); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-2",
		[]api.SubtitleFile{subFile("fr", vStd, srcExt, "srt", "/m/b.fr.srt")}); err != nil {
		t.Fatalf("seed B: %v", err)
	}
	// Clearing tmdb-1 must not touch tmdb-2.
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1", nil); err != nil {
		t.Fatalf("clear A: %v", err)
	}
	if rows := listFiles(t, db, covMT, "tmdb-2"); len(rows) != 1 {
		t.Errorf("tmdb-2 rows = %d, want 1 (unaffected)", len(rows))
	}
	if totalFiles(t, db) != 1 {
		t.Errorf("TotalSubtitleFiles = %d, want 1", totalFiles(t, db))
	}
}

// --- UpsertSubtitleFile / DeleteSubtitleFile ---

func TestUpsertSubtitleFile_insert_then_update_preserves_offset(t *testing.T) {
	db, _ := openTemp(t)
	f := subFile("en", vStd, srcExt, "srt", "/m/x.en.srt")
	if err := db.UpsertSubtitleFile(context.Background(), covMT, "tmdb-1", &f); err != nil {
		t.Fatalf("insert upsert: %v", err)
	}
	if totalFiles(t, db) != 1 {
		t.Fatalf("count after insert = %d, want 1", totalFiles(t, db))
	}
	setFileOffset(t, db, covMT, "tmdb-1", "en", vStd, srcExt, "/m/x.en.srt", 750)

	// Update the codec via upsert: offset preserved, no new row.
	f.Codec = "ass"
	if err := db.UpsertSubtitleFile(context.Background(), covMT, "tmdb-1", &f); err != nil {
		t.Fatalf("update upsert: %v", err)
	}
	rows := listFiles(t, db, covMT, "tmdb-1")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (upsert updates in place)", len(rows))
	}
	if rows[0].Codec != "ass" || rows[0].OffsetMs != 750 {
		t.Errorf("row = %+v, want codec=ass offset=750", rows[0])
	}
	if totalFiles(t, db) != 1 {
		t.Errorf("count after update = %d, want 1", totalFiles(t, db))
	}
}

func TestDeleteSubtitleFile_removes_and_noop(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1", []api.SubtitleFile{
		subFile("en", vStd, srcExt, "srt", "/m/x.en.srt"),
		subFile("fr", vStd, srcExt, "srt", "/m/x.fr.srt"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Delete the en row.
	if err := db.DeleteSubtitleFile(context.Background(), covMT, "tmdb-1", "en", vStd, srcExt, "/m/x.en.srt"); err != nil {
		t.Fatalf("DeleteSubtitleFile: %v", err)
	}
	if rows := listFiles(t, db, covMT, "tmdb-1"); len(rows) != 1 || rows[0].Language != "fr" {
		t.Fatalf("rows = %+v, want only fr", rows)
	}
	if totalFiles(t, db) != 1 {
		t.Errorf("count = %d, want 1", totalFiles(t, db))
	}
	// Deleting an absent row is a no-op and does not move the counter.
	if err := db.DeleteSubtitleFile(context.Background(), covMT, "tmdb-1", "de", vStd, srcExt, "/m/x.de.srt"); err != nil {
		t.Fatalf("DeleteSubtitleFile (absent): %v", err)
	}
	if totalFiles(t, db) != 1 {
		t.Errorf("count after no-op delete = %d, want 1", totalFiles(t, db))
	}
}

// --- GetSubtitleFiles prefix semantics ---

func TestGetSubtitleFiles_prefix_modes(t *testing.T) {
	db, _ := openTemp(t)
	// Episodes for two series plus a prefix-collision id (tvdb-1 vs tvdb-12).
	seed := map[string][]api.SubtitleFile{
		"tvdb-111-s01e01": {subFile("en", vStd, srcExt, "srt", "/m/111e01.en.srt")},
		"tvdb-111-s01e02": {subFile("en", vStd, srcExt, "srt", "/m/111e02.en.srt")},
		"tvdb-222-s01e01": {subFile("fr", vStd, srcExt, "srt", "/m/222e01.fr.srt")},
	}
	for mid, files := range seed {
		if _, err := db.RecordSubtitleFiles(context.Background(), api.MediaTypeEpisode, mid, files); err != nil {
			t.Fatalf("seed %s: %v", mid, err)
		}
	}

	// Empty filter: all episode rows.
	if rows := listFiles(t, db, api.MediaTypeEpisode, ""); len(rows) != 3 {
		t.Errorf("all rows = %d, want 3", len(rows))
	}
	// Prefix filter ending in "-": only tvdb-111-*.
	if rows := listFiles(t, db, api.MediaTypeEpisode, "tvdb-111-"); len(rows) != 2 {
		t.Errorf("tvdb-111- rows = %d, want 2", len(rows))
	}
	// Exact filter (no trailing "-"): only that exact media id.
	if rows := listFiles(t, db, api.MediaTypeEpisode, "tvdb-111-s01e01"); len(rows) != 1 {
		t.Errorf("exact rows = %d, want 1", len(rows))
	}
}

func TestGetSubtitleFiles_exact_match_no_prefix_collision(t *testing.T) {
	db, _ := openTemp(t)
	// "tmdb-1" must not match "tmdb-12" under an EXACT (no trailing -) query.
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1",
		[]api.SubtitleFile{subFile("en", vStd, srcExt, "srt", "/m/1.en.srt")}); err != nil {
		t.Fatalf("seed tmdb-1: %v", err)
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-12",
		[]api.SubtitleFile{subFile("fr", vStd, srcExt, "srt", "/m/12.fr.srt")}); err != nil {
		t.Fatalf("seed tmdb-12: %v", err)
	}
	rows := listFiles(t, db, covMT, "tmdb-1")
	if len(rows) != 1 || rows[0].MediaID != "tmdb-1" {
		t.Errorf("exact tmdb-1 rows = %+v, want only tmdb-1", rows)
	}
}

func TestGetSubtitleFiles_ordered_by_media_id_language_variant_source(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1", []api.SubtitleFile{
		subFile("fr", vStd, srcExt, "srt", "/m/x.fr.srt"),
		subFile("en", vHI, srcExt, "srt", "/m/x.en.hi.srt"),
		subFile("en", vStd, srcExt, "srt", "/m/x.en.srt"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rows := listFiles(t, db, covMT, "tmdb-1")
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = r.Language + "/" + r.Variant
	}
	// Sorted by language then variant: en/hi, en/standard, fr/standard.
	want := []string{"en/hi", "en/standard", "fr/standard"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// --- GetSubtitleFiles score / video_path join ---

func TestGetSubtitleFiles_score_and_videopath_from_auto_state(t *testing.T) {
	db, _ := openTemp(t)
	// An auto download for the triple feeds score + video_path into the join.
	rec := &api.DownloadRecord{
		MediaType: covMT, MediaID: "tmdb-500", Language: "en",
		ProviderName: testProv, ReleaseName: "Rel.A", Path: "/m/x.en.srt", Score: 90,
		Meta: &api.DownloadMeta{VideoPath: "/m/x.mkv", Manual: false},
	}
	if err := db.SaveDownload(context.Background(), rec); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-500", []api.SubtitleFile{
		subFile("en", vStd, srcExt, "srt", "/m/x.en.srt"),
		subFile("en", vStd, srcEmb, "subrip", ""), // embedded: forced score 0, empty video_path
	}); err != nil {
		t.Fatalf("RecordSubtitleFiles: %v", err)
	}

	rows := listFiles(t, db, covMT, "tmdb-500")
	var ext, emb *api.SubtitleEntry
	for i := range rows {
		switch rows[i].Source {
		case "external":
			ext = &rows[i]
		case "embedded":
			emb = &rows[i]
		}
	}
	if ext == nil || emb == nil {
		t.Fatalf("expected one external and one embedded row, got %+v", rows)
	}
	if ext.Score != 90 || ext.VideoPath != "/m/x.mkv" {
		t.Errorf("external join = score %d / video %q, want 90 / /m/x.mkv", ext.Score, ext.VideoPath)
	}
	if emb.Score != 0 || emb.VideoPath != "" {
		t.Errorf("embedded join = score %d / video %q, want 0 / \"\"", emb.Score, emb.VideoPath)
	}
}

func TestGetSubtitleFiles_no_auto_state_defaults_to_zero(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1",
		[]api.SubtitleFile{subFile("en", vStd, srcExt, "srt", "/m/x.en.srt")}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rows := listFiles(t, db, covMT, "tmdb-1")
	if len(rows) != 1 || rows[0].Score != 0 || rows[0].VideoPath != "" {
		t.Errorf("rows = %+v, want one row with score 0 and empty video_path", rows)
	}
}

// --- Coverage from keys without decoding values (Requirement 18.2) ---

// TestCoverage_fromKeysWithoutValueDecode proves per-media coverage (the set of
// (language, variant) tracked for a media item) is derivable from subtitle_files
// KEYS alone: it walks the bucket and parses keys with NO call to the value
// codec, then asserts the derived set matches what GetSubtitleFiles reports.
func TestCoverage_fromKeysWithoutValueDecode(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1", []api.SubtitleFile{
		subFile("en", vStd, srcExt, "srt", "/m/x.en.srt"),
		subFile("en", vHI, srcExt, "srt", "/m/x.en.hi.srt"),
		subFile("fr", vStd, srcExt, "srt", "/m/x.fr.srt"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	keyCoverage := make(map[string]bool)
	if err := db.db.View(func(tx *bolt.Tx) error {
		prefix := mediaPrefix(covMT, "tmdb-1")
		c := tx.Bucket([]byte(bucketSubtitleFiles)).Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			fk, ok := parseSubFileKey(k)
			if !ok {
				t.Errorf("key %x failed to parse", k)
				continue
			}
			keyCoverage[fk.lang+"/"+fk.variant] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("key walk: %v", err)
	}

	want := map[string]bool{"en/standard": true, "en/hi": true, "fr/standard": true}
	if len(keyCoverage) != len(want) {
		t.Fatalf("key-only coverage = %v, want %v", keyCoverage, want)
	}
	for k := range want {
		if !keyCoverage[k] {
			t.Errorf("key-only coverage missing %q", k)
		}
	}
}

// --- TotalSubtitleFiles counter correctness ---

func TestTotalSubtitleFiles_counter_matches_raw_after_ops(t *testing.T) {
	db, _ := openTemp(t)
	if totalFiles(t, db) != 0 {
		t.Fatalf("initial count = %d, want 0", totalFiles(t, db))
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-1", []api.SubtitleFile{
		subFile("en", vStd, srcExt, "srt", "/m/a.en.srt"),
		subFile("fr", vStd, srcExt, "srt", "/m/a.fr.srt"),
	}); err != nil {
		t.Fatalf("record A: %v", err)
	}
	f := subFile("de", vStd, srcExt, "srt", "/m/b.de.srt")
	if err := db.UpsertSubtitleFile(context.Background(), covMT, "tmdb-2", &f); err != nil {
		t.Fatalf("upsert B: %v", err)
	}
	if err := db.DeleteSubtitleFile(context.Background(), covMT, "tmdb-1", "fr", vStd, srcExt, "/m/a.fr.srt"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Counter (O(1)) must equal the raw bucket scan (ground truth).
	if got, raw := totalFiles(t, db), countFileRowsRaw(t, db); got != raw || got != 2 {
		t.Errorf("TotalSubtitleFiles = %d, raw scan = %d, want both 2", got, raw)
	}
}

// --- Property: diff-sync convergence + counter invariant ---

// TestRecordSubtitleFiles_property_convergence drives random RecordSubtitleFiles
// sequences across a small media-id pool and asserts, after every op, that
// GetSubtitleFiles for a media item equals the last set recorded for it, and
// that the O(1) TotalSubtitleFiles counter equals both the model's total and a
// raw bucket scan. This pins the diff-sync invariant (insert/update/delete
// converge to exactly the recorded set) and the counter-maintenance invariant.
func TestRecordSubtitleFiles_property_convergence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db, _ := openTemp(t)

		langs := []string{"en", "fr"}
		variants := []api.Variant{vStd, vHI}
		// model[mediaID][subFileKey] = codec
		model := map[string]map[subFileKey]string{}

		mediaPool := []string{"tmdb-1", "tmdb-2"}

		genFiles := func(rt *rapid.T) []api.SubtitleFile {
			n := rapid.IntRange(0, 4).Draw(rt, "n")
			out := make([]api.SubtitleFile, 0, n)
			for range n {
				lang := rapid.SampledFrom(langs).Draw(rt, "lang")
				variant := rapid.SampledFrom(variants).Draw(rt, "variant")
				external := rapid.Bool().Draw(rt, "external")
				codec := rapid.SampledFrom([]string{"srt", "ass"}).Draw(rt, "codec")
				if external {
					path := "/m/" + lang + "." + string(variant) + ".srt"
					out = append(out, subFile(lang, variant, srcExt, codec, path))
				} else {
					// embedded: empty path, codec is the track codec
					out = append(out, subFile(lang, variant, srcEmb, codec, ""))
				}
			}
			return out
		}

		ops := rapid.IntRange(1, 8).Draw(rt, "ops")
		for range ops {
			mid := rapid.SampledFrom(mediaPool).Draw(rt, "mid")
			files := genFiles(rt)

			if _, err := db.RecordSubtitleFiles(context.Background(), covMT, mid, files); err != nil {
				rt.Fatalf("RecordSubtitleFiles: %v", err)
			}

			// Update the model: last codec wins per key (map collapse).
			want := map[subFileKey]string{}
			for _, f := range files {
				want[subFileKey{f.Language, string(f.Variant), string(f.Source), f.Path}] = f.Codec
			}
			model[mid] = want

			// Assert this media item's listing equals the model set.
			rows := listFiles(t, db, covMT, mid)
			gotKeys := map[subFileKey]string{}
			for _, r := range rows {
				gotKeys[subFileKey{r.Language, r.Variant, r.Source, r.Path}] = r.Codec
			}
			if len(gotKeys) != len(want) {
				rt.Fatalf("mid %s: listed %d rows, model has %d (got %v want %v)", mid, len(gotKeys), len(want), gotKeys, want)
			}
			for k, codec := range want {
				if gotKeys[k] != codec {
					rt.Fatalf("mid %s key %+v: codec %q, want %q", mid, k, gotKeys[k], codec)
				}
			}
		}

		// Global counter invariant.
		total := 0
		for _, m := range model {
			total += len(m)
		}
		if got, raw := totalFiles(t, db), countFileRowsRaw(t, db); got != total || raw != total {
			rt.Fatalf("TotalSubtitleFiles = %d, raw = %d, model total = %d", got, raw, total)
		}
	})
}

// TestGetSubtitleFiles_videopath_from_highest_auto_state asserts the score /
// video_path join takes the HIGHEST-scored auto row when a triple has several
// of them (reconcile can leave more than one auto row; bestAutoStateID selects
// the max, matching CurrentScore). SaveDownload collapses auto rows into one,
// so the multi-row state is injected directly via putStateRow.
func TestGetSubtitleFiles_videopath_from_highest_auto_state(t *testing.T) {
	db, _ := openTemp(t)
	// Three auto rows with the peak score in the middle so neither the first
	// nor the last scanned coincides with the winner.
	putStateRow(t, db, covMT, "tmdb-600", "en", stateRec{Score: 70, Provider: testProv, Path: "/m/x.en.srt", VideoPath: "/m/v70.mkv"})
	putStateRow(t, db, covMT, "tmdb-600", "en", stateRec{Score: 95, Provider: testProv, Path: "/m/x.en.srt", VideoPath: "/m/v95.mkv"})
	putStateRow(t, db, covMT, "tmdb-600", "en", stateRec{Score: 80, Provider: testProv, Path: "/m/x.en.srt", VideoPath: "/m/v80.mkv"})
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-600",
		[]api.SubtitleFile{subFile("en", vStd, srcExt, "srt", "/m/x.en.srt")}); err != nil {
		t.Fatalf("RecordSubtitleFiles: %v", err)
	}

	rows := listFiles(t, db, covMT, "tmdb-600")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Score != 95 || rows[0].VideoPath != "/m/v95.mkv" {
		t.Errorf("join = score %d / video %q, want 95 / /m/v95.mkv (highest auto row)",
			rows[0].Score, rows[0].VideoPath)
	}
}

// TestGetSubtitleFiles_videopath_tie_keeps_first_auto_state asserts the
// score-tie tie-break in the coverage join: when two auto rows share the top
// score, the first in scan order (lowest surrogate id) is the representative,
// so its video_path is the one joined.
func TestGetSubtitleFiles_videopath_tie_keeps_first_auto_state(t *testing.T) {
	db, _ := openTemp(t)
	putStateRow(t, db, covMT, "tmdb-601", "en", stateRec{Score: 80, Provider: testProv, Path: "/m/x.en.srt", VideoPath: "/m/first.mkv"})
	putStateRow(t, db, covMT, "tmdb-601", "en", stateRec{Score: 80, Provider: testProv, Path: "/m/x.en.srt", VideoPath: "/m/second.mkv"})
	if _, err := db.RecordSubtitleFiles(context.Background(), covMT, "tmdb-601",
		[]api.SubtitleFile{subFile("en", vStd, srcExt, "srt", "/m/x.en.srt")}); err != nil {
		t.Fatalf("RecordSubtitleFiles: %v", err)
	}

	rows := listFiles(t, db, covMT, "tmdb-601")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].VideoPath != "/m/first.mkv" {
		t.Errorf("video_path = %q, want /m/first.mkv (tie keeps first-scanned auto row)", rows[0].VideoPath)
	}
}
