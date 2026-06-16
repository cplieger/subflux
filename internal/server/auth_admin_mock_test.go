package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/boltstore"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
)

func testAdminServer(t *testing.T) *Server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.bolt")
	db, err := boltstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(context.Background()) })

	authDB := authstore.New(db.BoltDB())
	if err := authDB.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { authDB.Close() })

	s := &Server{
		authDeps: authDeps{
			authStore:  authDB,
			adminDB:    authDB,
			secDB:      authDB,
			oidcDB:     authDB,
			ceremonies: authhandlers.NewCeremonyStore(),
		},
		activity: activity.New(10),
		alerts:   activity.NewAlertLog(10),
	}
	s.live.Store(&liveState{cfg: &authTestConfig{}})
	s.authH = &authhandlers.Handler{
		Store:      authDB,
		AdminDB:    authDB,
		SecDB:      authDB,
		OidcDB:     authDB,
		Ceremonies: s.ceremonies,
		Config:     func() authhandlers.AuthConfig { return s.state().cfg },
		Configured: func() bool { return s.configured.Load() },
	}
	return s
}

func TestHandleListUsers_empty(t *testing.T) {
	t.Parallel()
	s := testAdminServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/admin/users", nil)
	w := httptest.NewRecorder()
	s.authH.HandleListUsers(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var result []json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("got %d users, want 0", len(result))
	}
}
