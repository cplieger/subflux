package rpid

import (
	"errors"
	"testing"

	authwebauthn "github.com/cplieger/auth/v6/webauthn"
	"github.com/cplieger/webhttp/v3"
)

// The derivation path repairs a host (a stored value wants the registrable
// domain); the origin path refuses (a bound origin is a comparison key the
// browser re-presents). Every row pins the documented answer on BOTH sides, so
// neither normalizer can drift without this going red — including a
// reintroduced trailing-dot strip in ParseOrigin.
func TestHostPathsAgreeOrDivergeAsDocumented(t *testing.T) {
	tests := []struct {
		host      string
		wantCanon string
		// wantOrigin is ParseOrigin's host, "" when it refuses.
		wantOrigin string
		wantReason authwebauthn.OriginRejection
	}{
		// Agreeing rows.
		{host: "subflux.example.com", wantCanon: "subflux.example.com", wantOrigin: "subflux.example.com"},
		{host: "EXAMPLE.COM", wantCanon: "example.com", wantOrigin: "example.com"},
		{host: "example.com:8443", wantCanon: "example.com", wantOrigin: "example.com"},
		{host: "sub.localhost", wantCanon: "sub.localhost", wantOrigin: "sub.localhost"},
		{host: "10.0.0.5", wantCanon: "10.0.0.5", wantOrigin: "10.0.0.5"},
		{host: "-foo.example.com", wantCanon: "-foo.example.com", wantOrigin: "-foo.example.com"},
		{host: "foo.123", wantCanon: "foo.123", wantOrigin: "foo.123"},
		// Jointly refused.
		{host: "sub..example.com", wantReason: authwebauthn.RejectEmptyLabel},
		{host: "example.com..", wantReason: authwebauthn.RejectTrailingDot},
		{host: "[bad", wantReason: authwebauthn.RejectMalformed},
		{host: "allowed.example:garbage", wantReason: authwebauthn.RejectMalformed},
		{host: "bücher.example", wantReason: authwebauthn.RejectNonASCII},
		// Documented divergences.
		{host: "example.com.", wantCanon: "example.com", wantReason: authwebauthn.RejectTrailingDot},
		{host: "127.0.0.001", wantCanon: "", wantOrigin: "127.0.0.001"},
		{host: "[0:0:0:0:0:0:0:1]", wantCanon: "::1", wantOrigin: "0:0:0:0:0:0:0:1"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := webhttp.CanonicalHost(tt.host); got != tt.wantCanon {
				t.Errorf("CanonicalHost(%q) = %q, want %q", tt.host, got, tt.wantCanon)
			}
			o, err := authwebauthn.ParseOrigin("https://" + tt.host)
			if tt.wantReason != "" {
				var oe *authwebauthn.OriginError
				if !errors.As(err, &oe) {
					t.Fatalf("ParseOrigin(https://%s) = %v, %v; want a *OriginError", tt.host, o, err)
				}
				if oe.Reason != tt.wantReason {
					t.Errorf("ParseOrigin(https://%s) reason = %q, want %q", tt.host, oe.Reason, tt.wantReason)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOrigin(https://%s) error = %v, want nil", tt.host, err)
			}
			if o.Host() != tt.wantOrigin {
				t.Errorf("ParseOrigin(https://%s).Host() = %q, want %q", tt.host, o.Host(), tt.wantOrigin)
			}
		})
	}
}
