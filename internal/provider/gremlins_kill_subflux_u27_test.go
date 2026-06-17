package provider

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
)

// This file kills surviving gremlins mutants in internal/provider for unit
// subflux-u27. All identifiers introduced here are prefixed gk_subflux_u27_
// to avoid colliding with sibling units sharing this package. It reuses the
// existing retryFakeProvider / dlResult test helpers from retry_test.go.

// --- flexint.go: ParseFlexInt conditional-negation mutants ---
// Targets:
//   flexint.go:16:42 (err==nil on the number-unmarshal branch)
//   flexint.go:21:42 (err!=nil on the string-unmarshal branch)
//   flexint.go:24:7  (s=="" empty-string branch)
//   flexint.go:28:9  (err!=nil on the Atoi branch)
//
// Each input below is chosen so that flipping the operator at exactly one of
// those branches changes the observable (value, error) pair.
func TestGk_subflux_u27_ParseFlexInt_branches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		wantVal int
		wantErr bool
	}{
		// Number path succeeds: err==nil must return the number. Flipping
		// 16:42 to err!=nil falls through to string parsing, which fails on a
		// bare number, yielding (0, error). (kills 16:42)
		{name: "bare_number", in: `42`, wantVal: 42, wantErr: false},
		// Quoted numeric string: number-parse fails, string-parse err!=nil is
		// false, s!="" , Atoi err!=nil is false -> (123, nil). Flipping any of
		// 21:42 / 24:7 / 28:9 changes this. (kills 21:42, 24:7, 28:9)
		{name: "quoted_number", in: `"123"`, wantVal: 123, wantErr: false},
		// Empty quoted string: s=="" returns (0, nil). Flipping 24:7 to s!=""
		// instead runs Atoi("") -> (0, error). (kills 24:7 decisively)
		{name: "empty_string", in: `""`, wantVal: 0, wantErr: false},
		// Non-numeric quoted string: Atoi fails, err!=nil returns an error.
		// Flipping 28:9 to err==nil skips the error and returns (0, nil).
		// (kills 28:9 decisively)
		{name: "non_numeric_string", in: `"abc"`, wantErr: true},
		// JSON object is neither a number nor a string: string-parse err!=nil
		// returns "cannot unmarshal". Flipping 21:42 to err==nil skips the
		// error and falls through to (0, nil). (kills 21:42 decisively)
		{name: "json_object", in: `{}`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseFlexInt([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseFlexInt(%s) error = nil, want non-nil", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFlexInt(%s) error = %v, want nil", tc.in, err)
			}
			if got != tc.wantVal {
				t.Errorf("ParseFlexInt(%s) = %d, want %d", tc.in, got, tc.wantVal)
			}
		})
	}
}

// --- settings.go: SettingInt float64 range-guard boundary ---
// Target: settings.go:106:30 (the `n < math.MinInt` guard). At exactly
// n == float64(math.MinInt) (== -2^63, exactly representable and a valid int),
// the original guard is false so SettingInt returns the value (math.MinInt).
// A `<=` boundary mutant treats the equal case as out-of-range and returns def.
func TestGk_subflux_u27_SettingInt_minIntBoundary(t *testing.T) {
	t.Parallel()
	got := SettingInt(map[string]any{"gk_subflux_u27_k": float64(math.MinInt)}, SettingKey("gk_subflux_u27_k"), 777)
	if got != math.MinInt {
		t.Errorf("SettingInt(float64(math.MinInt)) = %d, want %d", got, math.MinInt)
	}
}

// --- retry.go: download-retry logging mutants ---
// The mutated operators only affect emitted slog records, so these tests
// capture the default logger and assert on the records.
//   retry.go:71:15 CONDITIONALS_BOUNDARY  (attempt>0 gate on the "recovered" log)
//   retry.go:71:15 CONDITIONALS_NEGATION  (same gate)
//   retry.go:73:53 ARITHMETIC_BASE        (attempt+1 -> "attempts" in recovered log)
//   retry.go:88:50 ARITHMETIC_BASE        (attempt+1 -> "attempt" in retrying-warn log)

type gk_subflux_u27_logRec struct {
	msg   string
	attrs map[string]any
}

type gk_subflux_u27_capHandler struct {
	mu   *sync.Mutex
	recs *[]gk_subflux_u27_logRec
}

func (h gk_subflux_u27_capHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h gk_subflux_u27_capHandler) Handle(_ context.Context, r slog.Record) error {
	m := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	*h.recs = append(*h.recs, gk_subflux_u27_logRec{msg: r.Message, attrs: m})
	h.mu.Unlock()
	return nil
}

