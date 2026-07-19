package server

// POST /api/activity/{id}/cancel endpoint tests: response contract
// (204 idempotent / 403 role / 404 unknown / 409 not cancellable) and the
// object-level authorization matrix — per-item scans stoppable by any
// configured user, full scans (manual AND scheduled) by admins only.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
)

// cancelReq drives handleCancelActivity with a path-value id and an
// authenticated user in context (nil user = no principal).
func cancelReq(t *testing.T, s *Server, id string, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	ctx := context.Background()
	if user != nil {
		ctx = api.NewUserContext(ctx, user)
	}
	req := httptest.NewRequestWithContext(ctx,
		http.MethodPost, "/api/activity/"+id+"/cancel", http.NoBody)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	s.handleCancelActivity(rec, req)
	return rec
}

func plainUser() *auth.User { return &auth.User{ID: 2, Username: "user", Role: auth.RoleUser} }
func adminUser() *auth.User { return &auth.User{ID: 1, Username: "admin", Role: auth.RoleAdmin} }

// startScanEntry seeds a running scan entry with a live stop registration and
// returns its id plus a probe for whether the stop callback fired.
func startScanEntry(s *Server, source activity.ActivitySource, scope activity.ScanScope, role auth.Role) (id string, stopped *bool) {
	id, _ = s.activity.StartScan("Scan", "detail", source, scope, role)
	fired := false
	s.stops.RegisterStop(id, func() { fired = true })
	return id, &fired
}

func TestHandleCancelActivity_unknown_id_404(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	rec := cancelReq(t, s, "does-not-exist", plainUser())
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleCancelActivity_per_item_scan_any_user_204(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	// Started implicitly by "someone else": per-item scans are stoppable by
	// ANY configured user (single-household policy) — there is no ownership
	// check, only the role gate.
	id, stopped := startScanEntry(s, activity.SourceManual,
		activity.ScanScope{Kind: activity.ScanKindSeries, MediaID: 42}, auth.RoleUser)

	rec := cancelReq(t, s, id, plainUser())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !*stopped {
		t.Error("stop callback did not fire")
	}

	// Idempotent second cancel: 204 already-stopping.
	rec = cancelReq(t, s, id, plainUser())
	if rec.Code != http.StatusNoContent {
		t.Errorf("second cancel status = %d, want 204 (idempotent)", rec.Code)
	}
}

func TestHandleCancelActivity_full_scan_role_matrix(t *testing.T) {
	t.Parallel()
	sources := []activity.ActivitySource{activity.SourceManual, activity.SourceScheduled}
	for _, source := range sources {
		t.Run(string(source), func(t *testing.T) {
			t.Parallel()
			s := newTestServer(&qhMockStore{}, &qhMockConfig{})
			id, stopped := startScanEntry(s, source,
				activity.ScanScope{Kind: activity.ScanKindFull}, auth.RoleAdmin)

			// A plain user may not stop a full scan — manual or scheduled.
			rec := cancelReq(t, s, id, plainUser())
			if rec.Code != http.StatusForbidden {
				t.Errorf("user cancel status = %d, want 403", rec.Code)
			}
			if *stopped {
				t.Fatal("stop callback fired despite 403")
			}

			// An admin may.
			rec = cancelReq(t, s, id, adminUser())
			if rec.Code != http.StatusNoContent {
				t.Errorf("admin cancel status = %d, want 204", rec.Code)
			}
			if !*stopped {
				t.Error("stop callback did not fire for admin")
			}
		})
	}
}

func TestHandleCancelActivity_admin_can_stop_per_item_scan(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	id, stopped := startScanEntry(s, activity.SourceManual,
		activity.ScanScope{Kind: activity.ScanKindMovie, MediaID: 7}, auth.RoleUser)

	rec := cancelReq(t, s, id, adminUser())
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (admin is a superset of user)", rec.Code)
	}
	if !*stopped {
		t.Error("stop callback did not fire")
	}
}

func TestHandleCancelActivity_not_cancellable_409(t *testing.T) {
	t.Parallel()

	t.Run("completed scan", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(&qhMockStore{}, &qhMockConfig{})
		id, _ := s.activity.StartScan("Scan", "d", activity.SourceManual,
			activity.ScanScope{Kind: activity.ScanKindSeries, MediaID: 1}, auth.RoleUser)
		s.activity.End(id) // terminal: its registration is gone

		rec := cancelReq(t, s, id, plainUser())
		if rec.Code != http.StatusConflict {
			t.Errorf("status = %d, want 409 (cancel-vs-end resolves as not cancellable)", rec.Code)
		}
	})

	t.Run("non-scan activity", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(&qhMockStore{}, &qhMockConfig{})
		// A running manual download has no stop registration: dismissal and
		// cancellation must not conflate.
		id := s.activity.Start("Manual Download", "d", activity.SourceManual)

		rec := cancelReq(t, s, id, plainUser())
		if rec.Code != http.StatusConflict {
			t.Errorf("status = %d, want 409", rec.Code)
		}
	})
}

func TestHandleCancelActivity_missing_id_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(api.NewUserContext(context.Background(), plainUser()),
		http.MethodPost, "/api/activity//cancel", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCancelActivity(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// The dismiss idiom must keep its semantics: DELETE /api/activity on a
// RUNNING (non-queued) scan neither stops nor removes it.
func TestDismissActivity_never_stops_running_scans(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	id, stopped := startScanEntry(s, activity.SourceManual,
		activity.ScanScope{Kind: activity.ScanKindSeries, MediaID: 42}, auth.RoleUser)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/activity?id="+id, http.NoBody)
	rec := httptest.NewRecorder()
	s.handleDismissActivity(rec, req)

	if *stopped {
		t.Error("dismiss stopped running work; it must never do that")
	}
	entry, ok := s.activity.Get(id)
	if !ok {
		t.Fatal("running entry was removed by dismiss")
	}
	if entry.Done || entry.Cancelled {
		t.Errorf("entry = done=%v cancelled=%v after dismiss, want still running", entry.Done, entry.Cancelled)
	}
}
