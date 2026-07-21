package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
	"github.com/cplieger/subflux/internal/search/release"
)

// retryFakeProvider records calls and returns configured errors.
type retryFakeProvider struct {
	name      string
	dlResults []dlResult
	dlCalls   int
}

type dlResult struct {
	err  error
	data []byte
}

func (f *retryFakeProvider) Name() api.ProviderID { return api.ProviderID(f.name) }

func (f *retryFakeProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return nil, nil
}

func (f *retryFakeProvider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	i := f.dlCalls
	f.dlCalls++
	if i < len(f.dlResults) {
		return f.dlResults[i].data, f.dlResults[i].err
	}
	return []byte("ok"), nil
}

// retryFakeCounterProvider also implements ShowSubtitleCounter.
type retryFakeCounterProvider struct {
	retryFakeProvider

	countCalls int
}

func (f *retryFakeCounterProvider) CountShowSubtitles(_ context.Context, _, _ string) (int, error) {
	f.countCalls++
	return 42, nil
}

func TestRetryProvider_error_classification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		dlResults []dlResult
		wantErr   bool
		wantCalls int
	}{
		{
			name:      "success_no_retry",
			dlResults: []dlResult{{data: []byte("subtitle data")}},
			wantErr:   false,
			wantCalls: 1,
		},
		{
			name: "retries_on_503",
			dlResults: []dlResult{
				{err: &httputil.HTTPStatusError{Code: 503}},
				{err: &httputil.HTTPStatusError{Code: 503}},
				{data: []byte("ok")},
			},
			wantErr:   false,
			wantCalls: 3,
		},
		{
			name: "retries_on_502",
			dlResults: []dlResult{
				{err: &httputil.HTTPStatusError{Code: 502}},
				{data: []byte("ok")},
			},
			wantErr:   false,
			wantCalls: 2,
		},
		{
			name: "retries_on_504",
			dlResults: []dlResult{
				{err: &httputil.HTTPStatusError{Code: 504}},
				{data: []byte("ok")},
			},
			wantErr:   false,
			wantCalls: 2,
		},
		{
			name:      "no_retry_on_500",
			dlResults: []dlResult{{err: &httputil.HTTPStatusError{Code: 500}}},
			wantErr:   true,
			wantCalls: 1,
		},
		{
			name:      "no_retry_on_4xx",
			dlResults: []dlResult{{err: &httputil.HTTPStatusError{Code: 400}}},
			wantErr:   true,
			wantCalls: 1,
		},
		{
			name:      "no_retry_on_AuthError",
			dlResults: []dlResult{{err: &api.AuthError{Msg: "invalid credentials"}}},
			wantErr:   true,
			wantCalls: 1,
		},
		{
			name:      "no_retry_on_RateLimitError",
			dlResults: []dlResult{{err: &api.RateLimitError{Msg: "rate limited"}}},
			wantErr:   true,
			wantCalls: 1,
		},
		{
			name: "exhausts_retries",
			dlResults: []dlResult{
				{err: &httputil.HTTPStatusError{Code: 503}},
				{err: &httputil.HTTPStatusError{Code: 503}},
				{err: &httputil.HTTPStatusError{Code: 503}},
			},
			wantErr:   true,
			wantCalls: 3,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner := &retryFakeProvider{
				name:      "test",
				dlResults: tc.dlResults,
			}
			p := WrapRetry(inner, 3, time.Millisecond)

			_, err := p.Download(context.Background(), &api.Subtitle{})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if inner.dlCalls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", inner.dlCalls, tc.wantCalls)
			}
		})
	}
}

func TestRetryProvider_context_cancellation(t *testing.T) {
	t.Parallel()
	inner := &retryFakeProvider{
		name: "test",
		dlResults: []dlResult{
			{err: &httputil.HTTPStatusError{Code: 503}},
		},
	}
	p := WrapRetry(inner, 3, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := p.Download(ctx, &api.Subtitle{})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(err, context.Canceled)", err)
	}
}

