package boltstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file covers the copy-to-new-file step kind (Requirement 4): the
// offline swap state machine carries every bucket, record, and sequence into
// the destination through the step's transform, stamps the destination before
// the swap, cleans up after itself, and leaves the original untouched when
// anything fails before the rename.

// copyTempFiles globs the swap machine's temp files next to the db (the
// deterministic <db>.migrate path; the wildcard also catches any stray
// suffixed leftover from an older naming scheme).
func copyTempFiles(t *testing.T, dbPath string) []string {
	t.Helper()
	matches, err := filepath.Glob(dbPath + ".migrate*")
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	return matches
}

// copyDomains returns an injected core-domain pair whose single ladder step is
// a copy-kind rewrite with the given transform.
func copyDomains(transform copyTransform) (*migrationDomain, *migrationDomain) {
	core := testCoreDomain(2, []migration{{
		from: 1, to: 2, kind: migrateCopy, transform: transform,
	}})
	return core, testAuthDomain(1, nil)
}

// mapDomains is copyDomains' bucket-map sibling: the single copy step carries
// the given bucket map and an identity record transform.
func mapDomains(mapBucket copyBucketMap) (*migrationDomain, *migrationDomain) {
	core := testCoreDomain(2, []migration{{
		from: 1, to: 2, kind: migrateCopy, mapBucket: mapBucket,
	}})
	return core, testAuthDomain(1, nil)
}

// plantLegacyBucket creates a non-schema bucket with the given records and
// sequence in the closed db at path — the shape of a bucket a future format
// rewrite renames or merges away.
func plantLegacyBucket(t *testing.T, path, name string, seq uint64, records map[string]string) {
	t.Helper()
	rawUpdate(t, path, func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket([]byte(name))
		if err != nil {
			return err
		}
		if err := b.SetSequence(seq); err != nil {
			return err
		}
		for k, v := range records {
			if err := b.Put([]byte(k), []byte(v)); err != nil {
				return err
			}
		}
		return nil
	})
}

// allBucketSnapshots copies every bucket's raw contents plus each bucket's
// sequence, for whole-file equality assertions across the swap.
func allBucketSnapshots(t *testing.T, path string) (contents map[string]map[string]string, seqs map[string]uint64) {
	t.Helper()
	contents = map[string]map[string]string{}
	seqs = map[string]uint64{}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer func() { _ = db.Close() }()
	err = db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			contents[string(name)] = bucketMap(tx, string(name))
			seqs[string(name)] = b.Sequence()
			return nil
		})
	})
	if err != nil {
		t.Fatalf("snapshot buckets: %v", err)
	}
	return contents, seqs
}

// TestMigrate_copyStepIdentity proves an identity copy step (nil transform)
// carries EVERY bucket, record, and bucket sequence into the swapped-in file,
// advances only its own stamp (the auth stamp byte-identical), removes the
// temp file, writes the snapshot, and returns a fully usable handle.
func TestMigrate_copyStepIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	fx := seedResetFixture(t, path)
	wantContents, wantSeqs := allBucketSnapshots(t, path)
	authStampBefore := rawReadStamp(t, path, metaKeyAuthSchemaVersion)

	core, auth := copyDomains(nil)
	db, err := openWithDomains(path, core, auth)
	if err != nil {
		t.Fatalf("copy migration open: %v", err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = db.Close(ctx) })

	// Whole-file equality: every bucket's contents and sequence carried over;
	// only the core stamp inside meta may differ.
	err = db.db.View(func(tx *bolt.Tx) error {
		seen := map[string]bool{}
		verr := tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			seen[string(name)] = true
			want, ok := wantContents[string(name)]
			if !ok {
				t.Errorf("bucket %q appeared out of nowhere in the copied file", name)
				return nil
			}
			if got := b.Sequence(); got != wantSeqs[string(name)] {
				t.Errorf("bucket %q sequence = %d, want %d (Bucket.Sequence must be preserved)", name, got, wantSeqs[string(name)])
			}
			got := bucketMap(tx, string(name))
			for k, wv := range want {
				if string(name) == bucketMeta && k == string(metaKeyCoreSchemaVersion) {
					continue // the migrating domain's stamp legitimately advances
				}
				if gv, ok := got[k]; !ok || gv != wv {
					t.Errorf("bucket %q entry %x changed across the copy", name, k)
				}
			}
			if len(got) != len(want) {
				t.Errorf("bucket %q: %d entries, want %d", name, len(got), len(want))
			}
			return nil
		})
		for name := range wantContents {
			if !seen[name] {
				t.Errorf("bucket %q lost in the copy", name)
			}
		}
		return verr
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if got := rawStampInOpenDB(t, db, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(2)) {
		t.Errorf("core stamp = %x, want be64(2)", got)
	}
	if got := rawStampInOpenDB(t, db, metaKeyAuthSchemaVersion); string(got) != string(authStampBefore) {
		t.Errorf("auth stamp changed across a core copy step: %x -> %x", authStampBefore, got)
	}
	if temps := copyTempFiles(t, path); len(temps) != 0 {
		t.Errorf("temp files left behind: %v", temps)
	}
	if snaps := migrationSnapshots(t, path); len(snaps) != 1 {
		t.Errorf("snapshots = %v, want one", snaps)
	}

	// The swapped-in handle is live: reads see the old rows, writes stick, and
	// a fresh insert allocates past the preserved sequence.
	locked, err := db.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt1", "en", api.VariantStandard)
	if err != nil || !locked {
		t.Errorf("IsManuallyLocked after copy = (%v, %v), want locked", locked, err)
	}
	rec := &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt9", Language: "en",
		ProviderName: api.ProviderNameOpenSubtitles, Path: "/m/tt9.en.srt", Score: 10,
		Meta: &api.DownloadMeta{Title: "T9", VideoPath: "/m/tt9.mkv"},
	}
	if err := db.SaveDownload(ctx, rec); err != nil {
		t.Fatalf("SaveDownload after copy: %v", err)
	}
	entries, err := db.GetState(ctx, &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState after copy: %v", err)
	}
	wantRows := len(fx.manualRows) + len(fx.autoRows) + 1
	if len(entries) != wantRows {
		t.Errorf("state rows after copy + insert = %d, want %d", len(entries), wantRows)
	}
	preservedSeq := int64(wantSeqs[bucketSubtitleState])
	for _, e := range entries {
		if e.MediaID == "tt9" && e.ID <= preservedSeq {
			t.Errorf("fresh row id %d collides with the preserved sequence %d", e.ID, preservedSeq)
		}
	}
}

