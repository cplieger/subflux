package auth

import (
	"testing"
	"time"

	"subflux/internal/api"
)

func FuzzValidateSession(f *testing.F) {
	f.Add(int64(0), int64(0), int64(3600), int64(86400), int64(1000), true)
	f.Add(int64(500), int64(100), int64(600), int64(1000), int64(700), false)
	f.Add(int64(0), int64(0), int64(1), int64(1), int64(9999), false)

	f.Fuzz(func(t *testing.T, createdSec, lastActivitySec, idleTimeoutSec, absTimeoutSec, nowSec int64, hasOIDCExpiry bool) {
		// Clamp to reasonable values to avoid overflow
		clamp := func(v int64) int64 {
			if v < 0 {
				return 0
			}
			if v > 1e12 {
				return 1e12
			}
			return v
		}
		createdSec = clamp(createdSec)
		lastActivitySec = clamp(lastActivitySec)
		idleTimeoutSec = clamp(idleTimeoutSec)
		absTimeoutSec = clamp(absTimeoutSec)
		nowSec = clamp(nowSec)

		sess := &api.Session{
			CreatedAt:    time.Unix(createdSec, 0),
			LastActivity: time.Unix(lastActivitySec, 0),
		}
		if hasOIDCExpiry {
			exp := time.Unix(nowSec-100, 0) // expired
			sess.OIDCExpiry = &exp
		}

		now := time.Unix(nowSec, 0)
		idle := time.Duration(idleTimeoutSec) * time.Second
		abs := time.Duration(absTimeoutSec) * time.Second

		err := ValidateSession(sess, idle, abs, now)
		_ = err
	})
}
