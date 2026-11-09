package statedb

import (
	"errors"
	"fmt"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
)

// The auth namespace's keys and rows (design sections 4 and 9). No row
// embeds an access record or encodes one through encoding/json, since
// access.Hash marshals only its redacted rendering: every hash is stored as
// its Reveal() text and read back through access.NewHash. access's string
// enums are open sets, so rows keep their text.

// maxID is the largest ID a create assigns (rule 17): 2^53 - 1, the largest
// integer a JSON number carries exactly to a browser.
const maxID = 1<<53 - 1

var errRowID = errors.New("statedb: stored id out of range")

// idKeys keys users, keys and key attributions by their int64 ID, big-endian
// so byte order is ID order. AppendKey takes any int64, so a lookup of an ID
// no row can have encodes and finds nothing; ParseKey refuses one.
var idKeys = keyCodec[int64]{
	version: 1,
	put:     func(dst []byte, id int64) []byte { return appendUint64(dst, uint64(id)) },
	parse:   (*keyReader).id,
}

// sessionKeys keys a session and its activity by user, then session ID.
var sessionKeys = keyCodec[access.SessionRef]{
	version: 1,
	put: func(dst []byte, r access.SessionRef) []byte {
		return appendText(idKeys.put(dst, r.UserID), r.ID)
	},
	parse: func(r *keyReader) access.SessionRef {
		return access.SessionRef{UserID: r.id(), ID: r.text("session id")}
	},
}

// passkeyKeys keys a passkey and its login stamps by user, then passkey ID.
var passkeyKeys = keyCodec[access.PasskeyRef]{
	version: 1,
	put: func(dst []byte, r access.PasskeyRef) []byte {
		return idKeys.put(idKeys.put(dst, r.UserID), r.ID)
	},
	parse: func(r *keyReader) access.PasskeyRef { return access.PasskeyRef{UserID: r.id(), ID: r.id()} },
}

