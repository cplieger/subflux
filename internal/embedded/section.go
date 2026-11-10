package embedded

import (
	"slices"
	"strings"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v4"
)

// The names Probe stores for the formats it knows.
const (
	codecSRT      = "srt"
	codecASS      = "ass"
	codecSSA      = "ssa"
	codecWebVTT   = "webvtt"
	codecMovText  = "mov_text"
	codecPGS      = "pgs"
	codecVobSub   = "vobsub"
	codecDVBSub   = "dvbsub"
	codecTeletext = "teletext"
	codecCEA608   = "cea608"
	codecTTML     = "ttml"
)

// Codecs is ignore_codecs' closed value set; DefaultIgnored is the
// section's default, the two picture formats.
var (
	Codecs = []string{
		codecSRT, codecASS, codecSSA, codecWebVTT, codecMovText, codecPGS,
		codecVobSub, codecDVBSub, codecTeletext, codecCEA608, codecTTML,
	}
	DefaultIgnored = []string{codecPGS, codecVobSub}
	// TextCodecs can serve as a sync reference; the image's ffmpeg decodes each.
	TextCodecs = []string{codecSRT, codecASS, codecSSA, codecWebVTT, codecMovText}
)

// maxTitleBytes bounds the track title Probe stores and reads.
const maxTitleBytes = 256

const fieldIgnoreCodecs = "ignore_codecs"

// Schema is the embedded_subtitles section's form.
var Schema = formschema.Schema{Fields: []formschema.Field{{
	Key: fieldIgnoreCodecs, Label: "Ignored embedded formats", Kind: formschema.StringList,
	Enum: Codecs, Default: "[" + strings.Join(DefaultIgnored, ", ") + "]",
	Help: "Subtitle tracks inside a video in these formats do not count as subtitles it has, so subflux still downloads them.",
}}}

// Config is the embedded_subtitles section. Its zero value ignores no
// format; ParseConfig of an absent section ignores DefaultIgnored.
type Config struct{ ignored []string }

// ParseConfig decodes the embedded_subtitles section, n being its node or nil
// when absent, reporting each error at embedded_subtitles.<key>.
func ParseConfig(n *yaml.Node) (Config, []formschema.FieldError) {
	vals, errs := Schema.Decode(n)
	if len(errs) > 0 {
		return Config{}, formschema.Prefix("embedded_subtitles", errs)
	}
	ignored := vals.StringList(fieldIgnoreCodecs)
	slices.Sort(ignored)
	return Config{ignored: slices.Compact(ignored)}, nil
}

// Ignored is the formats whose tracks count toward no coverage, sorted and
// each once: the Activation's IgnoredCodecs.
func (c Config) Ignored() []string { return slices.Clone(c.ignored) }
