package authhandlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cplieger/auth/v6"
	authwebauthn "github.com/cplieger/auth/v6/webauthn"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/rpid"
	"github.com/cplieger/subflux/internal/subflux"
)

// requestOrigin resolves the browser origin a request was made from. The
// Origin header is the only source: a browser sends it on every method other
// than GET and HEAD, and deriving a scheme from a forwarded header is a
// proxy-trust decision this path must not make.
func requestOrigin(r *http.Request) (authwebauthn.Origin, error) {
	return authwebauthn.ParseOrigin(r.Header.Get("Origin"))
}

// originRefusalMessage renders the library's typed refusal as a sentence that
// names subflux's own setting and, where one exists, the value to use.
func originRefusalMessage(e *authwebauthn.OriginError) string {
	switch e.Reason {
	case authwebauthn.RejectMalformed:
		if e.Origin == "" {
			return "this request carries no Origin header, so no passkey ceremony can be bound to it"
		}
		return fmt.Sprintf("this page's origin %q is not a usable browser origin", e.Origin)
	case authwebauthn.RejectTrailingDot:
		return fmt.Sprintf("this page's address ends in a dot. Reach subflux at %q", strings.TrimSuffix(e.Origin, "."))
	case authwebauthn.RejectEmptyLabel, authwebauthn.RejectNonASCII:
		return fmt.Sprintf("this page's origin %q cannot be scoped to a relying-party ID", e.Origin)
	case authwebauthn.RejectNoOrigin:
		return "no origin was presented for this passkey ceremony"
	case authwebauthn.RejectInsecureScheme:
		return fmt.Sprintf("this page's origin %q is served over plain HTTP. Passkeys need HTTPS or http://localhost", e.Origin)
	case authwebauthn.RejectIPHost:
		return fmt.Sprintf("this page's origin %q is an IP address, which can never have a passkey. Use a domain name", e.Origin)
	case authwebauthn.RejectHostNotCovered:
		msg := fmt.Sprintf("this page's origin %q is not covered by the configured relying-party ID %q", e.Origin, e.RPID)
		if derived, ok := suggestedRPID(e.Origin); ok {
			msg += fmt.Sprintf(". Set auth.webauthn_rp_id to %q in the Authentication section of Settings", derived)
		}
		return msg
	case authwebauthn.RejectNotAllowlisted:
		return fmt.Sprintf("this page's origin %q is not one the relying party allows passkey ceremonies from", e.Origin)
	default:
		return fmt.Sprintf("this page's origin %q cannot conduct a passkey ceremony", e.Origin)
	}
}

// suggestedRPID derives the relying-party ID a canonical origin string would
// get, reporting false when the host cannot have one.
func suggestedRPID(origin string) (string, bool) {
	o, err := authwebauthn.ParseOrigin(origin)
	if err != nil {
		return "", false
	}
	derived, err := rpid.Derive(o.Host())
	return derived, err == nil
}

// refuseOrigin answers a begin request whose origin the relying party will not
// conduct a ceremony at, before any challenge is minted.
func refuseOrigin(w http.ResponseWriter, r *http.Request, err error) {
	if oe, ok := errors.AsType[*authwebauthn.OriginError](err); ok {
		httpapi.BadRequestC(w, r, subflux.CodeWebAuthnUnsupportedOrigin, originRefusalMessage(oe))
		return
	}
	slog.Error("webauthn: begin", "error", err)
	httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
}

// --- GET /api/auth/webauthn/availability ---

// HandleWebAuthnAvailability handles GET /api/auth/webauthn/availability —
// reports whether a passkey ceremony could be conducted from the origin named
// by the required ?origin= parameter. A GET carries no Origin header, and
// r.Host is wrong behind a Host-rewriting proxy, so the page states its own.
// The answer is advisory: the authoritative check is at Begin.
func (h *Handler) HandleWebAuthnAvailability(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("origin")
	if raw == "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "missing origin parameter")
		return
	}

	var configuredRPID string
	if cfg := h.Config(); cfg != nil {
		configuredRPID = cfg.WebAuthnRPID()
	}

	o, err := authwebauthn.ParseOrigin(raw)
	if err != nil {
		httpapi.WriteJSON(w, subflux.WebAuthnAvailability{
			Reason: subflux.WebAuthnUnavailableAddressUnusable,
			RPID:   configuredRPID,
		})
		return
	}

	rp := h.relyingParty()
	if rp == nil {
		httpapi.WriteJSON(w, unconfiguredAvailability(configuredRPID, o))
		return
	}
	if err := rp.CheckOrigin(o); err != nil {
		httpapi.WriteJSON(w, refusedAvailability(rp.ID(), o, err))
		return
	}
	httpapi.WriteJSON(w, subflux.WebAuthnAvailability{Available: true})
}

