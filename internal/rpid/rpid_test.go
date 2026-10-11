package rpid

import (
	"errors"
	"testing"

	authwebauthn "github.com/cplieger/auth/v6/webauthn"
)

func TestDerive_accepts(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{"subflux.example.com", "example.com"},
		{"subflux.example.co.uk", "example.co.uk"},
		{"box.my-tailnet.ts.net", "my-tailnet.ts.net"},
		{"foo.duckdns.org", "foo.duckdns.org"},
		{"nas.synology.me", "nas.synology.me"},
		{"user.github.io", "user.github.io"},
		{"p.pages.dev", "p.pages.dev"},
		{"x.myqnapcloud.com", "x.myqnapcloud.com"},
		{"h.dynv6.net", "h.dynv6.net"},
		{"h.freeddns.org", "h.freeddns.org"},
		{"nas.internal", "nas.internal"},
		{"nas.lan", "nas.lan"},
		{"localhost", "localhost"},
		{"localhost:8374", "localhost"},
		{"localhost.", "localhost"},
		{"EXAMPLE.COM", "example.com"},
		{"example.com.", "example.com"},
		{"example.com:8443", "example.com"},
		{"-foo.example.com", "example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got, err := Derive(tt.host)
			if err != nil {
				t.Fatalf("Derive(%q) error = %v, want nil", tt.host, err)
			}
			if got != tt.want {
				t.Errorf("Derive(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestDerive_refuses(t *testing.T) {
	tests := []struct {
		host   string
		reason reason
	}{
		{"duckdns.org", reasonPublicSuffix},
		{"ts.net", reasonPublicSuffix},
		{"synology.me", reasonPublicSuffix},
		{"myqnapcloud.com", reasonPublicSuffix},
		{"dynv6.net", reasonPublicSuffix},
		{"freeddns.org", reasonPublicSuffix},
		{"pages.dev", reasonPublicSuffix},
		{"github.io", reasonPublicSuffix},
		{"nas", reasonSingleLabel},
		{"com", reasonSingleLabel},
		{"lan", reasonSingleLabel},
		{"10.0.0.5", reasonIPLiteral},
		{"127.0.0.1:8374", reasonIPLiteral},
		{"::1", reasonIPLiteral},
		{"[::1]:8443", reasonIPLiteral},
		{"foo.123", reasonIllegalDomain},
		{"allowed.example:garbage", reasonMalformedHost},
		{"[bad", reasonMalformedHost},
		{"sub..example.com", reasonMalformedHost},
		{"127.0.0.001", reasonMalformedHost},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got, err := Derive(tt.host)
			var e *refusal
			if !errors.As(err, &e) {
				t.Fatalf("Derive(%q) = %q, %v; want a *Error", tt.host, got, err)
			}
			if e.Reason != tt.reason {
				t.Errorf("Derive(%q) reason = %q, want %q (message %q)", tt.host, e.Reason, tt.reason, e.Error())
			}
		})
	}
}

func TestDerive_IPIsRefusedBeforeThePSL(t *testing.T) {
	_, err := Derive("10.0.0.5")
	var e *refusal
	if !errors.As(err, &e) {
		t.Fatalf("Derive(10.0.0.5) error = %v, want a *Error", err)
	}
	if e.Reason != reasonIPLiteral {
		t.Fatalf("Derive(10.0.0.5) reason = %q, want %q", e.Reason, reasonIPLiteral)
	}
	if e.Reason == reasonPublicSuffix {
		t.Fatal("Derive(10.0.0.5) reported public_suffix: the IP check ran after the lookup")
	}
}

func TestDerive_illegalDomainWrapsTheLibrarySentinel(t *testing.T) {
	_, err := Derive("foo.123")
	if !errors.Is(err, authwebauthn.ErrIllegalRPID) {
		t.Fatalf("Derive(foo.123) error = %v, want it to match ErrIllegalRPID", err)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		id     string
		reason reason // "" means accepted
	}{
		{"example.com", ""},
		{"sub.example.com", ""},
		{"localhost", ""},
		{"foo.duckdns.org", ""},
		{"", reasonMalformedHost},
		{"Example.COM", reasonNotCanonical},
		{"example.com.", reasonNotCanonical},
		{" example.com", reasonNotCanonical},
		{"http://example.com", reasonMalformedHost},
		{"example.com:8443", reasonMalformedHost},
		{"example.com/subflux", reasonMalformedHost},
		{"10.0.0.5", reasonIPLiteral},
		{"::1", reasonIPLiteral},
		{"duckdns.org", reasonPublicSuffix},
		{"nas", reasonSingleLabel},
		{"foo.123", reasonIllegalDomain},
		{"-foo.example.com", reasonIllegalDomain},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			err := Validate(tt.id)
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("Validate(%q) = %v, want nil", tt.id, err)
				}
				return
			}
			var e *refusal
			if !errors.As(err, &e) {
				t.Fatalf("Validate(%q) = %v, want a *Error", tt.id, err)
			}
			if e.Reason != tt.reason {
				t.Errorf("Validate(%q) reason = %q, want %q", tt.id, e.Reason, tt.reason)
			}
		})
	}
}

func TestValidate_notCanonicalNamesTheSpelling(t *testing.T) {
	err := Validate("Example.COM")
	var e *refusal
	if !errors.As(err, &e) {
		t.Fatalf("Validate(Example.COM) = %v, want a *Error", err)
	}
	if want := `spell it "example.com"`; e.Detail != want {
		t.Errorf("Validate(Example.COM) detail = %q, want %q", e.Detail, want)
	}
}

func TestValidateForHost(t *testing.T) {
	tests := []struct {
		id     string
		host   string
		reason reason
	}{
		{"example.com", "subflux.example.com", ""},
		{"example.com", "example.com", ""},
		{"example.com", "a.b.example.com:8443", ""},
		{"localhost", "localhost:8374", ""},
		{"example.com", "evilexample.com", reasonNotServed},
		{"example.net", "subflux.example.com", reasonNotServed},
		{"example.com", "10.0.0.5", reasonNotServed},
		{"Example.COM", "subflux.example.com", reasonNotCanonical},
	}
	for _, tt := range tests {
		t.Run(tt.id+"_"+tt.host, func(t *testing.T) {
			err := ValidateForHost(tt.id, tt.host)
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("ValidateForHost(%q, %q) = %v, want nil", tt.id, tt.host, err)
				}
				return
			}
			var e *refusal
			if !errors.As(err, &e) {
				t.Fatalf("ValidateForHost(%q, %q) = %v, want a *Error", tt.id, tt.host, err)
			}
			if e.Reason != tt.reason {
				t.Errorf("ValidateForHost(%q, %q) reason = %q, want %q", tt.id, tt.host, e.Reason, tt.reason)
			}
		})
	}
}

func TestValidateForHost_notServedNamesTheDerivedValue(t *testing.T) {
	err := ValidateForHost("example.net", "subflux.example.com")
	var e *refusal
	if !errors.As(err, &e) {
		t.Fatalf("ValidateForHost = %v, want a *Error", err)
	}
	want := `save it from a browser at a host inside "example.net", or use "example.com" for the host you are on`
	if e.Detail != want {
		t.Errorf("detail = %q, want %q", e.Detail, want)
	}
}
