package authhandlers

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

type testCeremonyVal struct {
	CreatedAt time.Time
	IP        string
	UserID    int64
}

func TestShardedCeremonyMap_Store_Load_roundtrip(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[testCeremonyVal]()
	val := testCeremonyVal{CreatedAt: time.Now(), UserID: 42, IP: "1.2.3.4"}
	if !sm.Store("key1", val) {
		t.Fatal("Store returned false")
	}
	got, ok := sm.LoadAndDelete("key1")
	if !ok {
		t.Fatal("LoadAndDelete returned false")
	}
	if got.UserID != 42 {
		t.Errorf("UserID = %d, want 42", got.UserID)
	}
}

func TestShardedCeremonyMap_Delete_removes(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[string]()
	sm.Store("k", "v")
	sm.LoadAndDelete("k")
	_, ok := sm.LoadAndDelete("k")
	if ok {
		t.Error("LoadAndDelete after delete should return false")
	}
}

func TestShardedCeremonyMap_max_capacity(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[int]()
	// Fill to capacity.
	for i := range MaxCeremonySessions {
		if !sm.Store(fmt.Sprintf("k%d", i), i) {
			t.Fatalf("Store failed at %d, expected success up to %d", i, MaxCeremonySessions)
		}
	}
	// Next store should fail.
	if sm.Store("overflow", 999) {
		t.Error("Store should return false when at capacity")
	}
}

func TestShardedCeremonyMap_concurrent(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[int]()
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Go(func() {
			key := fmt.Sprintf("k%d", i%50)
			if i%3 == 0 {
				sm.Store(key, i)
			} else {
				sm.LoadAndDelete(key)
			}
		})
	}
	wg.Wait()
}

func TestShardedCeremonyMap_LoadAndDelete_frees_capacity(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[int]()
	for i := range MaxCeremonySessions {
		if !sm.Store(fmt.Sprintf("k%d", i), i) {
			t.Fatalf("Store failed at %d, want success up to capacity %d", i, MaxCeremonySessions)
		}
	}
	// At capacity a new key is rejected.
	if sm.Store("overflow", -1) {
		t.Fatal("Store at capacity should fail")
	}
	if _, ok := sm.LoadAndDelete("k0"); !ok {
		t.Fatal("LoadAndDelete of an existing key returned false")
	}
	// Removing one entry must free exactly one slot. If LoadAndDelete failed
	// to decrement the live counter, the map stays "full" and this Store fails.
	if !sm.Store("after-free", 1) {
		t.Error("Store after LoadAndDelete should succeed: a freed slot was not reclaimed")
	}
}

func TestShardedCeremonyMap_Load_does_not_remove(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[string]()
	sm.Store("k", "v")
	if got, ok := sm.Load("k"); !ok || got != "v" {
		t.Fatalf("Load(k) = %q, %v; want \"v\", true", got, ok)
	}
	// Load is a peek: a second Load still finds the value.
	if got, ok := sm.Load("k"); !ok || got != "v" {
		t.Errorf("second Load(k) = %q, %v; want \"v\", true (Load must not remove)", got, ok)
	}
}

func TestShardedCeremonyMap_Cleanup_removes_expired_only(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[*WebAuthnSession]()
	sm.Store("fresh", &WebAuthnSession{CreatedAt: time.Now()})
	sm.Store("stale", &WebAuthnSession{CreatedAt: time.Now().Add(-time.Hour)})

	sm.Cleanup(func(v *WebAuthnSession) bool {
		return time.Since(v.CreatedAt) > time.Minute
	})

	if _, ok := sm.Load("fresh"); !ok {
		t.Error("Cleanup removed a fresh entry it should have kept")
	}
	if _, ok := sm.Load("stale"); ok {
		t.Error("Cleanup kept a stale entry it should have removed")
	}
}

func TestClientIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{"ipv4_with_port", "10.0.0.1:12345", "10.0.0.1"},
		{"ipv6_with_port", "[2001:db8::1]:443", "2001:db8::1"},
		{"no_port_returned_verbatim", "malformed-no-port", "malformed-no-port"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if got := ClientIP(req); got != tc.want {
				t.Errorf("ClientIP(%q) = %q, want %q", tc.remoteAddr, got, tc.want)
			}
		})
	}
}

func TestGenerateCeremonyToken_format_and_uniqueness(t *testing.T) {
	t.Parallel()
	tok1, err := GenerateCeremonyToken()
	if err != nil {
		t.Fatalf("GenerateCeremonyToken: %v", err)
	}
	if len(tok1) != 64 {
		t.Errorf("token length = %d, want 64 (32 random bytes hex-encoded)", len(tok1))
	}
	raw, err := hex.DecodeString(tok1)
	if err != nil {
		t.Errorf("token %q is not valid hex: %v", tok1, err)
	}
	if len(raw) != 32 {
		t.Errorf("decoded token = %d bytes, want 32 (256 bits of entropy)", len(raw))
	}
	tok2, err := GenerateCeremonyToken()
	if err != nil {
		t.Fatalf("GenerateCeremonyToken (second): %v", err)
	}
	if tok1 == tok2 {
		t.Error("two generated tokens are identical; ceremony tokens must be unpredictable")
	}
}

func TestConsumeWebAuthnSession_fresh_expired_and_single_use(t *testing.T) {
	t.Parallel()
	cs := NewCeremonyStore()
	data := &webauthn.SessionData{}

	// Fresh session returns the stored data.
	cs.WebAuthn.Store("fresh", &WebAuthnSession{Data: data, CreatedAt: time.Now()})
	if got := cs.ConsumeWebAuthnSession("fresh"); got != data {
		t.Errorf("fresh session: got %v, want the stored SessionData", got)
	}
	// Single-use: a second consume of the same token returns nil.
	if got := cs.ConsumeWebAuthnSession("fresh"); got != nil {
		t.Error("a consumed session must be single-use (second consume must return nil)")
	}
	// Missing token returns nil.
	if got := cs.ConsumeWebAuthnSession("never-stored"); got != nil {
		t.Error("missing session token must return nil")
	}
	// Expired session returns nil even though it was present.
	cs.WebAuthn.Store("stale", &WebAuthnSession{Data: data, CreatedAt: time.Now().Add(-2 * CeremonyTTL)})
	if got := cs.ConsumeWebAuthnSession("stale"); got != nil {
		t.Error("an expired session must return nil (TTL enforcement)")
	}
}

func TestCeremonyStore_Cleanup_expires_both_maps(t *testing.T) {
	t.Parallel()
	cs := NewCeremonyStore()
	cs.WebAuthn.Store("wa-fresh", &WebAuthnSession{CreatedAt: time.Now()})
	cs.WebAuthn.Store("wa-stale", &WebAuthnSession{CreatedAt: time.Now().Add(-2 * CeremonyTTL)})
	cs.Link.Store("ln-fresh", &PendingLink{CreatedAt: time.Now()})
	cs.Link.Store("ln-stale", &PendingLink{CreatedAt: time.Now().Add(-2 * CeremonyTTL)})

	cs.Cleanup()

	if _, ok := cs.WebAuthn.Load("wa-fresh"); !ok {
		t.Error("Cleanup expired a fresh WebAuthn session it should have kept")
	}
	if _, ok := cs.WebAuthn.Load("wa-stale"); ok {
		t.Error("Cleanup kept a stale WebAuthn session past the TTL")
	}
	if _, ok := cs.Link.Load("ln-fresh"); !ok {
		t.Error("Cleanup expired a fresh pending link it should have kept")
	}
	if _, ok := cs.Link.Load("ln-stale"); ok {
		t.Error("Cleanup kept a stale pending link past the TTL")
	}
}
