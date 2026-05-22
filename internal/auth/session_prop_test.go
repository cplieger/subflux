package auth

import (
	"testing"
	"time"

	"subflux/internal/api"

	"pgregory.net/rapid"
)

func TestProperty_ValidateSession_monotonicity(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		lastActivity := created.Add(time.Duration(rapid.Int64Range(0, int64(24*time.Hour)).Draw(t, "idle")))
		idleTimeout := time.Duration(rapid.Int64Range(int64(time.Minute), int64(48*time.Hour)).Draw(t, "idleTimeout"))
		absTimeout := time.Duration(rapid.Int64Range(int64(time.Hour), int64(30*24*time.Hour)).Draw(t, "absTimeout"))

		sess := &api.Session{
			CreatedAt:    created,
			LastActivity: lastActivity,
		}

		// If valid at time T, must also be valid at any T' < T (closer to activity).
		now := lastActivity.Add(time.Duration(rapid.Int64Range(0, int64(48*time.Hour)).Draw(t, "elapsed")))
		err1 := ValidateSession(sess, idleTimeout, absTimeout, now)

		// Check at an earlier time.
		earlier := lastActivity.Add(time.Duration(rapid.Int64Range(0, int64(now.Sub(lastActivity))).Draw(t, "earlier")))
		err2 := ValidateSession(sess, idleTimeout, absTimeout, earlier)

		// Monotonicity: if valid at later time, must be valid at earlier time.
		if err1 == nil && err2 != nil {
			t.Fatalf("valid at %v but invalid at earlier %v", now, earlier)
		}
	})
}
