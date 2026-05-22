// error_codes.ts -- TypeScript catalog mirroring internal/api/error_codes.go.
// These are the machine-readable codes in the JSON error envelope.

export const ErrorCode = {
  // generic
  BadRequest: 'bad_request',
  Unauthorized: 'unauthorized',
  Forbidden: 'forbidden',
  NotFound: 'not_found',
  MethodNotAllowed: 'method_not_allowed',
  Conflict: 'conflict',
  PayloadTooLarge: 'payload_too_large',
  RateLimited: 'rate_limited',
  BadGateway: 'bad_gateway',
  ServiceUnavailable: 'service_unavailable',
  InternalError: 'internal_error',

  // auth
  AuthInvalidCredentials: 'auth_invalid_credentials',
  AuthAccountDisabled: 'auth_account_disabled',
  AuthAccountNotSetup: 'auth_account_not_setup',
  AuthPasswordTooShort: 'auth_password_too_short',
  AuthPasswordBreached: 'auth_password_breached',
  AuthSessionInvalid: 'auth_session_invalid',
  AuthSessionRequired: 'auth_session_required',
  AuthReauthRequired: 'auth_reauth_required',
  AuthRoleRequired: 'auth_role_required',
  AuthAPIKeyInvalid: 'auth_apikey_invalid',
  AuthAPIKeyDisabled: 'auth_apikey_disabled',
  AuthCSRF: 'auth_csrf',

  // totp
  TOTPRequired: 'totp_required',
  TOTPInvalid: 'totp_invalid',
  TOTPReplay: 'totp_replay',
  TOTPAlreadyEnabled: 'totp_already_enabled',
  TOTPNotEnabled: 'totp_not_enabled',

  // webauthn
  WebAuthnSessionInvalid: 'webauthn_session_invalid',
  WebAuthnRegisterFailed: 'webauthn_register_failed',
  WebAuthnAssertionFailed: 'webauthn_assertion_failed',
  WebAuthnUnsupportedOrigin: 'webauthn_unsupported_origin',

  // oidc
  OIDCStateInvalid: 'oidc_state_invalid',
  OIDCNonceInvalid: 'oidc_nonce_invalid',
  OIDCExchangeFailed: 'oidc_exchange_failed',
  OIDCUserInfoFailed: 'oidc_userinfo_failed',
  OIDCAccountNotProvisioned: 'oidc_account_not_provisioned',

  // setup
  SetupAlreadyComplete: 'setup_already_complete',
  SetupPasswordInvalid: 'setup_password_invalid',

  // config
  ConfigInvalid: 'config_invalid',
  ConfigUnreachableArr: 'config_unreachable_arr',
  ConfigYAMLParse: 'config_yaml_parse',
  ConfigTooLarge: 'config_too_large',
  ConfigReloadFailed: 'config_reload_failed',

  // scan / search / manual ops
  ScanInProgress: 'scan_in_progress',
  ScanNoTargets: 'scan_no_targets',
  SearchInProgress: 'search_in_progress',
  SearchProviderDisabled: 'search_provider_disabled',
  SearchNoResults: 'search_no_results',
  DownloadFailed: 'download_failed',
  UnlockNotHeld: 'unlock_not_held',

  // file / preview / sync
  PathNotAllowed: 'path_not_allowed',
  MediaNotFound: 'media_not_found',
  SubtitleNotFound: 'subtitle_not_found',
  PreviewUnavailable: 'preview_unavailable',
  SyncUnsupportedFormat: 'sync_unsupported_format',
  SyncNoReference: 'sync_no_reference',
  SyncLowConfidence: 'sync_low_confidence',

  // query
  QueryInvalidFilter: 'query_invalid_filter',
  QueryLimitExceeded: 'query_limit_exceeded',

  // provider / arr
  ProviderTimedOut: 'provider_timed_out',
  ProviderNotConfigured: 'provider_not_configured',
  ArrUnreachable: 'arr_unreachable',
} as const;

export type ErrorCode = typeof ErrorCode[keyof typeof ErrorCode];

/** Returns true if err.code matches any of the given codes. Safe on undefined. */
export function hasCode(
  err: { code?: string } | null | undefined,
  ...codes: ErrorCode[]
): boolean {
  if (!err || !err.code) return false;
  return codes.includes(err.code as ErrorCode);
}
