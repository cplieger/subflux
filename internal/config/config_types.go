package config

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/cplieger/slogx"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config/defaults"
	"go.yaml.in/yaml/v3"
)

// Default config string constants.
const (
	defaultExcludeTag = defaults.ExcludeTag
	defaultLogLevel   = defaults.LogLevel
	defaultLogFormat  = defaults.LogFormat
)

// Duration wraps time.Duration with extended YAML parsing that supports
// day (D), month (M), and year (Y) suffixes in addition to Go's standard
// duration units (ns, us, ms, s, m, h). Only single-unit values are
// supported for extended units (e.g. "7D", "3M", "1Y").
//
// Conversions: 1D = 24h, 1M = 730h (30.4 days), 1Y = 8760h (365 days).
type Duration struct {
	D time.Duration
}

// UnmarshalYAML parses a duration string, extending time.ParseDuration
// with D (days), M (months), and Y (years) suffixes.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q: %w", value.Line, s, err)
	}
	d.D = parsed
	return nil
}

// Config is the top-level configuration.
type Config struct {
	// ruleIndex maps audio language code to its index in Languages.Rules for O(1) lookup.
	ruleIndex map[string]int
	Providers map[api.ProviderID]yamlProviderCfg `yaml:"providers"`
	Scoring   ScoringConfig                      `yaml:"scoring"`
	// cachedRuleTargets maps audio language to pre-computed []api.SubtitleTarget.
	cachedRuleTargets map[string][]api.SubtitleTarget
	// cachedProviderConfigs is the pre-computed result of ProviderConfigs().
	cachedProviderConfigs map[api.ProviderID]api.ProviderCfg
	Sonarr                yamlArrConfig `yaml:"sonarr"`
	Radarr                yamlArrConfig `yaml:"radarr"`
	Logging               LoggingConfig `yaml:"logging"`
	Languages             LanguageRules `yaml:"languages"`
	// cachedLangCodes is the pre-computed result of LanguageCodes().
	cachedLangCodes []string
	MediaRootDirs   []string `yaml:"media_roots"`
	// TrustedProxies lists reverse-proxy CIDR ranges (or single IPs as /32)
	// whose X-Forwarded-For may be trusted for client-IP resolution. Empty
	// (the default) trusts nothing: the socket peer is used and XFF ignored.
	TrustedProxies []string `yaml:"trusted_proxies"`
	// cachedDefaultTargets is the pre-computed default targets.
	cachedDefaultTargets []api.SubtitleTarget
	// cachedRoots holds pre-opened *os.Root handles for media_roots,
	// eliminating per-request OpenRoot syscalls.
	cachedRoots []*os.Root
	// cachedTrustedProxies is the parsed form of TrustedProxies, built at
	// load/hot-reload and consumed by the client-IP resolver.
	cachedTrustedProxies []*net.IPNet
	Auth                 yamlAuthConfig        `yaml:"auth"`
	Backup               yamlBackupConfig      `yaml:"backup"`
	SearchCfg            yamlSearchConfig      `yaml:"search"`
	AdaptiveCfg          yamlAdaptiveConfig    `yaml:"adaptive"`
	PostProcessing       yamlPostProcessConfig `yaml:"post_processing"`
	PollIntervalCfg      Duration              `yaml:"poll_interval"`
}

// LanguageRules maps detected audio languages to desired subtitle downloads.
type LanguageRules struct {
	// Rules maps an audio language (ISO 639-1) to subtitle targets.
	// Example: audio "en" -> download "fr" normal + "fr" forced
	Rules []AudioRule `yaml:"rules"`

	// Default applies when the audio language doesn't match any rule.
	// If empty, no subtitles are downloaded for unmatched audio.
	Default []yamlSubtitleTarget `yaml:"default,omitempty"`
}

// AudioRule maps a detected audio language to subtitle targets.
type AudioRule struct {
	Audio     string               `yaml:"audio"`     // ISO 639-1 audio to match
	Subtitles []yamlSubtitleTarget `yaml:"subtitles"` // What to download
}

// yamlSubtitleTarget is the internal yaml-tagged version of api.SubtitleTarget.
type yamlSubtitleTarget struct {
	MinScore  *int             `yaml:"min_score,omitempty"`
	Code      string           `yaml:"code"`
	Variant   string           `yaml:"variant,omitempty"`
	Variants  []string         `yaml:"variants,omitempty"`
	Providers []api.ProviderID `yaml:"providers,omitempty"`
	Exclude   []api.ProviderID `yaml:"exclude,omitempty"`
}

// toAPI converts a yamlSubtitleTarget to an api.SubtitleTarget.
func (t *yamlSubtitleTarget) toAPI() api.SubtitleTarget {
	return api.SubtitleTarget{
		MinScore:  t.MinScore,
		Code:      t.Code,
		Variant:   api.Variant(t.Variant),
		Variants:  t.Variants,
		Providers: t.Providers,
		Exclude:   t.Exclude,
	}
}

// targetsToAPI converts a slice of yamlSubtitleTarget to api.SubtitleTarget.
func targetsToAPI(targets []yamlSubtitleTarget) []api.SubtitleTarget {
	if targets == nil {
		return nil
	}
	out := make([]api.SubtitleTarget, len(targets))
	for i := range targets {
		out[i] = targets[i].toAPI()
	}
	return out
}

// yamlArrConfig holds Sonarr or Radarr connection details (yaml-tagged).
type yamlArrConfig struct {
	Enabled   *bool  `yaml:"enabled,omitempty"`
	URL       string `yaml:"url"`
	APIKey    string `yaml:"api_key"`
	PublicURL string `yaml:"public_url"`
}

