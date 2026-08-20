package server

import "reflect"

// requireServiceable panics unless every collaborator a request can reach is
// wired. Called at the top of both Start paths — the moment after which a
// request can arrive, and the first moment the whole set is required.
//
// # Why a panic, and why here
//
// New returns an object that is NOT yet serviceable, deliberately: SetAuth is
// fallible and separate, so main.go must run New → SetAuth → Start in that order.
// Nothing enforced it. registerRoutes dereferences s.authH about thirty times and
// s.authenticator through requireAuth, so calling Start without SetAuth was a nil
// map of handlers mounted on a live mux — a panic on the first request to any
// authenticated route, a long way from the cause.
//
// s.metrics is the same class of defect but is asserted in New instead, because
// initHandlers binds it into four child Deps by value before Start is reached.
//
// A panic rather than an error: every one of these is a programming mistake in
// this repo's own composition root, fixable only by editing main.go, and it is
// caught at process start by every test that boots a Server. Compare the runtime
// alternative — the server binds a port, reports ready, and dies on the first
// request that happens to touch the missing field.
//
// Deliberately NOT checked: the fields a request can only reach on the CONFIGURED
// path (wire, newSonarr, newRadarr). Unconfigured mode is a supported state that
// serves the UI and the config endpoints with no engine at all, so requiring them
// here would refuse to boot exactly the case that exists to be recoverable.
// activate() is where those are needed and where their absence is a config error.
func (s *Server) requireServiceable() {
	for _, c := range []struct {
		v    any
		name string
	}{
		// Set by New.
		{name: "db", v: s.db},
		{name: "events", v: s.events},
		{name: "activity", v: s.activity},
		{name: "alerts", v: s.alerts},
		{name: "stores", v: s.stores},

		// Set by SetAuth, which Start does not call and cannot verify any other
		// way. These two are the ones that actually shipped as a nil deref.
		{name: "authStore", v: s.authStore},
		{name: "authenticator", v: s.authenticator},
		{name: "authH", v: s.authH},

		// Set by initHandlers, and every one is mounted by registerRoutes. A
		// handler missing here is a route that panics when it is first hit.
		{name: "queryH", v: s.queryH},
		{name: "configH", v: s.configH},
		{name: "manualH", v: s.manualH},
		{name: "fileH", v: s.fileH},
		{name: "mediaH", v: s.mediaH},
		{name: "coverageH", v: s.coverageH},
		{name: "syncH", v: s.syncH},
		{name: "previewH", v: s.previewH},
		{name: "scanH", v: s.scanH},
	} {
		if isNil(c.v) {
			panic("server: " + c.name + " is not wired; Start requires New, then SetAuth, then Start")
		}
	}
}

// isNil reports whether v is nil, INCLUDING a typed nil pointer inside a
// non-nil interface.
//
// The bare `v == nil` misses that case, and it is the case that occurs here:
// every field above except stores is an interface or a pointer, so a field left
// at its zero value arrives as a nil *T boxed in a live interface, for which
// `v == nil` is false. A guard that only caught the untyped nil would have passed
// while the bug it exists for was live — which is how the same class of guard
// was first written wrong in the sibling app.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}
