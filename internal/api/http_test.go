package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Helpers ---

// decodeErrorBody decodes {"error": msg} into msg, failing the test on
// decode error.
func decodeErrorBody(t *testing.T, body io.Reader) string {
	t.Helper()
	var m map[string]string
	if err := json.NewDecoder(body).Decode(&m); err != nil {
		t.Fatalf("decode JSON error body: %v", err)
	}
	return m["error"]
}

// assertJSONHeaders verifies the standard JSON response headers are set
// by every helper that calls jsonHeaders.
func assertJSONHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != contentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", got, contentTypeJSON)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
}

// --- WriteJSON / WriteJSONStatus ---

func TestWriteJSON_sets_status_200_and_encodes_body(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	WriteJSON(rec, map[string]int{"count": 42})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertJSONHeaders(t, rec)

	var got map[string]int
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["count"] != 42 {
		t.Errorf("body[count] = %d, want 42", got["count"])
	}
}

func TestWriteJSONStatus_sets_custom_status_code(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code int
	}{
		{"201 created", http.StatusCreated},
		{"202 accepted", http.StatusAccepted},
		{"204 no content still writes body", http.StatusNoContent},
		{"418 teapot", http.StatusTeapot},
		{"500 internal", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()

			WriteJSONStatus(rec, tt.code, map[string]string{"msg": "ok"})

			if rec.Code != tt.code {
				t.Errorf("status = %d, want %d", rec.Code, tt.code)
			}
			assertJSONHeaders(t, rec)
		})
	}
}

func TestWriteJSONStatus_encode_error_does_not_panic(t *testing.T) {
	t.Parallel()

	// channels cannot be JSON-encoded; json.Encoder.Encode returns an
	// error. The helper must swallow the error (logged at DEBUG)
	// without panicking.
	rec := httptest.NewRecorder()
	WriteJSONStatus(rec, http.StatusOK, make(chan int))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// --- JSONError ---

func TestJSONError_wraps_error_text(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	JSONError(rec, errors.New("boom"), http.StatusBadRequest)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	assertJSONHeaders(t, rec)
	if got := decodeErrorBody(t, rec.Body); got != "boom" {
		t.Errorf("error body = %q, want %q", got, "boom")
	}
}

// --- Ok ---

func TestOk_returns_200_with_ok_true(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	Ok(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertJSONHeaders(t, rec)

	var got map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !got["ok"] {
		t.Errorf(`body = %v, want {"ok": true}`, got)
	}
}

// --- Named status helpers (table-driven) ---

func TestNamedStatusHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fn       func(http.ResponseWriter, *http.Request)
		name     string
		msg      string
		wantCode int
	}{
		{func(w http.ResponseWriter, r *http.Request) { BadRequestC(w, r, "", "missing field") }, "BadRequestC", "missing field", http.StatusBadRequest},
		{func(w http.ResponseWriter, r *http.Request) { UnauthorizedC(w, r, "", "login required") }, "UnauthorizedC", "login required", http.StatusUnauthorized},
		{func(w http.ResponseWriter, r *http.Request) { ForbiddenC(w, r, "", "no permission") }, "ForbiddenC", "no permission", http.StatusForbidden},
		{func(w http.ResponseWriter, r *http.Request) { NotFoundC(w, r, "", "does not exist") }, "NotFoundC", "does not exist", http.StatusNotFound},
		{func(w http.ResponseWriter, r *http.Request) { ConflictC(w, r, "", "already exists") }, "ConflictC", "already exists", http.StatusConflict},
		{func(w http.ResponseWriter, r *http.Request) { TooManyRequestsC(w, r, "", "slow down") }, "TooManyRequestsC", "slow down", http.StatusTooManyRequests},
		{func(w http.ResponseWriter, r *http.Request) { ServiceUnavailableC(w, r, "", "configuring") }, "ServiceUnavailableC", "configuring", http.StatusServiceUnavailable},
		{func(w http.ResponseWriter, r *http.Request) { BadGatewayC(w, r, "", "upstream failed") }, "BadGatewayC", "upstream failed", http.StatusBadGateway},
		{func(w http.ResponseWriter, r *http.Request) { PayloadTooLargeC(w, r, "", "body too big") }, "PayloadTooLargeC", "body too big", http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)

			tt.fn(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("%s status = %d, want %d",
					tt.name, rec.Code, tt.wantCode)
			}
			assertJSONHeaders(t, rec)
			if got := decodeErrorBody(t, rec.Body); got != tt.msg {
				t.Errorf("%s error body = %q, want %q",
					tt.name, got, tt.msg)
			}
		})
	}
}

