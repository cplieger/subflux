package authhandlers

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/cplieger/auth/v2"
	authwebauthn "github.com/cplieger/auth/v2/webauthn"
	"github.com/cplieger/subflux/internal/api"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// --- POST /api/auth/webauthn/login/begin ---

// HandleWebAuthnLoginBegin handles POST /api/auth/webauthn/login/begin —
// issues a WebAuthn assertion challenge. Supports both standard and
// conditional (passkey autofill) mediation modes.
func (h *Handler) HandleWebAuthnLoginBegin(w http.ResponseWriter, r *http.Request) {
	wa, ok := h.requireWebAuthn(w)
	if !ok {
		return
	}

	var (
		assertion   *protocol.CredentialAssertion
		sessionData *webauthn.SessionData
		err         error
	)
	if r.URL.Query().Get("mediation") == "conditional" {
		assertion, sessionData, err = authwebauthn.BeginConditionalLogin(wa)
	} else {
		assertion, sessionData, err = authwebauthn.BeginLogin(wa)
	}
	if err != nil {
		slog.Error("webauthn: begin login", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	token, err := GenerateCeremonyToken()
	if err != nil {
		slog.Error("webauthn: generate token", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	if !h.Ceremonies.WebAuthn.Store(token, &WebAuthnSession{
		Data:      sessionData,
		CreatedAt: time.Now(),
	}) {
		slog.Warn("webauthn: ceremony session limit reached")
		api.ServiceUnavailableC(w, r, api.CodeServiceUnavailable, "too many pending sessions")
		return
	}

	api.WriteJSON(w, WebAuthnLoginBeginResponse{
		PublicKey:    assertion,
		SessionToken: token,
	})
}

// --- POST /api/auth/webauthn/login/finish ---

// HandleWebAuthnLoginFinish handles POST /api/auth/webauthn/login/finish —
// verifies the assertion response, updates the credential sign count, and
// creates a session for the authenticated user.
func (h *Handler) HandleWebAuthnLoginFinish(w http.ResponseWriter, r *http.Request) {
	wa, ok := h.requireWebAuthn(w)
	if !ok {
		return
	}

	sessData := h.consumeWebAuthnSession(w, r)
	if sessData == nil {
		return
	}

	// The library completes the ceremony against the store: user + credential
	// resolution from the user handle, assertion verification, and the
	// post-login custody write (sign count + flags, including CloneWarning).
	// Account-status policy stays here.
	user, _, err := authwebauthn.CompleteLogin(r.Context(), wa, h.Store, sessData, r)
	if err != nil {
		slog.Warn("webauthn: finish login failed", "error", err)

		var unknownCred *protocol.ErrorUnknownCredential
		if errors.As(err, &unknownCred) {
			api.WriteJSONStatus(w, http.StatusUnauthorized, api.WebAuthnUnknownCredentialResponse{
				Error:  "unknown credential",
				Signal: "unknown_credential",
			})
			return
		}

		api.UnauthorizedC(w, r, api.CodeWebAuthnAssertionFailed, "authentication failed")
		return
	}

	if !user.Enabled {
		api.ForbiddenC(w, r, api.CodeAuthAccountDisabled, "account disabled")
		return
	}

	if err := h.createSessionAndRespond(w, r, user, auth.MethodPasskey); err != nil {
		slog.Error("webauthn: create session", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	Audit(r, slog.LevelInfo, AuditLoginSuccess, true, user.Username,
		slog.String("method", string(auth.MethodPasskey)))
}

// --- passkey reauth handlers removed (reauth step-up dropped) ---
