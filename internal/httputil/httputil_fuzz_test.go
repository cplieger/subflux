package httputil

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func FuzzParseRetryAfter(f *testing.F) {
	f.Add("")
	f.Add("60")
	f.Add("-1")
	f.Add("Sun, 06 Nov 1994 08:49:37 GMT")
	f.Add("garbage!@#$%")
	f.Add("0")
	f.Add("3600")

	f.Fuzz(func(t *testing.T, val string) {
		resp := &http.Response{Header: http.Header{"Retry-After": {val}}}
		d := ParseRetryAfter(resp)
		if d < 0 {
			t.Fatalf("ParseRetryAfter(%q) returned negative duration: %v", val, d)
		}
	})
}

func FuzzRedactTransportError(f *testing.F) {
	f.Add("connection refused", "prefix", "mykey123")
	f.Add("", "prefix", "")
	f.Add("error with mykey123 inside", "fetch", "mykey123")
	f.Add("no secret here", "download", "absent")
	f.Add("Get \"https://api.example.com?key=mykey123\": EOF", "api", "mykey123")

	f.Fuzz(func(t *testing.T, errMsg, prefix, secret string) {
		var err error
		if errMsg != "" {
			err = &testError{msg: errMsg}
		}
		result := RedactTransportError(err, prefix, secret)
		if err == nil {
			if result != nil {
				t.Fatal("expected nil for nil input")
			}
			return
		}
		if secret != "" && result != nil {
			// Skip check when the secret is a substring of the redaction
			// marker itself (e.g. secret="C" appears in "[REDACTED]").
			if !strings.Contains("[REDACTED]", secret) {
				if strings.Contains(result.Error(), secret) {
					t.Fatalf("output contains secret: %q", result.Error())
				}
			}
		}
	})
}

func FuzzSafeDouble(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(1))
	f.Add(int64(-1))
	f.Add(int64(time.Hour))
	f.Add(int64(1<<62 - 1))
	f.Add(int64(1<<63 - 1))

	f.Fuzz(func(t *testing.T, ns int64) {
		d := time.Duration(ns)
		result := SafeDouble(d)
		if d > 0 && result < 0 {
			t.Fatalf("SafeDouble(%v) returned negative: %v", d, result)
		}
		if d > 0 && result < d {
			t.Fatalf("SafeDouble(%v) returned smaller value: %v", d, result)
		}
	})
}

// testError is a simple error type for fuzz testing.
type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
