package subdl

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// --- Download path validation ---

func TestDownload_rejects_non_relative_path(t *testing.T) {
	t.Parallel()

	p := &Provider{apiKey: "test", client: http.DefaultClient}

	tests := []struct {
		name string
		url  string
	}{
		{name: "absolute URL", url: "https://evil.com/steal"},
		{name: "no leading slash", url: "dl/sub1.zip"},
		{name: "empty path", url: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sub := &api.Subtitle{DownloadURL: tt.url}
			_, err := p.Download(context.Background(), sub)
			if err == nil {
				t.Errorf("Download(%q) expected error", tt.url)
			}
		})
	}
}

// --- handleDownloadResponse ---

func TestHandleDownloadResponse_success(t *testing.T) {
	t.Parallel()
	body := "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	got, err := handleDownloadResponse(resp, 0, 0)
	if err != nil {
		t.Fatalf("handleDownloadResponse() error: %v", err)
	}
	if !strings.Contains(string(got), "Hello") {
		t.Errorf("handleDownloadResponse() = %q, want containing 'Hello'", got)
	}
}

func TestHandleDownloadResponse_429_rate_limit(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(429) expected error")
	}
	var rateErr *api.RateLimitError
	if !errors.As(err, &rateErr) {
		t.Errorf("handleDownloadResponse(429) error type = %T, want *api.RateLimitError", err)
	}
}

func TestHandleDownloadResponse_500_small_body_rate_limit(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode:    http.StatusInternalServerError,
		ContentLength: 10,
		Body:          io.NopCloser(strings.NewReader("limit")),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(500, small) expected error")
	}
	var rateErr *api.RateLimitError
	if !errors.As(err, &rateErr) {
		t.Errorf("handleDownloadResponse(500, small) error type = %T, want *api.RateLimitError", err)
	}
}

func TestHandleDownloadResponse_500_large_body_generic(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode:    http.StatusInternalServerError,
		ContentLength: 500,
		Body:          io.NopCloser(strings.NewReader("")),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(500, large) expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("handleDownloadResponse(500, large) error = %q, want 'HTTP 500'", err)
	}
}

func TestHandleDownloadResponse_500_unknown_length_generic(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode:    http.StatusInternalServerError,
		ContentLength: -1,
		Body:          io.NopCloser(strings.NewReader("")),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(500, unknown) expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("handleDownloadResponse(500, unknown) error = %q, want 'HTTP 500'", err)
	}
}

func TestHandleDownloadResponse_other_error(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(403) expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("handleDownloadResponse(403) error = %q, want 'HTTP 403'", err)
	}
}

func TestHandleDownloadResponse_extracts_from_zip(t *testing.T) {
	t.Parallel()

	// Build a minimal ZIP archive containing one SRT file.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("subtitle.srt")
	if err != nil {
		t.Fatal(err)
	}
	srtContent := "1\n00:00:01,000 --> 00:00:02,000\nHello from zip\n"
	if _, err := fw.Write([]byte(srtContent)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	got, err := handleDownloadResponse(resp, 0, 0)
	if err != nil {
		t.Fatalf("handleDownloadResponse(zip) error: %v", err)
	}
	if !strings.Contains(string(got), "Hello from zip") {
		t.Errorf("handleDownloadResponse(zip) = %q, want containing 'Hello from zip'", got)
	}
}

func TestHandleDownloadResponse_500_boundary_content_length(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		contentLength int64
		wantRateLimit bool
	}{
		{name: "zero length is rate limit", contentLength: 0, wantRateLimit: true},
		{name: "99 bytes is rate limit", contentLength: 99, wantRateLimit: true},
		{name: "100 bytes is generic error", contentLength: 100, wantRateLimit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{
				StatusCode:    http.StatusInternalServerError,
				ContentLength: tt.contentLength,
				Body:          io.NopCloser(strings.NewReader("")),
			}
			_, err := handleDownloadResponse(resp, 0, 0)
			if err == nil {
				t.Fatal("handleDownloadResponse(500) expected error")
			}
			var rateErr *api.RateLimitError
			isRateLimit := errors.As(err, &rateErr)
			if isRateLimit != tt.wantRateLimit {
				t.Errorf("handleDownloadResponse(500, ContentLength=%d) rate_limit=%v, want %v",
					tt.contentLength, isRateLimit, tt.wantRateLimit)
			}
		})
	}
}

func TestHandleDownloadResponse_rejects_binary_raw_data(t *testing.T) {
	t.Parallel()
	// Binary data that isn't a recognized archive format but fails
	// ValidateSubtitleData's non-text byte threshold.
	binaryData := make([]byte, 200)
	for i := range binaryData {
		binaryData[i] = 0x01 // non-text control byte
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(binaryData)),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(binary raw) expected error")
	}
	if !strings.Contains(err.Error(), "subdl:") {
		t.Errorf("handleDownloadResponse(binary raw) error = %q, want containing 'subdl:'", err)
	}
}

func TestHandleDownloadResponse_rejects_binary_in_archive(t *testing.T) {
	t.Parallel()
	// Build a ZIP containing a .srt file with binary content.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("subtitle.srt")
	if err != nil {
		t.Fatal(err)
	}
	binaryContent := make([]byte, 200)
	for i := range binaryContent {
		binaryContent[i] = 0x01
	}
	if _, err := fw.Write(binaryContent); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	_, err = handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(binary in zip) expected error")
	}
	if !strings.Contains(err.Error(), "subdl:") {
		t.Errorf("handleDownloadResponse(binary in zip) error = %q, want containing 'subdl:'", err)
	}
}