// TestMigrate_copyStepTransform proves the step's transform is applied during
// the walk: a targeted value rewrite lands, a nil-key return drops the
// record, and untouched records survive.
func TestMigrate_copyStepTransform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedResetFixture(t, path)

	// Rewrite one offset (+1) and drop the other; everything else unchanged.
	transform := func(bucketPath []string, k, v []byte) ([]byte, []byte, error) {
		if len(bucketPath) == 1 && bucketPath[0] == bucketSyncOffsets {
			switch string(k) {
			case "/m/tt1.en.1.srt":
				off, _ := kv.DecodeBe64(v)
				return k, kv.Be64(off + 1), nil
			case "/m/tt1.fr.srt":
				return nil, nil, nil // drop
			}
		}
		return k, v, nil
	}
	core, auth := copyDomains(transform)
	db, err := openWithDomains(path, core, auth)
	if err != nil {
		t.Fatalf("copy migration open: %v", err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = db.Close(ctx) })

	if got, err := db.GetSyncOffset(ctx, "/m/tt1.en.1.srt"); err != nil || got != 251 {
		t.Errorf("transformed offset = (%d, %v), want 251", got, err)
	}
	if got, err := db.GetSyncOffset(ctx, "/m/tt1.fr.srt"); err != nil || got != 0 {
		t.Errorf("dropped offset = (%d, %v), want 0 (record dropped)", got, err)
	}
	locked, err := db.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt1", "en", api.VariantStandard)
	if err != nil || !locked {
		t.Errorf("IsManuallyLocked after transform copy = (%v, %v), want locked", locked, err)
	}
}

// TestMigrate_copyStepFailureLeavesOriginal proves a pre-rename failure (a
// failing transform) aborts the open, removes the temp file, and leaves the
// original file byte-identical — with the error pointing at the snapshot.
func TestMigrate_copyStepFailureLeavesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedResetFixture(t, path)
	wantContents, wantSeqs := allBucketSnapshots(t, path)

	boom := errors.New("boom")
	failing := func(bucketPath []string, k, v []byte) ([]byte, []byte, error) {
		if len(bucketPath) == 1 && bucketPath[0] == bucketSubtitleState {
			return nil, nil, boom
		}
		return k, v, nil
	}
	core, auth := copyDomains(failing)
	db, err := openWithDomains(path, core, auth)
	if err == nil {
		_ = db.Close(context.Background())
		t.Fatal("copy migration with failing transform: error = nil, want failure")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the transform failure", err)
	}
	if !strings.Contains(err.Error(), "database unchanged") || !strings.Contains(err.Error(), ".pre-c1-a1-") {
		t.Errorf("error %q should state the database is unchanged and point at the snapshot", err)
	}
	if temps := copyTempFiles(t, path); len(temps) != 0 {
		t.Errorf("temp files left behind after pre-rename failure: %v", temps)
	}

	gotContents, gotSeqs := allBucketSnapshots(t, path)
	if fmt.Sprint(gotSeqs) != fmt.Sprint(wantSeqs) {
		t.Errorf("bucket sequences changed by a failed copy: %v -> %v", wantSeqs, gotSeqs)
	}
	for name, want := range wantContents {
		got := gotContents[name]
		if len(got) != len(want) {
			t.Errorf("bucket %q: %d entries after failed copy, want %d", name, len(got), len(want))
			continue
		}
		for k, wv := range want {
			if gv, ok := got[k]; !ok || gv != wv {
				t.Errorf("bucket %q entry %x changed by a failed copy", name, k)
			}
		}
	}
}