// failureKeys keys a failure record by name, method and trust context.
var failureKeys = keyCodec[account.FailureRef]{
	version: 1,
	put: func(dst []byte, f account.FailureRef) []byte {
		return append(appendText(appendText(dst, f.Name), string(f.Method)), boolByte(f.Trusted))
	},
	parse: func(r *keyReader) account.FailureRef {
		return account.FailureRef{Name: r.text("failure name"), Method: access.Method(r.text("method")), Trusted: r.bool("trusted")}
	},
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// 0x20 users, less ID, which the key holds.
type userRow struct {
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Username       string    `json:"username"`
	Email          string    `json:"email"`
	DisplayName    string    `json:"display_name"`
	PasswordHash   string    `json:"password_hash"`
	Role           string    `json:"role"`
	OIDCIssuer     string    `json:"oidc_issuer"`
	OIDCSubject    string    `json:"oidc_subject"`
	WebAuthnHandle []byte    `json:"webauthn_handle"`
	Enabled        bool      `json:"enabled"`
}

func (*userRow) check() error { return nil }

// 0x21 sessions, less the user and session IDs, which the key holds, and
// the activity, which 0x22 holds. The caps are nanoseconds.
type sessionRow struct {
	CreatedAt   time.Time `json:"created_at"`
	AuthTime    time.Time `json:"auth_time"`
	TokenHash   string    `json:"token_hash"`
	AuthMethod  string    `json:"auth_method"`
	Kind        string    `json:"kind"`
	UserAgent   string    `json:"user_agent"`
	Label       string    `json:"label"`
	IssuedByKey int64     `json:"issued_by_key"`
	IdleCap     int64     `json:"idle_cap"`
	AbsoluteCap int64     `json:"absolute_cap"`
}

func (r *sessionRow) check() error { return nonNegative("issued by key", r.IssuedByKey) }

// 0x22 session_activity: the stamps every verified request writes.
type activityRow struct {
	LastActivity time.Time `json:"last_activity"`
	LastIP       string    `json:"last_ip"`
}

func (*activityRow) check() error { return nil }

// 0x23 passkeys, less the user and passkey IDs, which the key holds, and the
// login stamps, which 0x24 holds. The four flag booleans are never stored:
// a read decodes them from 0x24's RawFlags (rule 8), so they cannot diverge.
type passkeyRow struct {
	CreatedAt         time.Time `json:"created_at"`
	Discoverable      *bool     `json:"discoverable"`
	AttestationType   string    `json:"attestation_type"`
	AttestationFormat string    `json:"attestation_format"`
	RPID              string    `json:"rp_id"`
	Transport         string    `json:"transport"`
	Name              string    `json:"name"`
	Attachment        string    `json:"attachment"`
	CredentialID      []byte    `json:"credential_id"`
	PublicKey         []byte    `json:"public_key"`
	AAGUID            []byte    `json:"aaguid"`
	RawAttestation    []byte    `json:"raw_attestation"`
}

func (*passkeyRow) check() error { return nil }

// 0x24 passkey_use: access.PasskeyLoginUpdate, the custody fields a login
// writes together.
type passkeyUseRow struct {
	At           time.Time `json:"at"`
	SignCount    uint32    `json:"sign_count"`
	RawFlags     uint8     `json:"raw_flags"`
	CloneWarning bool      `json:"clone_warning"`
}

func (*passkeyUseRow) check() error { return nil }

// 0x25 keys, less ID, which the key holds, and LastUsedAt, which 0x26 holds.
type keyRow struct {
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	KeyHash    string    `json:"key_hash"`
	KeyPrefix  string    `json:"key_prefix"`
	KeySuffix  string    `json:"key_suffix"`
	Label      string    `json:"label"`
	Scopes     []string  `json:"scopes"`
	LineageIDs []int64   `json:"lineage_ids"`
	UserID     int64     `json:"user_id"`
}

func (r *keyRow) check() error {
	if err := positive("user id", r.UserID); err != nil {
		return err
	}
	return positiveAll("lineage id", r.LineageIDs)
}

// 0x26 key_use.
type keyUseRow struct {
	LastUsedAt time.Time `json:"last_used_at"`
}

func (*keyUseRow) check() error { return nil }

// 0x27 tokens, less the hash, which the key holds.
type tokenRow struct {
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Purpose     string    `json:"purpose"`
	Role        string    `json:"role"`
	Subject     int64     `json:"subject"`
	IssuedByKey int64     `json:"issued_by_key"`
}

func (r *tokenRow) check() error {
	if err := nonNegative("subject", r.Subject); err != nil {
		return err
	}
	return nonNegative("issued by key", r.IssuedByKey)
}

// 0x28 failures: account.FailureCount less its FailureRef, which the key
// holds.
type failureRow struct {
	LockedAt   time.Time `json:"locked_at"`
	Count      int       `json:"count"`
	Locked     bool      `json:"locked"`
	Unreported bool      `json:"unreported"`
}

var errRowCount = errors.New("statedb: stored failure count is negative")

func (r *failureRow) check() error {
	if r.Count < 0 {
		return errRowCount
	}
	return nil
}

// 0x29 secrets.
type secretsRow struct {
	FailureKey sum32 `json:"failure_key"`
	DeviceKey  sum32 `json:"device_key"`
}

func (*secretsRow) check() error { return nil }

// 0x2A key_attributions, less ID, which the key holds.
type attributionRow struct {
	At         time.Time `json:"at"`
	Action     string    `json:"action"`
	KeyLineage []int64   `json:"key_lineage"`
	UserID     int64     `json:"user_id"`
}

func (r *attributionRow) check() error {
	if err := positive("user id", r.UserID); err != nil {
		return err
	}
	return positiveAll("lineage id", r.KeyLineage)
}

// 0x2B auth_method_state: AuthMethodState.
type methodStateRow struct {
	OIDCIssuer    string       `json:"oidc_issuer"`
	RPID          string       `json:"rp_id"`
	SignIn        signInKeyRow `json:"sign_in"`
	PasswordLogin bool         `json:"password_login"`
}

type signInKeyRow struct {
	OIDCClientID      string       `json:"oidc_client_id"`
	OIDCRedirectURL   string       `json:"oidc_redirect_url"`
	OIDCAllowedGroups setDigestRow `json:"oidc_allowed_groups"`
	PasskeyOrigins    setDigestRow `json:"passkey_origins"`
	OIDCClientSecret  sum32        `json:"oidc_client_secret"`
	OIDCAutoProvision bool         `json:"oidc_auto_provision"`
	DisableAuth       bool         `json:"disable_auth"`
}

type setDigestRow struct {
	Count  int   `json:"count"`
	SHA256 sum32 `json:"sha256"`
}

var errRowSetCount = errors.New("statedb: stored set count is negative")

func (r *methodStateRow) check() error {
	if r.SignIn.OIDCAllowedGroups.Count < 0 || r.SignIn.PasskeyOrigins.Count < 0 {
		return errRowSetCount
	}
	return nil
}

func positive(what string, id int64) error {
	if id < 1 {
		return fmt.Errorf("%w: %s %d", errRowID, what, id)
	}
	return nil
}

func positiveAll(what string, ids []int64) error {
	for _, id := range ids {
		if err := positive(what, id); err != nil {
			return err
		}
	}
	return nil
}

func nonNegative(what string, id int64) error {
	if id < 0 {
		return fmt.Errorf("%w: %s %d", errRowID, what, id)
	}
	return nil
}
