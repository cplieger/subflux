package statedb

import (
	"testing"
	"time"
)

func TestCredentialDisableRoundTrip(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.FixedZone("CET", 3600))
	head := headRev(t, s)
	d := CredentialDisable{Provider: "subdl", Fingerprint: "argon2id$test", Reason: "invalid key", At: at}
	if err := s.PutCredentialDisable(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.CredentialDisable(t.Context(), "subdl")
	if err != nil || !found || got.Provider != d.Provider || got.Fingerprint != d.Fingerprint || got.Reason != d.Reason || !got.At.Equal(at) {
		t.Fatalf("CredentialDisable = %+v %v %v; want %+v", got, found, err, d)
	}
	d.Reason = "quota"
	if err := s.PutCredentialDisable(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.CredentialDisable(t.Context(), "subdl"); got.Reason != "quota" {
		t.Errorf("a second put left reason %q, want quota", got.Reason)
	}
	if _, found, err := s.CredentialDisable(t.Context(), "opensubtitles"); err != nil || found {
		t.Errorf("another provider's record = %v %v; want absent", found, err)
	}
	if rev := headRev(t, s); rev != head {
		t.Errorf("credential writes moved the revision from %d to %d; the table is silent", head, rev)
	}
	if err := s.DeleteCredentialDisable(t.Context(), "subdl"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.CredentialDisable(t.Context(), "subdl"); err != nil || found {
		t.Errorf("after delete = %v %v; want absent", found, err)
	}
	if err := s.DeleteCredentialDisable(t.Context(), "subdl"); err != nil {
		t.Errorf("deleting an absent record = %v, want nil", err)
	}
	if err := s.PutCredentialDisable(t.Context(), CredentialDisable{Reason: "x"}); err == nil {
		t.Error("a record naming no provider was stored")
	}
	if err := s.DeleteCredentialDisable(t.Context(), ""); err == nil {
		t.Error("deleting no provider succeeded")
	}
}
