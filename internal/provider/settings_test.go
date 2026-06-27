package provider

import (
	"math"
	"testing"
)

func TestSettingBool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings map[string]any
		key      SettingKey
		def      bool
		want     bool
	}{
		{"native true", map[string]any{"k": true}, "k", false, true},
		{"native false", map[string]any{"k": false}, "k", true, false},
		{"string true", map[string]any{"k": "true"}, "k", false, true},
		{"string false", map[string]any{"k": "false"}, "k", true, false},
		{"missing key returns default true", map[string]any{}, "k", true, true},
		{"missing key returns default false", map[string]any{}, "k", false, false},
		{"nil map returns default", nil, "k", true, true},
		{"non-bool non-string returns default", map[string]any{"k": 42}, "k", true, true},
		{"string yes is not true", map[string]any{"k": "yes"}, "k", false, false},
		{"unrecognized string returns default", map[string]any{"k": "yes"}, "k", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SettingBool(tt.settings, tt.key, tt.def)
			if got != tt.want {
				t.Errorf("SettingBool(%v, %q, %v) = %v, want %v",
					tt.settings, tt.key, tt.def, got, tt.want)
			}
		})
	}
}

func TestSettingString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings map[string]any
		key      SettingKey
		want     string
	}{
		{"present", map[string]any{"k": "val"}, "k", "val"},
		{"empty string", map[string]any{"k": ""}, "k", ""},
		{"missing key", map[string]any{}, "k", ""},
		{"nil map", nil, "k", ""},
		{"non-string value", map[string]any{"k": 42}, "k", ""},
		{"bool value", map[string]any{"k": true}, "k", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SettingString(tt.settings, tt.key)
			if got != tt.want {
				t.Errorf("SettingString(%v, %q) = %q, want %q",
					tt.settings, tt.key, got, tt.want)
			}
		})
	}
}

func TestSettingInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings map[string]any
		key      SettingKey
		def      int
		want     int
	}{
		{"native int", map[string]any{"k": 42}, "k", 0, 42},
		{"native int64", map[string]any{"k": int64(7)}, "k", 0, 7},
		{"native float64 whole", map[string]any{"k": 3.0}, "k", 0, 3},
		{"native float64 non-whole returns default", map[string]any{"k": 3.5}, "k", 99, 99},
		{"numeric string", map[string]any{"k": "12"}, "k", 0, 12},
		{"negative numeric string accepted", map[string]any{"k": "-5"}, "k", 0, -5},
		{"non-numeric string returns default", map[string]any{"k": "abc"}, "k", 7, 7},
		{"missing key returns default", map[string]any{}, "k", 99, 99},
		{"nil map returns default", nil, "k", 5, 5},
		{"bool returns default", map[string]any{"k": true}, "k", 3, 3},
		// Float boundaries: float64(math.MaxInt) rounds up to 2^63, which is
		// not representable as an int, so it must fall back to the default;
		// float64(math.MinInt) == -2^63 is exactly representable and valid.
		{"float64 math.MaxInt is unrepresentable, returns default", map[string]any{"k": float64(math.MaxInt)}, "k", 7, 7},
		{"float64 math.MinInt is representable", map[string]any{"k": float64(math.MinInt)}, "k", 777, math.MinInt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SettingInt(tt.settings, tt.key, tt.def)
			if got != tt.want {
				t.Errorf("SettingInt(%v, %q, %d) = %d, want %d",
					tt.settings, tt.key, tt.def, got, tt.want)
			}
		})
	}
}

func TestSettingFloat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings map[string]any
		key      SettingKey
		def      float64
		want     float64
	}{
		{"native float64", map[string]any{"k": 1.5}, "k", 0, 1.5},
		{"native int promoted", map[string]any{"k": 4}, "k", 0, 4.0},
		{"native int64 promoted", map[string]any{"k": int64(7)}, "k", 0, 7.0},
		{"numeric string", map[string]any{"k": "2.5"}, "k", 0, 2.5},
		{"non-numeric string returns default", map[string]any{"k": "abc"}, "k", 1.0, 1.0},
		{"missing key returns default", map[string]any{}, "k", 0.75, 0.75},
		{"nil map returns default", nil, "k", 0.1, 0.1},
		{"bool returns default", map[string]any{"k": true}, "k", 0.5, 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SettingFloat(tt.settings, tt.key, tt.def)
			if got != tt.want {
				t.Errorf("SettingFloat(%v, %q, %v) = %v, want %v",
					tt.settings, tt.key, tt.def, got, tt.want)
			}
		})
	}
}
