package api

import "time"

// --- Provider errors ---

// AuthError indicates invalid or expired credentials.
type AuthError struct{ Msg string }

func (e *AuthError) Error() string { return e.Msg }

// RateLimitError indicates the provider's rate limit was exceeded.
// RetryAfter, when non-zero, is the hint from the upstream's Retry-After
// header (delta-seconds or HTTP-date resolved to a positive duration).
// Consumers may use it to schedule the next attempt; a zero value means
// no hint was provided.
type RateLimitError struct {
	Msg        string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return e.Msg }

// TimeoutStatus is the state of a single provider's timeout.
type TimeoutStatus struct {
	LastError         string        `json:"last_error,omitempty"`
	CooldownRemaining time.Duration `json:"cooldown_remaining,omitempty"`
	RecentFailures    int           `json:"recent_failures"`
	Threshold         int           `json:"threshold"`
	TimedOut          bool          `json:"timed_out"`
}

// --- Config types (canonical, moved from config package) ---

// SubtitleTarget defines a single subtitle to search for.
type SubtitleTarget struct {
	MinScore  *int
	Code      string
	Variant   Variant
	Variants  []string
	Providers []ProviderID
	Exclude   []ProviderID
}

// Variant is a typed string enum for subtitle variant identifiers.
// Valid variants: standard (default), hi (hearing impaired), forced.
type Variant string

// Subtitle variant identifiers. Shared across api, search, server, and cli
// so the auto and manual download paths agree on a single vocabulary.
const (
	VariantStandard Variant = "standard"
	DefaultVariant          = VariantStandard
	VariantHI       Variant = "hi"
	VariantForced   Variant = "forced"

	// VariantAliasSDH is the alternative name for HI subtitles used in external
	// subtitle filenames and provider metadata.
	VariantAliasSDH = "sdh"
	// VariantAliasForeign is the alternative name for forced subtitles used in
	// external subtitle filenames and provider metadata.
	VariantAliasForeign = "foreign"
)

// VariantFromFlags derives the variant from HI/forced flags on a picked
// subtitle. HI wins over forced when both happen to be set.
func VariantFromFlags(hi, forced bool) Variant {
	switch {
	case hi:
		return VariantHI
	case forced:
		return VariantForced
	default:
		return DefaultVariant
	}
}

// EffectiveVariant returns the variant, defaulting to DefaultVariant.
func (t *SubtitleTarget) EffectiveVariant() Variant {
	if t.Variant == "" {
		return DefaultVariant
	}
	return t.Variant
}

// ArrConfig holds Sonarr or Radarr connection details.
type ArrConfig struct {
	URL       string
	APIKey    string
	PublicURL string
}

// ProviderCfg is a generic provider configuration block.
type ProviderCfg struct {
	Settings map[string]any
	Priority int // Lower = higher trust. 0 means unset (defaults to 99).
	Enabled  bool
}

// SearchConfig controls search behavior.
type SearchConfig struct {
	ExcludeArrTags         []string
	ScanInterval           time.Duration
	ProviderTimeout        time.Duration
	ScanDelay              time.Duration
	MinScore               int
	UpgradeWindowDays      int
	DownloadMaxAttempts    int // Max download attempts per search (0 = default DefaultDownloadMaxAttempts)
	MaxProviderConcurrency int // Max parallel provider searches (0 = default DefaultProviderConcurrency)
	MaxSSEClients          int // Max concurrent SSE connections (0 = default 32)
	UpgradeEnabled         bool
}

// DefaultDownloadMaxAttempts is the fallback number of download attempts
// per search when the config value is zero.
const DefaultDownloadMaxAttempts = 3

// DefaultProviderPriority is used when no priority is configured for a provider.
const DefaultProviderPriority = 99

// DefaultProviderConcurrency is the default maximum number of parallel
// provider searches when the config value is zero.
const DefaultProviderConcurrency = 4

// DefaultManualProviderTimeout is the per-provider search timeout for
// CLI and manual searches. Shared between clisearch and manualops to
// prevent silent divergence.
const DefaultManualProviderTimeout = 30 * time.Second

// MaxSafeFileBytes is the maximum file size (10 MB) for config files,
// subtitle files, and other user-supplied payloads. Prevents OOM from
// oversized or malicious inputs. Shared across config, subsync, and
// provider packages.
const MaxSafeFileBytes = 10 << 20

