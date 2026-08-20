package server

import (
	"net/http"

	"github.com/cplieger/subflux/internal/server/adminsocket"
)

// AdminHandler returns the admin-socket plane's handler.
//
// Exported for the composition root: main.go owns that listener's lifecycle and
// binds it to the Unix socket. The plane itself lives in
// internal/server/adminsocket — its three handlers read one collaborator and no
// live state, so they were the last handler cluster with no reason to sit on this
// type.
//
// The plane is constructed HERE rather than held as a field, because a field would
// need building after SetAuth supplies the store and would be nil until then —
// another ordering rule in a type that already has one too many. adminsocket.Plane
// holds nothing but its Deps, so building one costs nothing and this is called once
// per process anyway.
func (s *Server) AdminHandler() http.Handler {
	return adminsocket.New(adminsocket.Deps{
		Store:   s.authStore,
		Metrics: s.metrics,
	}).Handler()
}

// RecordPersistentAlert records a manually-dismissable operator alert.
// Exported for the composition root: main.go owns the admin-socket listener
// lifecycle and reports its failure as a persistent alert here (degraded
// mode — bootstrap unavailable — rather than fatal).
func (s *Server) RecordPersistentAlert(source, msg string) {
	s.alerts.RecordPersistent(source, msg)
}
