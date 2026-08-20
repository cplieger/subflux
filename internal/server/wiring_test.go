package server

import (
	"strings"
	"testing"
)

// TestRequireServiceable_CatchesMissingAuth pins the guard against the mistake it
// exists for: Start without SetAuth.
//
// The order New → SetAuth → Start was enforced by nothing, and skipping the middle
// step mounted a nil handler set on a live mux — registerRoutes dereferences
// s.authH about thirty times, so the failure was a panic on the first request to
// any authenticated route, arbitrarily far from the cause.
//
// The test asserts the PANIC MESSAGE NAMES THE FIELD, not merely that something
// panicked: a nil deref panics too, and the whole value of the guard is saying
// which collaborator is missing and in what order to wire it.
func TestRequireServiceable_CatchesMissingAuth(t *testing.T) {
	t.Parallel()

	s := &Server{}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("requireServiceable did not panic on a Server with nothing wired")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is %T, want a string naming the field: %v", r, r)
		}
		if !strings.Contains(msg, "is not wired") {
			t.Errorf("panic message %q does not say what is wrong", msg)
		}
		if !strings.Contains(msg, "SetAuth") {
			t.Errorf("panic message %q does not name the required call order", msg)
		}
	}()
	s.requireServiceable()
}

// TestIsNil_TypedNilInInterface is the case the guard would otherwise miss.
//
// Every field requireServiceable checks is an interface or a pointer, so a field
// left at its zero value arrives as a nil *T boxed in a live interface. A bare
// `v == nil` is FALSE for that, so a guard written the obvious way passes while
// the bug it exists for is live — which is exactly how this class of guard was
// first written wrong in the sibling app.
func TestIsNil_TypedNilInInterface(t *testing.T) {
	t.Parallel()

	// `boxed != nil` here is a language guarantee, not something to assert:
	// staticcheck can decide the comparison statically (SA4023), which makes
	// asserting it a tautology rather than a demonstration. What is worth
	// asserting is that isNil sees through the box.
	var typedNil *Server
	var boxed any = typedNil

	if !isNil(boxed) {
		t.Error("isNil(typed nil *Server in an interface) = false, want true")
	}
	if isNil(&Server{}) {
		t.Error("isNil(non-nil *Server) = true, want false")
	}
	if !isNil(nil) {
		t.Error("isNil(untyped nil) = false, want true")
	}
}
