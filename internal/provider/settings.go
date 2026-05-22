package provider

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"

	"subflux/internal/api"
)

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
	KeyIgnorePGS       SettingKey = SettingKey(api.EmbeddedSettingIgnorePGS)
	KeyIgnoreVobSub    SettingKey = SettingKey(api.EmbeddedSettingIgnoreVobSub)
	KeyIgnoreASS       SettingKey = SettingKey(api.EmbeddedSettingIgnoreASS)
	KeyAniDBClientKey  SettingKey = "anidb_client_key"
	KeyMode            SettingKey = "mode"
	KeyErrorMessage    SettingKey = "error_message"
	KeyDownloadError   SettingKey = "download_error"
	KeySubtitleContent SettingKey = "subtitle_content"
	KeyIncludeHash     SettingKey = "include_hash"
	KeyHearingImpaired SettingKey = "hearing_impaired"
	KeyForced          SettingKey = "forced"
	KeyResultCount     SettingKey = "result_count"
	KeyScoreBase       SettingKey = "score_base"
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
		if b == "true" {
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
		if n != math.Trunc(n) || n < math.MinInt || n > math.MaxInt {
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
// This is a phased migration: providers can use FromMap to get compile-time safety
// for common fields while provider-specific settings remain in Custom.
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
