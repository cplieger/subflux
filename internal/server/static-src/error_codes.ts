// error_codes.ts -- TypeScript catalog mirroring internal/subflux/error_codes.go.
// These are the machine-readable codes in the JSON error envelope.

export const ErrorCode = {
  // generic
  BadRequest: "bad_request",
  Unauthorized: "unauthorized",
  Forbidden: "forbidden",
  NotFound: "not_found",
  MethodNotAllowed: "method_not_allowed",
  Conflict: "conflict",
  RateLimited: "rate_limited",
  BadGateway: "bad_gateway",
  ServiceUnavailable: "service_unavailable",
  InternalError: "internal_error",

  // auth
  AuthInvalidCredentials: "auth_invalid_credentials",
  AuthAccountDisabled: "auth_account_disabled",
  AuthSessionInvalid: "auth_session_invalid",
  AuthSessionRequired: "auth_session_required",
  AuthRoleRequired: "auth_role_required",

  // webauthn
  WebAuthnSessionInvalid: "webauthn_session_invalid",
  WebAuthnRegisterFailed: "webauthn_register_failed",
  WebAuthnNotDiscoverable: "webauthn_not_discoverable",
  WebAuthnAssertionFailed: "webauthn_assertion_failed",
  WebAuthnUnsupportedOrigin: "webauthn_unsupported_origin",
  WebAuthnUnconfigured: "webauthn_unconfigured",

  // oidc
  OIDCStateInvalid: "oidc_state_invalid",
  OIDCExchangeFailed: "oidc_exchange_failed",

  // setup
  SetupAlreadyComplete: "setup_already_complete",

  // config
  ConfigInvalid: "config_invalid",
  ConfigUnreachableArr: "config_unreachable_arr",
  ConfigTooLarge: "config_too_large",
  ConfigReloadFailed: "config_reload_failed",

  // scan / search / manual ops
  ScanInProgress: "scan_in_progress",
  SearchProviderDisabled: "search_provider_disabled",

  // file / preview / sync
  PathNotAllowed: "path_not_allowed",
  MediaNotFound: "media_not_found",
  SubtitleNotFound: "subtitle_not_found",
  PreviewUnavailable: "preview_unavailable",
  SyncUnsupportedFormat: "sync_unsupported_format",

  // query
  QueryInvalidFilter: "query_invalid_filter",

  // provider / arr
  ArrUnreachable: "arr_unreachable",
} as const;

export type ErrorCode = (typeof ErrorCode)[keyof typeof ErrorCode];

/** Returns true if err.code matches any of the given codes. Safe on undefined. */
export function hasCode(err: { code?: string } | null | undefined, ...codes: ErrorCode[]): boolean {
  if (!err?.code) {
    return false;
  }
  return codes.includes(err.code as ErrorCode);
}
