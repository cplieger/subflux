package queryhandlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// This file kills surviving gremlins mutants in internal/server/queryhandlers
// for unit subflux-u27. Identifiers are prefixed gk_subflux_u27_ to avoid
// colliding with sibling units sharing this package. A recording QueryStore
// captures the *api.StateQuery that HandleState derives from the query string,
// so the assertions depend on the exact limit/offset guards in query.go.

// gk_subflux_u27_recStore records the StateQuery passed to GetState; the other
// QueryStore methods are unused stubs.
type gk_subflux_u27_recStore struct {
	lastState *api.StateQuery
}

func (s *gk_subflux_u27_recStore) GetState(_ context.Context, q *api.StateQuery) ([]api.StateEntry, error) {
	cp := *q
	s.lastState = &cp
	return nil, nil
}

func (s *gk_subflux_u27_recStore) GetBackoffItems(context.Context) ([]api.BackoffEntry, error) {
	return nil, nil
}

func (s *gk_subflux_u27_recStore) GetBackoffByPrefix(context.Context, api.MediaType, string) ([]api.BackoffEntry, error) {
	return nil, nil
}

func (s *gk_subflux_u27_recStore) GetManualLocks(context.Context) ([]api.ManualLockEntry, error) {
	return nil, nil
}

func (s *gk_subflux_u27_recStore) Stats(context.Context) (int, int, error) { return 0, 0, nil }

// gk_subflux_u27_runState drives HandleState with the given raw query string
// and returns the StateQuery the handler built.
func gk_subflux_u27_runState(t *testing.T, rawQuery string) *api.StateQuery {
	t.Helper()
	st := &gk_subflux_u27_recStore{}
	h := New(Deps{QueryDB: st})
	req := httptest.NewRequest(http.MethodGet, "/api/state?"+rawQuery, nil)
	h.HandleState(httptest.NewRecorder(), req)
	if st.lastState == nil {
		t.Fatalf("HandleState(?%s): GetState was not called", rawQuery)
	}
	return st.lastState
}

// A non-empty, valid, positive limit must be applied verbatim (default is 50).
// Mutants that would break this:
//
//	28:28 (v!="" -> v==""): block skipped, Limit stays 50.
//	29:37 (err==nil -> err!=nil): parse-success rejected, Limit stays 50.
//	29:49 negation (n>0 -> n<=0): 7<=0 false, Limit stays 50.
//	33:11 negation (limit>10000 -> limit<=10000): 7<=10000 true, capped to 10000.
func TestGk_subflux_u27_HandleState_limitApplied(t *testing.T) {
	t.Parallel()
	q := gk_subflux_u27_runState(t, "limit=7")
	if q.Limit != 7 {
		t.Errorf("HandleState(limit=7).Limit = %d, want 7", q.Limit)
	}
}

// limit=0 must NOT override the default of 50: the guard is `n > 0`.
// Mutants that would break this (both accept 0 -> Limit=0):
//
//	29:49 boundary (n>0 -> n>=0): 0>=0 true.
//	29:49 negation (n>0 -> n<=0): 0<=0 true.
func TestGk_subflux_u27_HandleState_limitZeroIgnored(t *testing.T) {
	t.Parallel()
	q := gk_subflux_u27_runState(t, "limit=0")
	if q.Limit != 50 {
		t.Errorf("HandleState(limit=0).Limit = %d, want 50 (zero must not override default)", q.Limit)
	}
}

// A non-empty, valid, positive offset must be applied verbatim (default 0).
// Mutant: 38:29 (v!="" -> v==""): offset block skipped, Offset stays 0.
func TestGk_subflux_u27_HandleState_offsetApplied(t *testing.T) {
	t.Parallel()
	q := gk_subflux_u27_runState(t, "offset=3")
	if q.Offset != 3 {
		t.Errorf("HandleState(offset=3).Offset = %d, want 3", q.Offset)
	}
}
