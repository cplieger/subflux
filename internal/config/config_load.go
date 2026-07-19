package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/envx/yamlenv"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config/defaults"
	"go.yaml.in/yaml/v3"
)

// Compile-time assertion: *Config satisfies api.ConfigProvider.
var _ api.ConfigProvider = (*Config)(nil)

// ParseDuration extends time.ParseDuration with D, M, and Y suffixes.
func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, errors.New("empty duration")
	}
	last := s[len(s)-1]
	switch last {
	case 'D':
		return parseMul(s, 24*time.Hour, "day")
	case 'M':
		return parseMul(s, 730*time.Hour, "month")
	case 'Y':
		return parseMul(s, 8760*time.Hour, "year")
	default:
		return time.ParseDuration(s)
	}
}

// maxDurationFloat is the largest float64 that fits in time.Duration without overflow.
const maxDurationFloat = float64(1<<63 - 1)

// parseMul parses a numeric string with a single-char suffix removed,
// multiplies by the given unit, and rejects negative, non-finite, and
// overflowing values.
func parseMul(s string, unit time.Duration, name string) (time.Duration, error) {
	n, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s duration %q: %w", name, s, err)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("invalid %s duration %q: non-finite value", name, s)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative duration not allowed: %g %s", n, name)
	}
	ns := n * float64(unit)
	if ns > maxDurationFloat {
		return 0, fmt.Errorf("overflow: %g %s(s) exceeds maximum duration", n, name)
	}
	return time.Duration(ns), nil
}

// isAllowedEnvVar returns true if the env var name is safe to expand.
// Only SUBFLUX_* vars and common deployment vars are allowed. It is the
// allowlist policy LoadFromBytes hands to yamlenv.Expand (the shared
// post-parse, string-values-only expansion engine).
func isAllowedEnvVar(key string) bool {
	if strings.HasPrefix(key, "SUBFLUX_") {
		return true
	}
	switch key {
	case "CONFIG_ROOT", "MEDIA_FOLDER", "PUID", "PGID", "TZ",
		"LAN_IP", "HOSTNAME":
		return true
	}
	return false
}

// ErrConfigTooLarge indicates the config file or data exceeds the maximum allowed size.
var ErrConfigTooLarge = errors.New("config too large")

// ErrVariantConflict indicates both "variant" and "variants" are set on the same target.
var ErrVariantConflict = errors.New("cannot set both variant and variants")

// maxConfigSize references the shared file-size cap from api.
const maxConfigSize = api.MaxSafeFileBytes

// newWithDefaults returns a Config pre-populated with default values.
// YAML unmarshalling overlays user values on top of these defaults.
func newWithDefaults() *Config {
	return &Config{
		PollIntervalCfg: Duration{D: defaults.DefaultPollInterval},
		SearchCfg: yamlSearchConfig{
			MinScore:            0,
			ScanInterval:        Duration{D: defaults.DefaultScanInterval},
			ProviderTimeout:     Duration{D: defaults.DefaultProviderTimeout},
			ScanDelay:           Duration{D: defaults.DefaultScanDelay},
			UpgradeEnabled:      true,
			UpgradeWindowDays:   defaults.DefaultUpgradeWindowDays,
			MaxSSEClients:       defaults.DefaultMaxSSEClients,
			ExcludeArrTags:      []string{defaultExcludeTag},
			DownloadMaxAttempts: api.DefaultDownloadMaxAttempts,
		},
		AdaptiveCfg: yamlAdaptiveConfig{
			Enabled:           true,
			InitialDelay:      Duration{D: defaults.DefaultAdaptiveInitDelay},
			MaxDelay:          Duration{D: defaults.DefaultAdaptiveMaxDelay},
			BackoffMultiplier: defaults.DefaultBackoffMultiplier,
		},
		Logging: LoggingConfig{
			Level:  defaultLogLevel,
			Format: defaultLogFormat,
		},
		PostProcessing: yamlPostProcessConfig{
			StripHI:          false,
			StripTags:        true,
			NormalizeUTF8:    true,
			CleanWhitespace:  true,
			NormalizeEndings: true,
			RemoveEmpty:      true,
			SyncSubtitles:    true,
		},
		EmbeddedSubtitles: yamlEmbeddedConfig{
			IgnorePGS:    defaults.EmbeddedIgnorePGS,
			IgnoreVobSub: defaults.EmbeddedIgnoreVobSub,
			IgnoreASS:    defaults.EmbeddedIgnoreASS,
		},
	}
}

