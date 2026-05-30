// error_codes.go: machine-readable error codes for the JSON envelope.
//
// These constants are referenced from BadRequestC / UnauthorizedC /
// etc. call sites where the same code appears 3+ times across the
// codebase. One-off codes (used by a single call site) are passed as
// string literals at the call site to avoid pollution here.
//
// Source of truth for the full taxonomy:
// /.kiro/notes/subflux-error-codes.md.
//
// Adding a code: see the "Adding a new code" section in that doc.
// Renaming a code: do NOT — clients consume these as a contract.

package api

// Generic codes (status mapped 1:1).
const (
	CodeBadRequest         = "bad_request"
	CodeUnauthorized       = "unauthorized"
	CodeForbidden          = "forbidden"
	CodeNotFound           = "not_found"
	CodeMethodNotAllowed   = "method_not_allowed"
	CodeConflict           = "conflict"
	CodePayloadTooLarge    = "payload_too_large"
	CodeRateLimited        = "rate_limited"
	CodeBadGateway         = "bad_gateway"
	CodeServiceUnavailable = "service_unavailable"
	CodeInternalError      = "internal_error"
)

// Auth codes.
//
//nolint:gosec // G101 false positive: these are error-code identifiers, not credentials.
const (
	CodeAuthInvalidCredentials = "auth_invalid_credentials"
	CodeAuthAccountDisabled    = "auth_account_disabled"
	CodeAuthAccountNotSetup    = "auth_account_not_setup"
	CodeAuthPasswordTooShort   = "auth_password_too_short"
	CodeAuthPasswordBreached   = "auth_password_breached"
	CodeAuthSessionInvalid     = "auth_session_invalid"
	CodeAuthSessionRequired    = "auth_session_required"
	CodeAuthRoleRequired       = "auth_role_required"
	CodeAuthAPIKeyInvalid      = "auth_apikey_invalid"
	CodeAuthAPIKeyDisabled     = "auth_apikey_disabled"
	CodeAuthCSRF               = "auth_csrf"
)

// WebAuthn codes.
const (
	CodeWebAuthnSessionInvalid    = "webauthn_session_invalid"
	CodeWebAuthnRegisterFailed    = "webauthn_register_failed"
	CodeWebAuthnAssertionFailed   = "webauthn_assertion_failed"
	CodeWebAuthnUnsupportedOrigin = "webauthn_unsupported_origin"
)

// OIDC codes.
const (
	CodeOIDCStateInvalid          = "oidc_state_invalid"
	CodeOIDCNonceInvalid          = "oidc_nonce_invalid"
	CodeOIDCExchangeFailed        = "oidc_exchange_failed"
	CodeOIDCUserInfoFailed        = "oidc_userinfo_failed"
	CodeOIDCAccountNotProvisioned = "oidc_account_not_provisioned"
)

// Setup codes.
const (
	CodeSetupAlreadyComplete = "setup_already_complete"
	CodeSetupPasswordInvalid = "setup_password_invalid"
)

// Config codes.
const (
	CodeConfigInvalid        = "config_invalid"
	CodeConfigUnreachableArr = "config_unreachable_arr"
	CodeConfigYAMLParse      = "config_yaml_parse"
	CodeConfigTooLarge       = "config_too_large"
	CodeConfigReloadFailed   = "config_reload_failed"
)

// Scan / search / manual ops codes.
const (
	CodeScanInProgress         = "scan_in_progress"
	CodeScanNoTargets          = "scan_no_targets"
	CodeSearchInProgress       = "search_in_progress"
	CodeSearchProviderDisabled = "search_provider_disabled"
	CodeSearchNoResults        = "search_no_results"
	CodeDownloadFailed         = "download_failed"
	CodeUnlockNotHeld          = "unlock_not_held"
)

// File / preview / sync codes.
const (
	CodePathNotAllowed        = "path_not_allowed"
	CodeMediaNotFound         = "media_not_found"
	CodeSubtitleNotFound      = "subtitle_not_found"
	CodePreviewUnavailable    = "preview_unavailable"
	CodeSyncUnsupportedFormat = "sync_unsupported_format"
	CodeSyncNoReference       = "sync_no_reference"
	CodeSyncLowConfidence     = "sync_low_confidence"
)

// Query codes.
const (
	CodeQueryInvalidFilter = "query_invalid_filter"
	CodeQueryLimitExceeded = "query_limit_exceeded"
)

// Provider / arr action codes.
const (
	CodeProviderTimedOut      = "provider_timed_out"
	CodeProviderNotConfigured = "provider_not_configured"
	CodeArrUnreachable        = "arr_unreachable"
)
