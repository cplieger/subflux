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

// Reason names why a value cannot be a relying-party ID.
type Reason string

// The refusal reasons, each carrying its own remedy in Error.Detail.
const (
	ReasonMalformedHost Reason = "malformed_host"
	ReasonIPLiteral     Reason = "ip_literal"
	ReasonSingleLabel   Reason = "single_label"
	ReasonPublicSuffix  Reason = "public_suffix"
	ReasonIllegalDomain Reason = "illegal_domain"
	ReasonNotCanonical  Reason = "not_canonical"
	ReasonNotServed     Reason = "not_served"
)

// Error names the offending value, why it was refused, and the remedy. The
// message carries no configuration key: every caller adds its own context.
type Error struct {
	err    error
	Value  string
	Detail string
	Reason Reason
}

func (e *Error) Error() string {
	return fmt.Sprintf("%q is not usable as a WebAuthn relying-party ID: %s; %s", e.Value, e.Reason.clause(), e.Detail)
}

// Unwrap exposes the library's refusal on the illegal_domain arm, so
// errors.Is(err, authwebauthn.ErrIllegalRPID) reaches through.
func (e *Error) Unwrap() error { return e.err }

func (r Reason) clause() string {
	switch r {
	case ReasonMalformedHost:
		return "the host is not a well-formed name or address"
	case ReasonIPLiteral:
		return "an IP address can never be a relying-party ID"
	case ReasonSingleLabel:
		return "a single-label hostname has no registrable domain"
	case ReasonPublicSuffix:
		return "the host is itself a public suffix, which no relying party may claim"
	case ReasonIllegalDomain:
		return "the registrable domain is not a legal relying-party ID"
	case ReasonNotCanonical:
		return "the value is not in canonical form"
	case ReasonNotServed:
		return "the host this save comes from is not inside it"
	default:
		return string(r)
	}
}

// Normalize returns the canonical spelling of an operator-typed value: trimmed,
// ASCII-lowercased, without a trailing dot. A port is not stripped; Validate
// refuses it.
func Normalize(raw string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
}

// Derive returns the relying-party ID for host: the registrable domain of a
// DNS name, or "localhost" verbatim. The error is a *Error.
func Derive(host string) (string, error) {
	canon := webhttp.CanonicalHost(host)
	if canon == "" {
		return "", &Error{Value: host, Reason: ReasonMalformedHost, Detail: "reach subflux at a DNS name"}
	}
	// Before the suffix lookup: PublicSuffix("10.0.0.5") is "10.0.0.5", so an
	// address reaching it would be reported as a bare public suffix.
	if net.ParseIP(canon) != nil {
		return "", &Error{Value: canon, Reason: ReasonIPLiteral, Detail: "reach subflux at a domain name over HTTPS, or at http://localhost"}
	}
	if canon == "localhost" {
		return canon, nil
	}
	etld1, err := registrableDomain(canon)
	if err != nil {
		return "", err
	}
	if err := authwebauthn.ValidateRPID(etld1); err != nil {
		return "", &Error{err: err, Value: etld1, Reason: ReasonIllegalDomain, Detail: err.Error()}
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
		return "", &Error{Value: canon, Reason: ReasonSingleLabel, Detail: "use a dotted name, or localhost"}
	}
	return "", &Error{Value: canon, Reason: ReasonPublicSuffix, Detail: "reach subflux at a subdomain of " + canon}
}

// Validate judges an operator-typed value as written, without deriving: a
// value narrower than the registrable domain is specification-legal and is
// accepted. The error is a *Error.
func Validate(id string) error {
	if id == "" {
		return &Error{Value: id, Reason: ReasonMalformedHost, Detail: "set a domain name"}
	}
	if canon := Normalize(id); canon != id {
		return &Error{Value: id, Reason: ReasonNotCanonical, Detail: fmt.Sprintf("spell it %q", canon)}
	}
	if webhttp.CanonicalHost(id) != id {
		return &Error{Value: id, Reason: ReasonMalformedHost, Detail: "use a bare domain name, without a scheme, port or path"}
	}
	if net.ParseIP(id) != nil {
		return &Error{Value: id, Reason: ReasonIPLiteral, Detail: "reach subflux at a domain name over HTTPS, or at http://localhost"}
	}
	if id == "localhost" {
		return nil
	}
	if _, err := registrableDomain(id); err != nil {
		return err
	}
	if err := authwebauthn.ValidateRPID(id); err != nil {
		return &Error{err: err, Value: id, Reason: ReasonIllegalDomain, Detail: err.Error()}
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
	return &Error{Value: id, Reason: ReasonNotServed, Detail: detail}
}
