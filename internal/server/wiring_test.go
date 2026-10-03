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
