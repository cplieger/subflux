package authhandlers

import (
	"encoding/binary"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"subflux/internal/api"
	"subflux/internal/auth"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// --- POST /api/auth/webauthn/login/begin ---

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
		assertion, sessionData, err = auth.BeginConditionalLogin(h.WebAuthn)
	} else {
		assertion, sessionData, err = auth.BeginLogin(h.WebAuthn)
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

func (h *Handler) HandleWebAuthnLoginFinish(w http.ResponseWriter, r *http.Request) {
	if !h.requireWebAuthn(w) {
		return
	}

	sessData := h.consumeWebAuthnSession(w, r)
	if sessData == nil {
		return
	}

	ctx := r.Context()

	userFinder := func(_, userHandle []byte) (webauthn.User, error) {
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

		return auth.NewWebAuthnUser(user, creds)
	}

	resolvedUser, cred, err := auth.FinishLogin(h.WebAuthn, sessData, r, userFinder)
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

	if errSC := h.Store.UpdatePasskeyAfterLogin(ctx, cred.ID, cred.Authenticator.SignCount, api.PasskeyFlags{
		UserPresent:    cred.Flags.UserPresent,
		UserVerified:   cred.Flags.UserVerified,
		BackupEligible: cred.Flags.BackupEligible,
		BackupState:    cred.Flags.BackupState,
	}); errSC != nil {
		slog.Warn("webauthn: update credential after login", "error", errSC)
	}

	webauthnUser, ok := resolvedUser.(*auth.WebAuthnUser)
	if !ok || webauthnUser.User == nil {
		slog.Error("webauthn: unexpected user type from passkey login")
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	user := webauthnUser.User

	if !user.Enabled {
		api.ForbiddenC(w, r, api.CodeAuthAccountDisabled, "account disabled")
		return
	}

	if err := h.createSessionAndRespond(w, r, user, api.MethodPasskey); err != nil {
		slog.Error("webauthn: create session", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	Audit(r, slog.LevelInfo, AuditLoginSuccess, true, user.Username,
		slog.String("method", string(api.MethodPasskey)))
}

// --- POST /api/auth/reauth/passkey/begin ---

func (h *Handler) HandleReauthPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if !h.requireWebAuthn(w) {
		return
	}

	user := api.UserFromContext(r.Context())

	creds, err := h.Store.GetPasskeysByUserID(r.Context(), user.ID)
	if err != nil {
		slog.Error("reauth passkey begin: get passkeys", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	if len(creds) == 0 {
		api.BadRequestC(w, r, api.CodeBadRequest, "no passkeys configured")
		return
	}

	webauthnUser, err := auth.NewWebAuthnUser(user, creds)
	if err != nil {
		slog.Error("reauth passkey begin: nil user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	assertion, sessionData, err := auth.BeginUserLogin(h.WebAuthn, webauthnUser)
	if err != nil {
		slog.Error("reauth passkey begin: ceremony", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	token, err := GenerateCeremonyToken()
	if err != nil {
		slog.Error("reauth passkey begin: generate token", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	if !h.Ceremonies.WebAuthn.Store(token, &WebAuthnSession{
		Data:      sessionData,
		CreatedAt: time.Now(),
	}) {
		slog.Warn("reauth passkey begin: ceremony session limit reached")
		api.ServiceUnavailableC(w, r, api.CodeServiceUnavailable, "too many pending ceremonies")
		return
	}

	api.WriteJSON(w, WebAuthnLoginBeginResponse{
		PublicKey:    assertion,
		SessionToken: token,
	})
}

// --- POST /api/auth/reauth/passkey/finish ---

func (h *Handler) HandleReauthPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	if !h.requireWebAuthn(w) {
		return
	}

	user := api.UserFromContext(r.Context())

	sessData := h.consumeWebAuthnSession(w, r)
	if sessData == nil {
		return
	}

	ctx := r.Context()
	creds, err := h.Store.GetPasskeysByUserID(ctx, user.ID)
	if err != nil {
		slog.Error("reauth passkey finish: get passkeys", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	webauthnUser, err := auth.NewWebAuthnUser(user, creds)
	if err != nil {
		slog.Error("reauth passkey finish: nil user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	cred, err := auth.FinishUserLogin(h.WebAuthn, webauthnUser, sessData, r)
	if err != nil {
		slog.Warn("reauth passkey finish: verify", "error", err)
		api.UnauthorizedC(w, r, api.CodeWebAuthnAssertionFailed, "authentication failed")
		return
	}

	if errSC := h.Store.UpdatePasskeyAfterLogin(ctx, cred.ID,
		cred.Authenticator.SignCount,
		api.PasskeyFlags{
			UserPresent:    cred.Flags.UserPresent,
			UserVerified:   cred.Flags.UserVerified,
			BackupEligible: cred.Flags.BackupEligible,
			BackupState:    cred.Flags.BackupState,
		}); errSC != nil {
		slog.Warn("reauth passkey finish: update credential", "error", errSC)
	}

	if sessHash := api.SessionHashFromContext(ctx); sessHash != "" {
		if errR := h.Store.UpdateSessionReauth(ctx, sessHash, time.Now()); errR != nil {
			slog.Warn("reauth passkey finish: update session", "error", errR)
		}
	}

	slog.Info("security: passkey reauth",
		"username", user.Username, "ip", ClientIP(r))

	api.Ok(w)
	Audit(r, slog.LevelInfo, AuditReauthSuccess, true, user.Username,
		slog.String("method", string(api.MethodPasskey)))
}
