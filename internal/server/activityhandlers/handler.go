// Package activityhandlers serves the activity log, the alert list, and the SSE
// event stream: the HTTP surface over the three in-memory registries that record
// what the server is doing and what went wrong.
//
// It exists because every other data package under internal/server acquired a
// handlers sibling — coverage has coveragehandlers, config has confighandlers —
// while activity and events did not, so six handlers over four fields stayed on
// Server. They were the narrowest cluster on that type: no live state, no store,
// no metrics, no config, and all four dependencies were already sub-package types.
// The package's own extraction criterion is that a concern moves out once it can be
// handed its dependencies rather than reaching for a snapshot, and this one never
// needed the snapshot at all.
package activityhandlers

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/cplieger/auth/v5"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/syncjobs"
	"github.com/cplieger/subflux/internal/subflux"
)

// activityPageSize is the maximum number of recent activities returned.
const activityPageSize = 20

// SyncJobCanceller routes an activity-id cancellation into the sync-job
// dispatcher: the activity-id→dispatcher lookup this surface consults BEFORE
// the legacy scan path on DELETE. *syncjobs.Dispatcher satisfies it.
type SyncJobCanceller interface {
	Cancel(activityID string) syncjobs.CancelOutcome
}

// Deps is what this surface needs, and it is the whole of it: three registries
// and the event hub, each already owned by a sibling package.
//
// Stops is a pointer because Server holds the registry by value and the scan and
// scheduler packages register into that same one — the registry's zero value is
// ready to use, so it lives as a field and is shared by address, the way a
// sync.WaitGroup is.
type Deps struct {
	// Activity is the running/completed activity log this surface pages over.
	Activity *activity.Log
	// Alerts is the persistent-alert list the UI banner reads.
	Alerts *activity.AlertLog
	// Stops is the live stop-callback registry: it supplies the cancellable flag
	// on a running entry and receives the explicit stop request.
	Stops *activity.StopRegistry
	// Events is the SSE hub. The client cap lives on the hub itself, set at
	// construction and re-applied by hot reload, so no per-request config read
	// happens here.
	Events *events.EventBus
	// SyncJobs cancels queued sync jobs addressed by activity id (D1). A
	// queued DELETE releases the admission slot immediately; one that lost
	// the race to admission converts to a running-cancel.
	SyncJobs SyncJobCanceller
}

// Handler serves the activity, alert and event routes.
type Handler struct {
	deps Deps
}

// New returns a Handler over deps.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// --- Alerts ---

// HandleGetAlerts handles GET /api/alerts. Method dispatch lives in routes.go
// (GET here, DELETE on HandleDismissAlert) like every other route.
func (h *Handler) HandleGetAlerts(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteJSON(w, h.deps.Alerts.VisibleAlerts())
}

// HandleDismissAlert handles DELETE /api/alerts?id=N.
func (h *Handler) HandleDismissAlert(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "id parameter required")
		return
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "invalid id")
		return
	}
	if h.deps.Alerts.Dismiss(id) {
		httpapi.WriteJSON(w, map[string]string{subflux.KeyStatus: "dismissed"})
		return
	}
	httpapi.NotFoundC(w, r, subflux.CodeNotFound, "alert not found")
}

// --- Activity ---

// HandleGetActivity returns the most recent activity entries. Running entries
// survive the page cap unconditionally — a busy system must never hide a live
// cancellable scan, because UI restoration depends on seeing it — and each
// running entry carries the serialization-time cancellable flag merged from the
// stop registry. Retention is NOT this handler's job: the server's prune
// ticker is the one owner of PruneCompleted, so a read never mutates the log.
func (h *Handler) HandleGetActivity(w http.ResponseWriter, _ *http.Request) {
	src := h.deps.Activity.Entries()
	if len(src) == 0 {
		httpapi.WriteJSON(w, []activity.Entry{})
		return
	}
	if len(src) > activityPageSize {
		// Prepend running entries older than the page window, preserving
		// chronological order.
		page := src[len(src)-activityPageSize:]
		out := make([]activity.Entry, 0, len(page))
		for i := range src[:len(src)-activityPageSize] {
			if !src[i].Done {
				out = append(out, src[i])
			}
		}
		out = append(out, page...)
		src = out
	}
	for i := range src {
		if !src[i].Done {
			src[i].Cancellable = h.deps.Stops.Cancellable(src[i].ID)
		}
	}
	httpapi.WriteJSON(w, src)
}

// HandleDismissActivity removes a completed activity or cancels a queued one.
// DELETE /api/activity?id={id}
//
// A QUEUED sync job routes through the dispatcher BEFORE the legacy scan
// path: its slot releases immediately (the next 202 enters), and a delete
// that lost the lock race to admission converts to a running-cancel through
// the just-registered stop entry — 204 either way, with the job settling
// done(cancelled) on worker exit. A TERMINAL sync row's dismissal never
// touches the registry: it falls through to the plain activity dismiss.
func (h *Handler) HandleDismissActivity(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "id required")
		return
	}
	if h.deps.SyncJobs != nil {
		switch h.deps.SyncJobs.Cancel(id) {
		case syncjobs.CancelledQueued, syncjobs.CancelConverted:
			w.WriteHeader(http.StatusNoContent)
			return
		case syncjobs.CancelTerminal, syncjobs.CancelUnknown:
			// Terminal sync rows dismiss like any completed entry; unknown
			// ids take the legacy scan path.
		}
	}
	if h.deps.Activity.Cancel(id) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.deps.Activity.Dismiss(id)
	w.WriteHeader(http.StatusNoContent)
}

// HandleCancelActivity handles POST /api/activity/{id}/cancel — the explicit
// graceful stop for running background scans. The scan ends after the item in
// flight completes (a stop SIGNAL, never a context hard-kill).
//
// Two idioms now address activity entries and must not be confused:
// DELETE /api/activity?id= (dismiss) removes a completed row or cancels a QUEUED
// one and never stops running work; this endpoint stops RUNNING work and nothing
// else.
//
// Authorization is object-level against the entry's required role: per-item scans
// are cancellable by any configured user (single-household policy), full scans —
// manual and scheduled both run through the same path — by admins only.
// Responses: 204 stopped or already stopping (idempotent), 403 role too low,
// 404 unknown id, 409 not cancellable (completed, queued dismiss-cancelled, or
// not a stoppable activity). Every attempt is logged.
func (h *Handler) HandleCancelActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "activity id required")
		return
	}
	user := authhandlers.UserFromContext(r.Context())
	username := ""
	if user != nil {
		username = user.Username
	}

	entry, ok := h.deps.Activity.Get(id)
	if !ok {
		slog.Info("activity cancel rejected: unknown id",
			"activity_id", id, "user", username)
		httpapi.NotFoundC(w, r, subflux.CodeNotFound, "activity not found")
		return
	}
	if entry.RequiredRole == auth.RoleAdmin && !auth.HasRole(user, auth.RoleAdmin) {
		slog.Info("activity cancel rejected: admin role required",
			"activity_id", id, "user", username, "action", entry.Action)
		httpapi.ForbiddenC(w, r, subflux.CodeAuthRoleRequired, "admin role required to cancel this scan")
		return
	}

	switch h.deps.Stops.RequestStop(id) {
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
		httpapi.ConflictC(w, r, "activity_not_cancellable", "activity is not cancellable")
	}
}

// --- Events ---

// HandleEvents serves the SSE stream.
func (h *Handler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	events.Handle(h.deps.Events, w, r)
}