// isEnabled returns whether this arr config is active.
// nil (omitted) defaults to true for backward compatibility.
func (c *yamlArrConfig) isEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// yamlProviderCfg is a generic provider configuration block (yaml-tagged).
type yamlProviderCfg struct {
	Settings map[string]any `yaml:"settings"`
	Enabled  bool           `yaml:"enabled"`
	Priority int            `yaml:"priority"` // Lower = higher trust (default 99).
}

// yamlSearchConfig controls search behavior (yaml-tagged).
type yamlSearchConfig struct {
	ExcludeArrTags         []string `yaml:"exclude_arr_tags"`
	ScanInterval           Duration `yaml:"scan_interval"`
	ProviderTimeout        Duration `yaml:"provider_timeout"`
	ScanDelay              Duration `yaml:"scan_delay"`
	MinScore               int      `yaml:"min_score"`
	UpgradeWindowDays      int      `yaml:"upgrade_window_days"`
	DownloadMaxAttempts    int      `yaml:"download_max_attempts"`
	MaxProviderConcurrency int      `yaml:"max_provider_concurrency"`
	MaxSSEClients          int      `yaml:"max_sse_clients"`
	UpgradeEnabled         bool     `yaml:"upgrade_enabled"`
}

// ScoringConfig allows users to customize scoring weights.
type ScoringConfig struct {
	Weights *api.Scores `yaml:"weights,omitempty"`
}

// yamlAdaptiveConfig controls adaptive search backoff (yaml-tagged).
type yamlAdaptiveConfig struct {
	Enabled           bool     `yaml:"enabled"`
	InitialDelay      Duration `yaml:"initial_delay"`
	MaxDelay          Duration `yaml:"max_delay"`
	BackoffMultiplier float64  `yaml:"backoff_multiplier"`
	MaxAttempts       int      `yaml:"max_attempts"` // 0 = search forever
}

// LogLevel is a typed string for log verbosity levels.
type LogLevel = api.LogLevel

// LogLevel constants for the supported log verbosity levels.
const (
	LogLevelError LogLevel = "error"
	LogLevelWarn  LogLevel = "warn"
	LogLevelInfo  LogLevel = "info"
	LogLevelDebug LogLevel = "debug"
)

// ValidLogLevel returns true if the level is a recognized value.
func ValidLogLevel(l LogLevel) bool {
	switch l {
	case LogLevelError, LogLevelWarn, LogLevelInfo, LogLevelDebug:
		return true
	}
	return false
}

// LogFormat is a typed string for log output formats.
type LogFormat = api.LogFormat

// LogFormat constants for the supported log output formats.
const (
	LogFormatJSON LogFormat = "json"
	LogFormatText LogFormat = "text"
)

// ValidLogFormat returns true if the format is a recognized value, judged by
// slogx.ParseFormat — the same case-insensitive, trimming normalization
// setupLogging applies when it consumes the value — so validation and
// consumption cannot drift. The empty string is "unset" (the caller falls back
// to the default), not a valid value.
func ValidLogFormat(f LogFormat) bool {
	if strings.TrimSpace(string(f)) == "" {
		return false
	}
	_, ok := slogx.ParseFormat(string(f), slogx.JSON)
	return ok
}

// LoggingConfig controls log output.
type LoggingConfig struct {
	Level  LogLevel  `yaml:"level"`
	Format LogFormat `yaml:"format"`
}

// yamlPostProcessConfig controls subtitle post-processing (yaml-tagged).
type yamlPostProcessConfig struct {
	StripHI           bool    `yaml:"strip_hi"`
	StripTags         bool    `yaml:"strip_tags"`
	NormalizeUTF8     bool    `yaml:"normalize_utf8"`
	CleanWhitespace   bool    `yaml:"clean_whitespace"`
	NormalizeEndings  bool    `yaml:"normalize_endings"`
	RemoveEmpty       bool    `yaml:"remove_empty"`
	SyncSubtitles     bool    `yaml:"sync_subtitles"`
	AudioSyncFallback bool    `yaml:"audio_sync_fallback"`
	SyncMinConfidence float64 `yaml:"sync_min_confidence"`
}

// yamlOIDCConfig holds OIDC provider connection details (yaml-tagged).
type yamlOIDCConfig struct {
	IssuerURL    string `yaml:"issuer_url"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RedirectURI  string `yaml:"redirect_uri"`
}

// yamlBackupConfig controls scheduled SQLite backups (yaml-tagged).
type yamlBackupConfig struct {
	Path      string   `yaml:"path"`
	Frequency Duration `yaml:"frequency"`
	Retention int      `yaml:"retention"`
	Enabled   bool     `yaml:"enabled"`
}

// yamlAuthConfig holds authentication settings (yaml-tagged).
type yamlAuthConfig struct {
	BasicEnabled     *bool          `yaml:"basic_enabled,omitempty"`
	CheckBreached    *bool          `yaml:"check_breached_passwords,omitempty"`
	OIDC             yamlOIDCConfig `yaml:"oidc"`
	WebAuthnRPID     string         `yaml:"webauthn_rp_id"`
	SessionIdle      Duration       `yaml:"session_idle_timeout"`
	SessionAbsolute  Duration       `yaml:"session_absolute_timeout"`
	OIDCEnabled      bool           `yaml:"oidc_enabled"`
	OIDCAutoRedirect bool           `yaml:"oidc_auto_redirect"`
	DisableAuth      bool           `yaml:"disable_auth"`
}

// ServerPort is the fixed HTTP server port.
const ServerPort = 8374

// Validator is satisfied by types that can self-validate after loading.
type Validator interface {
	Validate() error
}

// Compile-time assertion: *Config satisfies Validator.
var _ Validator = (*Config)(nil)