func (h gk_subflux_u27_capHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h gk_subflux_u27_capHandler) WithGroup(string) slog.Handler      { return h }

// gk_subflux_u27_captureLogs redirects the default slog logger to an in-memory
// recorder for the duration of the test (restored via t.Cleanup). These tests
// must NOT be parallel because slog.Default is process-global.
func gk_subflux_u27_captureLogs(t *testing.T) *[]gk_subflux_u27_logRec {
	t.Helper()
	recs := &[]gk_subflux_u27_logRec{}
	mu := &sync.Mutex{}
	prev := slog.Default()
	slog.SetDefault(slog.New(gk_subflux_u27_capHandler{mu: mu, recs: recs}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return recs
}

func gk_subflux_u27_countMsg(recs []gk_subflux_u27_logRec, msg string) int {
	n := 0
	for _, r := range recs {
		if r.msg == msg {
			n++
		}
	}
	return n
}

func gk_subflux_u27_firstAttr(recs []gk_subflux_u27_logRec, msg, key string) (any, bool) {
	for _, r := range recs {
		if r.msg == msg {
			v, ok := r.attrs[key]
			return v, ok
		}
	}
	return nil, false
}

const (
	gk_subflux_u27_msgRecovered = "download recovered after transient errors"
	gk_subflux_u27_msgRetrying  = "download failed with transient error, retrying"
)

// A first-attempt success (attempt index 0) must NOT emit the "recovered" log,
// because the original gates it on `attempt > 0`. The boundary mutant (`>= 0`)
// and the negation mutant (`<= 0`) both make attempt 0 truthy and would log.
// (kills 71:15 boundary AND negation)
func TestGk_subflux_u27_Retry_noRecoveredLogOnFirstAttempt(t *testing.T) {
	recs := gk_subflux_u27_captureLogs(t)
	inner := &retryFakeProvider{name: "p", dlResults: []dlResult{{data: []byte("ok")}}}
	p := WrapRetry(inner, 3, time.Millisecond)
	data, err := p.Download(context.Background(), &api.Subtitle{})
	if err != nil {
		t.Fatalf("Download() error = %v, want nil", err)
	}
	if string(data) != "ok" {
		t.Fatalf("Download() = %q, want %q", data, "ok")
	}
	if got := gk_subflux_u27_countMsg(*recs, gk_subflux_u27_msgRecovered); got != 0 {
		t.Errorf("recovered-log count after first-attempt success = %d, want 0", got)
	}
}

// Recovery on the second attempt must emit exactly one "recovered" log with
// attempts == attempt+1 == 2, and exactly one "retrying" warn with
// attempt == attempt+1 == 1.
// (kills 71:15 negation [recovered must be present at attempt 1],
//  73:53 [attempt+1 -> attempts=2], 88:50 [attempt+1 -> attempt=1])
func TestGk_subflux_u27_Retry_recoverOnSecondAttemptLogs(t *testing.T) {
	recs := gk_subflux_u27_captureLogs(t)
	inner := &retryFakeProvider{name: "p", dlResults: []dlResult{
		{err: &httputil.HTTPStatusError{Code: 503}}, // transient: retried
		{data: []byte("ok")},                        // success on attempt index 1
	}}
	p := WrapRetry(inner, 3, time.Millisecond)
	data, err := p.Download(context.Background(), &api.Subtitle{})
	if err != nil {
		t.Fatalf("Download() error = %v, want nil", err)
	}
	if string(data) != "ok" {
		t.Fatalf("Download() = %q, want %q", data, "ok")
	}
	if got := gk_subflux_u27_countMsg(*recs, gk_subflux_u27_msgRecovered); got != 1 {
		t.Fatalf("recovered-log count = %d, want 1", got)
	}
	if v, ok := gk_subflux_u27_firstAttr(*recs, gk_subflux_u27_msgRecovered, "attempts"); !ok || v != int64(2) {
		t.Errorf("recovered-log attempts = %v (present=%v), want 2", v, ok)
	}
	if got := gk_subflux_u27_countMsg(*recs, gk_subflux_u27_msgRetrying); got != 1 {
		t.Fatalf("retrying-warn count = %d, want 1", got)
	}
	if v, ok := gk_subflux_u27_firstAttr(*recs, gk_subflux_u27_msgRetrying, "attempt"); !ok || v != int64(1) {
		t.Errorf("retrying-warn attempt = %v (present=%v), want 1", v, ok)
	}
}
