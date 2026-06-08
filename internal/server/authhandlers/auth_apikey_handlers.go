package authhandlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/auth"
)

// --- GET /api/auth/apikeys ---

func (h *Handler) HandleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	keys, err := h.SecDB.ListAPIKeysByUserID(r.Context(), user.ID)
	if err != nil {
		slog.Error("list api keys: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	type apiKeyInfo struct {
		CreatedAt time.Time `json:"created_at"`
		KeyPrefix string    `json:"key_prefix"`
		KeySuffix string    `json:"key_suffix"`
		Label     string    `json:"label"`
		ID        int64     `json:"id"`
	}

	out := make([]apiKeyInfo, 0, len(keys))
	for _, k := range keys {
		out = append(out, apiKeyInfo{
			ID:        k.ID,
			KeyPrefix: k.KeyPrefix,
			KeySuffix: k.KeySuffix,
			Label:     k.Label,
			CreatedAt: k.CreatedAt,
		})
	}

	api.WriteJSON(w, out)
}

// --- POST /api/auth/apikeys ---

func (h *Handler) HandleGenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	var req struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid request body")
		return
	}

	if len([]rune(req.Label)) > maxAPIKeyLabelLen {
		api.BadRequestC(w, r, api.CodeBadRequest, "label too long")
		return
	}

	plaintext, hash, prefix, suffix, err := auth.GenerateAPIKey()
	if err != nil {
		slog.Error("generate api key: generate", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	now := time.Now()
	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     req.Label,
		CreatedAt: now,
	}

	if err := h.SecDB.CreateAPIKey(r.Context(), apiKey); err != nil {
		slog.Error("generate api key: store", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("security: API key generated",
		"username", user.Username, "label", req.Label, "ip", ClientIP(r))

	api.WriteJSON(w, api.KeyGeneratedResponse{
		ID:        apiKey.ID,
		Key:       plaintext,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     req.Label,
		CreatedAt: now,
	})
	Audit(r, slog.LevelInfo, AuditAPIKeyCreate, true, user.Username,
		slog.Int64("key_id", apiKey.ID),
		slog.String("label", req.Label))
}

// --- DELETE /api/auth/apikeys/{id} ---

func (h *Handler) HandleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	keyID, ok := parseIDFromPath(w, r.URL.Path, "/api/auth/apikeys/", "api key id")
	if !ok {
		return
	}

	if err := h.SecDB.DeleteAPIKey(r.Context(), keyID, user.ID); err != nil {
		slog.Error("revoke api key: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("security: API key revoked",
		"username", user.Username, "key_id", keyID, "ip", ClientIP(r))

	api.Ok(w)
	Audit(r, slog.LevelInfo, AuditAPIKeyRevoke, true, user.Username,
		slog.Int64("key_id", keyID))
}
