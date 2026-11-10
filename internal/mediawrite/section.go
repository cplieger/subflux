package mediawrite

import (
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
)

const (
	fieldStripHI              = "strip_hi"
	fieldStripTags            = "strip_tags"
	fieldNormalizeUTF8        = "normalize_utf8"
	fieldNormalizeLineEndings = "normalize_line_endings"
	fieldCleanWhitespace      = "clean_whitespace"
	fieldRemoveEmpty          = "remove_empty"

	on  = "true"
	off = "false"
)

// Schema is the post_processing section's form: what Deliver does to a
// downloaded subtitle before it writes it.
var Schema = formschema.Schema{Fields: []formschema.Field{
	{
		Key: fieldStripHI, Label: "Strip hearing-impaired notes", Kind: formschema.Bool, Default: off,
		Help: "Remove notes for deaf and hard-of-hearing viewers, such as [music], (laughs) and speaker labels.",
	},
	{
		Key: fieldStripTags, Label: "Strip formatting tags", Kind: formschema.Bool, Default: on,
		Help: "Remove <i>, <b>, <u>, <s> and <font> tags.",
	},
	{
		Key: fieldNormalizeUTF8, Label: "Convert to UTF-8", Kind: formschema.Bool, Default: on,
		Help: "Convert UTF-16, Windows-1252 and the other encodings subflux recognizes to UTF-8. A file in an encoding it cannot tell keeps its bytes.",
	},
	{
		Key: fieldNormalizeLineEndings, Label: "Standard line endings", Kind: formschema.Bool, Default: on,
		Help: "Convert line endings to CRLF, the SRT standard.",
	},
	{
		Key: fieldCleanWhitespace, Label: "Clean whitespace", Kind: formschema.Bool, Default: on,
		Help: "Trim spaces at either end of each line and remove lines left empty.",
	},
	{
		Key: fieldRemoveEmpty, Label: "Remove empty subtitles", Kind: formschema.Bool, Default: on,
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
	return Config{Clean: timing.CleanOptions{
		StripHI: vals.Bool(fieldStripHI), StripTags: vals.Bool(fieldStripTags), NormalizeUTF8: vals.Bool(fieldNormalizeUTF8),
		CleanWhitespace: vals.Bool(fieldCleanWhitespace), NormalizeLineEndings: vals.Bool(fieldNormalizeLineEndings), RemoveEmpty: vals.Bool(fieldRemoveEmpty),
	}}, nil
}

// SetConfig replaces the post_processing section, which applies from the
// next delivery, and asks Run for a probe pass, which re-reads the roots.
func (w *Writer) SetConfig(c Config) {
	w.cfg.Store(&c)
	w.reprobe()
}
