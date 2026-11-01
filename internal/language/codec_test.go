package language

import (
	"encoding"
	"encoding/json"
	"strings"
	"testing"
)

var (
	_ encoding.TextMarshaler   = Tag{}
	_ encoding.TextUnmarshaler = (*Tag)(nil)
	_ encoding.TextMarshaler   = Variant(0)
	_ encoding.TextUnmarshaler = (*Variant)(nil)
	_ encoding.TextMarshaler   = Closeness(0)
	_ encoding.TextUnmarshaler = (*Closeness)(nil)
	_ encoding.TextMarshaler   = Class(0)
	_ encoding.TextUnmarshaler = (*Class)(nil)
)

func TestTagUnmarshalRefusesNonCanonical(t *testing.T) {
	for in, wantErr := range map[string]string{
		"zh-TW": `use "zh-Hant"`, "mo": `use "ro-MD"`, "pt-br": `use "pt-BR"`, "pb": "not a language tag", "und": "not a language tag",
	} {
		t.Run(in, func(t *testing.T) {
			tag := mustParse(t, "en")
			err := tag.UnmarshalText([]byte(in))
			if err == nil || !strings.Contains(err.Error(), wantErr) {
				t.Errorf("UnmarshalText(%q) = %v; want an error containing %q", in, err, wantErr)
			}
			if tag.String() != "en" {
				t.Errorf("a refused UnmarshalText changed the tag to %q", tag)
			}
		})
	}
	for _, in := range []string{"", "pt-BR", "zh-Hant", "ku-Arab-IQ", "cht"} {
		t.Run("accepts "+in, func(t *testing.T) {
			tag := mustParse(t, "en")
			if err := tag.UnmarshalText([]byte(in)); err != nil || tag.String() != in {
				t.Errorf("UnmarshalText(%q) = %q, %v; want the tag", in, tag, err)
			}
		})
	}
}

func TestTagJSON(t *testing.T) {
	in := struct{ A, B Tag }{A: mustParse(t, "zh-TW")}
	b, err := json.Marshal(in)
	if err != nil || string(b) != `{"A":"zh-Hant","B":""}` {
		t.Fatalf("json.Marshal = %s, %v", b, err)
	}
	var out struct{ A, B Tag }
	if err := json.Unmarshal(b, &out); err != nil || out != in {
		t.Errorf("json.Unmarshal(%s) = %+v, %v; want %+v", b, out, err, in)
	}
}

type textCodec struct {
	marshal   func(uint8) ([]byte, error)
	unmarshal func([]byte) (uint8, error)
	name      string
	spellings []string
}

var enumCodecs = []textCodec{
	{
		name:      "Variant",
		spellings: []string{"standard", "hi", "forced"},
		marshal:   func(v uint8) ([]byte, error) { return Variant(v).MarshalText() },
		unmarshal: func(b []byte) (uint8, error) { var v Variant; err := v.UnmarshalText(b); return uint8(v), err },
	},
	{
		name:      "Closeness",
		spellings: []string{"identical", "same_language", "other_region", "intelligible", "other_script", "unrelated"},
		marshal:   func(v uint8) ([]byte, error) { return Closeness(v).MarshalText() },
		unmarshal: func(b []byte) (uint8, error) { var v Closeness; err := v.UnmarshalText(b); return uint8(v), err },
	},
	{
		name:      "Class",
		spellings: []string{"exact", "substitute", "manual_only", "unrelated"},
		marshal:   func(v uint8) ([]byte, error) { return Class(v).MarshalText() },
		unmarshal: func(b []byte) (uint8, error) { var v Class; err := v.UnmarshalText(b); return uint8(v), err },
	},
}

func TestEnumTextRoundTrip(t *testing.T) {
	for _, c := range enumCodecs {
		t.Run(c.name, func(t *testing.T) {
			for i, want := range c.spellings {
				v := uint8(i + 1)
				b, err := c.marshal(v)
				if err != nil || string(b) != want {
					t.Errorf("MarshalText(%d) = %q, %v; want %q", v, b, err, want)
				}
				if got, err := c.unmarshal([]byte(want)); err != nil || got != v {
					t.Errorf("UnmarshalText(%q) = %d, %v; want %d", want, got, err, v)
				}
			}
			for _, v := range []uint8{0, uint8(len(c.spellings) + 1)} {
				if b, err := c.marshal(v); err == nil {
					t.Errorf("MarshalText(%d) = %q; want an error", v, b)
				}
			}
			for _, bad := range []string{"", "Standard", "EXACT", "same-language", "sdh", "foreign", "0", "1"} {
				if got, err := c.unmarshal([]byte(bad)); err == nil {
					t.Errorf("UnmarshalText(%q) = %d; want an error", bad, got)
				}
			}
		})
	}
}
