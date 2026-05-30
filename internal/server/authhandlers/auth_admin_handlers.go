package authhandlers

import (
	"log/slog"
	"net/http"
	"time"

	"subflux/internal/api"
)

// --- GET /api/auth/users ---

func (h *Handler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.AdminDB.ListUsers(r.Context())
	if err != nil {
		slog.Error("list users: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	type userInfo struct {
		CreatedAt time.Time `json:"created_at"`
		Username  string    `json:"username"`
		Email     string    `json:"email"`
		Role      api.Role  `json:"role"`
		ID        int64     `json:"id"`
		Enabled   bool      `json:"enabled"`
	}

	out := make([]userInfo, 0, len(users))
	for i := range users {
		out = append(out, userInfo{
			ID:        users[i].ID,
			Username:  users[i].Username,
			Email:     users[i].Email,
			Role:      users[i].Role,
			Enabled:   users[i].Enabled,
			CreatedAt: users[i].CreatedAt,
		})
	}

	api.WriteJSON(w, out)
}

// --- POST /api/auth/users ---

func (h *Handler) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	admin := api.UserFromContext(r.Context())

	req, ok := decodeAuthBody[struct {
		Username string   `json:"username"`
		Password string   `json:"password"`
		Role     api.Role `json:"role"`
		Email    string   `json:"email"`
	}](w, r)
	if !ok {
		return
	}

	if req.Username == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "username required")
		return
	}
	if len([]rune(req.Username)) > maxUsernameLen {
		api.BadRequestC(w, r, api.CodeBadRequest, "username too long")
		return
	}
	if req.Role == "" {
		req.Role = api.RoleUser
	}
	if req.Role != api.RoleAdmin && req.Role != api.RoleUser {
		api.BadRequestC(w, r, api.CodeBadRequest, "role must be admin or user")
		return
	}

	checkBreach := false
	cfg := h.Config()
	if cfg != nil {
		checkBreach = cfg.CheckBreachedPasswords()
	}
	hash, userMsg, err := ValidateAndHashPassword(r.Context(), req.Password, req.Username, true, checkBreach, h.HTTPClient)
	if userMsg != "" {
		api.BadRequestC(w, r, api.CodeBadRequest, userMsg)
		return
	}
	if err != nil {
		slog.Error("create user: hash password", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	now := time.Now()
	newUser := &api.User{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: hash,
		Role:         req.Role,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := h.AdminDB.CreateUser(r.Context(), newUser); err != nil {
		slog.Error("create user: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("admin: user created",
		"admin", admin.Username, "new_user", req.Username, "role", req.Role)

	api.WriteJSON(w, api.AdminUserCreatedResponse{
		ID:       newUser.ID,
		Username: newUser.Username,
		Email:    newUser.Email,
		Role:     newUser.Role,
	})
}

// --- DELETE /api/auth/users/{id} ---

func (h *Handler) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	admin := api.UserFromContext(r.Context())

	userID, ok := parseIDFromPath(w, r.URL.Path, "/api/auth/users/", "user id")
	if !ok {
		return
	}

	// Cannot delete self.
	if userID == admin.ID {
		api.ConflictC(w, r, api.CodeConflict, "cannot delete your own account")
		return
	}

	// Last-admin protection: never delete the only remaining admin account,
	// which would leave the instance unmanageable.
	users, listErr := h.AdminDB.ListUsers(r.Context())
	if listErr != nil {
		slog.Error("delete user: list users", "error", listErr)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	var target *api.User
	adminCount, localAdmins := 0, 0
	for i := range users {
		if users[i].Role == api.RoleAdmin {
			adminCount++
			if users[i].PasswordHash != "" {
				localAdmins++
			}
		}
		if users[i].ID == userID {
			target = &users[i]
		}
	}
	if target != nil && target.Role == api.RoleAdmin {
		if adminCount <= 1 {
			api.ConflictC(w, r, api.CodeConflict, "cannot delete the last admin account")
			return
		}
		if target.PasswordHash != "" && localAdmins <= 1 {
			api.ConflictC(w, r, api.CodeConflict, "cannot delete the last local (break-glass) admin account")
			return
		}
	}

	if err := h.AdminDB.DeleteUser(r.Context(), userID); err != nil {
		slog.Error("delete user: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("admin: user deleted",
		"admin", admin.Username, "deleted_user_id", userID, "ip", ClientIP(r))

	api.Ok(w)
}