// --- MethodNotAllowed ---

func TestMethodNotAllowed_returns_405_with_fixed_message(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	MethodNotAllowedC(rec, req, "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
	assertJSONHeaders(t, rec)
	if got := decodeErrorBody(t, rec.Body); got != "method not allowed" {
		t.Errorf("error body = %q, want %q", got, "method not allowed")
	}
}

// --- InternalError ---

func TestInternalError_returns_500_with_generic_message(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	// The underlying error must NEVER leak into the response body.
	InternalErrorC(rec, req, errors.New("database password: hunter2"), "")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
	assertJSONHeaders(t, rec)
	body := decodeErrorBody(t, rec.Body)
	if body != "internal error" {
		t.Errorf("error body = %q, want %q", body, "internal error")
	}
	if strings.Contains(body, "hunter2") {
		t.Errorf("error body leaked underlying error text: %q", body)
	}
}

func TestInternalError_nil_error_still_returns_500(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	InternalErrorC(rec, req, nil, "")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
	if got := decodeErrorBody(t, rec.Body); got != "internal error" {
		t.Errorf("error body = %q, want %q", got, "internal error")
	}
}

func TestInternalError_accepts_extra_log_attrs(t *testing.T) {
	t.Parallel()

	// Variadic logAttrs must not affect the HTTP response shape.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	InternalErrorC(rec, req, errors.New("boom"), "",
		"request_id", "req-42",
		"user_id", int64(7),
	)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
	if got := decodeErrorBody(t, rec.Body); got != "internal error" {
		t.Errorf("error body = %q, want %q", got, "internal error")
	}
}

// --- Security header invariant ---

// Every helper that writes JSON must set X-Content-Type-Options:
// nosniff. This prevents browsers from MIME-sniffing an error payload
// into an executable content type. Enforced once across the whole
// helper set so a new helper that forgets jsonHeaders fails this test.
func TestAllHelpers_set_nosniff_header(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)

	helpers := []struct {
		call func(http.ResponseWriter)
		name string
	}{
		{func(w http.ResponseWriter) { WriteJSON(w, 1) }, "WriteJSON"},
		{func(w http.ResponseWriter) { WriteJSONStatus(w, 201, 1) }, "WriteJSONStatus"},
		{func(w http.ResponseWriter) { JSONError(w, errors.New("x"), 400) }, "JSONError"},
		{Ok, "Ok"},
		{func(w http.ResponseWriter) { BadRequestC(w, req, "", "x") }, "BadRequestC"},
		{func(w http.ResponseWriter) { UnauthorizedC(w, req, "", "x") }, "UnauthorizedC"},
		{func(w http.ResponseWriter) { ForbiddenC(w, req, "", "x") }, "ForbiddenC"},
		{func(w http.ResponseWriter) { NotFoundC(w, req, "", "x") }, "NotFoundC"},
		{func(w http.ResponseWriter) { MethodNotAllowedC(w, req, "") }, "MethodNotAllowedC"},
		{func(w http.ResponseWriter) { ConflictC(w, req, "", "x") }, "ConflictC"},
		{func(w http.ResponseWriter) { TooManyRequestsC(w, req, "", "x") }, "TooManyRequestsC"},
		{func(w http.ResponseWriter) { ServiceUnavailableC(w, req, "", "x") }, "ServiceUnavailableC"},
		{func(w http.ResponseWriter) { BadGatewayC(w, req, "", "x") }, "BadGatewayC"},
		{func(w http.ResponseWriter) { PayloadTooLargeC(w, req, "", "x") }, "PayloadTooLargeC"},
		{func(w http.ResponseWriter) { InternalErrorC(w, req, errors.New("x"), "") }, "InternalErrorC"},
	}

	for _, h := range helpers {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()

			h.call(rec)

			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("%s: X-Content-Type-Options = %q, want %q",
					h.name, got, "nosniff")
			}
			if got := rec.Header().Get("Content-Type"); got != contentTypeJSON {
				t.Errorf("%s: Content-Type = %q, want %q",
					h.name, got, contentTypeJSON)
			}
		})
	}
}

