// Package authhandlers provides shared types and utilities for the server's
// authentication handler cluster: login, TOTP, WebAuthn, OIDC, admin user
// management, and security management (password change, API keys, passkeys).
package authhandlers

import (
	"crypto/rand"
	"encoding/hex"
	"hash/fnv"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"subflux/internal/auth"

	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	// CeremonyTTL is the maximum age for pending TOTP and WebAuthn sessions.
	CeremonyTTL = auth.CeremonyTimeout

	// MaxCeremonySessions caps the in-memory ceremony maps to prevent OOM
	// from unauthenticated flooding of /api/auth/login or /api/auth/webauthn/login/begin.
	MaxCeremonySessions = 10000

	// CeremonyShards is the number of shards for ceremony maps.
	CeremonyShards = 16

	// HeaderWebAuthnSession is the HTTP header carrying the WebAuthn session token.
	HeaderWebAuthnSession = "X-WebAuthn-Session"
)

// PendingTOTP holds state for a user who passed password verification
// but still needs to provide a TOTP code.
type PendingTOTP struct {
	CreatedAt time.Time
	SessHash  string // hash of the session-to-be, for binding
	IP        string
	UserID    int64
}

// WebAuthnSession holds ephemeral WebAuthn ceremony data.
type WebAuthnSession struct {
	Data      *webauthn.SessionData
	CreatedAt time.Time
}

// ShardedCeremonyMap is a sharded map for ephemeral ceremony state.
// Sharding reduces lock contention under concurrent auth requests.
type ShardedCeremonyMap[V any] struct {
	shards [CeremonyShards]struct {
		m  map[string]V
		mu sync.Mutex
	}
	count atomic.Int64
}

// NewShardedCeremonyMap creates a new sharded ceremony map.
func NewShardedCeremonyMap[V any]() *ShardedCeremonyMap[V] {
	sm := &ShardedCeremonyMap[V]{}
	for i := range sm.shards {
		sm.shards[i].m = make(map[string]V)
	}
	return sm
}

func (sm *ShardedCeremonyMap[V]) shard(key string) *struct {
	m  map[string]V
	mu sync.Mutex
} {
	h := fnv.New32a()
	h.Write([]byte(key))
	return &sm.shards[h.Sum32()%CeremonyShards]
}

// Store adds a value to the map. Returns false if the session limit is reached.
func (sm *ShardedCeremonyMap[V]) Store(key string, val V) bool {
	if sm.count.Load() >= MaxCeremonySessions {
		return false
	}
	s := sm.shard(key)
	s.mu.Lock()
	if _, exists := s.m[key]; !exists {
		sm.count.Add(1)
	}
	s.m[key] = val
	s.mu.Unlock()
	return true
}

// LoadAndDelete atomically retrieves and removes a value from the map.
func (sm *ShardedCeremonyMap[V]) LoadAndDelete(key string) (V, bool) {
	s := sm.shard(key)
	s.mu.Lock()
	val, ok := s.m[key]
	if ok {
		delete(s.m, key)
		sm.count.Add(-1)
	}
	s.mu.Unlock()
	return val, ok
}

// Load retrieves a value from the sharded ceremony map by key without removing it.
func (sm *ShardedCeremonyMap[V]) Load(key string) (V, bool) {
	s := sm.shard(key)
	s.mu.Lock()
	val, ok := s.m[key]
	s.mu.Unlock()
	return val, ok
}

// Cleanup removes entries matching the isExpired predicate.
func (sm *ShardedCeremonyMap[V]) Cleanup(isExpired func(V) bool) {
	for i := range sm.shards {
		s := &sm.shards[i]
		s.mu.Lock()
		for k, v := range s.m {
			if isExpired(v) {
				delete(s.m, k)
				sm.count.Add(-1)
			}
		}
		s.mu.Unlock()
	}
}

// CeremonyStore holds ephemeral ceremony state for auth flows.
// Owned by the Server struct to enable per-instance isolation in tests.
type CeremonyStore struct {
	TOTP     *ShardedCeremonyMap[*PendingTOTP]
	WebAuthn *ShardedCeremonyMap[*WebAuthnSession]
}

// NewCeremonyStore creates a new ceremony store.
func NewCeremonyStore() *CeremonyStore {
	return &CeremonyStore{
		TOTP:     NewShardedCeremonyMap[*PendingTOTP](),
		WebAuthn: NewShardedCeremonyMap[*WebAuthnSession](),
	}
}

// ConsumeWebAuthnSession atomically retrieves and removes a WebAuthn session.
// Returns nil if the session is missing or expired.
func (cs *CeremonyStore) ConsumeWebAuthnSession(token string) *webauthn.SessionData {
	ws, ok := cs.WebAuthn.LoadAndDelete(token)
	if !ok || time.Since(ws.CreatedAt) > CeremonyTTL {
		return nil
	}
	return ws.Data
}

// GenerateCeremonyToken generates a random hex token for ephemeral ceremony state.
func GenerateCeremonyToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	var dst [64]byte
	hex.Encode(dst[:], b[:])
	return string(dst[:]), nil
}

// Cleanup removes expired pending TOTP and WebAuthn sessions.
// Called periodically by the server.
func (cs *CeremonyStore) Cleanup() {
	now := time.Now()
	cs.TOTP.Cleanup(func(v *PendingTOTP) bool {
		return now.Sub(v.CreatedAt) > CeremonyTTL
	})
	cs.WebAuthn.Cleanup(func(v *WebAuthnSession) bool {
		return now.Sub(v.CreatedAt) > CeremonyTTL
	})
}

// ClientIP extracts the client IP address from the request, stripping the port.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
