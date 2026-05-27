package scheduler_test

import (
	"testing"
)

// --- Run ---

func TestRun_ExecutesScanAtInterval(t *testing.T) {
	t.Skip("TODO: triggers scan after configured interval elapses")
}

func TestRun_RespectsContextCancellation(t *testing.T) {
	t.Skip("TODO: stops cleanly when context is cancelled")
}

func TestRun_SkipsWhenScanAlreadyInProgress(t *testing.T) {
	t.Skip("TODO: does not start concurrent scans")
}

// --- GuardedScan ---

func TestGuardedScan_AcquiresLock(t *testing.T) {
	t.Skip("TODO: acquires scan lock before executing")
}

func TestGuardedScan_ReleasesLockOnCompletion(t *testing.T) {
	t.Skip("TODO: releases lock even on scan error")
}

func TestGuardedScan_EmitsStartAndDoneEvents(t *testing.T) {
	t.Skip("TODO: sends scan:start and scan:done SSE events")
}
