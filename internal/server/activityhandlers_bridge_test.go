package server

import "github.com/cplieger/subflux/internal/server/activityhandlers"

// activityH builds the activity/alert/event handler from a Server's own fields.
//
// The tests in this package construct *Server{} by literal, which bypasses
// initHandlers and so leaves s.activityH nil. Rather than teach every fixture to
// call initHandlers (which wires nine handlers and needs a store), this mirrors
// the one Deps literal production uses, so the tests exercise the extracted
// handler against the fixtures they already had.
func activityH(s *Server) *activityhandlers.Handler {
	return activityhandlers.New(activityhandlers.Deps{
		Activity: s.activity,
		Alerts:   s.alerts,
		Stops:    &s.stops,
		Events:   s.events,
	})
}
