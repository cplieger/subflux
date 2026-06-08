// Package provider contains the provider registry and retry wrappers.
package provider

import (
	"context"
	"log/slog"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
)

// DownloadRetryAttempts is the maximum number of download attempts per provider.
const DownloadRetryAttempts = 3

// DownloadRetryInitBackoff is the initial backoff duration between download retries.
const DownloadRetryInitBackoff = 2 * time.Second

// maxBackoff caps the per-attempt sleep to prevent a misconfigured
// maxAttempts from producing multi-hour stalls.
const maxRetryBackoff = 30 * time.Second

// retryProvider wraps a Provider and retries Download on transient errors
// (HTTP 5xx gateway responses and common network-level failures). Search
// is not retried because the scan engine already handles partial results
// and per-provider timeouts.
type retryProvider struct {
	inner       api.Provider
	maxAttempts int
	initBackoff time.Duration
}

// WrapRetryAll wraps each provider with download retry logic.
func WrapRetryAll(providers []api.Provider, maxAttempts int, initBackoff time.Duration) []api.Provider {
	wrapped := make([]api.Provider, len(providers))
	for i, p := range providers {
		wrapped[i] = WrapRetry(p, maxAttempts, initBackoff)
	}
	return wrapped
}

// WrapRetry wraps a provider with download retry logic.
// If the inner provider implements ShowSubtitleCounter, the wrapper
// preserves that interface.
func WrapRetry(p api.Provider, maxAttempts int, initBackoff time.Duration) api.Provider {
	rp := &retryProvider{inner: p, maxAttempts: maxAttempts, initBackoff: initBackoff}
	if c, ok := p.(api.ShowSubtitleCounter); ok {
		return &retryCounterProvider{retryProvider: rp, counter: c}
	}
	return rp
}

func (r *retryProvider) Name() api.ProviderID { return r.inner.Name() }

func (r *retryProvider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	return r.inner.Search(ctx, req)
}

func (r *retryProvider) Download(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	if r.maxAttempts <= 0 {
		return r.inner.Download(ctx, sub)
	}
	backoff := r.initBackoff
	var lastErr error
	for attempt := range r.maxAttempts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := r.inner.Download(ctx, sub)
		if err == nil {
			if attempt > 0 {
				slog.Info("download recovered after transient errors",
					"provider", r.inner.Name(), "attempts", attempt+1)
			}
			return data, nil
		}
		if !httputil.IsTransient(err) {
			slog.Debug("download error is not transient, not retrying",
				"provider", r.inner.Name(), "error", err)
			return nil, err
		}
		lastErr = err
		if attempt == r.maxAttempts-1 {
			break
		}
		delay := httputil.JitteredBackoff(backoff)
		slog.Warn("download failed with transient error, retrying",
			"provider", r.inner.Name(), "attempt", attempt+1,
			"backoff_ms", delay.Milliseconds(), "error", err)
		if err := httputil.SleepCtx(ctx, delay); err != nil {
			return nil, err
		}
		backoff = httputil.SafeDouble(backoff)
		backoff = min(backoff, maxRetryBackoff)
	}
	slog.Error("download failed after all attempts",
		"provider", r.inner.Name(), "attempts", r.maxAttempts, "error", lastErr)
	return nil, lastErr
}

// retryCounterProvider extends retryProvider for providers that also
// implement ShowSubtitleCounter (e.g. OpenSubtitles).
type retryCounterProvider struct {
	*retryProvider

	counter api.ShowSubtitleCounter
}

// CountShowSubtitles delegates to the inner provider without retry.
func (r *retryCounterProvider) CountShowSubtitles(ctx context.Context, imdbID, lang string) (int, error) {
	return r.counter.CountShowSubtitles(ctx, imdbID, lang)
}

// ClearCache forwards to the inner provider if it implements api.CacheClearer.
func (r *retryProvider) ClearCache() {
	if cc, ok := r.inner.(api.CacheClearer); ok {
		cc.ClearCache()
	}
}