// Load reads and validates a config file. The context enables cancellation
// during file I/O and validation (e.g. media_roots stat loop).
func Load(ctx context.Context, path string) (*Config, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat config: %w", err)
	}
	if info.Size() > maxConfigSize {
		return nil, fmt.Errorf("config file %w: %d bytes (max %d)", ErrConfigTooLarge, info.Size(), maxConfigSize)
	}

	data, err := io.ReadAll(io.LimitReader(f, maxConfigSize))
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg, err := LoadFromBytes(ctx, data)
	if err != nil {
		return nil, err
	}

	slog.Info("config loaded",
		"path", path,
		"sonarr", cfg.SonarrConfig().URL != "",
		"radarr", cfg.RadarrConfig().URL != "",
		"providers", len(cfg.Providers),
		"rules", len(cfg.Languages.Rules))

	return cfg, nil
}

// expandVariants expands targets using the "variants" shorthand into
// individual single-variant targets. A target with variants: [normal, forced]
// becomes two targets with variant: normal and variant: forced, each
// inheriting the same code, providers, exclude, and min_score.
// Returns an error if both "variant" and "variants" are set on the same target.
func expandVariants(cfg *Config) error {
	for i, rule := range cfg.Languages.Rules {
		expanded, err := expandTargetList(rule.Subtitles, rule.Audio)
		if err != nil {
			return err
		}
		cfg.Languages.Rules[i].Subtitles = expanded
	}
	expanded, err := expandTargetList(cfg.Languages.Default, "default")
	if err != nil {
		return err
	}
	cfg.Languages.Default = expanded
	return nil
}

// expandTargetList expands any target with a "variants" list into individual
// single-variant targets, preserving code, providers, exclude, and min_score.
func expandTargetList(targets []yamlSubtitleTarget, ruleCtx string) ([]yamlSubtitleTarget, error) {
	var result []yamlSubtitleTarget
	for _, t := range targets {
		if t.Variant != "" && len(t.Variants) > 0 {
			return nil, fmt.Errorf(
				"%w (code=%s, context=%s)", ErrVariantConflict, t.Code, ruleCtx)
		}
		if len(t.Variants) == 0 {
			result = append(result, t)
			continue
		}
		for _, v := range t.Variants {
			expanded := yamlSubtitleTarget{
				Code:      t.Code,
				Variant:   v,
				MinScore:  t.MinScore,
				Providers: t.Providers,
				Exclude:   t.Exclude,
			}
			result = append(result, expanded)
		}
	}
	return result, nil
}

// LoadFromBytes parses config from raw YAML bytes.
func LoadFromBytes(ctx context.Context, data []byte) (*Config, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slog.Debug("parsing config from bytes", "size", len(data))
	if len(data) > maxConfigSize {
		return nil, fmt.Errorf("config data %w: %d bytes (max %d)", ErrConfigTooLarge, len(data), maxConfigSize)
	}

	// Parse FIRST, then expand ${VAR} references inside string scalar VALUES
	// only (yamlenv.Expand). The former pre-parse text expansion (os.Expand
	// over the raw bytes) let an environment value containing YAML syntax — a
	// quote, a newline, a '#' — change the document structure or truncate the
	// value; the post-parse walk makes that impossible. Only the braced ${VAR}
	// form expands now: unbraced $VAR, mapping keys, and non-string scalars
	// stay byte-for-byte literal.
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// Syntax errors from yaml.v3 are structural today, but the raw
		// document can hold pasted literal secrets; sanitize for the same
		// redact-everything stance as the post-expansion decode below.
		return nil, fmt.Errorf("parse YAML: %w", yamlenv.SanitizeDecodeError(err))
	}

	cfg := newWithDefaults()
	// An empty document (zero node) keeps the defaults, matching the former
	// yaml.Unmarshal(empty, cfg) no-op.
	if doc.Kind != 0 {
		if unresolved := yamlenv.Expand(&doc, isAllowedEnvVar); len(unresolved) > 0 {
			slog.Warn("config references environment variables that are not set; the literal ${VAR} is kept",
				"vars", strings.Join(unresolved, ","))
		}
		if err := doc.Decode(cfg); err != nil {
			return nil, fmt.Errorf("parse YAML: %w", sanitizeDecodeErr(err))
		}
	}

	if err := expandVariants(cfg); err != nil {
		return nil, err
	}

	if err := validate(ctx, cfg); err != nil {
		slog.Warn("config validation failed", "error", err)
		return nil, err
	}

	cfg.buildCaches(ctx)

	return cfg, nil
}

