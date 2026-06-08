package server

import (
	"context"
	"net/http"
	"strconv"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/fsutil"
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

// atomicWriteConfig writes data to path atomically with 0o600 permissions.
func atomicWriteConfig(ctx context.Context, path string, data []byte, _ string) error {
	return fsutil.AtomicWriteFileMode(ctx, path, data, 0o600)
}

// handleGetConfig delegates to the confighandlers subpackage.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.configH.HandleGetConfig(w, r)
}

// handleSaveConfig delegates to the confighandlers subpackage.
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	s.configH.HandleSaveConfig(w, r)
}

// handleResetConfig delegates to the confighandlers subpackage.
func (s *Server) handleResetConfig(w http.ResponseWriter, r *http.Request) {
	s.configH.HandleResetConfig(w, r)
}

// handleConfigSchema delegates to the confighandlers subpackage.
func (s *Server) handleConfigSchema(w http.ResponseWriter, r *http.Request) {
	s.configH.HandleConfigSchema(w, r)
}

// --- Alert API handlers ---

// handleGetAlerts returns visible alerts or dispatches DELETE to dismiss.
func (s *Server) handleGetAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		s.handleDismissAlert(w, r)
		return
	}
	if !requireGET(w, r) {
		return
	}

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

// handleGetActivity returns the most recent activity entries.
func (s *Server) handleGetActivity(w http.ResponseWriter, _ *http.Request) {
	s.activity.PruneCompleted(activity.DefaultPruneAge)

	src := s.activity.Entries()
	if len(src) == 0 {
		api.WriteJSON(w, []activity.Entry{})
		return
	}
	if len(src) > activityPageSize {
		src = src[len(src)-activityPageSize:]
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
