// error_codes.go: machine-readable error codes for the JSON envelope.
//
// Constants exist for codes used at 3+ httpapi call sites; one-off codes stay
// string literals at their call site. The enum lives in this package because
// internal/wirespec auto-discovers it by name for the TypeScript string-union it
// generates, so the catalog is a wire type, not response plumbing.
//
// Renaming a code: do NOT — clients consume these as a contract.

package subflux

// ErrorCode is the machine-readable code carried in the JSON error envelope.
// A defined type (not an alias) so the wire generator discovers the catalog
// as a TS string-union; one-off codes still pass as string literals (untyped
// constants convert).
type ErrorCode string

// Generic codes (status mapped 1:1).
const (
	CodeBadRequest         ErrorCode = "bad_request"
	CodeUnauthorized       ErrorCode = "unauthorized"
	CodeForbidden          ErrorCode = "forbidden"
	CodeNotFound           ErrorCode = "not_found"
	CodeMethodNotAllowed   ErrorCode = "method_not_allowed"
	CodeConflict           ErrorCode = "conflict"
	CodePayloadTooLarge    ErrorCode = "payload_too_large"
	CodeRateLimited        ErrorCode = "rate_limited"
	CodeBadGateway         ErrorCode = "bad_gateway"
	CodeServiceUnavailable ErrorCode = "service_unavailable"
	CodeInternalError      ErrorCode = "internal_error"
)

// Auth codes.
//
//nolint:gosec // G101 false positive: these are error-code identifiers, not credentials.
const (
	CodeAuthInvalidCredentials ErrorCode = "auth_invalid_credentials"
	CodeAuthAccountDisabled    ErrorCode = "auth_account_disabled"
	CodeAuthAccountNotSetup    ErrorCode = "auth_account_not_setup"
	CodeAuthPasswordTooShort   ErrorCode = "auth_password_too_short"
	CodeAuthPasswordBreached   ErrorCode = "auth_password_breached"
	CodeAuthSessionInvalid     ErrorCode = "auth_session_invalid"
	CodeAuthSessionRequired    ErrorCode = "auth_session_required"
	CodeAuthRoleRequired       ErrorCode = "auth_role_required"
	CodeAuthAPIKeyInvalid      ErrorCode = "auth_apikey_invalid"
	CodeAuthAPIKeyDisabled     ErrorCode = "auth_apikey_disabled"
	CodeAuthCSRF               ErrorCode = "auth_csrf"
)

// WebAuthn codes.
const (
	CodeWebAuthnSessionInvalid    ErrorCode = "webauthn_session_invalid"
	CodeWebAuthnRegisterFailed    ErrorCode = "webauthn_register_failed"
	CodeWebAuthnNotDiscoverable   ErrorCode = "webauthn_not_discoverable"
	CodeWebAuthnAssertionFailed   ErrorCode = "webauthn_assertion_failed"
	CodeWebAuthnUnsupportedOrigin ErrorCode = "webauthn_unsupported_origin"
	CodeWebAuthnUnconfigured      ErrorCode = "webauthn_unconfigured"
)

// OIDC codes.
const (
	CodeOIDCStateInvalid          ErrorCode = "oidc_state_invalid"
	CodeOIDCNonceInvalid          ErrorCode = "oidc_nonce_invalid"
	CodeOIDCExchangeFailed        ErrorCode = "oidc_exchange_failed"
	CodeOIDCUserInfoFailed        ErrorCode = "oidc_userinfo_failed"
	CodeOIDCAccountNotProvisioned ErrorCode = "oidc_account_not_provisioned"
)

// Setup codes.
const (
	CodeSetupAlreadyComplete ErrorCode = "setup_already_complete"
	CodeSetupPasswordInvalid ErrorCode = "setup_password_invalid"
)

// Config codes.
const (
	CodeConfigInvalid        ErrorCode = "config_invalid"
	CodeConfigUnreachableArr ErrorCode = "config_unreachable_arr"
	CodeConfigYAMLParse      ErrorCode = "config_yaml_parse"
	CodeConfigTooLarge       ErrorCode = "config_too_large"
	CodeConfigReloadFailed   ErrorCode = "config_reload_failed"
)

// Scan / search / manual ops codes.
const (
	CodeScanInProgress         ErrorCode = "scan_in_progress"
	CodeScanNoTargets          ErrorCode = "scan_no_targets"
	CodeSearchInProgress       ErrorCode = "search_in_progress"
	CodeSearchProviderDisabled ErrorCode = "search_provider_disabled"
	CodeSearchNoResults        ErrorCode = "search_no_results"
	CodeDownloadFailed         ErrorCode = "download_failed"
	CodeUnlockNotHeld          ErrorCode = "unlock_not_held"
	// CodeMediaUnwritable: a media folder the work would write into failed
	// its write test, so nothing was started.
	CodeMediaUnwritable ErrorCode = "media_unwritable"
)

// File / preview / sync codes.
const (
	CodePathNotAllowed        ErrorCode = "path_not_allowed"
	CodeMediaNotFound         ErrorCode = "media_not_found"
	CodeSubtitleNotFound      ErrorCode = "subtitle_not_found"
	CodePreviewUnavailable    ErrorCode = "preview_unavailable"
	CodeSyncUnsupportedFormat ErrorCode = "sync_unsupported_format"
	CodeSyncNoReference       ErrorCode = "sync_no_reference"
	CodeSyncLowConfidence     ErrorCode = "sync_low_confidence"
	// CodeSubtitleExtensionNotAllowed is the 409 a delete answers when the
	// target's extension lacks the delete capability in the subtitle
	// extension authority (a server-derived stored-state disagreement, not
	// caller authorization — hence 409, not 403).
	CodeSubtitleExtensionNotAllowed ErrorCode = "subtitle_extension_not_allowed"
)

// Query codes.
const (
	CodeQueryInvalidFilter ErrorCode = "query_invalid_filter"
	CodeQueryLimitExceeded ErrorCode = "query_limit_exceeded"
)

// Provider / arr action codes.
const (
	CodeProviderTimedOut      ErrorCode = "provider_timed_out"
	CodeProviderNotConfigured ErrorCode = "provider_not_configured"
	CodeArrUnreachable        ErrorCode = "arr_unreachable"
)
