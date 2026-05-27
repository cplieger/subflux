package auth

import (
	"context"
	"sync"
	"time"

	"subflux/internal/api"
)

// fakeSessionStore is an in-memory implementation of SessionStore plus
// the setup methods needed by auth tests. It eliminates the test-time
// dependency on the real SQLite store package.
type fakeSessionStore struct {
	users    map[int64]*api.User
	sessions map[string]*api.Session // keyed by TokenHash
	apiKeys  map[string]*api.Key     // keyed by KeyHash
	passkeys []api.PasskeyCredential
	nextID   int64
	mu       sync.Mutex
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{
		users:    make(map[int64]*api.User),
		sessions: make(map[string]*api.Session),
		apiKeys:  make(map[string]*api.Key),
		nextID:   1,
	}
}

// --- SessionStore interface (3 methods) ---

func (f *fakeSessionStore) GetSessionByHash(_ context.Context, tokenHash string) (*api.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[tokenHash]
	if !ok {
		return nil, nil
	}
	return s, nil
}

func (f *fakeSessionStore) GetUserByID(_ context.Context, id int64) (*api.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return nil, nil
	}
	return u, nil
}

func (f *fakeSessionStore) GetAPIKeyByHash(_ context.Context, hash string) (*api.Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.apiKeys[hash]
	if !ok {
		return nil, nil
	}
	return k, nil
}

// --- Test setup helpers ---

func (f *fakeSessionStore) CreateUser(_ context.Context, u *api.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u.ID = f.nextID
	f.nextID++
	cp := *u
	f.users[cp.ID] = &cp
	return nil
}

func (f *fakeSessionStore) CreateSession(_ context.Context, s *api.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *s
	f.sessions[cp.TokenHash] = &cp
	return nil
}

func (f *fakeSessionStore) CreateAPIKey(_ context.Context, k *api.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *k
	if cp.ID == 0 {
		cp.ID = f.nextID
		f.nextID++
	}
	f.apiKeys[cp.KeyHash] = &cp
	return nil
}

func (f *fakeSessionStore) DeleteUserSessions(_ context.Context, userID int64, exceptHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for hash, s := range f.sessions {
		if s.UserID == userID && hash != exceptHash {
			delete(f.sessions, hash)
		}
	}
	return nil
}

func (f *fakeSessionStore) CleanupExpiredSessions(_ context.Context, now time.Time, idleTimeout, absTimeout time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var deleted int64
	for hash, s := range f.sessions {
		idleExpired := now.Sub(s.LastActivity) > idleTimeout
		absExpired := now.Sub(s.CreatedAt) > absTimeout
		if idleExpired || absExpired {
			delete(f.sessions, hash)
			deleted++
		}
	}
	return deleted, nil
}

func (f *fakeSessionStore) CreatePasskey(_ context.Context, c *api.PasskeyCredential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c.ID == 0 {
		c.ID = f.nextID
		f.nextID++
	}
	f.passkeys = append(f.passkeys, *c)
	return nil
}

func (f *fakeSessionStore) GetPasskeyByCredentialID(_ context.Context, credID []byte) (*api.PasskeyCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.passkeys {
		if bytesEqual(f.passkeys[i].CredentialID, credID) {
			cp := f.passkeys[i]
			return &cp, nil
		}
	}
	return nil, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
