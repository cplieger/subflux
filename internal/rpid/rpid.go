// Package rpid derives and validates the WebAuthn relying-party ID for the host
// subflux is reached on: the registrable domain (eTLD+1) from the public suffix
// list, never a label strip, so a host whose provider is itself a public suffix
// (foo.duckdns.org, nas.synology.me) collapses to the exact host instead of to
// an illegal bare suffix.
package rpid

import (
	"fmt"
	"net"
	"strings"

	authwebauthn "github.com/cplieger/auth/v6/webauthn"
	"github.com/cplieger/webhttp/v3"
	"golang.org/x/net/publicsuffix"
)

// reason names why a value cannot be a relying-party ID.
type reason string

// The refusal reasons, each carrying its own remedy in Error.Detail.
const (
	reasonMalformedHost reason = "malformed_host"
	reasonIPLiteral     reason = "ip_literal"
	reasonSingleLabel   reason = "single_label"
	reasonPublicSuffix  reason = "public_suffix"
	reasonIllegalDomain reason = "illegal_domain"
	reasonNotCanonical  reason = "not_canonical"
	reasonNotServed     reason = "not_served"
)

// refusal names the offending value, why it was refused, and the remedy. The
// message carries no configuration key: every caller adds its own context.
type refusal struct {
	err    error
	Value  string
	Detail string
	Reason reason
}

func (e *refusal) Error() string {
	return fmt.Sprintf("%q is not usable as a WebAuthn relying-party ID: %s; %s", e.Value, e.Reason.clause(), e.Detail)
}

// Unwrap exposes the library's refusal on the illegal_domain arm, so
// errors.Is(err, authwebauthn.ErrIllegalRPID) reaches through.
func (e *refusal) Unwrap() error { return e.err }

func (r reason) clause() string {
	switch r {
	case reasonMalformedHost:
		return "the host is not a well-formed name or address"
	case reasonIPLiteral:
		return "an IP address can never be a relying-party ID"
	case reasonSingleLabel:
		return "a single-label hostname has no registrable domain"
	case reasonPublicSuffix:
		return "the host is itself a public suffix, which no relying party may claim"
	case reasonIllegalDomain:
		return "the registrable domain is not a legal relying-party ID"
	case reasonNotCanonical:
		return "the value is not in canonical form"
	case reasonNotServed:
		return "the host this save comes from is not inside it"
	default:
		return string(r)
	}
}

// normalize returns the canonical spelling of an operator-typed value: trimmed,
// ASCII-lowercased, without a trailing dot. A port is not stripped; Validate
// refuses it.
func normalize(raw string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
}

// Derive returns the relying-party ID for host: the registrable domain of a
// DNS name, or "localhost" verbatim. The error is a *Error.
func Derive(host string) (string, error) {
	canon := webhttp.CanonicalHost(host)
	if canon == "" {
		return "", &refusal{Value: host, Reason: reasonMalformedHost, Detail: "reach subflux at a DNS name"}
	}
	// Before the suffix lookup: PublicSuffix("10.0.0.5") is "10.0.0.5", so an
	// address reaching it would be reported as a bare public suffix.
	if net.ParseIP(canon) != nil {
		return "", &refusal{Value: canon, Reason: reasonIPLiteral, Detail: "reach subflux at a domain name over HTTPS, or at http://localhost"}
	}
	if canon == "localhost" {
		return canon, nil
	}
	etld1, err := registrableDomain(canon)
	if err != nil {
		return "", err
	}
	if err := authwebauthn.ValidateRPID(etld1); err != nil {
		return "", &refusal{err: err, Value: etld1, Reason: reasonIllegalDomain, Detail: err.Error()}
	}
	return etld1, nil
}

// registrableDomain is the eTLD+1 lookup shared by Derive and Validate, with
// the label count as the discriminant between a single label and a bare public
// suffix: EffectiveTLDPlusOne's only reachable error here leaves
// PublicSuffix(x) == x in both cases, so a suffix comparison cannot tell them
// apart.
func registrableDomain(canon string) (string, error) {
	etld1, err := publicsuffix.EffectiveTLDPlusOne(canon)
	if err == nil {
		return etld1, nil
	}
	if strings.Count(canon, ".") == 0 {
		return "", &refusal{Value: canon, Reason: reasonSingleLabel, Detail: "use a dotted name, or localhost"}
	}
	return "", &refusal{Value: canon, Reason: reasonPublicSuffix, Detail: "reach subflux at a subdomain of " + canon}
}

// Validate judges an operator-typed value as written, without deriving: a
// value narrower than the registrable domain is specification-legal and is
// accepted. The error is a *Error.
func Validate(id string) error {
	if id == "" {
		return &refusal{Value: id, Reason: reasonMalformedHost, Detail: "set a domain name"}
	}
	if canon := normalize(id); canon != id {
		return &refusal{Value: id, Reason: reasonNotCanonical, Detail: fmt.Sprintf("spell it %q", canon)}
	}
	if webhttp.CanonicalHost(id) != id {
		return &refusal{Value: id, Reason: reasonMalformedHost, Detail: "use a bare domain name, without a scheme, port or path"}
	}
	if net.ParseIP(id) != nil {
		return &refusal{Value: id, Reason: reasonIPLiteral, Detail: "reach subflux at a domain name over HTTPS, or at http://localhost"}
	}
	if id == "localhost" {
		return nil
	}
	if _, err := registrableDomain(id); err != nil {
		return err
	}
	if err := authwebauthn.ValidateRPID(id); err != nil {
		return &refusal{err: err, Value: id, Reason: reasonIllegalDomain, Detail: err.Error()}
	}
	return nil
}

// ValidateForHost is Validate plus the served check: host must be id or a
// subdomain of it. The dot in the needle is the guard against
// "evilexample.com" matching "example.com".
func ValidateForHost(id, host string) error {
	if err := Validate(id); err != nil {
		return err
	}
	canon := webhttp.CanonicalHost(host)
	if canon == id || strings.HasSuffix(canon, "."+id) {
		return nil
	}
	detail := fmt.Sprintf("save it from a browser at a host inside %q", id)
	if derived, err := Derive(host); err == nil {
		detail += fmt.Sprintf(", or use %q for the host you are on", derived)
	}
	return &refusal{Value: id, Reason: reasonNotServed, Detail: detail}
}
