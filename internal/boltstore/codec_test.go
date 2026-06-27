package boltstore

import (
	"bytes"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	boltkv "github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// TestRecordRoundTrip asserts every core-domain record encodes and decodes back
// to an identical value through the typed codec wrappers.
func TestRecordRoundTrip(t *testing.T) {
	t.Run("attemptRec", func(t *testing.T) {
		roundTrip(t, &attemptRec{LastTried: time.Unix(1, 0).UTC(), NextRetry: time.Unix(2, 0).UTC(), Failures: 7})
	})
	t.Run("stateRec", func(t *testing.T) {
		roundTrip(t, &stateRec{ID: 1, Provider: api.ProviderNameSubDL, ReleaseName: "r", Path: "p", Title: "t", ImdbID: "tt1", ReleaseTag: "WEB", Score: 50, Season: 2, Episode: 3, Manual: false, VideoPath: "v", MediaImported: time.Unix(99, 0).UTC()})
	})
	t.Run("fileRec", func(t *testing.T) {
		roundTrip(t, &fileRec{Codec: "ass", OffsetMs: 1234, UpdatedAt: time.Unix(5, 0).UTC()})
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
	skip, derr := decodeRecord(boltkv.FailClosed, "test", []byte("k"), enc, &got)
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
	skip, err := decodeRecord(boltkv.TolerantSkip, bucketSubtitleState, []byte("k"), malformed, &v1)
	if !skip || err != nil {
		t.Errorf("TolerantSkip on malformed = (skip=%v, err=%v), want (true, nil)", skip, err)
	}

	var v2 stateRec
	skip, err = decodeRecord(boltkv.FailClosed, bucketAuthUsers, []byte("k"), malformed, &v2)
	if skip || err == nil {
		t.Errorf("FailClosed on malformed = (skip=%v, err=%v), want (false, non-nil)", skip, err)
	}
}

// TestBucketDecodeMode asserts the four derived core buckets default to
// tolerant-skip and the auth/meta buckets fail closed (requirement 13.4).
func TestBucketDecodeMode(t *testing.T) {
	tolerant := []string{bucketSearchAttempts, bucketSubtitleState, bucketSubtitleFiles, bucketScanState}
	for _, b := range tolerant {
		if got := bucketDecodeMode(b); got != boltkv.TolerantSkip {
			t.Errorf("bucketDecodeMode(%q) = %v, want TolerantSkip", b, got)
		}
	}
	failClosed := []string{bucketAuthUsers, bucketAuthPasskeys, bucketAuthAPIKeys, bucketMeta, "unknown_bucket"}
	for _, b := range failClosed {
		if got := bucketDecodeMode(b); got != boltkv.FailClosed {
			t.Errorf("bucketDecodeMode(%q) = %v, want FailClosed", b, got)
		}
	}
}

// TestCheckSchemaVersion covers the detect-and-refuse policy: fresh and
// equal-or-lower stored versions pass; a newer stored version is refused.
func TestCheckSchemaVersion(t *testing.T) {
	cases := []struct {
		name    string
		stored  uint64
		current uint64
		present bool
		wantErr bool
	}{
		{name: "fresh-file", stored: 0, present: false, current: 1, wantErr: false},
		{name: "equal", stored: 1, present: true, current: 1, wantErr: false},
		{name: "older-additive", stored: 1, present: true, current: 2, wantErr: false},
		{name: "newer-breaking", stored: 2, present: true, current: 1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSchemaVersion("core", tc.stored, tc.present, tc.current)
			if (err != nil) != tc.wantErr {
				t.Errorf("checkSchemaVersion(%d, %v, %d) err=%v, wantErr=%v", tc.stored, tc.present, tc.current, err, tc.wantErr)
			}
		})
	}
}

// TestSchemaVersion_readWriteVerify exercises the meta-bucket schema-version
// helpers against a real bbolt file: a fresh DB verifies clean, a write then
// reads back, and a planted future version is refused.
func TestSchemaVersion_readWriteVerify(t *testing.T) {
	db := openTempDB(t)

	// Fresh DB: meta bucket absent -> not present, verify passes.
	if err := db.View(func(tx *bolt.Tx) error {
		if _, present := readSchemaVersion(tx, metaKeyCoreSchemaVersion); present {
			t.Error("fresh DB should report core schema version absent")
		}
		return verifySchemaVersions(tx)
	}); err != nil {
		t.Fatalf("verify on fresh DB: %v", err)
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
		v, present := readSchemaVersion(tx, metaKeyCoreSchemaVersion)
		if !present || v != coreSchemaVersion {
			t.Errorf("core schema read = (%d, %v), want (%d, true)", v, present, coreSchemaVersion)
		}
		return verifySchemaVersions(tx)
	}); err != nil {
		t.Fatalf("verify after write: %v", err)
	}

	// Plant a future core version; verify must refuse.
	if err := db.Update(func(tx *bolt.Tx) error {
		return writeSchemaVersion(tx, metaKeyCoreSchemaVersion, coreSchemaVersion+1)
	}); err != nil {
		t.Fatalf("plant future version: %v", err)
	}
	if err := db.View(verifySchemaVersions); err == nil {
		t.Error("verifySchemaVersions should refuse a future core schema version")
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
