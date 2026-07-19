package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/metrics"
)

// The custody trio (R1.5): the admin bootstrap channel lives exclusively on
// the Unix-socket admin plane. (a) the TCP mux never routes it, (b) a socket
// peer completes bootstrap with zero credentials over a real unix listener,
// (c) the socket path is not under the /config volume (directory-mode
// assertions live in the main package beside ensureAdminSocketDir).

// TestAdminBootstrap_TCPFallthrough proves the public TCP mux has no
// bootstrap route: a request to /api/admin/bootstrap falls through to the
// SPA catch-all (handleUI), never a bootstrap handler. A bootstrap handler
// would answer JSON ("unknown action" 400 or a status envelope); the SPA
// catch-all serves HTML.
func TestAdminBootstrap_TCPFallthrough(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	body := strings.NewReader(`{"action":"reset-password","username":"admin","password":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/bootstrap", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("TCP /api/admin/bootstrap answered Content-Type %q (status %d, body %q) — a handler ran; want the SPA fallthrough (text/html)",
			ct, w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "unknown action") {
		t.Fatal("bootstrap handler reachable over TCP")
	}

	// The route table records no bootstrap registration in any group.
	for _, reg := range s.routeRegs {
		if strings.Contains(reg.Pattern, "/api/admin/bootstrap") {
			t.Fatalf("registerRoutes still registers %q in group %q", reg.Pattern, reg.Group)
		}
	}
}

// TestAdminBootstrap_UnixSocketRoundTrip proves a socket peer completes a
// bootstrap action with zero credentials over a real net.Listen("unix")
// round-trip: create a user, reset its password through the socket, and
// verify the new password verifies against the stored hash.
func TestAdminBootstrap_UnixSocketRoundTrip(t *testing.T) {
	t.Parallel()
	s := testAdminServer(t)
	s.metrics = metrics.New() // AdminHandler's recover hook records panics

	hash, err := auth.HashPassword("old-password-123456")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	user := &auth.User{Username: "admin", PasswordHash: hash, Role: auth.RoleAdmin, Enabled: true}
	if err := s.authStore.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	sockPath := t.TempDir() + "/adm.sock"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.AdminHandler(), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln) //nolint:errcheck // returns on Close
	t.Cleanup(func() { srv.Close() })

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
			},
		},
	}

	const newPassword = "brand-new-password-9876"
	body := `{"action":"reset-password","username":"admin","password":"` + newPassword + `"}`
	resp, err := client.Post("http://admin.sock"+config.AdminBootstrapURLPath,
		"application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("socket round-trip failed: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap over socket = %d, body %s; want 200 with zero credentials", resp.StatusCode, respBody)
	}
	var env struct {
		Status   string `json:"status"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		t.Fatalf("unmarshal response %s: %v", respBody, err)
	}
	if env.Status != "ok" || env.Username != "admin" {
		t.Fatalf("response = %+v, want status ok for admin", env)
	}

	updated, err := s.authStore.GetUserByUsername(ctx, "admin")
	if err != nil || updated == nil {
		t.Fatalf("lookup after reset: %v", err)
	}
	ok, err := auth.VerifyPassword(newPassword, updated.PasswordHash)
	if err != nil || !ok {
		t.Fatalf("new password does not verify against the stored hash (ok=%v err=%v)", ok, err)
	}
}

// TestAdminSocketPath_NotUnderConfig pins the custody constant: the socket
// must never live under the /config volume mount (a user-controlled volume
// would carry the socket path across the container trust boundary).
func TestAdminSocketPath_NotUnderConfig(t *testing.T) {
	t.Parallel()
	if strings.HasPrefix(config.AdminSocketPath, "/config") {
		t.Fatalf("AdminSocketPath %q is under /config", config.AdminSocketPath)
	}
	if !strings.HasPrefix(config.AdminSocketPath, config.AdminSocketDir+"/") {
		t.Fatalf("AdminSocketPath %q is not inside AdminSocketDir %q", config.AdminSocketPath, config.AdminSocketDir)
	}
}