// PostProcessConfig controls subtitle post-processing.
type PostProcessConfig struct {
	StripHI          bool `json:"strip_hi"`
	StripTags        bool `json:"strip_tags"`
	NormalizeUTF8    bool `json:"normalize_utf8"`
	CleanWhitespace  bool `json:"clean_whitespace"`
	NormalizeEndings bool `json:"normalize_endings"`
	RemoveEmpty      bool `json:"remove_empty"`
}

// SyncConfig controls subtitle synchronization on import.
type SyncConfig struct {
	// SyncSubtitles enables automatic timing sync against embedded
	// reference subtitles when a new subtitle is downloaded.
	SyncSubtitles bool `json:"sync_subtitles"`
	// AudioSyncFallback enables audio-based sync as a fallback when
	// no embedded reference subtitle is available or sync fails.
	AudioSyncFallback bool `json:"audio_sync_fallback"`
	// SyncMinConfidence is the minimum confidence threshold (0.0–1.0) for
	// accepting an automatic sync result. Defaults to DefaultSyncMinConfidence when zero.
	SyncMinConfidence float64 `json:"sync_min_confidence,omitempty"`
}

// DefaultSyncMinConfidence is the default minimum confidence for auto-sync
// to be applied. Used by config and syncing packages.
const DefaultSyncMinConfidence = 0.6

// AdaptiveConfig controls adaptive search backoff.
type AdaptiveConfig struct {
	InitialDelay      time.Duration
	MaxDelay          time.Duration
	BackoffMultiplier float64
	MaxAttempts       int
	Enabled           bool
}

// --- Provider schema ---

// ProviderSchemaField describes a single provider setting for the UI schema.
type ProviderSchemaField struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Type    string `json:"type"` // text, secret, bool
	Default string `json:"default,omitempty"`
	Help    string `json:"help,omitempty"`
	Secret  bool   `json:"secret,omitempty"`
}

// --- Schema types ---

// SchemaField describes a single configuration field for the UI.
type SchemaField struct {
	Key         string         `json:"key"`
	Label       string         `json:"label"`
	Type        string         `json:"type"`
	Default     string         `json:"default,omitempty"`
	Help        string         `json:"help,omitempty"`
	Placeholder string         `json:"placeholder,omitempty"`
	Min         string         `json:"min,omitempty"`
	Max         string         `json:"max,omitempty"`
	ShowWhen    string         `json:"show_when,omitempty"`
	Requires    string         `json:"requires,omitempty"`
	Group       string         `json:"group,omitempty"`
	Fields      []SchemaField  `json:"fields,omitempty"`
	Options     []SchemaOption `json:"options,omitempty"`
	Secret      bool           `json:"secret,omitempty"`
	Required    bool           `json:"required,omitempty"`
}

// SchemaOption is a value+label pair for select fields.
type SchemaOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// SchemaSection describes a top-level config section.
type SchemaSection struct {
	Key              string           `json:"key"`
	Title            string           `json:"title"`
	Type             string           `json:"type"`
	Help             string           `json:"help,omitempty"`
	RequiredGroup    string           `json:"required_group,omitempty"`
	EnableKey        string           `json:"enable_key,omitempty"`
	Fields           []SchemaField    `json:"fields,omitempty"`
	ProviderTemplate []SchemaField    `json:"provider_template,omitempty"`
	Providers        []ProviderSchema `json:"providers,omitempty"`
}

// ProviderSchema describes a single provider's settings fields.
type ProviderSchema struct {
	Name          string        `json:"name"`
	Label         string        `json:"label"`
	Settings      []SchemaField `json:"settings,omitempty"`
	AlwaysEnabled bool          `json:"always_enabled,omitempty"`
}

// --- Language rules (JSON-serializable for settings UI) ---

// LanguageRulesJSON is the language rules in a JSON-serializable format
// for the settings UI.
type LanguageRulesJSON struct {
	Rules   []AudioRuleJSON    `json:"rules,omitempty"`
	Default []SubtitleTargJSON `json:"default,omitempty"`
}

// AudioRuleJSON is a JSON-friendly audio rule.
type AudioRuleJSON struct {
	Audio     string             `json:"audio"`
	Subtitles []SubtitleTargJSON `json:"subtitles"`
}

// SubtitleTargJSON is a JSON-friendly subtitle target.
type SubtitleTargJSON struct {
	MinScore  *int     `json:"min_score,omitempty"`
	Code      string   `json:"code"`
	Variant   string   `json:"variant,omitempty"`
	Variants  []string `json:"variants,omitempty"`
	Providers []string `json:"providers,omitempty"`
	Exclude   []string `json:"exclude,omitempty"`
}
