package authhandlers

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureSlog swaps the default slog logger for one writing JSON to a
// buffer, runs fn, and restores the previous default. Returns the
// emitted records (one per logged event) parsed from JSON.
func captureSlog(t *testing.T, fn func()) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	fn()

	var records []map[string]any
	for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("malformed slog JSON line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func TestAudit_emits_fixed_attributes(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("User-Agent", "test-agent/1.0")

	records := captureSlog(t, func() {
		Audit(req, slog.LevelInfo, AuditLoginSuccess, true, "alice",
			slog.String("method", "password"))
	})

	if got := len(records); got != 1 {
		t.Fatalf("want 1 audit record, got %d", got)
	}
	rec := records[0]
	if got := rec["msg"]; got != "audit" {
		t.Errorf("msg: got %v, want \"audit\"", got)
	}
	if got := rec["event_kind"]; got != "auth" {
		t.Errorf("event_kind: got %v, want \"auth\"", got)
	}
	if got := rec["event"]; got != string(AuditLoginSuccess) {
		t.Errorf("event: got %v, want %v", got, AuditLoginSuccess)
	}
	if got := rec["success"]; got != true {
		t.Errorf("success: got %v, want true", got)
	}
	if got := rec["user"]; got != "alice" {
		t.Errorf("user: got %v, want \"alice\"", got)
	}
	if got := rec["ip"]; got != "10.0.0.1" {
		t.Errorf("ip: got %v, want \"10.0.0.1\"", got)
	}
	if got := rec["user_agent"]; got != "test-agent/1.0" {
		t.Errorf("user_agent: got %v, want \"test-agent/1.0\"", got)
	}
	if got := rec["method"]; got != "password" {
		t.Errorf("extra attr method: got %v, want \"password\"", got)
	}
	if got := rec["level"]; got != "INFO" {
		t.Errorf("level: got %v, want \"INFO\"", got)
	}
}

func TestAudit_failure_emits_at_warn(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "192.168.1.50:5432"

	records := captureSlog(t, func() {
		Audit(req, slog.LevelWarn, AuditLoginFailure, false, "bob",
			slog.String("reason", "invalid_password"))
	})

	if got := len(records); got != 1 {
		t.Fatalf("want 1 audit record, got %d", got)
	}
	rec := records[0]
	if got := rec["level"]; got != "WARN" {
		t.Errorf("level: got %v, want \"WARN\"", got)
	}
	if got := rec["success"]; got != false {
		t.Errorf("success: got %v, want false", got)
	}
	if got := rec["reason"]; got != "invalid_password" {
		t.Errorf("reason: got %v, want \"invalid_password\"", got)
	}
}

func TestAudit_unknown_user_records_empty_string(t *testing.T) {
	// Failed login on an unknown username should still emit an audit
	// record so forensic queries see the attempt; user is empty string.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "203.0.113.7:1234"

	records := captureSlog(t, func() {
		Audit(req, slog.LevelWarn, AuditLoginFailure, false, "",
			slog.String("reason", "unknown_username"))
	})

	if got := len(records); got != 1 {
		t.Fatalf("want 1 audit record, got %d", got)
	}
	if got := records[0]["user"]; got != "" {
		t.Errorf("user: got %v, want empty string", got)
	}
	if got := records[0]["ip"]; got != "203.0.113.7" {
		t.Errorf("ip: got %v, want \"203.0.113.7\"", got)
	}
}

func TestAudit_emits_all_extra_attributes(t *testing.T) {
	// Audit must append every caller-supplied attribute verbatim. Passing
	// more than six extra attrs also exercises the attrs-slice capacity
	// calculation (6+len(kvs)): an under-allocation there makes a negative
	// slice size and panics, so the recover converts that into a clean
	// failure and the per-attr assertions prove every attr is emitted.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.9:5555"

	var records []map[string]any
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("Audit panicked with 7 extra attrs: %v (capacity must be 6+len(kvs))", rec)
			}
		}()
		records = captureSlog(t, func() {
			Audit(req, slog.LevelInfo, AuditLoginSuccess, true, "alice",
				slog.String("a", "1"), slog.String("b", "2"), slog.String("c", "3"),
				slog.String("d", "4"), slog.String("e", "5"), slog.String("f", "6"),
				slog.String("g", "7"))
		})
	}()

	if got := len(records); got != 1 {
		t.Fatalf("want 1 audit record, got %d", got)
	}
	rec := records[0]
	if got := rec["a"]; got != "1" {
		t.Errorf("attr a: got %v, want \"1\"", got)
	}
	if got := rec["d"]; got != "4" {
		t.Errorf("attr d: got %v, want \"4\"", got)
	}
	if got := rec["g"]; got != "7" {
		t.Errorf("attr g: got %v, want \"7\" (all extra attrs must be emitted)", got)
	}
}

func TestAudit_accepts_raw_key_value_pairs(t *testing.T) {
	// Audit's contract allows raw key/value pairs alongside slog.Attr values
	// (documented: "raw kv pairs work too but lose type info"). Exercise that
	// path through the public API and confirm the pair lands in the record.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.2:4444"

	records := captureSlog(t, func() {
		Audit(req, slog.LevelWarn, AuditLoginFailure, false, "bob", "reason", "locked_out")
	})

	if got := len(records); got != 1 {
		t.Fatalf("want 1 audit record, got %d", got)
	}
	if got := records[0]["reason"]; got != "locked_out" {
		t.Errorf("raw kv pair: reason = %v, want \"locked_out\"", got)
	}
}
