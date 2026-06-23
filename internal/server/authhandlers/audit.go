package authhandlers

import (
	"log/slog"
	"net/http"
)

// Auth-event audit logging. Emits structured slog records with a fixed
// `event_kind=auth` attribute so log-collection tooling (Loki, journald,
// stdout aggregators) can filter the audit trail from ordinary
// operational logs. Replaces ad-hoc slog calls at security-relevant
// call sites; one emit per event (audit + operator-visible message
// share a single log line at WARN for failures, INFO for successes).
//
// The audit record always carries:
//   - event_kind: "auth" (constant; the filter token)
//   - event:      one of the AuditEvent values (login.success, etc.)
//   - success:    bool
//   - user:       username when known, "" for failed logins on unknown users
//   - ip:         client IP from r.RemoteAddr (port stripped)
//   - user_agent: request User-Agent header
//
// Per-event extra attributes (reason for failures, key_id for apikey
// events, passkey_id for passkey events, etc.) are passed via kvs and
// appended verbatim. Use slog.String / slog.Int / slog.Bool to keep
// the structured shape; raw kv pairs work too but lose type info.
//
// Why slog and not a DB table: a homelab user already has Loki for
// container logs. A new SQLite audit_events table adds schema, a
// retention policy, and a UI for ~3x the LOC and no incremental value
// over `loki | grep event_kind=auth | grep user=alice`. See
// .kiro/notes/rewrite-analysis/subflux/auth.md line 35 for the design
// rationale.

// AuditEventKind is the fixed attribute value used to mark auth audit
// records. Filter on `event_kind="auth"` in log queries.
const AuditEventKind = "auth"

// AuditEvent enumerates the security-relevant events captured in the
// audit trail. Add new events here when introducing new auth flows.
type AuditEvent string

// AuditEvent constants enumerate the security-relevant events captured in the audit trail.
const (
	AuditLoginSuccess     AuditEvent = "login.success"
	AuditLoginFailure     AuditEvent = "login.failure"
	AuditLoginRateLimited AuditEvent = "login.rate_limited"
	AuditLogout           AuditEvent = "logout"
	AuditPasswordChange   AuditEvent = "password.change"
	AuditPasskeyAdd       AuditEvent = "passkey.add"
	AuditPasskeyDelete    AuditEvent = "passkey.delete"
	AuditPasskeyRename    AuditEvent = "passkey.rename"
	AuditAPIKeyCreate     AuditEvent = "apikey.create"
	AuditAPIKeyRevoke     AuditEvent = "apikey.revoke"
	AuditOIDCCallback     AuditEvent = "oidc.callback"
)

// Audit emits a structured auth audit record at the specified slog
// level. Failures should emit at WARN; successes at INFO (so operator
// dashboards still see failures distinct from routine success traffic
// while auditors filter the full trail by event_kind).
//
// `user` is the username when known; pass "" for failures on unknown
// usernames (still useful as the audit record will carry the IP and
// the failure reason).
//
// Extra attributes are appended verbatim. Use `slog.String("reason",
// "invalid_password")` or `slog.String("key_id", id)` to keep the
// structured shape consistent.
func Audit(r *http.Request, level slog.Level, event AuditEvent, success bool, user string, kvs ...any) {
	attrs := make([]any, 0, 6+len(kvs))
	attrs = append(attrs,
		slog.String("event_kind", AuditEventKind),
		slog.String("event", string(event)),
		slog.Bool("success", success),
		slog.String("user", user),
		slog.String("ip", ClientIP(r)),
		slog.String("user_agent", r.UserAgent()),
	)
	attrs = append(attrs, kvs...)
	slog.LogAttrs(r.Context(), level, "audit", toAttrs(attrs)...)
}

// toAttrs converts a mixed slog.Attr / key-value slice into a pure
// []slog.Attr. slog.LogAttrs requires Attr (not the looser any-pair
// shape). Safe under repeated calls because slog.Any wraps non-Attr
// kv pairs as Attr internally.
func toAttrs(kvs []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(kvs))
	for i := 0; i < len(kvs); i++ {
		v := kvs[i]
		if a, ok := v.(slog.Attr); ok {
			out = append(out, a)
			continue
		}
		// Treat as key+value pair. Empty-string key when first element
		// is not a string is also handled; the resulting attr will
		// surface as `slog.Any("", val)` and be visible in logs as a
		// schema error rather than silently dropped.
		key, ok := v.(string)
		_ = ok // ok==false leaves key as "", surfaced visibly in logs
		var val any
		if i+1 < len(kvs) {
			val = kvs[i+1]
			i++
		}
		out = append(out, slog.Any(key, val))
	}
	return out
}