// TestMigrate_copyStepRenamesBucket proves the bucket-level rewrite capability
// (Requirement 4.1): a copy step's bucket map relocates a whole bucket —
// records AND sequence — to a new name, the old name does not survive the
// swap, and the untouched buckets, stamps, and store behavior carry over
// exactly as in an identity copy.
func TestMigrate_copyStepRenamesBucket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)
	plantLegacyBucket(t, path, "legacy_names", 42, map[string]string{"k1": "v1", "k2": "v2"})
	_, wantSeqs := allBucketSnapshots(t, path)
	authStampBefore := rawReadStamp(t, path, metaKeyAuthSchemaVersion)

	core, auth := mapDomains(func(p []string) ([]string, error) {
		if len(p) == 1 && p[0] == "legacy_names" {
			return []string{"modern_names"}, nil
		}
		return p, nil
	})
	db, err := openWithDomains(path, core, auth)
	if err != nil {
		t.Fatalf("rename copy migration open: %v", err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = db.Close(ctx) })

	err = db.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("legacy_names")) != nil {
			t.Error("renamed bucket still exists under its old name")
		}
		nb := tx.Bucket([]byte("modern_names"))
		if nb == nil {
			t.Fatal("renamed bucket missing under its new name")
		}
		if got := nb.Sequence(); got != 42 {
			t.Errorf("renamed bucket sequence = %d, want 42 (sequence must survive the rename)", got)
		}
		got := bucketMap(tx, "modern_names")
		if len(got) != 2 || got["k1"] != "v1" || got["k2"] != "v2" {
			t.Errorf("renamed bucket records = %v, want k1/v1 and k2/v2", got)
		}
		// An untouched bucket's sequence survives identically.
		if got := tx.Bucket([]byte(bucketSubtitleState)).Sequence(); got != wantSeqs[bucketSubtitleState] {
			t.Errorf("subtitle_state sequence = %d, want %d", got, wantSeqs[bucketSubtitleState])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if got := rawStampInOpenDB(t, db, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(2)) {
		t.Errorf("core stamp = %x, want be64(2)", got)
	}
	if got := rawStampInOpenDB(t, db, metaKeyAuthSchemaVersion); string(got) != string(authStampBefore) {
		t.Errorf("auth stamp changed across a rename copy step: %x -> %x", authStampBefore, got)
	}
	if locked, err := db.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt1", "en", api.VariantStandard); err != nil || !locked {
		t.Errorf("IsManuallyLocked after rename copy = (%v, %v), want locked", locked, err)
	}
	if temps := copyTempFiles(t, path); len(temps) != 0 {
		t.Errorf("temp files left behind: %v", temps)
	}
}

// TestMigrate_copyStepMergesBuckets proves the merge half of the bucket-level
// rewrite capability: two source buckets mapped to one destination combine
// their records, the destination keeps the HIGHEST merged sequence (so future
// allocations cannot collide with any merged-in surrogate key), neither
// source name survives, and stamps carry over.
func TestMigrate_copyStepMergesBuckets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)
	plantLegacyBucket(t, path, "legacy_a", 5, map[string]string{"a1": "v1", "a2": "v2"})
	plantLegacyBucket(t, path, "legacy_b", 9, map[string]string{"b3": "v3"})
	authStampBefore := rawReadStamp(t, path, metaKeyAuthSchemaVersion)

	core, auth := mapDomains(func(p []string) ([]string, error) {
		if len(p) == 1 && (p[0] == "legacy_a" || p[0] == "legacy_b") {
			return []string{"merged"}, nil
		}
		return p, nil
	})
	db, err := openWithDomains(path, core, auth)
	if err != nil {
		t.Fatalf("merge copy migration open: %v", err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = db.Close(ctx) })

	err = db.db.View(func(tx *bolt.Tx) error {
		for _, gone := range []string{"legacy_a", "legacy_b"} {
			if tx.Bucket([]byte(gone)) != nil {
				t.Errorf("merged source bucket %q still exists", gone)
			}
		}
		mb := tx.Bucket([]byte("merged"))
		if mb == nil {
			t.Fatal("merge destination bucket missing")
		}
		if got := mb.Sequence(); got != 9 {
			t.Errorf("merged bucket sequence = %d, want the highest merged-in 9", got)
		}
		got := bucketMap(tx, "merged")
		want := map[string]string{"a1": "v1", "a2": "v2", "b3": "v3"}
		if len(got) != len(want) {
			t.Errorf("merged bucket holds %d records, want %d", len(got), len(want))
		}
		for k, wv := range want {
			if got[k] != wv {
				t.Errorf("merged record %q = %q, want %q", k, got[k], wv)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if got := rawStampInOpenDB(t, db, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(2)) {
		t.Errorf("core stamp = %x, want be64(2)", got)
	}
	if got := rawStampInOpenDB(t, db, metaKeyAuthSchemaVersion); string(got) != string(authStampBefore) {
		t.Errorf("auth stamp changed across a merge copy step: %x -> %x", authStampBefore, got)
	}
	if temps := copyTempFiles(t, path); len(temps) != 0 {
		t.Errorf("temp files left behind: %v", temps)
	}
}
