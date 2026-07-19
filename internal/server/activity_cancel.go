package server

import (
	"log/slog"
	"net/http"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
)

// handleCancelActivity handles POST /api/activity/{id}/cancel — the explicit
// graceful stop for running background scans. The scan ends after the item
// in flight completes (a stop SIGNAL, never a context hard-kill).
//
// Two idioms now address activity entries and must not be confused:
// DELETE /api/activity?id= (dismiss) removes a completed row or cancels a
// QUEUED one and never stops running work; this endpoint stops RUNNING work
// and nothing else.
//
// Authorization is object-level against the entry's required role: per-item
// scans are cancellable by any configured user (single-household policy),
// full scans — manual and scheduled both run through the same path — by
// admins only. Responses: 204 stopped or already stopping (idempotent),
// 403 role too low, 404 unknown id, 409 not cancellable (completed, queued
// dismiss-cancelled, or not a stoppable activity). Every attempt is logged.
func (s *Server) handleCancelActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "activity id required")
		return
	}
	user := api.UserFromContext(r.Context())
	username := ""
	if user != nil {
		username = user.Username
	}

	entry, ok := s.activity.Get(id)
	if !ok {
		slog.Info("activity cancel rejected: unknown id",
			"activity_id", id, "user", username)
		api.NotFoundC(w, r, api.CodeNotFound, "activity not found")
		return
	}
	if entry.RequiredRole == auth.RoleAdmin && !auth.HasRole(user, auth.RoleAdmin) {
		slog.Info("activity cancel rejected: admin role required",
			"activity_id", id, "user", username, "action", entry.Action)
		api.ForbiddenC(w, r, api.CodeAuthRoleRequired, "admin role required to cancel this scan")
		return
	}

	switch s.stops.RequestStop(id) {
	case activity.StopRequested:
		slog.Info("activity cancel requested: stopping after current item",
			"activity_id", id, "user", username, "action", entry.Action)
		w.WriteHeader(http.StatusNoContent)
	case activity.StopAlreadyStopping:
		slog.Info("activity cancel repeated: already stopping",
			"activity_id", id, "user", username, "action", entry.Action)
		w.WriteHeader(http.StatusNoContent)
	default: // StopNotFound: entry exists but has no live stop registration.
		slog.Info("activity cancel rejected: not cancellable",
			"activity_id", id, "user", username, "action", entry.Action,
			"done", entry.Done)
		api.ConflictC(w, r, "activity_not_cancellable", "activity is not cancellable")
	}
}