// sanitizeDecodeErr redacts yaml.v3's own decode errors before they reach
// the two operator-facing surfaces (the startup log and the PUT /api/config
// response body): a *yaml.TypeError entry embeds a backtick-quoted excerpt
// of the offending scalar, which after yamlenv.Expand may be an expanded
// ${VAR} secret. Errors raised by this package's own UnmarshalYAML
// implementations (Duration's "invalid duration ...") pass through
// unchanged: their vocabulary is app-owned and pinned by tests.
func sanitizeDecodeErr(err error) error {
	if _, ok := errors.AsType[*yaml.TypeError](err); ok || strings.HasPrefix(err.Error(), "yaml:") {
		return yamlenv.SanitizeDecodeError(err)
	}
	return err
}

// buildCaches pre-computes lookup structures after config is fully loaded.
func (c *Config) buildCaches(ctx context.Context) {
	// Build rule index for O(1) matchRule lookup.
	c.ruleIndex = make(map[string]int, len(c.Languages.Rules))
	for i, rule := range c.Languages.Rules {
		c.ruleIndex[rule.Audio] = i
	}

	// Pre-compute language codes.
	c.cachedLangCodes = computeLangCodes(c.Languages.Rules, c.Languages.Default)

	// Pre-compute provider configs map.
	pc := make(map[api.ProviderID]api.ProviderCfg, len(c.Providers))
	for k, v := range c.Providers {
		pc[k] = api.ProviderCfg{Settings: v.Settings, Enabled: v.Enabled, Priority: v.Priority}
	}
	c.cachedProviderConfigs = pc

	// Pre-compute rule targets to avoid per-call allocations in matchRule.
	c.cachedRuleTargets = make(map[string][]api.SubtitleTarget, len(c.Languages.Rules))
	for _, rule := range c.Languages.Rules {
		c.cachedRuleTargets[rule.Audio] = targetsToAPI(rule.Subtitles)
	}
	c.cachedDefaultTargets = targetsToAPI(c.Languages.Default)

	// Parse the trusted-proxy CIDR set. validate() already rejected any
	// malformed entry before buildCaches runs, so the error is unreachable
	// here; discard it defensively and fall back to an empty (trust-nothing) set.
	c.cachedTrustedProxies, _ = parseTrustedProxies(c.TrustedProxies)

	// Check context before the expensive media-root syscall loop.
	if ctx.Err() != nil {
		return
	}

	// Pre-open media root handles to eliminate per-request OpenRoot syscalls.
	c.cachedRoots = nil
	for _, root := range c.MediaRootDirs {
		if err := ctx.Err(); err != nil {
			break
		}
		rd, err := os.OpenRoot(root)
		if err != nil {
			slog.Warn("media root inaccessible at cache time", "root", root, "error", err)
			continue
		}
		c.cachedRoots = append(c.cachedRoots, rd)
	}
}

// Close releases all cached os.Root handles. Call at shutdown.
func (c *Config) Close() error {
	for _, rd := range c.cachedRoots {
		rd.Close()
	}
	c.cachedRoots = nil
	return nil
}
