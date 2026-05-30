package api

import "time"

// SetupStatusResponse is the JSON response for GET /api/auth/setup.
type SetupStatusResponse struct {
	SetupRequired bool `json:"setup_required"`
	ConfigValid   bool `json:"config_valid"`
}

// UserMeResponse is the JSON response for GET /api/auth/me.
type UserMeResponse struct {
	Username    string `json:"username"`
	Role        Role   `json:"role"`
	ID          int64  `json:"id"`
	HasPasskeys bool   `json:"has_passkeys"`
	OIDCLinked  bool   `json:"oidc_linked"`
	HasPassword bool   `json:"has_password"`
}

// LoginSuccessResponse is the JSON response after successful login.
type LoginSuccessResponse struct {
	Redirect string         `json:"redirect"`
	User     UserMeResponse `json:"user"`
}

// WebAuthnUnknownCredentialResponse signals an unknown credential to the client.
type WebAuthnUnknownCredentialResponse struct {
	Error  string `json:"error"`
	Signal string `json:"signal"`
}

// WebAuthnSignalDataResponse is the JSON response for GET /api/auth/webauthn/signal-data.
type WebAuthnSignalDataResponse struct {
	RPID          string   `json:"rp_id"`
	UserID        string   `json:"user_id"`
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	CredentialIDs []string `json:"credential_ids"`
}

// PasskeyRegisteredResponse is the JSON response after successful passkey registration.
type PasskeyRegisteredResponse struct {
	CreatedAt time.Time `json:"created_at"`
	Name      string    `json:"name"`
	Transport string    `json:"transport"`
	ID        int64     `json:"id"`
}

// KeyGeneratedResponse is the JSON response after generating an API key.
type KeyGeneratedResponse struct {
	CreatedAt time.Time `json:"created_at"`
	Key       string    `json:"key"`
	KeyPrefix string    `json:"key_prefix"`
	KeySuffix string    `json:"key_suffix"`
	Label     string    `json:"label"`
	ID        int64     `json:"id"`
}

// AdminUserCreatedResponse is the JSON response after admin creates a user.
type AdminUserCreatedResponse struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     Role   `json:"role"`
	ID       int64  `json:"id"`
}
