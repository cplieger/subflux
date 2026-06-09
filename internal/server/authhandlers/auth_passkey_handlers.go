package authhandlers

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	authlib "github.com/cplieger/auth"
	authwebauthn "github.com/cplieger/auth/webauthn"
	"github.com/cplieger/subflux/internal/api"
)

// --- GET /api/auth/passkeys ---

func (h *Handler) HandleListPasskeys(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	creds, err := h.SecDB.GetPasskeysByUserID(r.Context(), user.ID)
	if err != nil {
		slog.Error("list passkeys: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	type passkeyInfo struct {
		CreatedAt      time.Time `json:"created_at"`
		Name           string    `json:"name"`
		Transport      string    `json:"transport,omitempty"`
		ID             int64     `json:"id"`
		BackupEligible bool      `json:"backup_eligible"`
	}

	out := make([]passkeyInfo, 0, len(creds))
	for i := range creds {
		out = append(out, passkeyInfo{
			ID:             creds[i].ID,
			Name:           creds[i].Name,
			Transport:      creds[i].Transport,
			CreatedAt:      creds[i].CreatedAt,
			BackupEligible: creds[i].BackupEligible,
		})
	}

	api.WriteJSON(w, out)
}

// --- GET /api/auth/webauthn/signal-data ---

func (h *Handler) HandleWebAuthnSignalData(w http.ResponseWriter, r *http.Request) {
	if !h.requireWebAuthn(w) {
		return
	}

	user := api.UserFromContext(r.Context())

	creds, err := h.SecDB.GetPasskeysByUserID(r.Context(), user.ID)
	if err != nil {
		slog.Error("signal data: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	webauthnUser, err := authwebauthn.NewWebAuthnUser(user, nil)
	if err != nil {
		slog.Error("webauthn info: nil user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	userID := Base64URLEncode(webauthnUser.WebAuthnID())

	credIDs := make([]string, 0, len(creds))
	for i := range creds {
		credIDs = append(credIDs, Base64URLEncode(creds[i].CredentialID))
	}

	displayName := user.DisplayName
	if displayName == "" {
		displayName = user.Username
	}

	api.WriteJSON(w, api.SignalData{
		RPID:          h.WebAuthn.Config.RPID,
		UserID:        userID,
		CredentialIDs: credIDs,
		Name:          user.Username,
		DisplayName:   displayName,
	})
}

// Base64URLEncode encodes bytes as base64url without padding.
func Base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// --- POST /api/auth/webauthn/register/begin ---

func (h *Handler) HandleWebAuthnRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if !h.requireWebAuthn(w) {
		return
	}

	user := api.UserFromContext(r.Context())

	// Adding a passkey creates a local login credential. SSO-governed accounts
	// (no password) cannot self-provision one; local accounts must prove their
	// password.
	if user.PasswordHash == "" {
		api.ForbiddenC(w, r, api.CodeForbidden, "this account is managed by your identity provider")
		return
	}
	req, ok := decodeAuthBody[struct {
		Password string `json:"password"`
	}](w, r)
	if !ok {
		return
	}
	ip := ClientIP(r)
	allowed, retryAfter := h.RateLimiter.Allow(ip, user.Username)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		api.TooManyRequestsC(w, r, api.CodeRateLimited, "too many attempts")
		return
	}
	if okPass, perr := authlib.VerifyPassword(req.Password, user.PasswordHash); perr != nil || !okPass {
		h.RateLimiter.Record(ip, user.Username)
		api.UnauthorizedC(w, r, api.CodeAuthInvalidCredentials, "invalid password")
		return
	}
	h.RateLimiter.Reset(ip, user.Username)

	ctx := r.Context()
	creds, err := h.SecDB.GetPasskeysByUserID(ctx, user.ID)
	if err != nil {
		slog.Error("webauthn register: get passkeys", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	webauthnUser, err := authwebauthn.NewWebAuthnUser(user, creds)
	if err != nil {
		slog.Error("webauthn register: nil user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	creation, sessionData, err := authwebauthn.BeginRegistration(h.WebAuthn, webauthnUser)
	if err != nil {
		slog.Error("webauthn register: begin", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	token, err := GenerateCeremonyToken()
	if err != nil {
		slog.Error("webauthn register: generate token", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	if !h.Ceremonies.WebAuthn.Store(token, &WebAuthnSession{
		Data:      sessionData,
		CreatedAt: time.Now(),
	}) {
		slog.Warn("webauthn register: ceremony session limit reached")
		api.ServiceUnavailableC(w, r, api.CodeServiceUnavailable, "too many pending ceremonies")
		return
	}

	api.WriteJSON(w, WebAuthnRegisterBeginResponse{
		PublicKey:    creation,
		SessionToken: token,
	})
}

// --- POST /api/auth/webauthn/register/finish ---

func (h *Handler) HandleWebAuthnRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if !h.requireWebAuthn(w) {
		return
	}

	user := api.UserFromContext(r.Context())

	sessData := h.consumeWebAuthnSession(w, r)
	if sessData == nil {
		return
	}

	ctx := r.Context()
	creds, err := h.SecDB.GetPasskeysByUserID(ctx, user.ID)
	if err != nil {
		slog.Error("webauthn register finish: get passkeys", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	webauthnUser, err := authwebauthn.NewWebAuthnUser(user, creds)
	if err != nil {
		slog.Error("webauthn register finish: nil user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	credential, err := authwebauthn.FinishRegistration(h.WebAuthn, webauthnUser, sessData, r)
	if err != nil {
		slog.Warn("webauthn register finish: failed", "error", err)
		api.BadRequestC(w, r, api.CodeWebAuthnRegisterFailed, "registration failed")
		return
	}

	existingNames := make([]string, len(creds))
	for i := range creds {
		existingNames[i] = creds[i].Name
	}
	friendlyName := authwebauthn.PasskeyFriendlyName(credential.Authenticator.AAGUID, existingNames)

	passkey := authwebauthn.CredentialToAPI(credential, user.ID, friendlyName)
	passkey.CreatedAt = time.Now()
	if err := h.SecDB.CreatePasskey(ctx, passkey); err != nil {
		slog.Error("webauthn register finish: store credential", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("security: passkey registered",
		"username", user.Username, "name", friendlyName, "ip", ClientIP(r))

	api.WriteJSON(w, api.PasskeyRegistered{
		ID:        passkey.ID,
		Name:      passkey.Name,
		Transport: passkey.Transport,
		CreatedAt: passkey.CreatedAt,
	})
	Audit(r, slog.LevelInfo, AuditPasskeyAdd, true, user.Username,
		slog.Int64("passkey_id", passkey.ID),
		slog.String("name", friendlyName))
}

// --- DELETE /api/auth/passkeys/{id} ---

func (h *Handler) HandleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	passkeyID, ok := parseIDFromPath(w, r.URL.Path, "/api/auth/passkeys/", "passkey id")
	if !ok {
		return
	}

	ctx := r.Context()

	passkeyCount, errPK := h.SecDB.PasskeyCountForUser(ctx, user.ID)
	if errPK != nil {
		slog.Warn("delete passkey: passkey count", "error", errPK)
	}
	cfg := h.Config()
	hasPassword := user.PasswordHash != ""
	oidcLinked := user.OIDCSub != ""

	if !authlib.CanDisableAuthMethod(api.MethodPasskey, hasPassword, passkeyCount-1, cfg.OIDCEnabled(), oidcLinked) {
		api.ConflictC(w, r, api.CodeConflict, "cannot remove last authentication method")
		return
	}

	if err := h.SecDB.DeletePasskey(ctx, passkeyID, user.ID); err != nil {
		slog.Error("delete passkey: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("security: passkey deleted",
		"username", user.Username, "passkey_id", passkeyID, "ip", ClientIP(r))

	api.Ok(w)
	Audit(r, slog.LevelInfo, AuditPasskeyDelete, true, user.Username,
		slog.Int64("passkey_id", passkeyID))
}

// --- PUT /api/auth/passkeys/{id} ---

func (h *Handler) HandleRenamePasskey(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	passkeyID, ok := parseIDFromPath(w, r.URL.Path, "/api/auth/passkeys/", "passkey id")
	if !ok {
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "name required")
		return
	}

	if len([]rune(req.Name)) > maxPasskeyNameLen {
		api.BadRequestC(w, r, api.CodeBadRequest, "name too long")
		return
	}

	if err := h.SecDB.RenamePasskey(r.Context(), passkeyID, user.ID, req.Name); err != nil {
		slog.Error("rename passkey: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	api.Ok(w)
	Audit(r, slog.LevelInfo, AuditPasskeyRename, true, user.Username,
		slog.Int64("passkey_id", passkeyID),
		slog.String("name", req.Name))
}
