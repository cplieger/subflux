package authhandlers

import (
	"context"
	"encoding/binary"
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
	if !h.requireWebAuthn(w) {
		return
	}

	var (
		assertion   *protocol.CredentialAssertion
		sessionData *webauthn.SessionData
		err         error
	)
	if r.URL.Query().Get("mediation") == "conditional" {
		assertion, sessionData, err = authwebauthn.BeginConditionalLogin(h.WebAuthn)
	} else {
		assertion, sessionData, err = authwebauthn.BeginLogin(h.WebAuthn)
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
	if !h.requireWebAuthn(w) {
		return
	}

	sessData := h.consumeWebAuthnSession(w, r)
	if sessData == nil {
		return
	}

	ctx := r.Context()

	resolvedUser, cred, err := authwebauthn.FinishLogin(h.WebAuthn, sessData, r, h.webAuthnUserFinder(ctx))
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

	if errSC := h.Store.UpdatePasskeyAfterLogin(ctx, cred.ID, cred.Authenticator.SignCount, auth.PasskeyFlags{
		UserPresent:    cred.Flags.UserPresent,
		UserVerified:   cred.Flags.UserVerified,
		BackupEligible: cred.Flags.BackupEligible,
		BackupState:    cred.Flags.BackupState,
	}); errSC != nil {
		slog.Warn("webauthn: update credential after login", "error", errSC)
	}

	webauthnUser, ok := resolvedUser.(*authwebauthn.User)
	if !ok || webauthnUser.AuthUser == nil {
		slog.Error("webauthn: unexpected user type from passkey login")
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	user := webauthnUser.AuthUser

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

// webAuthnUserFinder returns the credential-lookup callback FinishLogin uses to
// resolve the asserting user and their registered passkeys from the store. The
// returned errors are deliberately generic so the assertion response never
// reveals whether a particular user handle exists.
func (h *Handler) webAuthnUserFinder(ctx context.Context) func(rawID, userHandle []byte) (webauthn.User, error) {
	return func(_, userHandle []byte) (webauthn.User, error) {
		userID, _ := binary.Varint(userHandle)
		if userID == 0 {
			return nil, errors.New("invalid user handle")
		}

		user, err := h.Store.GetUserByID(ctx, userID)
		if err != nil || user == nil {
			return nil, errors.New("user not found")
		}

		creds, err := h.Store.GetPasskeysByUserID(ctx, user.ID)
		if err != nil {
			return nil, errors.New("get passkeys failed")
		}

		return authwebauthn.NewWebAuthnUser(user, creds)
	}
}

// --- passkey reauth handlers removed (reauth step-up dropped) ---
