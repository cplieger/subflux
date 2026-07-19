package boltstore

import (
	"bytes"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// TestRecordRoundTrip asserts every core-domain record encodes and decodes back
// to an identical value through the typed codec wrappers.
func TestRecordRoundTrip(t *testing.T) {
	t.Run("attemptRec", func(t *testing.T) {
		roundTrip(t, &attemptRec{LastTried: time.Unix(1, 0).UTC(), NextRetry: time.Unix(2, 0).UTC(), Failures: 7})
	})
	t.Run("stateRec", func(t *testing.T) {
		roundTrip(t, &stateRec{ID: 1, MediaType: api.MediaTypeEpisode, MediaID: "tt1-s02e03", Language: "fr", Variant: api.VariantStandard, Provider: api.ProviderNameSubDL, ReleaseName: "r", Path: "p", Title: "t", ImdbID: "tt1", ReleaseTag: "WEB", Score: 50, Season: 2, Episode: 3, Manual: false, VideoPath: "v", MediaImported: time.Unix(99, 0).UTC()})
	})
	t.Run("fileRec", func(t *testing.T) {
		roundTrip(t, &fileRec{Codec: "ass", UpdatedAt: time.Unix(5, 0).UTC()})
	})
	t.Run("scanRec", func(t *testing.T) {
		roundTrip(t, &scanRec{Title: "M", AudioLang: "ja", Season: 1, Episode: 9, ScannedAt: time.Unix(7, 0).UTC()})
	})
}

// roundTrip encodes v, decodes it back under FailClosed (the strict path), and
// asserts the re-encoded bytes are byte-identical.
func roundTrip[T any](t *testing.T, v *T) {
	t.Helper()
	enc, err := encodeRecord(v)
	if err != nil {
		t.Fatalf("encodeRecord: %v", err)
	}
	var got T
	skip, derr := decodeRecord(kv.FailClosed, "test", []byte("k"), enc, &got)
	if skip {
		t.Fatal("FailClosed decode must never skip")
	}
	if derr != nil {
		t.Fatalf("decodeRecord: %v", derr)
	}
	reEnc, err := encodeRecord(&got)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if !bytes.Equal(enc, reEnc) {
		t.Errorf("round-trip not stable:\n have %s\n want %s", reEnc, enc)
	}
}

// TestDecodeRecord_policy asserts the two decode policies behave per
// requirement 13.4: TolerantSkip swallows the error and signals skip, while
// FailClosed surfaces the error and never skips.
func TestDecodeRecord_policy(t *testing.T) {
	malformed := []byte("{not json")

	var v1 stateRec
	skip, err := decodeRecord(kv.TolerantSkip, bucketSubtitleState, []byte("k"), malformed, &v1)
	if !skip || err != nil {
		t.Errorf("TolerantSkip on malformed = (skip=%v, err=%v), want (true, nil)", skip, err)
	}

	var v2 stateRec
	skip, err = decodeRecord(kv.FailClosed, bucketAuthUsers, []byte("k"), malformed, &v2)
	if skip || err == nil {
		t.Errorf("FailClosed on malformed = (skip=%v, err=%v), want (false, non-nil)", skip, err)
	}
}

// TestBucketDecodeMode asserts the four derived core buckets default to
// tolerant-skip and the auth/meta buckets fail closed (requirement 13.4).
func TestBucketDecodeMode(t *testing.T) {
	tolerant := []string{bucketSearchAttempts, bucketSubtitleState, bucketSubtitleFiles, bucketScanState}
	for _, b := range tolerant {
		if got := bucketDecodeMode(b); got != kv.TolerantSkip {
			t.Errorf("bucketDecodeMode(%q) = %v, want TolerantSkip", b, got)
		}
	}
	failClosed := []string{bucketAuthUsers, bucketAuthPasskeys, bucketAuthAPIKeys, bucketMeta, "unknown_bucket"}
	for _, b := range failClosed {
		if got := bucketDecodeMode(b); got != kv.FailClosed {
			t.Errorf("bucketDecodeMode(%q) = %v, want FailClosed", b, got)
		}
	}
}

// TestSchemaVersion_readWrite exercises the strict stamp reader and the write
// helper against a real bbolt file: a fresh DB reads as missing (never
// malformed), a write reads back present, and a planted non-8-byte value reads
// as malformed — the distinction the fail-closed open policy builds on. The
// open-level policy itself (refuse newer / malformed / missing-in-populated)
// is covered by the guard fixtures in migrate_test.go.
func TestSchemaVersion_readWrite(t *testing.T) {
	db := openTempDB(t)

	// Fresh DB: meta bucket absent -> missing.
	if err := db.View(func(tx *bolt.Tx) error {
		if _, st := readStamp(tx, metaKeyCoreSchemaVersion); st != stampMissing {
			t.Errorf("fresh DB core stamp state = %v, want missing", st)
		}
		return nil
	}); err != nil {
		t.Fatalf("read on fresh DB: %v", err)
	}

	// Create the meta bucket and write both current versions.
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketMeta)); err != nil {
			return err
		}
		if err := writeSchemaVersion(tx, metaKeyCoreSchemaVersion, coreSchemaVersion); err != nil {
			return err
		}
		return writeSchemaVersion(tx, metaKeyAuthSchemaVersion, authSchemaVersion)
	}); err != nil {
		t.Fatalf("write schema versions: %v", err)
	}

	if err := db.View(func(tx *bolt.Tx) error {
		v, st := readStamp(tx, metaKeyCoreSchemaVersion)
		if st != stampPresent || v != coreSchemaVersion {
			t.Errorf("core schema read = (%d, %v), want (%d, present)", v, st, coreSchemaVersion)
		}
		return nil
	}); err != nil {
		t.Fatalf("read after write: %v", err)
	}

	// Plant a non-8-byte stamp: strictly malformed, never treated as absent.
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketMeta)).Put(metaKeyCoreSchemaVersion, []byte("bogus"))
	}); err != nil {
		t.Fatalf("plant malformed stamp: %v", err)
	}
	if err := db.View(func(tx *bolt.Tx) error {
		if _, st := readStamp(tx, metaKeyCoreSchemaVersion); st != stampMalformed {
			t.Errorf("malformed core stamp state = %v, want malformed", st)
		}
		return nil
	}); err != nil {
		t.Fatalf("read malformed stamp: %v", err)
	}
}

// TestWriteSchemaVersion_noMetaBucket asserts the write helper errors rather
// than panicking when the meta bucket has not been bootstrapped.
func TestWriteSchemaVersion_noMetaBucket(t *testing.T) {
	db := openTempDB(t)
	if err := db.Update(func(tx *bolt.Tx) error {
		return writeSchemaVersion(tx, metaKeyCoreSchemaVersion, coreSchemaVersion)
	}); err == nil {
		t.Error("writeSchemaVersion without a meta bucket should error")
	}
}

// openTempDB opens a throwaway bbolt database in the test's temp dir and
// registers cleanup.
func openTempDB(t *testing.T) *bolt.DB {
	t.Helper()
	path := t.TempDir() + "/codec_test.bolt"
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		t.Fatalf("open temp bbolt: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
