package search

import (
	"strings"

	"subflux/internal/api"
)

// ignoredCodecMap maps provider setting keys to the codec names they control.
// Adding a new ignored codec requires only a new map entry.
var ignoredCodecMap = map[string][]string{
	api.EmbeddedSettingIgnorePGS:    {"pgs"},
	api.EmbeddedSettingIgnoreVobSub: {"vobsub"},
	api.EmbeddedSettingIgnoreASS:    {"ass", "ssa"},
}

// parseBoolSetting reads a bool from a settings map, defaulting to false.
func parseBoolSetting(settings map[string]any, key string) bool {
	v, ok := settings[key]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(b, "true")
	default:
		return false
	}
}

// IgnoredCodecsFromConfig builds the set of embedded codecs that should be
// treated as "present but not usable" based on the embedded provider settings.
// Accepts any type that provides ProviderConfigs() (satisfied by both
// api.ConfigProvider and search.SearchCfg).
func IgnoredCodecsFromConfig(cfg interface {
	ProviderConfigs() map[api.ProviderID]api.ProviderCfg
}) map[string]bool {
	provCfgs := cfg.ProviderConfigs()
	embCfg, ok := provCfgs[api.ProviderNameEmbedded]
	if !ok {
		return nil
	}
	s := embCfg.Settings
	if s == nil {
		return nil
	}
	ignored := make(map[string]bool)
	for settingKey, codecs := range ignoredCodecMap {
		if parseBoolSetting(s, settingKey) {
			for _, codec := range codecs {
				ignored[codec] = true
			}
		}
	}
	if len(ignored) == 0 {
		return nil
	}
	return ignored
}
