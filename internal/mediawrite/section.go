package mediawrite

import (
	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v4"
)

const (
	keyStripHI              = "strip_hi"
	keyStripTags            = "strip_tags"
	keyNormalizeUTF8        = "normalize_utf8"
	keyNormalizeLineEndings = "normalize_line_endings"
	keyCleanWhitespace      = "clean_whitespace"
	keyRemoveEmpty          = "remove_empty"

	on  = "true"
	off = "false"
)

// Schema is the post_processing section's form: what Deliver does to a
// downloaded subtitle before it writes it.
var Schema = formschema.Schema{Fields: []formschema.Field{
	{
		Key: keyStripHI, Label: "Strip hearing-impaired notes", Kind: formschema.Bool, Default: off,
		Help: "Remove notes for deaf and hard-of-hearing viewers, such as [music], (laughs) and speaker labels.",
	},
	{
		Key: keyStripTags, Label: "Strip formatting tags", Kind: formschema.Bool, Default: on,
		Help: "Remove <i>, <b>, <u> and <font> tags.",
	},
	{
		Key: keyNormalizeUTF8, Label: "Convert to UTF-8", Kind: formschema.Bool, Default: on,
		Help: "Convert UTF-16, Windows-1252 and the other encodings subflux recognizes to UTF-8. A file in an encoding it cannot tell keeps its bytes.",
	},
	{
		Key: keyNormalizeLineEndings, Label: "Standard line endings", Kind: formschema.Bool, Default: on,
		Help: "Convert line endings to CRLF, the SRT standard.",
	},
	{
		Key: keyCleanWhitespace, Label: "Clean whitespace", Kind: formschema.Bool, Default: on,
		Help: "Trim spaces at either end of each line and remove lines left empty.",
	},
	{
		Key: keyRemoveEmpty, Label: "Remove empty subtitles", Kind: formschema.Bool, Default: on,
		Help: "Drop subtitle lines left with no text, renumbering the rest.",
	},
}}

// ParseConfig decodes the post_processing section, n being its node or nil
// when absent, reporting each error at post_processing.<key>.
func ParseConfig(n *yaml.Node) (Config, []formschema.FieldError) {
	vals, errs := Schema.Decode(n)
	if len(errs) > 0 {
		return Config{}, formschema.Prefix("post_processing", errs)
	}
	return Config{
		StripHI: vals.Bool(keyStripHI), StripTags: vals.Bool(keyStripTags), NormalizeUTF8: vals.Bool(keyNormalizeUTF8),
		CleanWhitespace: vals.Bool(keyCleanWhitespace), NormalizeLineEndings: vals.Bool(keyNormalizeLineEndings), RemoveEmpty: vals.Bool(keyRemoveEmpty),
	}, nil
}

// SetConfig replaces the post_processing section, which applies from the
// next delivery, and asks Run for a probe pass, which re-reads the roots.
func (w *Writer) SetConfig(c Config) {
	w.cfg.Store(&c)
	w.reprobe()
}
