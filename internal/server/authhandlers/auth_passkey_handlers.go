package authhandlers

import (
	"encoding/base64"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/auth/v4"
	"github.com/cplieger/auth/v4/ratelimit"
	authwebauthn "github.com/cplieger/auth/v4/webauthn"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httpapi"
)

// --- GET /api/auth/passkeys ---

// HandleListPasskeys handles GET /api/auth/passkeys — lists passkeys for the current user.
func (h *Handler) HandleListPasskeys(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	creds, err := h.SecDB.GetPasskeysByUserID(r.Context(), user.ID)
	if err != nil {
		slog.Error("list passkeys: db error", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	out := make([]PasskeyInfo, 0, len(creds))
	for i := range creds {
		out = append(out, PasskeyInfo{
			ID:             creds[i].ID,
			Name:           creds[i].Name,
			Transport:      creds[i].Transport,
			CreatedAt:      creds[i].CreatedAt,
			BackupEligible: creds[i].BackupEligible,
		})
	}

	httpapi.WriteJSON(w, out)
}

// --- GET /api/auth/webauthn/signal-data ---

// HandleWebAuthnSignalData handles GET /api/auth/webauthn/signal-data — returns
// the WebAuthn signal data needed by the browser for credential management.
func (h *Handler) HandleWebAuthnSignalData(w http.ResponseWriter, r *http.Request) {
	wa, ok := h.requireWebAuthn(w)
	if !ok {
		return
	}

	user := UserFromContext(r.Context())

	creds, err := h.SecDB.GetPasskeysByUserID(r.Context(), user.ID)
	if err != nil {
		slog.Error("signal data: db error", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	webauthnUser, err := authwebauthn.NewUser(user, nil)
	if err != nil {
		slog.Error("webauthn info: nil user", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
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

	httpapi.WriteJSON(w, api.SignalData{
		RPID:          wa.Config.RPID,
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

// HandleWebAuthnRegisterBegin handles POST /api/auth/webauthn/register/begin —
// initiates passkey registration. Requires password verification before issuing
// the creation challenge to prevent unauthorized credential provisioning.
func (h *Handler) HandleWebAuthnRegisterBegin(w http.ResponseWriter, r *http.Request) {
	wa, ok := h.requireWebAuthn(w)
	if !ok {
		return
	}

	user := UserFromContext(r.Context())

	// Adding a passkey creates a local login credential. SSO-governed accounts
	// (no password) cannot self-provision one; local accounts must prove their
	// password.
	if user.PasswordHash == "" {
		httpapi.ForbiddenC(w, r, api.CodeForbidden, "this account is managed by your identity provider")
		return
	}
	req, ok := decodeAuthBody[struct {
		Password string `json:"password"`
	}](w, r)
	if !ok {
		return
	}
	// The limiter's two dimensions are distinctly typed, so the IP and the
	// username cannot be transposed on the way in.
	rlIP, rlUser := ratelimit.ClientIP(ClientIP(r)), ratelimit.Username(user.Username)
	allowed, retryAfter := h.RateLimiter.Allow(rlIP, rlUser)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		httpapi.TooManyRequestsC(w, r, api.CodeRateLimited, "too many attempts")
		return
	}
	if okPass, perr := auth.VerifyPassword(req.Password, user.PasswordHash); perr != nil || !okPass {
		h.RateLimiter.Record(rlIP, rlUser)
		httpapi.UnauthorizedC(w, r, api.CodeAuthInvalidCredentials, "invalid password")
		return
	}
	h.RateLimiter.Reset(rlIP, rlUser)

	ctx := r.Context()
	creds, err := h.SecDB.GetPasskeysByUserID(ctx, user.ID)
	if err != nil {
		slog.Error("webauthn register: get passkeys", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	webauthnUser, err := authwebauthn.NewUser(user, creds)
	if err != nil {
		slog.Error("webauthn register: nil user", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	creation, sessionData, err := authwebauthn.BeginRegistration(wa, webauthnUser)
	if err != nil {
		slog.Error("webauthn register: begin", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	token, err := GenerateCeremonyToken()
	if err != nil {
		slog.Error("webauthn register: generate token", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	if !h.Ceremonies.WebAuthn.Store(token, &WebAuthnSession{
		Data:      sessionData,
		CreatedAt: time.Now(),
	}) {
		slog.Warn("webauthn register: ceremony session limit reached")
		httpapi.ServiceUnavailableC(w, r, api.CodeServiceUnavailable, "too many pending ceremonies")
		return
	}

	httpapi.WriteJSON(w, WebAuthnRegisterBeginResponse{
		PublicKey:    creation,
		SessionToken: token,
	})
}

// --- POST /api/auth/webauthn/register/finish ---

// HandleWebAuthnRegisterFinish handles POST /api/auth/webauthn/register/finish —
// completes passkey registration, stores the new credential, and emits an audit record.
func (h *Handler) HandleWebAuthnRegisterFinish(w http.ResponseWriter, r *http.Request) {
	wa, ok := h.requireWebAuthn(w)
	if !ok {
		return
	}

	user := UserFromContext(r.Context())

	sessData := h.consumeWebAuthnSession(w, r)
	if sessData == nil {
		return
	}

	ctx := r.Context()
	creds, err := h.SecDB.GetPasskeysByUserID(ctx, user.ID)
	if err != nil {
		slog.Error("webauthn register finish: get passkeys", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	webauthnUser, err := authwebauthn.NewUser(user, creds)
	if err != nil {
		slog.Error("webauthn register finish: nil user", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	credential, err := authwebauthn.FinishRegistration(wa, webauthnUser, sessData, r)
	if err != nil {
		slog.Warn("webauthn register finish: failed", "error", err)
		httpapi.BadRequestC(w, r, api.CodeWebAuthnRegisterFailed, "registration failed")
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
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("security: passkey registered",
		"username", user.Username, "name", friendlyName, "ip", ClientIP(r))

	httpapi.WriteJSON(w, api.PasskeyRegistered{
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

// HandleDeletePasskey handles DELETE /api/auth/passkeys/{id} — removes a passkey.
// Refuses when deleting would leave the account with no authentication method.
func (h *Handler) HandleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	passkeyID, ok := parseIDFromPath(w, r.URL.Path, "/api/auth/passkeys/", "passkey id")
	if !ok {
		return
	}

	ctx := r.Context()

	passkeyCount, errPK := h.SecDB.PasskeyCountForUser(ctx, user.ID)
	if errPK != nil {
		slog.Warn("delete passkey: passkey count", "error", errPK)
	}
	// Reachable in unconfigured mode, where Config() is nil: treat OIDC as
	// disabled so it is never counted as a remaining auth method we cannot
	// verify (conservative: refuses the delete rather than stranding the user).
	oidcEnabled := false
	if cfg := h.Config(); cfg != nil {
		oidcEnabled = cfg.OIDCEnabled()
	}
	remaining := auth.MethodAvailability{
		PasskeyCount: passkeyCount - 1,
		HasPassword:  user.PasswordHash != "",
		OIDCEnabled:  oidcEnabled,
		OIDCLinked:   user.OIDCSub != "",
	}
	if !auth.CanDisableMethod(auth.MethodPasskey, remaining) {
		httpapi.ConflictC(w, r, api.CodeConflict, "cannot remove last authentication method")
		return
	}

	if err := h.SecDB.DeletePasskey(ctx, auth.PasskeyRef{ID: passkeyID, UserID: user.ID}); err != nil {
		slog.Error("delete passkey: db error", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("security: passkey deleted",
		"username", user.Username, "passkey_id", passkeyID, "ip", ClientIP(r))

	httpapi.Ok(w)
	Audit(r, slog.LevelInfo, AuditPasskeyDelete, true, user.Username,
		slog.Int64("passkey_id", passkeyID))
}

// --- PUT /api/auth/passkeys/{id} ---

// HandleRenamePasskey handles PUT /api/auth/passkeys/{id} — renames a passkey.
func (h *Handler) HandleRenamePasskey(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	passkeyID, ok := parseIDFromPath(w, r.URL.Path, "/api/auth/passkeys/", "passkey id")
	if !ok {
		return
	}

	req, ok := decodeAuthBody[struct {
		Name string `json:"name"`
	}](w, r)
	if !ok {
		return
	}

	if req.Name == "" {
		httpapi.BadRequestC(w, r, api.CodeBadRequest, "name required")
		return
	}

	if len([]rune(req.Name)) > maxPasskeyNameLen {
		httpapi.BadRequestC(w, r, api.CodeBadRequest, "name too long")
		return
	}

	if err := h.SecDB.RenamePasskey(r.Context(), auth.PasskeyRef{ID: passkeyID, UserID: user.ID}, req.Name); err != nil {
		slog.Error("rename passkey: db error", "error", err)
		httpapi.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	httpapi.Ok(w)
	Audit(r, slog.LevelInfo, AuditPasskeyRename, true, user.Username,
		slog.Int64("passkey_id", passkeyID),
		slog.String("name", req.Name))
}
