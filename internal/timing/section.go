package timing

import (
	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v4"
)

// Config is the timing section. Its zero value turns sync off; an absent
// section is ParseConfig(nil).
type Config struct{ Sync, AudioFallback bool }

const (
	keySync          = "sync"
	keyAudioFallback = "audio_fallback"
)

// Schema is the timing section's form. It has no confidence setting: the
// reference and audio gates sit on different measures, so one number would
// gate one of them wrongly.
var Schema = formschema.Schema{Fields: []formschema.Field{
	{
		Key: keySync, Label: "Sync timing", Kind: formschema.Bool, Default: "true",
		Help: "Re-time each downloaded subtitle against a subtitle track inside the video.",
	},
	{
		Key: keyAudioFallback, Label: "Audio fallback", Kind: formschema.Bool, Default: "false",
		Help: "Also try the video's speech when its subtitle tracks cannot verify a download's timing. Slower, and it can spot a different cut only when the video also has a usable subtitle track.",
	},
}}

// ParseConfig decodes the timing section, n being its node or nil when
// absent, reporting each error at timing.<key>.
func ParseConfig(n *yaml.Node) (Config, []formschema.FieldError) {
	vals, errs := Schema.Decode(n)
	if len(errs) > 0 {
		return Config{}, formschema.Prefix("timing", errs)
	}
	return Config{Sync: vals.Bool(keySync), AudioFallback: vals.Bool(keyAudioFallback)}, nil
}
