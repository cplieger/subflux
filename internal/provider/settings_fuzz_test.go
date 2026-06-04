package provider

import (
	"math"
	"testing"
)

// FuzzFromMap exercises FromMap with arbitrary key/value pairs, verifying
// that common keys never leak into the Custom map and that the function
// never panics regardless of input shapes.
func FuzzFromMap(f *testing.F) {
	f.Add("api_key", "secret123", "custom_field", "value", true, int64(42), 1.5)
	f.Add("", "", "", "", false, int64(0), 0.0)
	f.Add("username", "user", "password", "pass", true, int64(-1), -1.5)
	f.Add("token", "tok", "use_hash", "true", false, int64(math.MaxInt64), math.MaxFloat64)

	f.Fuzz(func(t *testing.T, k1, v1, k2, v2 string, boolVal bool, intVal int64, floatVal float64) {
		settings := map[string]any{
			k1: v1,
			k2: boolVal,
			"extra_int":   intVal,
			"extra_float": floatVal,
		}
		if v2 != "" {
			settings[v2] = v2
		}

		result := FromMap(settings)

		// Invariant 1: common keys never appear in Custom.
		commonKeys := []string{"api_key", "username", "password", "passkey", "token", "use_hash"}
		for _, ck := range commonKeys {
			if result.Custom != nil {
				if _, exists := result.Custom[ck]; exists {
					t.Fatalf("common key %q leaked into Custom", ck)
				}
			}
		}

		// Invariant 2: if k1 is a common key with string value, the
		// corresponding typed field must hold that value.
		switch SettingKey(k1) {
		case KeyAPIKey:
			if result.APIKey != v1 {
				t.Fatalf("APIKey = %q, want %q", result.APIKey, v1)
			}
		case KeyUsername:
			if result.Username != v1 {
				t.Fatalf("Username = %q, want %q", result.Username, v1)
			}
		case KeyPassword:
			if result.Password != v1 {
				t.Fatalf("Password = %q, want %q", result.Password, v1)
			}
		case KeyPasskey:
			if result.Passkey != v1 {
				t.Fatalf("Passkey = %q, want %q", result.Passkey, v1)
			}
		case KeyToken:
			if result.Token != v1 {
				t.Fatalf("Token = %q, want %q", result.Token, v1)
			}
		}
	})
}

// FuzzSettingInt exercises SettingInt with arbitrary map values, verifying
// it never panics and returns the default for unrecognized types.
func FuzzSettingInt(f *testing.F) {
	f.Add("key", int64(42), "strval", 99)
	f.Add("", int64(0), "", 0)
	f.Add("k", int64(math.MinInt64), "abc", -1)
	f.Add("k", int64(math.MaxInt64), "999", 7)

	f.Fuzz(func(t *testing.T, key string, intVal int64, strVal string, def int) {
		// Test with int64 value.
		m := map[string]any{key: intVal}
		got := SettingInt(m, SettingKey(key), def)

		// If intVal fits in int, result must equal it.
		if intVal >= math.MinInt && intVal <= math.MaxInt {
			if got != int(intVal) {
				t.Fatalf("SettingInt(int64(%d)) = %d, want %d", intVal, got, int(intVal))
			}
		} else {
			// Overflow: must return default.
			if got != def {
				t.Fatalf("SettingInt(int64(%d)) overflow = %d, want default %d", intVal, got, def)
			}
		}

		// Test with string value.
		m2 := map[string]any{key: strVal}
		_ = SettingInt(m2, SettingKey(key), def)
	})
}

// FuzzSettingFloat exercises SettingFloat with arbitrary inputs.
func FuzzSettingFloat(f *testing.F) {
	f.Add("key", 1.5, "3.14", 0.0)
	f.Add("", 0.0, "", -1.0)
	f.Add("k", math.Inf(1), "NaN", 99.9)
	f.Add("k", math.NaN(), "-inf", 0.5)

	f.Fuzz(func(t *testing.T, key string, floatVal float64, strVal string, def float64) {
		m := map[string]any{key: floatVal}
		got := SettingFloat(m, SettingKey(key), def)

		// Native float64 must be returned as-is (including NaN/Inf).
		if math.IsNaN(floatVal) {
			if !math.IsNaN(got) {
				t.Fatalf("SettingFloat(NaN) = %v, want NaN", got)
			}
		} else if got != floatVal {
			t.Fatalf("SettingFloat(%v) = %v, want %v", floatVal, got, floatVal)
		}

		// Test with string value — must not panic.
		m2 := map[string]any{key: strVal}
		_ = SettingFloat(m2, SettingKey(key), def)
	})
}
