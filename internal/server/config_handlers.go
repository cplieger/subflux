package server

import (
	"net/http"
	"strconv"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/server/activity"
)

// --- Constants and utilities ---

const (
	// activityPageSize is the maximum number of recent activities returned.
	activityPageSize = 20
)

// cfgFilePath is the config file path inside the container.
// Tests override this via the package-level variable.
var cfgFilePath = config.DefaultConfigPath

// configFilePath returns the config file path.
func configFilePath() string {
	return cfgFilePath
}

// --- Alert API handlers ---

// handleGetAlerts handles GET /api/alerts. Method dispatch lives in
// routes.go (GET here, DELETE on handleDismissAlert) like every other route.
func (s *Server) handleGetAlerts(w http.ResponseWriter, _ *http.Request) {
	visible := s.alerts.VisibleAlerts()
	api.WriteJSON(w, visible)
}

// handleDismissAlert handles DELETE /api/alerts?id=N.
func (s *Server) handleDismissAlert(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "id parameter required")
		return
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid id")
		return
	}
	if s.alerts.Dismiss(id) {
		api.WriteJSON(w, map[string]string{jsonKeyStatus: "dismissed"})
	} else {
		api.NotFoundC(w, r, api.CodeNotFound, "alert not found")
	}
}

// --- Activity API handlers ---

// handleGetActivity returns the most recent activity entries. Running
// entries survive the page cap unconditionally — a busy system must never
// hide a live cancellable scan (UI restoration depends on seeing it) — and
// each running entry carries the serialization-time cancellable flag merged
// from the stop registry.
func (s *Server) handleGetActivity(w http.ResponseWriter, _ *http.Request) {
	s.activity.PruneCompleted(activity.DefaultPruneAge)

	src := s.activity.Entries()
	if len(src) == 0 {
		api.WriteJSON(w, []activity.Entry{})
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
			src[i].Cancellable = s.stops.Cancellable(src[i].ID)
		}
	}
	api.WriteJSON(w, src)
}

// handleDismissActivity removes a completed activity or cancels a queued one.
// DELETE /api/activity?id={id}
func (s *Server) handleDismissActivity(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "id required")
		return
	}
	if s.activity.Cancel(id) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.activity.Dismiss(id)
	w.WriteHeader(http.StatusNoContent)
}
