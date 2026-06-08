package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/store"
)

func testAdminServer(t *testing.T) *Server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(context.Background()) })
	s := &Server{
		authDeps: authDeps{
			authStore:  db,
			adminDB:    db,
			secDB:      db,
			oidcDB:     db,
			ceremonies: authhandlers.NewCeremonyStore(),
		},
		activity: activity.New(10),
		alerts:   activity.NewAlertLog(10),
	}
	s.live.Store(&liveState{cfg: &authTestConfig{}})
	s.authH = &authhandlers.Handler{
		Store:      db,
		AdminDB:    db,
		SecDB:      db,
		OidcDB:     db,
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
	s.handleListUsers(w, req)
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