// --- Typed-code envelope tests ---

func TestBadRequestC_emits_typed_envelope(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/x", http.NoBody)
	req = req.WithContext(WithRequestID(req.Context(), "abc123"))

	BadRequestC(rec, req, CodeBadRequest, "bad input")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var envelope errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error != "bad input" {
		t.Errorf("error = %q, want %q", envelope.Error, "bad input")
	}
	if envelope.Code != string(CodeBadRequest) {
		t.Errorf("code = %q, want %q", envelope.Code, CodeBadRequest)
	}
	if envelope.RequestID != "abc123" {
		t.Errorf("request_id = %q, want %q", envelope.RequestID, "abc123")
	}
}

func TestBadRequestC_empty_code_omits_code_and_request_id(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	BadRequestC(rec, req, "", "bad input")

	body := rec.Body.String()
	// When code is empty and no request-id is in context, the wire
	// format matches the old legacy shape: only `error` is present.
	if strings.Contains(body, `"code"`) {
		t.Errorf("BadRequestC with empty code leaked `code` field: %s", body)
	}
	if strings.Contains(body, `"request_id"`) {
		t.Errorf("BadRequestC without request-id leaked `request_id` field: %s", body)
	}
	if !strings.Contains(body, `"error":"bad input"`) {
		t.Errorf("BadRequestC missing error field: %s", body)
	}
}

// --- Log-guard tests (serial: they capture the default slog logger) ---

// TestWriteJSONStatus_logs_on_encode_failure pins the encode-error guard: an
// unencodable value (a channel) makes the JSON encoder fail and the helper
// logs at DEBUG, while a value that encodes cleanly produces no such log.
// Not t.Parallel: captureSlog swaps the default logger.
func TestWriteJSONStatus_logs_on_encode_failure(t *testing.T) {
	// Encode failure path logs at DEBUG.
	buf := captureSlog(t)
	WriteJSONStatus(httptest.NewRecorder(), http.StatusOK, make(chan int))
	if !strings.Contains(buf.String(), "writeJSON encode failed") {
		t.Errorf("encode failure: expected DEBUG log, got %q", buf.String())
	}

	// Successful encode path does not log.
	buf2 := captureSlog(t)
	WriteJSONStatus(httptest.NewRecorder(), http.StatusOK, map[string]int{"k": 1})
	if strings.Contains(buf2.String(), "writeJSON encode failed") {
		t.Errorf("encode success: expected no log, got %q", buf2.String())
	}
}

// TestInternalErrorC_logs_cause_only_when_error_nonnil pins the err != nil
// guard around the ERROR log: a non-nil cause is logged, while a nil cause
// produces no log. (That the cause never leaks into the response body is
// asserted by TestInternalError_returns_500_with_generic_message.)
// Not t.Parallel: captureSlog swaps the default logger.
func TestInternalErrorC_logs_cause_only_when_error_nonnil(t *testing.T) {
	// Non-nil error: the cause is logged at ERROR.
	buf := captureSlog(t)
	InternalErrorC(httptest.NewRecorder(), nil, errors.New("boom-cause"), "")
	if !strings.Contains(buf.String(), "boom-cause") {
		t.Errorf("non-nil error: expected ERROR log with cause, got %q", buf.String())
	}

	// Nil error: nothing is logged.
	buf2 := captureSlog(t)
	InternalErrorC(httptest.NewRecorder(), nil, nil, "")
	if buf2.Len() != 0 {
		t.Errorf("nil error: expected no log, got %q", buf2.String())
	}
}
