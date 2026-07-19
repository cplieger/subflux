// Package buckets is the single owner of the AUTH-domain bbolt bucket names
// shared by the two packages that operate on one database file: the core
// store (internal/boltstore) bootstraps every bucket in its schema, and the
// auth store (internal/authstore) builds keys inside these. Both alias the
// constants here, so the former "MUST match" comment pair is now a
// by-construction guarantee instead of reviewer discipline (S15).
//
// The core-domain bucket names stay in internal/boltstore/keys.go: they have
// exactly one consumer, so a shared home would be indirection without a
// second reader. If a third package ever needs them, they move here too.
package buckets

// Auth primary buckets.
const (
	AuthUsers    = "auth_users"    // be64(id) -> userRec
	AuthPasskeys = "auth_passkeys" //nolint:gosec // G101: bbolt bucket name, not a credential
	AuthAPIKeys  = "auth_api_keys" //nolint:gosec // G101: bbolt bucket name, not a credential
)

// Auth secondary (index) buckets.
const (
	IxUserName    = "ix_user_name"    // lower(username) -> be64(user_id)
	IxUserOIDC    = "ix_user_oidc"    // issuer 0x00 sub -> be64(user_id)
	IxPasskeyUser = "ix_passkey_user" // be64(user_id) 0x00 credential_id -> (empty)
	IxAPIKeyUser  = "ix_apikey_user"  //nolint:gosec // G101: bbolt bucket name, not a credential
)

// Auth lists every auth-domain bucket, primaries then indexes — the set the
// core store bootstraps and the auth store's foundation test asserts exists.
func Auth() []string {
	return []string{
		AuthUsers, AuthPasskeys, AuthAPIKeys,
		IxUserName, IxUserOIDC, IxPasskeyUser, IxAPIKeyUser,
	}
}
