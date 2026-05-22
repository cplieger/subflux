// Package migrations defines the schema DDL and migration steps for the
// subflux SQLite database. Separating definitions from execution allows
// tooling to validate migration ordering without importing the full store.
package migrations

// Schema is the full declarative DDL for the database.
// Uses IF NOT EXISTS so it works on both fresh and existing databases.
const Schema = `
CREATE TABLE IF NOT EXISTS search_attempts (
	media_type TEXT NOT NULL,
	media_id   TEXT NOT NULL,
	language   TEXT NOT NULL,
	provider   TEXT NOT NULL,
	last_tried DATETIME NOT NULL,
	failures   INTEGER NOT NULL DEFAULT 0,
	next_retry DATETIME NOT NULL,
	PRIMARY KEY (media_type, media_id, language, provider)
);
CREATE TABLE IF NOT EXISTS subtitle_state (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	media_type     TEXT NOT NULL,
	media_id       TEXT NOT NULL,
	language       TEXT NOT NULL,
	provider       TEXT NOT NULL,
	release_name   TEXT,
	score          INTEGER,
	path           TEXT NOT NULL,
	title          TEXT NOT NULL DEFAULT '',
	imdb_id        TEXT NOT NULL DEFAULT '',
	season         INTEGER NOT NULL DEFAULT 0,
	episode        INTEGER NOT NULL DEFAULT 0,
	release_tag    TEXT NOT NULL DEFAULT '',
	manual         INTEGER NOT NULL DEFAULT 0,
	video_path     TEXT NOT NULL DEFAULT '',
	media_imported DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_attempts_retry ON search_attempts(next_retry);
CREATE INDEX IF NOT EXISTS idx_state_media ON subtitle_state(media_type, media_id);
CREATE INDEX IF NOT EXISTS idx_state_imported ON subtitle_state(media_imported);
CREATE TABLE IF NOT EXISTS subtitle_files (
	media_type TEXT NOT NULL,
	media_id   TEXT NOT NULL,
	language   TEXT NOT NULL,
	variant    TEXT NOT NULL,
	source     TEXT NOT NULL,
	codec      TEXT NOT NULL DEFAULT '',
	path       TEXT NOT NULL DEFAULT '',
	offset_ms  INTEGER NOT NULL DEFAULT 0,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (media_type, media_id, language, variant, source, path)
);
CREATE TABLE IF NOT EXISTS scan_state (
	media_type TEXT NOT NULL,
	media_id   TEXT NOT NULL,
	title      TEXT NOT NULL DEFAULT '',
	season     INTEGER NOT NULL DEFAULT 0,
	episode    INTEGER NOT NULL DEFAULT 0,
	audio_lang TEXT NOT NULL DEFAULT '',
	scanned_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (media_type, media_id)
);
CREATE TABLE IF NOT EXISTS poll_state (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS auth_users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    email         TEXT NOT NULL DEFAULT '' COLLATE NOCASE,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    role          TEXT NOT NULL DEFAULT 'user',
    oidc_sub      TEXT NOT NULL DEFAULT '',
    oidc_issuer   TEXT NOT NULL DEFAULT '',
    totp_secret   BLOB,
    totp_enabled  INTEGER NOT NULL DEFAULT 0,
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_totp_step INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_oidc
    ON auth_users(oidc_issuer, oidc_sub) WHERE oidc_sub != '';

CREATE TABLE IF NOT EXISTS auth_sessions (
    token_hash   TEXT PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    auth_method  TEXT NOT NULL,
    ip_address   TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_activity DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reauth_at    DATETIME,
    oidc_expiry  DATETIME
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON auth_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON auth_sessions(last_activity);
CREATE INDEX IF NOT EXISTS idx_sessions_cleanup ON auth_sessions(last_activity, created_at);

CREATE TABLE IF NOT EXISTS auth_passkeys (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    credential_id   BLOB NOT NULL UNIQUE,
    public_key      BLOB NOT NULL,
    aaguid          BLOB NOT NULL DEFAULT X'00000000000000000000000000000000',
    attestation_type TEXT NOT NULL DEFAULT '',
    transport       TEXT NOT NULL DEFAULT '',
    sign_count      INTEGER NOT NULL DEFAULT 0,
    name            TEXT NOT NULL DEFAULT '',
    backup_eligible INTEGER NOT NULL DEFAULT 0,
    backup_state    INTEGER NOT NULL DEFAULT 0,
    user_present    INTEGER NOT NULL DEFAULT 0,
    user_verified   INTEGER NOT NULL DEFAULT 0,
    raw_attestation BLOB,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_passkeys_user ON auth_passkeys(user_id);

CREATE TABLE IF NOT EXISTS auth_api_keys (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    key_hash   TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL DEFAULT '',
    key_suffix TEXT NOT NULL DEFAULT '',
    label      TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_apikeys_user ON auth_api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_apikeys_hash ON auth_api_keys(key_hash);

CREATE TABLE IF NOT EXISTS auth_recovery_codes (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id   INTEGER NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_recovery_user ON auth_recovery_codes(user_id);

CREATE TABLE IF NOT EXISTS auth_oidc_states (
    state      TEXT PRIMARY KEY,
    nonce      TEXT NOT NULL,
    code_verifier TEXT NOT NULL,
    redirect_uri TEXT NOT NULL DEFAULT '/',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// Migrations is the ordered list of post-schema migrations.
// Each entry is a SQL statement that is safe to re-run (idempotent).
var Migrations = []string{
	"ALTER TABLE auth_passkeys ADD COLUMN backup_eligible INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE auth_passkeys ADD COLUMN backup_state INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE auth_passkeys ADD COLUMN user_present INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE auth_passkeys ADD COLUMN user_verified INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE auth_passkeys ADD COLUMN raw_attestation BLOB",
}
