package provider

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"

	"github.com/cplieger/subflux/internal/api"
)

const valTrue = "true"

// SettingKey is a typed constant for provider setting keys, preventing typos
// and enabling IDE navigation.
type SettingKey string

// Common provider setting keys.
const (
	KeyAPIKey          SettingKey = "api_key"
	KeyUsername        SettingKey = "username"
	KeyPassword        SettingKey = "password"
	KeyPasskey         SettingKey = "passkey"
	KeyToken           SettingKey = "token"
	KeyUseHash         SettingKey = "use_hash"
	KeyIncludeAI       SettingKey = "include_ai_translated"
	KeyAniDBClientKey  SettingKey = "anidb_client_key"
	KeyMode            SettingKey = "mode"
	KeyErrorMessage    SettingKey = "error_message"
	KeyDownloadError   SettingKey = "download_error"
	KeySubtitleContent SettingKey = "subtitle_content"
	KeyIncludeHash     SettingKey = "include_hash"
	KeyHearingImpaired SettingKey = "hearing_impaired"
	KeyForced          SettingKey = "forced"
	KeyResultCount     SettingKey = "result_count"
	KeyFlakyRate       SettingKey = "flaky_rate"
	KeyDelayMs         SettingKey = "delay_ms"
)

// SettingBool returns the boolean value for key in settings.
// Accepts native bool or string "true"/"false". Returns def if the key is
// missing or the value is not a recognized boolean.
func SettingBool(settings map[string]any, key SettingKey, def bool) bool {
	v, ok := settings[string(key)]
	if !ok {
		return def
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		if b == valTrue {
			return true
		}
		if b == "false" {
			return false
		}
		slog.Debug("provider setting type mismatch",
			"key", key, "expected", "bool", "got_type", "string", "got_value", b)
		return def
	default:
		slog.Debug("provider setting type mismatch",
			"key", key, "expected", "bool", "got_type", fmt.Sprintf("%T", v))
		return def
	}
}

// SettingString returns the string value for key in settings.
// Returns "" if the key is missing or the value is not a string.
func SettingString(settings map[string]any, key SettingKey) string {
	v, ok := settings[string(key)]
	if !ok {
		return ""
	}
	s, isStr := v.(string)
	if !isStr {
		slog.Debug("provider setting type mismatch",
			"key", key, "expected", "string", "got_type", fmt.Sprintf("%T", v))
		return ""
	}
	return s
}

// SettingInt returns the integer value for key in settings.
// Accepts native int/int64/float64 (whole YAML numeric) or numeric
// string. Returns def if the key is missing or the value is not
// convertible to int. Non-whole float64 values return def.
func SettingInt(settings map[string]any, key SettingKey, def int) int {
	v, ok := settings[string(key)]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		if n < math.MinInt || n > math.MaxInt {
			return def
		}
		return int(n)
	case float64:
		// float64(math.MaxInt) rounds up to 2^63, which is NOT representable
		// as an int (int64 max is 2^63-1); int(n) would yield a garbage value.
		// Use >= to reject that boundary as out of range.
		if n != math.Trunc(n) || n < math.MinInt || n >= math.MaxInt {
			return def
		}
		return int(n)
	case string:
		parsed, err := strconv.Atoi(n)
		if err == nil {
			return parsed
		}
	}
	slog.Debug("provider setting type mismatch",
		"key", key, "expected", "int", "got_type", fmt.Sprintf("%T", v))
	return def
}

// SettingFloat returns the float64 value for key in settings.
// Accepts native float64/int/int64 or numeric string. Returns def if
// the key is missing or the value is not convertible to float64.
func SettingFloat(settings map[string]any, key SettingKey, def float64) float64 {
	v, ok := settings[string(key)]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		parsed, err := strconv.ParseFloat(n, 64)
		if err == nil {
			return parsed
		}
	}
	slog.Debug("provider setting type mismatch",
		"key", key, "expected", "float", "got_type", fmt.Sprintf("%T", v))
	return def
}

// Settings provides typed access to common provider configuration fields.
// Every common field has migrated here (FromMap gives compile-time safety);
// Custom carries only genuinely provider-specific extras.
type Settings struct {
	Custom   map[string]any // provider-specific extras
	APIKey   string
	Username string
	Password string
	Passkey  string
	Token    string
	UseHash  bool
}

// FromMap constructs a Settings from the raw settings map,
// extracting common fields with type-safe accessors and preserving
// remaining entries in Custom for provider-specific use.
func FromMap(settings map[string]any) Settings {
	ps := Settings{
		APIKey:   SettingString(settings, KeyAPIKey),
		Username: SettingString(settings, KeyUsername),
		Password: SettingString(settings, KeyPassword),
		Passkey:  SettingString(settings, KeyPasskey),
		Token:    SettingString(settings, KeyToken),
		UseHash:  SettingBool(settings, KeyUseHash, false),
	}
	// Collect non-common keys into Custom.
	commonKeys := map[SettingKey]struct{}{
		KeyAPIKey: {}, KeyUsername: {}, KeyPassword: {},
		KeyPasskey: {}, KeyToken: {}, KeyUseHash: {},
	}
	for k, v := range settings {
		if _, isCommon := commonKeys[SettingKey(k)]; !isCommon {
			if ps.Custom == nil {
				ps.Custom = make(map[string]any)
			}
			ps.Custom[k] = v
		}
	}
	return ps
}

// NormalizeSettings returns a copy of the raw settings map with every
// declared-but-absent field filled from its schema Default, coerced per the
// declared Type (P14). The registry applies this before invoking a factory,
// which makes the providerEntries declaration the SINGLE source of a
// setting's default: factories read the normalized map through FromMap /
// the typed accessors and never re-encode a default of their own (the
// use_hash dual-encoding this replaces drifted exactly once already).
// Undeclared keys pass through untouched, and a declared field the user DID
// set is never rewritten.
func NormalizeSettings(fields []api.ProviderSchemaField, raw map[string]any) map[string]any {
	if len(fields) == 0 {
		return raw
	}
	out := make(map[string]any, len(raw)+len(fields))
	for k, v := range raw {
		out[k] = v
	}
	for _, f := range fields {
		if f.Default == "" {
			continue
		}
		if _, present := out[f.Key]; present {
			continue
		}
		out[f.Key] = coerceDefault(f)
	}
	return out
}

// coerceDefault converts a schema field's string Default into the native
// type its declared Type implies, so normalized maps carry the same value
// shapes YAML parsing produces.
func coerceDefault(f api.ProviderSchemaField) any {
	switch f.Type {
	case "bool":
		return f.Default == valTrue
	case "number":
		if n, err := strconv.Atoi(f.Default); err == nil {
			return n
		}
		return f.Default
	default: // text, secret, select
		return f.Default
	}
}
