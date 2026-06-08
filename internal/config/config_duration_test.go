package config

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/config/defaults"
	"pgregory.net/rapid"
)

// --- ParseDuration ---

func TestParseDuration_standard_units(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"hours", "1h", time.Hour},
		{"minutes", "30m", 30 * time.Minute},
		{"milliseconds", "500ms", 500 * time.Millisecond},
		{"compound", "1h30m", 90 * time.Minute},
		{"large hours", "168h", 168 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDuration(tt.input)
			if err != nil {
				t.Fatalf("ParseDuration(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDuration_days(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"one day", "1D", 24 * time.Hour},
		{"seven days", "7D", 7 * 24 * time.Hour},
		{"half day", "0.5D", 12 * time.Hour},
		{"thirty days", "30D", 30 * 24 * time.Hour},
		{"zero days", "0D", 0},
		{"zero months", "0M", 0},
		{"zero years", "0Y", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDuration(tt.input)
			if err != nil {
				t.Fatalf("ParseDuration(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDuration_months(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"one month", "1M", 730 * time.Hour},
		{"three months", "3M", 3 * 730 * time.Hour},
		{"half month", "0.5M", 365 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDuration(tt.input)
			if err != nil {
				t.Fatalf("ParseDuration(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDuration_years(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"one year", "1Y", 8760 * time.Hour},
		{"two years", "2Y", 2 * 8760 * time.Hour},
		{"half year", "0.5Y", 4380 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDuration(tt.input)
			if err != nil {
				t.Fatalf("ParseDuration(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDuration_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"bare D suffix", "D"},
		{"bare M suffix", "M"},
		{"bare Y suffix", "Y"},
		{"non-numeric day", "abcD"},
		{"non-numeric month", "abcM"},
		{"non-numeric year", "abcY"},
		{"NaN day", "NaND"},
		{"Inf month", "InfM"},
		{"+Inf year", "+InfY"},
		{"-Inf year", "-InfY"},
		{"negative day", "-1D"},
		{"negative month", "-3M"},
		{"negative year", "-2Y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseDuration(tt.input)
			if err == nil {
				t.Errorf("ParseDuration(%q) expected error", tt.input)
			}
		})
	}
}

func TestParseDuration_overflow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{"years overflow", "300Y"},
		{"months overflow", "4000M"},
		{"days overflow", "110000D"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseDuration(tt.input)
			if err == nil {
				t.Errorf("ParseDuration(%q) expected overflow error", tt.input)
			}
		})
	}
}

func TestParseDuration_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Float64Range(0.001, 100).Draw(t, "n")
		suffix := rapid.SampledFrom([]string{"D", "M", "Y"}).Draw(t, "suffix")
		input := strconv.FormatFloat(n, 'f', 3, 64) + suffix
		d, err := ParseDuration(input)
		if err != nil {
			t.Fatalf("ParseDuration(%q) error: %v", input, err)
		}
		if d <= 0 {
			t.Fatalf("ParseDuration(%q) = %v, want positive", input, d)
		}
	})
}

func TestDuration_YAML_unmarshal(t *testing.T) {
	t.Parallel()
	data := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  opensubtitles:
    enabled: true
    settings:
      api_key: "test"
adaptive:
  initial_delay: 7D
  max_delay: 3M
search:
  provider_timeout: 2h
`
	cfg, err := LoadFromBytes(context.Background(), []byte(data))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if cfg.AdaptiveCfg.InitialDelay.D != 7*24*time.Hour {
		t.Errorf("InitialDelay = %v, want 168h", cfg.AdaptiveCfg.InitialDelay.D)
	}
	if cfg.AdaptiveCfg.MaxDelay.D != 3*730*time.Hour {
		t.Errorf("MaxDelay = %v, want 2190h", cfg.AdaptiveCfg.MaxDelay.D)
	}
	if cfg.SearchCfg.ProviderTimeout.D != 2*time.Hour {
		t.Errorf("ProviderTimeout = %v, want 2h", cfg.SearchCfg.ProviderTimeout.D)
	}
}

func TestParseDuration_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"standard seconds", "30s", 30 * time.Second, false},
		{"standard minutes", "5m", 5 * time.Minute, false},
		{"standard hours", "2h", 2 * time.Hour, false},
		{"days suffix", "7D", 7 * 24 * time.Hour, false},
		{"months suffix", "3M", 3 * 730 * time.Hour, false},
		{"years suffix", "1Y", 8760 * time.Hour, false},
		{"fractional days", "1.5D", time.Duration(float64(24*time.Hour) * 1.5), false},
		{"empty string", "", 0, true},
		{"invalid string", "abc", 0, true},
		{"zero", "0s", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDuration(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDuration(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- FormatDuration/ParseDuration round-trip property ---

func TestFormatDuration_ParseDuration_roundtrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Generate durations that are exact multiples of their unit so
		// FormatDuration produces a clean representation.
		unit := rapid.SampledFrom([]time.Duration{
			time.Second,
			time.Minute,
			time.Hour,
			24 * time.Hour,  // day
			730 * time.Hour, // month
		}).Draw(t, "unit")
		n := rapid.IntRange(1, 500).Draw(t, "n")
		d := time.Duration(n) * unit

		formatted := defaults.FormatDuration(d)
		parsed, err := ParseDuration(formatted)
		if err != nil {
			t.Fatalf("ParseDuration(%q) error: %v (original: %v)", formatted, err, d)
		}
		if parsed != d {
			t.Fatalf("round-trip failed: FormatDuration(%v) = %q, ParseDuration(%q) = %v", d, formatted, formatted, parsed)
		}
	})
}

// --- Coverage gap: UnmarshalYAML error paths ---

func TestDuration_YAML_unmarshal_invalid_duration_string(t *testing.T) {
	t.Parallel()
	data := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  os:
    enabled: true
search:
  provider_timeout: "not_a_duration"
`
	_, err := LoadFromBytes(context.Background(), []byte(data))
	if err == nil {
		t.Fatal("LoadFromBytes() expected error for invalid duration string")
	}
	if !strings.Contains(err.Error(), "invalid duration") {
		t.Errorf("error = %q, want 'invalid duration'", err)
	}
}

func TestDuration_YAML_unmarshal_non_string_node(t *testing.T) {
	t.Parallel()
	data := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  os:
    enabled: true
search:
  provider_timeout:
    nested: value
`
	_, err := LoadFromBytes(context.Background(), []byte(data))
	if err == nil {
		t.Fatal("LoadFromBytes() expected error for non-string duration node")
	}
}
