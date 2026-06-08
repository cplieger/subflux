package auth

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

// Feature: subflux-authentication, Property 11: Session token uniqueness and entropy
// **Validates: Requirements 5.1**
func TestProperty_SessionTokenUniquenessAndEntropy(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(2, 20).Draw(t, "n")
		tokens := make(map[string]struct{}, n)
		hashes := make(map[string]struct{}, n)

		for i := range n {
			plaintext, hash, err := GenerateSessionToken()
			if err != nil {
				t.Fatalf("GenerateSessionToken[%d] error: %v", i, err)
			}

			// Decode hex to check raw byte length (must be >= 32 bytes).
			raw, err := hex.DecodeString(plaintext)
			if err != nil {
				t.Fatalf("token is not valid hex: %v", err)
			}
			if len(raw) < 32 {
				t.Fatalf("token raw length %d < 32 bytes", len(raw))
			}

			// Hash must differ from plaintext.
			if plaintext == hash {
				t.Fatalf("hash equals plaintext")
			}

			// All tokens must be unique.
			if _, dup := tokens[plaintext]; dup {
				t.Fatalf("duplicate token at index %d", i)
			}
			tokens[plaintext] = struct{}{}

			// All hashes must be unique.
			if _, dup := hashes[hash]; dup {
				t.Fatalf("duplicate hash at index %d", i)
			}
			hashes[hash] = struct{}{}
		}
	})
}

// Feature: subflux-authentication, Property 12: Session expiry enforcement
// **Validates: Requirements 5.5, 5.9**
func TestProperty_SessionExpiryEnforcement(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Generate random timeout durations (1 minute to 30 days).
		idleTimeout := time.Duration(rapid.Int64Range(int64(time.Minute), int64(30*24*time.Hour)).Draw(t, "idleTimeout"))
		absTimeout := time.Duration(rapid.Int64Range(int64(time.Minute), int64(30*24*time.Hour)).Draw(t, "absTimeout"))

		now := time.Now()

		// Generate random offsets for LastActivity and CreatedAt relative to now.
		// Offset range: 0 to 60 days in the past.
		lastActivityAge := time.Duration(rapid.Int64Range(0, int64(60*24*time.Hour)).Draw(t, "lastActivityAge"))
		createdAge := time.Duration(rapid.Int64Range(0, int64(60*24*time.Hour)).Draw(t, "createdAge"))

		// Ensure createdAge >= lastActivityAge (session was created before last activity).
		if createdAge < lastActivityAge {
			createdAge, lastActivityAge = lastActivityAge, createdAge
		}

		sess := &api.Session{
			CreatedAt:    now.Add(-createdAge),
			LastActivity: now.Add(-lastActivityAge),
		}

		// Optionally set OIDCExpiry.
		hasOIDC := rapid.Bool().Draw(t, "hasOIDC")
		var oidcExpired bool
		if hasOIDC {
			// Offset from now: negative = past (expired), positive = future (valid).
			oidcOffset := time.Duration(rapid.Int64Range(int64(-30*24*time.Hour), int64(30*24*time.Hour)).Draw(t, "oidcOffset"))
			expiry := now.Add(oidcOffset)
			sess.OIDCExpiry = &expiry
			oidcExpired = now.After(expiry)
		}

		idleExpired := lastActivityAge > idleTimeout
		absExpired := createdAge > absTimeout

		err := ValidateSession(sess, idleTimeout, absTimeout, now)

		if idleExpired || absExpired || oidcExpired {
			if err == nil {
				t.Fatalf("expected ErrSessionExpired: idle=%v(timeout=%v) abs=%v(timeout=%v) oidc=%v",
					lastActivityAge, idleTimeout, createdAge, absTimeout, oidcExpired)
			}
			if err != ErrSessionExpired {
				t.Fatalf("expected ErrSessionExpired, got: %v", err)
			}
		} else {
			if err != nil {
				t.Fatalf("expected valid session, got: %v (idle=%v/%v abs=%v/%v oidc=%v)",
					err, lastActivityAge, idleTimeout, createdAge, absTimeout, oidcExpired)
			}
		}
	})
}

