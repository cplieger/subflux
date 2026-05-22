package schema

import (
	"testing"
	"time"

	"subflux/internal/api"
	"subflux/internal/config"
	"subflux/internal/config/defaults"
)

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		want  string
		input time.Duration
	}{
		{"zero", "0s", 0},
		{"seconds", "45s", 45 * time.Second},
		{"minutes", "5m", 5 * time.Minute},
		{"hours", "3h", 3 * time.Hour},
		{"one day", "1D", 24 * time.Hour},
		{"seven days", "7D", 7 * 24 * time.Hour},
		{"thirty days", "30D", 30 * 24 * time.Hour},
		{"ninety days", "90D", 90 * 24 * time.Hour},
		{"one month (730h)", "1M", 730 * time.Hour},
		{"three months (2190h)", "3M", 3 * 730 * time.Hour},
		{"one second", "1s", time.Second},
		{"one minute", "1m", time.Minute},
		{"one hour", "1h", time.Hour},
		{"two days", "2D", 48 * time.Hour},
		{"sixty days", "60D", 60 * 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := defaults.FormatDuration(tt.input)
			if got != tt.want {
				t.Errorf("defaults.FormatDuration(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatDuration_round_trip_with_ParseDuration(t *testing.T) {
	t.Parallel()
	// FormatDuration output should be parseable by config.ParseDuration and
	// produce the same duration (round-trip for clean durations).
	tests := []time.Duration{
		0,
		time.Second,
		30 * time.Second,
		time.Minute,
		5 * time.Minute,
		time.Hour,
		3 * time.Hour,
		24 * time.Hour,
		7 * 24 * time.Hour,
		730 * time.Hour,
		3 * 730 * time.Hour,
	}
	for _, d := range tests {
		t.Run(d.String(), func(t *testing.T) {
			t.Parallel()
			formatted := defaults.FormatDuration(d)
			parsed, err := config.ParseDuration(formatted)
			if err != nil {
				t.Fatalf("ParseDuration(defaults.FormatDuration(%v) = %q) error: %v", d, formatted, err)
			}
			if parsed != d {
				t.Errorf("round trip failed: %v -> %q -> %v", d, formatted, parsed)
			}
		})
	}
}

func TestFormatDuration_non_round_durations_fall_to_seconds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		want  string
		input time.Duration
	}{
		{"90 seconds", "90s", 90 * time.Second},
		{"sub-minute with millis", "1s", 1500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := defaults.FormatDuration(tt.input)
			if got != tt.want {
				t.Errorf("defaults.FormatDuration(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSchema_returns_all_sections(t *testing.T) {
	t.Parallel()
	sections := Schema(nil)

	wantKeys := []string{
		"sonarr", "radarr", "media_roots", "poll_interval",
		"languages", "providers", "search", "adaptive",
		"post_processing", "auth", "scoring", "logging",
	}
	if len(sections) != len(wantKeys) {
		t.Fatalf("Schema() returned %d sections, want %d", len(sections), len(wantKeys))
	}
	for i, want := range wantKeys {
		if sections[i].Key != want {
			t.Errorf("Schema()[%d].Key = %q, want %q", i, sections[i].Key, want)
		}
	}
}

func TestSchema_sonarr_section_has_required_fields(t *testing.T) {
	t.Parallel()
	sections := Schema(nil)
	sonarr := sections[0]
	if sonarr.Key != "sonarr" {
		t.Fatalf("sections[0].Key = %q, want sonarr", sonarr.Key)
	}
	if sonarr.EnableKey != "enabled" {
		t.Errorf("sonarr.EnableKey = %q, want %q", sonarr.EnableKey, "enabled")
	}
	if sonarr.RequiredGroup != "arr" {
		t.Errorf("sonarr.RequiredGroup = %q, want %q", sonarr.RequiredGroup, "arr")
	}
	if len(sonarr.Fields) != 3 {
		t.Errorf("sonarr has %d fields, want 3 (url, api_key, public_url)", len(sonarr.Fields))
	}
	if !sonarr.Fields[0].Required {
		t.Error("sonarr url field should be required")
	}
	if !sonarr.Fields[1].Required {
		t.Error("sonarr api_key field should be required")
	}
	if sonarr.Fields[1].Type != "secret" {
		t.Errorf("sonarr api_key field Type = %q, want %q", sonarr.Fields[1].Type, "secret")
	}
}

func TestSchema_providers_section_passes_through(t *testing.T) {
	t.Parallel()
	providers := []api.ProviderSchema{
		{Name: "opensubtitles", Label: "OpenSubtitles"},
		{Name: "yify", Label: "YIFY"},
	}
	sections := Schema(providers)

	var provSection *api.SchemaSection
	for i := range sections {
		if sections[i].Key == "providers" {
			provSection = &sections[i]
			break
		}
	}
	if provSection == nil {
		t.Fatal("Schema() missing providers section")
	}
	if len(provSection.Providers) != 2 {
		t.Errorf("providers section has %d providers, want 2", len(provSection.Providers))
	}
	if provSection.Providers[0].Name != "opensubtitles" {
		t.Errorf("providers[0].Name = %q, want %q", provSection.Providers[0].Name, "opensubtitles")
	}
}