func TestRetryProvider_cancellation_during_backoff(t *testing.T) {
	t.Parallel()
	inner := &retryFakeProvider{
		name: "test",
		dlResults: []dlResult{
			{err: &httputil.HTTPStatusError{Code: 503}},
			{err: &httputil.HTTPStatusError{Code: 503}},
			{err: &httputil.HTTPStatusError{Code: 503}},
		},
	}
	p := WrapRetry(inner, 3, 500*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	_, err := p.Download(ctx, &api.Subtitle{})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(err, context.Canceled)", err)
	}
	if elapsed > 400*time.Millisecond {
		t.Errorf("elapsed = %v, want < 400ms (cancellation should short-circuit backoff)", elapsed)
	}
	if inner.dlCalls != 1 {
		t.Errorf("dlCalls = %d, want 1 (backoff interrupted before second attempt)", inner.dlCalls)
	}
}

func TestRetryProvider_backoff_doubles_between_attempts(t *testing.T) {
	t.Parallel()

	callTimes := make([]time.Time, 0, 4)
	inner := &retryFakeProviderTimed{
		retryFakeProvider: retryFakeProvider{
			name: "test",
			dlResults: []dlResult{
				{err: &httputil.HTTPStatusError{Code: 503}},
				{err: &httputil.HTTPStatusError{Code: 503}},
				{err: &httputil.HTTPStatusError{Code: 503}},
				{data: []byte("ok")},
			},
		},
		callTimes: &callTimes,
	}
	p := WrapRetry(inner, 4, 50*time.Millisecond)

	_, err := p.Download(context.Background(), &api.Subtitle{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(callTimes) != 4 {
		t.Fatalf("callTimes len = %d, want 4", len(callTimes))
	}

	d1 := callTimes[1].Sub(callTimes[0])
	d3 := callTimes[3].Sub(callTimes[2])

	if d3 < time.Duration(float64(d1)*1.5) {
		t.Errorf("backoff not doubling: d1=%v, d3=%v (d3 should be ≥1.5x d1)", d1, d3)
	}
}

func TestRetryProvider_maxAttempts_one_calls_once_no_backoff(t *testing.T) {
	t.Parallel()
	inner := &retryFakeProvider{
		name: "test",
		dlResults: []dlResult{
			{err: &httputil.HTTPStatusError{Code: 503}},
			{data: []byte("should-not-reach")},
		},
	}
	p := WrapRetry(inner, 1, 500*time.Millisecond)

	start := time.Now()
	_, err := p.Download(context.Background(), &api.Subtitle{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v, want 503 status", err)
	}
	if inner.dlCalls != 1 {
		t.Errorf("dlCalls = %d, want 1 (maxAttempts=1 means exactly one attempt)", inner.dlCalls)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("elapsed = %v, want < 200ms (no backoff sleep after final attempt)", elapsed)
	}
}

func TestRetryProvider_preserves_name(t *testing.T) {
	t.Parallel()
	inner := &retryFakeProvider{name: "myProvider"}
	p := WrapRetry(inner, 3, time.Millisecond)
	if p.Name() != "myProvider" {
		t.Errorf("Name() = %q, want %q", p.Name(), "myProvider")
	}
}

func TestRetryProvider_delegates_search(t *testing.T) {
	t.Parallel()
	inner := &retryFakeProvider{name: "test"}
	p := WrapRetry(inner, 3, time.Millisecond)

	results, err := p.Search(context.Background(), &api.SearchRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("results = %v, want nil", results)
	}
}

func TestWrapRetry_preserves_ShowSubtitleCounter(t *testing.T) {
	t.Parallel()
	inner := &retryFakeCounterProvider{
		retryFakeProvider: retryFakeProvider{name: "opensubtitles"},
	}
	p := WrapRetry(inner, 3, time.Millisecond)

	counter, ok := p.(api.ShowSubtitleCounter)
	if !ok {
		t.Fatal("wrapped provider does not implement ShowSubtitleCounter")
	}
	count, err := counter.CountShowSubtitles(context.Background(), "tt123", "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 42 {
		t.Errorf("count = %d, want 42", count)
	}
	if inner.countCalls != 1 {
		t.Errorf("countCalls = %d, want 1", inner.countCalls)
	}
}

func TestWrapRetry_plain_provider_no_counter(t *testing.T) {
	t.Parallel()
	inner := &retryFakeProvider{name: "hdbits"}
	p := WrapRetry(inner, 3, time.Millisecond)

	if _, ok := p.(api.ShowSubtitleCounter); ok {
		t.Error("plain provider should not implement ShowSubtitleCounter")
	}
}

func TestRetryProvider_ClearCache_delegates(t *testing.T) {
	t.Parallel()
	inner := &retryFakeCacheProvider{retryFakeProvider: retryFakeProvider{name: "test"}}
	p := WrapRetry(inner, 3, time.Millisecond)

	cc, ok := p.(api.CacheClearer)
	if !ok {
		t.Fatal("wrapped provider does not implement ClearCache")
	}
	cc.ClearCache()
	if !inner.cleared {
		t.Error("ClearCache was not delegated to inner provider")
	}
}

func TestRetryProvider_zero_retries_delegates_directly(t *testing.T) {
	t.Parallel()
	inner := &retryFakeProvider{
		name:      "test",
		dlResults: []dlResult{{data: []byte("direct")}},
	}
	p := WrapRetry(inner, 0, time.Millisecond)

	data, err := p.Download(context.Background(), &api.Subtitle{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "direct" {
		t.Errorf("data = %q, want %q", data, "direct")
	}
	if inner.dlCalls != 1 {
		t.Errorf("calls = %d, want 1", inner.dlCalls)
	}
}

func TestRetryProvider_negative_retries_delegates_directly(t *testing.T) {
	t.Parallel()
	inner := &retryFakeProvider{
		name:      "test",
		dlResults: []dlResult{{err: &httputil.HTTPStatusError{Code: 503}}},
	}
	p := WrapRetry(inner, -1, time.Millisecond)

	_, err := p.Download(context.Background(), &api.Subtitle{})
	if err == nil {
		t.Fatal("expected error")
	}
	if inner.dlCalls != 1 {
		t.Errorf("calls = %d, want 1 (no retry with negative maxAttempts)", inner.dlCalls)
	}
}

func TestRetryProvider_ClearCache_noop_without_inner(t *testing.T) {
	t.Parallel()
	inner := &retryFakeProvider{name: "test"}
	p := WrapRetry(inner, 3, time.Millisecond)

	type clearer interface{ ClearCache() }
	if cc, ok := p.(clearer); ok {
		cc.ClearCache()
	}
}

// retryFakeCacheProvider implements ClearCache for testing delegation.
type retryFakeCacheProvider struct {
	retryFakeProvider

	cleared bool
}

func (f *retryFakeCacheProvider) ClearCache() { f.cleared = true }

// retryFakeProviderTimed records the wall-clock time of each Download call.
type retryFakeProviderTimed struct {
	callTimes *[]time.Time
	retryFakeProvider
}

func (f *retryFakeProviderTimed) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	*f.callTimes = append(*f.callTimes, time.Now())
	i := f.dlCalls
	f.dlCalls++
	if i < len(f.dlResults) {
		return f.dlResults[i].data, f.dlResults[i].err
	}
	return []byte("ok"), nil
}

// --- Benchmarks ---

type benchNoopProvider struct{}

func (benchNoopProvider) Name() api.ProviderID { return "bench" }
func (benchNoopProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return nil, nil
}

func (benchNoopProvider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return []byte("ok"), nil
}

func BenchmarkRetryProvider(b *testing.B) {
	inner := benchNoopProvider{}
	sub := &api.Subtitle{Provider: "bench", ID: "1"}

	for _, attempts := range []int{1, 2, 3} {
		name := fmt.Sprintf("attempts=%d", attempts)
		b.Run("Download/"+name, func(b *testing.B) {
			rp := WrapRetry(inner, attempts, 100*time.Millisecond)
			ctx := context.Background()
			b.ResetTimer()
			for range b.N {
				_, _ = rp.Download(ctx, sub)
			}
		})
		b.Run("Search/"+name, func(b *testing.B) {
			rp := WrapRetry(inner, attempts, 100*time.Millisecond)
			ctx := context.Background()
			req := &api.SearchRequest{Title: "test"}
			b.ResetTimer()
			for range b.N {
				_, _ = rp.Search(ctx, req)
			}
		})
	}
}

// --- download retry logging ---
//
// The retry wrapper emits two slog records carrying human-facing (one-based)
// attempt counts: a "recovered" info log only after at least one transient
// failure, and a "retrying" warn log before each backoff. These tests capture
// the default logger and assert on those records, so they must NOT run in
// parallel (slog.Default is process-global).

const (
	msgDownloadRecovered = "download recovered after transient errors"
	msgDownloadRetrying  = "download failed with transient error, retrying"
)

// Attempt-count attrs are asserted through capture.Recorder.AttrValue (the
// former in-package firstLogAttr walk is gone): first match in capture
// order, rendered form, so an Int64 2 asserts as "2". Each assertion sits
// behind a CountExact(msg) == 1 check, so first-match equals only-match.

// A first-attempt success must not emit the "recovered" log, which is gated on
// having already failed at least once.
func TestRetryProvider_noRecoveredLogOnFirstAttempt(t *testing.T) {
	recs := capture.Default(t)
	inner := &retryFakeProvider{name: "p", dlResults: []dlResult{{data: []byte("ok")}}}
	p := WrapRetry(inner, 3, time.Millisecond)
	data, err := p.Download(context.Background(), &api.Subtitle{})
	if err != nil {
		t.Fatalf("Download() error = %v, want nil", err)
	}
	if string(data) != "ok" {
		t.Fatalf("Download() = %q, want %q", data, "ok")
	}
	if got := recs.CountExact(msgDownloadRecovered); got != 0 {
		t.Errorf("recovered-log count after first-attempt success = %d, want 0", got)
	}
}

// Recovering on the second attempt must emit exactly one "recovered" log
// reporting attempts=2 and exactly one "retrying" warn reporting attempt=1.
func TestRetryProvider_recoveredLogOnSecondAttempt(t *testing.T) {
	recs := capture.Default(t)
	inner := &retryFakeProvider{name: "p", dlResults: []dlResult{
		{err: &httputil.HTTPStatusError{Code: 503}}, // transient: retried
		{data: []byte("ok")},                        // success on the second attempt
	}}
	p := WrapRetry(inner, 3, time.Millisecond)
	data, err := p.Download(context.Background(), &api.Subtitle{})
	if err != nil {
		t.Fatalf("Download() error = %v, want nil", err)
	}
	if string(data) != "ok" {
		t.Fatalf("Download() = %q, want %q", data, "ok")
	}
	if got := recs.CountExact(msgDownloadRecovered); got != 1 {
		t.Fatalf("recovered-log count = %d, want 1", got)
	}
	if v, ok := recs.AttrValue(msgDownloadRecovered, "attempts"); !ok || v != "2" {
		t.Errorf("recovered-log attempts = %q (present=%v), want \"2\"", v, ok)
	}
	if got := recs.CountExact(msgDownloadRetrying); got != 1 {
		t.Fatalf("retrying-warn count = %d, want 1", got)
	}
	if v, ok := recs.AttrValue(msgDownloadRetrying, "attempt"); !ok || v != "1" {
		t.Errorf("retrying-warn attempt = %q (present=%v), want \"1\"", v, ok)
	}
}

// searchResultProvider returns fixed search results (for the ReleaseName
// clamp boundary test).
type searchResultProvider struct {
	retryFakeProvider

	subs []api.Subtitle
}

func (f *searchResultProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return f.subs, nil
}

// TestRetryProvider_search_clamps_release_names pins the provider-boundary
// enforcement of the release-parsing layer's input bound (release package
// doc "Input bound"): every wrapped provider's search results have
// ReleaseName truncated to release.MaxNameLen bytes.
func TestRetryProvider_search_clamps_release_names(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", release.MaxNameLen+200)
	inner := &searchResultProvider{subs: []api.Subtitle{
		{ReleaseName: "Movie.2024.1080p.BluRay.x264-GRP"},
		{ReleaseName: long},
	}}
	p := WrapRetry(inner, 1, time.Millisecond)

	subs, err := p.Search(context.Background(), &api.SearchRequest{})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}
	if len(subs) != 2 {
		t.Fatalf("Search() returned %d subs, want 2", len(subs))
	}
	if subs[0].ReleaseName != "Movie.2024.1080p.BluRay.x264-GRP" {
		t.Errorf("short name changed: %q", subs[0].ReleaseName)
	}
	if len(subs[1].ReleaseName) != release.MaxNameLen {
		t.Errorf("long name len = %d, want clamped to %d", len(subs[1].ReleaseName), release.MaxNameLen)
	}
}
