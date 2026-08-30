package arrsvc

import (
	"context"
	"errors"
	"time"
)

// Arr-read wrapper budgets (the A4 design constants). Every instant compared
// against them is a time.Now value captured once, never rounded, serialized,
// or reconstructed.
const (
	// arrCacheTTL (ARR_CACHE_TTL_S): a plain read is at most one TTL old when
	// performed — a bound on the entry's write instant.
	arrCacheTTL = 15 * time.Second
	// waveFloor (RECOVERY_WAVE_FLOOR_MS): one recovery pass per resource per
	// floor, measured at actual read-begin instants.
	waveFloor = 2 * time.Second
	// maxConcurrentWaves (RECOVERY_MAX_CONCURRENT_WAVES): aggregate ceiling on
	// admitted passes, both arr sides together.
	maxConcurrentWaves = 2
	// admissionBudget (RECOVERY_ADMISSION_BUDGET_MS): scheduled instant to
	// read-begin; expiry answers every waiter ErrRecoveryRefused.
	admissionBudget = 6 * time.Second
	// executionBudget (RECOVERY_EXECUTION_BUDGET_MS): single-attempt bound on
	// an admitted pass, and the wave clients' arrapi WithTimeout value.
	executionBudget = 20 * time.Second
	// requestDeadline (RECOVERY_REQUEST_DEADLINE_MS): ONE per marked HTTP
	// request, armed by WithRecovery at server request arrival; every wrapper
	// read the request makes charges against it. 5 s under the client's 30 s.
	requestDeadline = 25 * time.Second
)

// Sentinel errors of the wrapper's recovery (marked) reads, exported so the
// coverage and media handlers can map them to the wire. The wrapper never
// writes HTTP itself.
var (
	// ErrRecoveryRefused is the refusal sentinel: the admission budget
	// expired before the wave was admitted, or the request deadline expired
	// before the read settled. Handlers map it to 429
	// (httpapi.TooManyRequestsC) — a refusal to keep waiting, never a 500
	// through the generic InternalErrorC arm.
	ErrRecoveryRefused = errors.New("recovery read refused")
	// ErrRecoveryFailed is the execution-failure sentinel: an admitted wave
	// pass failed (upstream error, execution-budget expiry, or server
	// shutdown), fanned out to every waiter. Handlers map it to the family's
	// upstream-failure status (502).
	ErrRecoveryFailed = errors.New("recovery wave failed")
	// ErrUnknownSeries is the ordered membership gate's post-wave verdict: a
	// marked episodes read whose series id misses the fresh series list.
	// Handlers map it to 404; no upstream episodes call was made.
	ErrUnknownSeries = errors.New("series not in the sonarr library")
)

// recoveryKey carries the recoveryState of a ?recovery=1 request.
type recoveryKey struct{}

// recoveryState is the marked-request state: one deadline per marked HTTP
// request, armed at server request arrival.
type recoveryState struct{ deadline time.Time }

// WithRecovery marks ctx as carrying a ?recovery=1 read and arms the request
// deadline at the current instant. The handler calls it once at server
// request arrival, before the consumer-interface call; every wrapper read on
// the returned context selects wave admission and charges against the one
// deadline. An unmarked context reads plain.
func WithRecovery(ctx context.Context) context.Context {
	return context.WithValue(ctx, recoveryKey{},
		recoveryState{deadline: time.Now().Add(requestDeadline)})
}

// recoveryFrom reports whether ctx carries the recovery marker.
func recoveryFrom(ctx context.Context) (recoveryState, bool) {
	rec, ok := ctx.Value(recoveryKey{}).(recoveryState)
	return rec, ok
}

// RecoveryMarked reports whether ctx carries the ?recovery=1 marker set by
// WithRecovery. The handler-level honoring pin reads it through fake clients:
// exactly the honoring endpoints interpret the query parameter, and this is
// what lets a test observe that from outside the package.
func RecoveryMarked(ctx context.Context) bool {
	_, ok := recoveryFrom(ctx)
	return ok
}
