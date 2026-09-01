package authhandlers

import (
	"log/slog"
	"net/http"
)

// Auth-event audit logging: structured slog records with a fixed
// `event_kind=auth` attribute so log-collection tooling can filter the audit
// trail from ordinary operational logs. slog rather than a DB table: a
// self-hosted deployment already has Loki for container logs.

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
	AuditProfileUpdate    AuditEvent = "profile.update"
	AuditPasskeyAdd       AuditEvent = "passkey.add"
	AuditPasskeyDelete    AuditEvent = "passkey.delete"
	AuditPasskeyRename    AuditEvent = "passkey.rename"
	AuditAPIKeyCreate     AuditEvent = "apikey.create"
	AuditAPIKeyRevoke     AuditEvent = "apikey.revoke"
	AuditOIDCCallback     AuditEvent = "oidc.callback"
)

// Audit emits a structured auth audit record at the specified slog level.
// Failures should emit at WARN; successes at INFO. `user` is the username
// when known; pass "" for failures on unknown usernames.
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
		// A non-string key surfaces as slog.Any("", val) rather than
		// dropping silently.
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
