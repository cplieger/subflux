package authstore

// Round-2 mutant-killing tests for internal/authstore.
//
// asciiFold ASCII upper-bound:
//
//	129:31 / 139:20 CONDITIONALS_BOUNDARY (`c <= 'Z'` -> `c < 'Z'`). 'Z' is the
//	  top of the folded range; the `< 'Z'` mutant excludes it, so a 'Z' is
//	  neither detected (first loop) nor lowercased (second loop).
//
// sweeper error-log guards:
//
//	109:84 / 112:68 CONDITIONALS_NEGATION (`if err != nil` -> `if err == nil`).
//	  CleanupExpired{Sessions,OIDCStates} always succeed (in-memory maps), so on
//	  a healthy sweep the original logs no failure. The negated guards log a
//	  spurious "cleanup failed" on every successful sweep.

import (
	"testing"
	"time"
)

func TestGkSubfluxR2_AsciiFoldUppercaseZ(t *testing.T) {
	if got := asciiFold("Z"); got != "z" {
		t.Errorf("asciiFold(%q) = %q, want %q", "Z", got, "z")
	}
	// A multi-character case that also exercises the second (lowercasing) loop.
	if got := asciiFold("AZ"); got != "az" {
		t.Errorf("asciiFold(%q) = %q, want %q", "AZ", got, "az")
	}
}

func TestGkSubfluxR2_SweepOnceNoSpuriousFailureLogs(t *testing.T) {
	getLogs := gk_subflux_u24_captureLogs(t)
	s := gk_subflux_u24_newStore(t)

	s.sweepOnce(time.Now()) // healthy sweep: both cleanups succeed (return nil)

	recs := getLogs()
	if n := gk_subflux_u24_countMsg(recs, "auth sweeper: session cleanup failed"); n != 0 {
		t.Errorf("session cleanup failure logged %d times on a healthy sweep, want 0", n)
	}
	if n := gk_subflux_u24_countMsg(recs, "auth sweeper: oidc cleanup failed"); n != 0 {
		t.Errorf("oidc cleanup failure logged %d times on a healthy sweep, want 0", n)
	}
}
