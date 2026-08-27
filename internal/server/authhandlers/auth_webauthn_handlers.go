package authhandlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/cplieger/auth/v5"
	authwebauthn "github.com/cplieger/auth/v5/webauthn"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/subflux"
)

// --- POST /api/auth/webauthn/login/begin ---

// HandleWebAuthnLoginBegin handles POST /api/auth/webauthn/login/begin —
// issues a WebAuthn assertion challenge. Supports both standard and
// conditional (passkey autofill) mediation modes.
func (h *Handler) HandleWebAuthnLoginBegin(w http.ResponseWriter, r *http.Request) {
	rp, ok := h.requireWebAuthn(w)
	if !ok {
		return
	}

	var (
		assertion *authwebauthn.CredentialAssertion
		ceremony  authwebauthn.Ceremony
		err       error
	)
	if r.URL.Query().Get("mediation") == "conditional" {
		assertion, ceremony, err = authwebauthn.BeginConditionalLogin(rp)
	} else {
		assertion, ceremony, err = authwebauthn.BeginLogin(rp)
	}
	if err != nil {
		slog.Error("webauthn: begin login", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
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
	rp, ok := h.requireWebAuthn(w)
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
	Audit(r, slog.LevelInfo, AuditLoginSuccess, true, user.Username,
		slog.String("method", string(auth.MethodPasskey)))
}

// --- passkey reauth handlers removed (reauth step-up dropped) ---
