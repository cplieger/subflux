package confighandlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/testsupport"
)

// A refused save answers the browser with the reason; the WARN is what leaves
// it in the server log too. Serial: these swap slog's process-wide default.

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	testsupport.SwapDefaultLogger(t, slog.New(slog.NewTextHandler(&buf, nil)))
	return &buf
}

func TestStructuredSave_logs_a_rejected_save(t *testing.T) {
	buf := captureLog(t)
	h, _ := newStructuredHandler(t, "")

	rec := doStructuredSave(t, h, `{"sections": {"auth": {"oidc.client_secret": ""}}}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HandleSaveConfigStructured(unknown key) = %d, want 400", rec.Code)
	}
	line := buf.String()
	if !strings.Contains(line, `level=WARN msg="config save rejected" code=config_invalid error="invalid configuration: `) {
		t.Errorf("rejected structured save logged %q, want one WARN naming code=config_invalid and the error", line)
	}
}

func TestRawSave_logs_a_rejected_save(t *testing.T) {
	buf := captureLog(t)
	h, _ := newStructuredHandler(t, "")

	rec := httptest.NewRecorder()
	h.HandleSaveConfig(rec, httptest.NewRequestWithContext(t.Context(),
		http.MethodPut, "/api/config", strings.NewReader("sonarr: [unclosed\n")))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HandleSaveConfig(malformed YAML) = %d, want 400", rec.Code)
	}
	if line := buf.String(); !strings.Contains(line, `level=WARN msg="config save rejected" code=config_invalid error=`) {
		t.Errorf("rejected raw save logged %q, want one WARN naming code=config_invalid and the error", line)
	}
}

func TestStructuredSave_accepted_save_logs_no_rejection(t *testing.T) {
	buf := captureLog(t)
	h, _ := newStructuredHandler(t, "")

	rec := doStructuredSave(t, h, `{"sections": {
		"sonarr": {"url": "http://sonarr:8989", "api_key": "placeholder-key"},
		"languages": {"default": [{"code": "en"}]}
	}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleSaveConfigStructured(valid) = %d, want 200: %s", rec.Code, rec.Body)
	}
	if strings.Contains(buf.String(), "config save rejected") {
		t.Errorf("accepted save logged a rejection: %q", buf.String())
	}
}