// Feature: subflux-authentication, Property 13: Session cleanup completeness
// **Validates: Requirements 5.8**
func TestProperty_SessionCleanupCompleteness(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		db := newFakeSessionStore()
		ctx := context.Background()

		idleTimeout := time.Duration(rapid.Int64Range(int64(10*time.Minute), int64(24*time.Hour)).Draw(rt, "idleTimeout"))
		absTimeout := time.Duration(rapid.Int64Range(int64(10*time.Minute), int64(7*24*time.Hour)).Draw(rt, "absTimeout"))

		now := time.Now()

		// Create a user to own the sessions.
		user := &api.User{
			Username:     "testuser",
			PasswordHash: "dummy",
			Role:         "admin",
			Enabled:      true,
		}
		if err := db.CreateUser(ctx, user); err != nil {
			rt.Fatalf("CreateUser: %v", err)
		}

		nSessions := rapid.IntRange(1, 15).Draw(rt, "nSessions")
		var validHashes []string

		for i := range nSessions {
			// Decide if this session should be expired or valid.
			expired := rapid.Bool().Draw(rt, "expired")

			var createdAt, lastActivity time.Time
			if expired {
				// Pick which expiry condition to trigger.
				triggerIdle := rapid.Bool().Draw(rt, "triggerIdle")
				if triggerIdle {
					// LastActivity older than idleTimeout.
					extra := time.Duration(rapid.Int64Range(int64(time.Second), int64(24*time.Hour)).Draw(rt, "idleExtra"))
					lastActivity = now.Add(-idleTimeout - extra)
					createdAt = lastActivity.Add(-time.Duration(rapid.Int64Range(0, int64(24*time.Hour)).Draw(rt, "createdBefore")))
				} else {
					// CreatedAt older than absTimeout.
					extra := time.Duration(rapid.Int64Range(int64(time.Second), int64(24*time.Hour)).Draw(rt, "absExtra"))
					createdAt = now.Add(-absTimeout - extra)
					// LastActivity can be recent (only abs timeout triggers).
					lastActivity = now.Add(-time.Duration(rapid.Int64Range(0, int64(idleTimeout/4)).Draw(rt, "recentActivity")))
				}
			} else {
				// Valid session: both well within BOTH timeouts.
				// Use half the smaller timeout as the maximum age to ensure
				// the session is safely within both windows.
				maxAge := max(min(idleTimeout/2, absTimeout/2), time.Second)
				age := time.Duration(rapid.Int64Range(0, int64(maxAge)).Draw(rt, "validAge"))
				lastActivity = now.Add(-age)
				createdAt = now.Add(-age)
			}

			// Generate a unique token hash for each session.
			_, hash, err := GenerateSessionToken()
			if err != nil {
				rt.Fatalf("GenerateSessionToken[%d]: %v", i, err)
			}

			sess := &api.Session{
				TokenHash:    hash,
				UserID:       user.ID,
				AuthMethod:   "password",
				IPAddress:    "127.0.0.1",
				CreatedAt:    createdAt,
				LastActivity: lastActivity,
			}
			if err := db.CreateSession(ctx, sess); err != nil {
				rt.Fatalf("CreateSession[%d]: %v", i, err)
			}

			if !expired {
				validHashes = append(validHashes, hash)
			}
		}

		// Run cleanup.
		_, err := db.CleanupExpiredSessions(ctx, now, idleTimeout, absTimeout)
		if err != nil {
			rt.Fatalf("CleanupExpiredSessions: %v", err)
		}

		// Verify all valid sessions still exist.
		for _, h := range validHashes {
			s, err := db.GetSessionByHash(ctx, h)
			if err != nil {
				rt.Fatalf("GetSessionByHash(%s): %v", h, err)
			}
			if s == nil {
				rt.Fatalf("valid session %s was deleted by cleanup", h)
			}
		}

		// Verify no expired sessions remain by running a second cleanup
		// with the same parameters. If the first cleanup was complete,
		// the second should delete zero rows.
		deleted2, err := db.CleanupExpiredSessions(ctx, now, idleTimeout, absTimeout)
		if err != nil {
			rt.Fatalf("second cleanup: %v", err)
		}
		if deleted2 != 0 {
			rt.Fatalf("second cleanup deleted %d sessions (expected 0; first cleanup missed them)", deleted2)
		}
	})
}

func TestValidateSession_TimeMonotonicity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		now := time.Now()
		idleTimeout := time.Duration(rapid.Int64Range(int64(time.Minute), int64(24*time.Hour)).Draw(t, "idle"))
		absTimeout := time.Duration(rapid.Int64Range(int64(time.Hour), int64(30*24*time.Hour)).Draw(t, "abs"))

		sess := &api.Session{
			CreatedAt:    now.Add(-time.Duration(rapid.Int64Range(0, int64(absTimeout/2)).Draw(t, "age"))),
			LastActivity: now.Add(-time.Duration(rapid.Int64Range(0, int64(idleTimeout/2)).Draw(t, "idle_age"))),
		}

		// If valid at t1, must be valid at t0 < t1 (monotonicity).
		t1 := now
		err1 := ValidateSession(sess, idleTimeout, absTimeout, t1)
		if err1 != nil {
			return // session already expired, nothing to check
		}

		// Check at an earlier time.
		delta := time.Duration(rapid.Int64Range(int64(time.Second), int64(time.Hour)).Draw(t, "delta"))
		t0 := t1.Add(-delta)
		err0 := ValidateSession(sess, idleTimeout, absTimeout, t0)
		if err0 != nil {
			t.Errorf("session valid at %v but expired at earlier %v", t1, t0)
		}
	})
}