// unconfiguredAvailability answers the probe when no relying party exists: a
// stored-but-refused value is init_failed, an empty one is unconfigured with
// the value the host would derive, or unconfigurable when it cannot have one.
func unconfiguredAvailability(configuredRPID string, o authwebauthn.Origin) subflux.WebAuthnAvailability {
	if configuredRPID != "" {
		return subflux.WebAuthnAvailability{Reason: subflux.WebAuthnUnavailableInitFailed, RPID: configuredRPID}
	}
	derived, err := rpid.Derive(o.Host())
	if err != nil {
		return subflux.WebAuthnAvailability{Reason: subflux.WebAuthnUnavailableUnconfigurable}
	}
	return subflux.WebAuthnAvailability{Reason: subflux.WebAuthnUnavailableUnconfigured, SuggestedRPID: derived}
}

// refusedAvailability maps one CheckOrigin rejection onto one wire reason. The
// mapping is a function of the rejection alone, with a default arm so a reason
// a later library adds cannot render as an empty one.
func refusedAvailability(rpID string, o authwebauthn.Origin, err error) subflux.WebAuthnAvailability {
	out := subflux.WebAuthnAvailability{Reason: subflux.WebAuthnUnavailableAddressUnusable, RPID: rpID}
	oe, ok := errors.AsType[*authwebauthn.OriginError](err)
	if !ok {
		return out
	}
	switch oe.Reason {
	case authwebauthn.RejectIPHost:
		out.Reason = subflux.WebAuthnUnavailableUnconfigurable
	case authwebauthn.RejectInsecureScheme:
		out.Reason = subflux.WebAuthnUnavailableInsecureScheme
	case authwebauthn.RejectHostNotCovered:
		out.Reason = subflux.WebAuthnUnavailableOrigin
		if derived, derr := rpid.Derive(o.Host()); derr == nil {
			out.SuggestedRPID = derived
		}
	case authwebauthn.RejectNotAllowlisted:
		out.Reason = subflux.WebAuthnUnavailableNotAllowlisted
	}
	return out
}

// --- POST /api/auth/webauthn/login/begin ---

// HandleWebAuthnLoginBegin handles POST /api/auth/webauthn/login/begin —
// issues a WebAuthn assertion challenge bound to the request's origin.
// Supports both standard and conditional (passkey autofill) mediation modes.
func (h *Handler) HandleWebAuthnLoginBegin(w http.ResponseWriter, r *http.Request) {
	rp, ok := h.requireWebAuthn(w, r)
	if !ok {
		return
	}
	origin, err := requestOrigin(r)
	if err != nil {
		refuseOrigin(w, r, err)
		return
	}

	var (
		assertion *authwebauthn.CredentialAssertion
		ceremony  authwebauthn.Ceremony
	)
	if r.URL.Query().Get("mediation") == "conditional" {
		assertion, ceremony, err = authwebauthn.BeginConditionalLogin(rp, origin)
	} else {
		assertion, ceremony, err = authwebauthn.BeginLogin(rp, origin)
	}
	if err != nil {
		refuseOrigin(w, r, err)
		return
	}

	token, err := GenerateCeremonyToken()
	if err != nil {
		slog.Error("webauthn: generate token", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}

	if !h.Ceremonies.WebAuthn.Store(token, ceremony) {
		slog.Warn("webauthn: ceremony session limit reached")
		httpapi.ServiceUnavailableC(w, r, subflux.CodeServiceUnavailable, "too many pending sessions")
		return
	}

	httpapi.WriteJSON(w, WebAuthnLoginBeginResponse{
		PublicKey:    assertion,
		SessionToken: token,
	})
}

// --- POST /api/auth/webauthn/login/finish ---

// HandleWebAuthnLoginFinish handles POST /api/auth/webauthn/login/finish —
// verifies the assertion response, updates the credential sign count, and
// creates a session for the authenticated user.
func (h *Handler) HandleWebAuthnLoginFinish(w http.ResponseWriter, r *http.Request) {
	rp, ok := h.requireWebAuthn(w, r)
	if !ok {
		return
	}

	ceremony, ok := h.consumeWebAuthnSession(w, r)
	if !ok {
		return
	}

	// The library completes the ceremony against the store: user + credential
	// resolution from the user handle, assertion verification, and the
	// post-login custody write (sign count + flags, including CloneWarning).
	// Account-status policy stays here.
	user, err := authwebauthn.CompleteLogin(r.Context(), rp, h.Store, ceremony, r)
	if err != nil {
		slog.Warn("webauthn: finish login failed", "error", err)

		if errors.Is(err, authwebauthn.ErrUnknownCredential) {
			httpapi.WriteJSONStatus(w, http.StatusUnauthorized, subflux.WebAuthnUnknownCredentialResponse{
				Error:  "unknown credential",
				Signal: "unknown_credential",
			})
			return
		}

		httpapi.UnauthorizedC(w, r, subflux.CodeWebAuthnAssertionFailed, "authentication failed")
		return
	}

	if !user.Enabled {
		httpapi.ForbiddenC(w, r, subflux.CodeAuthAccountDisabled, "account disabled")
		return
	}

	if err := h.createSessionAndRespond(w, r, user, auth.MethodPasskey); err != nil {
		slog.Error("webauthn: create session", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}
	audit(r, slog.LevelInfo, auditLoginSuccess, true, user.Username,
		slog.String("method", string(auth.MethodPasskey)))
}

// --- passkey reauth handlers removed (reauth step-up dropped) ---
