package boltstore

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
	bolt "go.etcd.io/bbolt"
)

func TestProviderAuthRecords_empty_bucket_returns_none(t *testing.T) {
	db, _ := openTemp(t)
	got, err := db.ProviderAuthRecords(t.Context())
	if err != nil {
		t.Fatalf("ProviderAuthRecords() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ProviderAuthRecords() = %v, want none", got)
	}
}

func TestProviderAuthRecord_round_trips_through_reopen(t *testing.T) {
	db, path := openTemp(t)
	ctx := t.Context()
	at := time.Date(2026, 9, 21, 10, 42, 0, 0, time.UTC)
	want := subflux.ProviderAuthRecord{
		Provider:      "hdbits",
		Fingerprint:   "$argon2id$v=19$m=19456,t=2,p=1$placeholder-salt$placeholder-key",
		LastError:     "HDBits refused the username and passkey (status 5: Auth failed)",
		FailedOps:     []string{"search", "download"},
		Failures:      3,
		LastFailureAt: at,
		NextAttemptAt: at.Add(30 * time.Minute),
		DisabledAt:    at,
	}
	if err := db.PutProviderAuthRecord(ctx, &want); err != nil {
		t.Fatalf("PutProviderAuthRecord() error = %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened := openTempAt(t, filepath.Dir(path))

	got, err := reopened.ProviderAuthRecords(ctx)
	if err != nil {
		t.Fatalf("ProviderAuthRecords() after reopen error = %v", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("ProviderAuthRecords() after reopen = %+v, want [%+v]", got, want)
	}
}

func TestPutProviderAuthRecord_replaces_the_previous_record(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	for _, n := range []int{1, 2} {
		if err := db.PutProviderAuthRecord(ctx, &subflux.ProviderAuthRecord{Provider: "subdl", Failures: n}); err != nil {
			t.Fatalf("PutProviderAuthRecord(failures %d) error = %v", n, err)
		}
	}
	got, err := db.ProviderAuthRecords(ctx)
	if err != nil {
		t.Fatalf("ProviderAuthRecords() error = %v", err)
	}
	if len(got) != 1 || got[0].Failures != 2 {
		t.Errorf("ProviderAuthRecords() = %+v, want one record with failures 2", got)
	}
}

func TestPutProviderAuthRecord_refuses_an_empty_provider(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.PutProviderAuthRecord(t.Context(), &subflux.ProviderAuthRecord{}); err == nil {
		t.Error("PutProviderAuthRecord(empty provider) = nil, want an error")
	}
}

func TestDeleteProviderAuthRecord_removes_only_that_provider(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	for _, id := range []subflux.ProviderID{"hdbits", "subdl"} {
		if err := db.PutProviderAuthRecord(ctx, &subflux.ProviderAuthRecord{Provider: id, Failures: 1}); err != nil {
			t.Fatalf("PutProviderAuthRecord(%s) error = %v", id, err)
		}
	}
	if err := db.DeleteProviderAuthRecord(ctx, "hdbits"); err != nil {
		t.Fatalf("DeleteProviderAuthRecord(hdbits) error = %v", err)
	}
	if err := db.DeleteProviderAuthRecord(ctx, "never-stored"); err != nil {
		t.Errorf("DeleteProviderAuthRecord(absent) error = %v, want nil", err)
	}
	got, err := db.ProviderAuthRecords(ctx)
	if err != nil {
		t.Fatalf("ProviderAuthRecords() error = %v", err)
	}
	if len(got) != 1 || got[0].Provider != "subdl" {
		t.Errorf("ProviderAuthRecords() after delete = %+v, want only subdl", got)
	}
}

func TestProviderAuthRecords_skips_an_undecodable_record(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	if err := db.PutProviderAuthRecord(ctx, &subflux.ProviderAuthRecord{Provider: "subdl", Failures: 1}); err != nil {
		t.Fatalf("PutProviderAuthRecord() error = %v", err)
	}
	if err := db.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketProviderAuth)).Put([]byte("hdbits"), []byte("{not json"))
	}); err != nil {
		t.Fatalf("plant corrupt record: %v", err)
	}
	got, err := db.ProviderAuthRecords(ctx)
	if err != nil {
		t.Fatalf("ProviderAuthRecords() error = %v, want the corrupt record skipped", err)
	}
	if len(got) != 1 || got[0].Provider != "subdl" {
		t.Errorf("ProviderAuthRecords() = %+v, want only the readable subdl record", got)
	}
	if err := db.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket([]byte(bucketProviderAuth)).Get([]byte("hdbits")); v != nil {
			t.Errorf("provider_auth[hdbits] = %q after the read, want the undecodable record deleted", v)
		}
		return nil
	}); err != nil {
		t.Fatalf("read back provider_auth: %v", err)
	}
}

func TestProviderAuthRecords_keeps_a_record_rewritten_since_the_read(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	if err := db.PutProviderAuthRecord(ctx, &subflux.ProviderAuthRecord{Provider: "hdbits", Failures: 2}); err != nil {
		t.Fatalf("PutProviderAuthRecord() error = %v", err)
	}
	if err := db.deleteCorruptProviderAuth(map[string][]byte{"hdbits": []byte("{not json")}); err != nil {
		t.Fatalf("deleteCorruptProviderAuth() error = %v", err)
	}
	got, err := db.ProviderAuthRecords(ctx)
	if err != nil || len(got) != 1 || got[0].Failures != 2 {
		t.Errorf("ProviderAuthRecords() = %+v, %v; want the rewritten hdbits record kept", got, err)
	}
}
