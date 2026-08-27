package authhandlers

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	authwebauthn "github.com/cplieger/auth/v5/webauthn"
)

// liveCeremony returns a real in-flight WebAuthn ceremony. Only the library can
// mint one (Ceremony's state is unexported), and its own deadline is what the
// store evicts on.
func liveCeremony(t *testing.T) authwebauthn.Ceremony {
	t.Helper()
	rp, err := authwebauthn.New(authwebauthn.RPConfig{
		ID:          "example.com",
		DisplayName: "Test RP",
		Origins:     []string{"https://example.com"},
	})
	if err != nil {
		t.Fatalf("webauthn.New: %v", err)
	}
	_, ceremony, err := authwebauthn.BeginLogin(rp)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	return ceremony
}

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

func TestShardedCeremonyMap_Cleanup_removes_expired_only(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[*PendingLink]()
	sm.Store("fresh", &PendingLink{CreatedAt: time.Now()})
	sm.Store("stale", &PendingLink{CreatedAt: time.Now().Add(-time.Hour)})

	sm.Cleanup(func(v *PendingLink) bool {
		return time.Since(v.CreatedAt) > time.Minute
	})

	if _, ok := sm.LoadAndDelete("fresh"); !ok {
		t.Error("Cleanup removed a fresh entry it should have kept")
	}
	if _, ok := sm.LoadAndDelete("stale"); ok {
		t.Error("Cleanup kept a stale entry it should have removed")
	}
}

// TestShardedCeremonyMap_Cleanup_frees_capacity: the sweep of expired
// entries must return their slots to the live counter, like LoadAndDelete
// does. A counter that drifts up instead would refuse every later ceremony
// while the map itself sat empty, locking users out of the login flow.
func TestShardedCeremonyMap_Cleanup_frees_capacity(t *testing.T) {
	t.Parallel()
	sm := NewShardedCeremonyMap[*PendingLink]()
	expired := time.Now().Add(-time.Hour)
	for i := range MaxCeremonySessions {
		if !sm.Store(fmt.Sprintf("k%d", i), &PendingLink{CreatedAt: expired}) {
			t.Fatalf("Store failed at %d, want success up to capacity %d", i, MaxCeremonySessions)
		}
	}
	if sm.Store("overflow", &PendingLink{CreatedAt: time.Now()}) {
		t.Fatal("Store at capacity should fail")
	}

	sm.Cleanup(func(v *PendingLink) bool {
		return time.Since(v.CreatedAt) > time.Minute
	})

	if !sm.Store("after-cleanup", &PendingLink{CreatedAt: time.Now()}) {
		t.Error("Store after Cleanup should succeed: the swept slots were not reclaimed")
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
	ceremony := liveCeremony(t)

	// A live ceremony comes back intact.
	cs.WebAuthn.Store("fresh", ceremony)
	got, found := cs.ConsumeWebAuthnSession("fresh")
	if !found || got != ceremony {
		t.Errorf("ConsumeWebAuthnSession(fresh) = (%v, %t), want the stored ceremony and true", got, found)
	}
	// Single-use: a second consume of the same token finds nothing.
	if _, found := cs.ConsumeWebAuthnSession("fresh"); found {
		t.Error("ConsumeWebAuthnSession(spent) found = true, want false: a ceremony is single-use")
	}
	// An unknown token finds nothing.
	if _, found := cs.ConsumeWebAuthnSession("never-stored"); found {
		t.Error("ConsumeWebAuthnSession(unknown) found = true, want false")
	}
	// A ceremony past its deadline is refused even though it was present. The
	// zero Ceremony reports a zero deadline, and it is the only past-deadline
	// value a consumer can construct.
	cs.WebAuthn.Store("stale", authwebauthn.Ceremony{})
	if _, found := cs.ConsumeWebAuthnSession("stale"); found {
		t.Error("ConsumeWebAuthnSession(past deadline) found = true, want false")
	}
}

func TestCeremonyStore_Cleanup_expires_both_maps(t *testing.T) {
	t.Parallel()
	cs := NewCeremonyStore()
	cs.WebAuthn.Store("wa-fresh", liveCeremony(t))
	cs.WebAuthn.Store("wa-stale", authwebauthn.Ceremony{})
	cs.Link.Store("ln-fresh", &PendingLink{CreatedAt: time.Now()})
	cs.Link.Store("ln-stale", &PendingLink{CreatedAt: time.Now().Add(-2 * CeremonyTTL)})

	cs.Cleanup()

	if _, ok := cs.WebAuthn.LoadAndDelete("wa-fresh"); !ok {
		t.Error("Cleanup expired a fresh WebAuthn session it should have kept")
	}
	if _, ok := cs.WebAuthn.LoadAndDelete("wa-stale"); ok {
		t.Error("Cleanup kept a WebAuthn ceremony past its deadline")
	}
	if _, ok := cs.Link.LoadAndDelete("ln-fresh"); !ok {
		t.Error("Cleanup expired a fresh pending link it should have kept")
	}
	if _, ok := cs.Link.LoadAndDelete("ln-stale"); ok {
		t.Error("Cleanup kept a stale pending link past the TTL")
	}
}
