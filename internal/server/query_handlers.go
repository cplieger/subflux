package server

import (
	"net/http"

	"subflux/internal/server/queryhandlers"
)

// ensureQueryH lazily initializes queryH for tests that construct Server
// directly without calling New().
func (s *Server) ensureQueryH() *queryhandlers.Handler {
	if s.queryH == nil {
		s.queryH = queryhandlers.New(queryhandlers.Deps{
			QueryDB:      s.stores.query,
			CovDB:        s.db,
			Metrics:      s.metrics,
			State:        s.queryLiveState,
			Configured:   func() bool { return s.configured.Load() },
			CountMissing: countMissing,
		})
	}
	return s.queryH
}

// handleState delegates to the queryhandlers subpackage.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleState(w, r)
}

// handleBackoff delegates to the queryhandlers subpackage.
func (s *Server) handleBackoff(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleBackoff(w, r)
}

// handleBackoffByPrefix delegates to the queryhandlers subpackage.
func (s *Server) handleBackoffByPrefix(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleBackoffByPrefix(w, r)
}

// handleLocks delegates to the queryhandlers subpackage.
func (s *Server) handleLocks(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleLocks(w, r)
}

// handleProviders delegates to the queryhandlers subpackage.
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleProviders(w, r)
}

// handleConfigParsed delegates to the queryhandlers subpackage.
func (s *Server) handleConfigParsed(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleConfigParsed(w, r)
}

// handleScore delegates to the queryhandlers subpackage.
func (s *Server) handleScore(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleScore(w, r)
}

// handleSearchTargets delegates to the queryhandlers subpackage.
func (s *Server) handleSearchTargets(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleSearchTargets(w, r)
}

// handleProviderTimeout delegates to the queryhandlers subpackage.
func (s *Server) handleProviderTimeout(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleProviderTimeout(w, r)
}

// handleProviderTimeoutReset delegates to the queryhandlers subpackage.
func (s *Server) handleProviderTimeoutReset(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleProviderTimeoutReset(w, r)
}

// handleStateStats delegates to the queryhandlers subpackage.
func (s *Server) handleStateStats(w http.ResponseWriter, r *http.Request) {
	s.ensureQueryH().HandleStateStats(w, r)
}
