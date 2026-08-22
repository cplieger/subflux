package subsync

import (
	"bytes"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"
)

// Abort vs report in this file: a value mismatch reports with t.Errorf so
// the siblings still run. The switch arms below are mutually exclusive, so
// an arm holding a single assertion keeps t.Fatalf — there is no sibling on
// that path for an abort to hide.

func TestNormalizeEncoding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		wantExact    string
		wantContains string
		input        []byte
		wantEmpty    bool
		wantValidUTF bool
	}{
		{name: "nil_input", input: nil, wantEmpty: true},
		{name: "empty_input", input: []byte{}, wantEmpty: true},
		{name: "valid_UTF8_passthrough", input: []byte("Hello, 世界! Ça va?"), wantExact: "Hello, 世界! Ça va?"},
		{name: "strips_UTF8_BOM", input: append([]byte{0xEF, 0xBB, 0xBF}, []byte("Hello")...), wantExact: "Hello"},
		{name: "UTF16LE_BOM", input: []byte{0xFF, 0xFE, 'H', 0x00, 'i', 0x00}, wantExact: "Hi"},
		{name: "UTF16BE_BOM", input: []byte{0xFE, 0xFF, 0x00, 'H', 0x00, 'i'}, wantExact: "Hi"},
		{name: "UTF16LE_no_BOM", input: []byte{'A', 0x00, 'B', 0x00}, wantExact: "AB"},
		{name: "UTF16BE_no_BOM", input: []byte{0x00, 'A', 0x00, 'B'}, wantExact: "AB"},
		// Four bytes is the shortest input the BOM-less heuristic accepts, and a
		// C1 code unit is where UTF-16 and the Windows-1252 fallback disagree:
		// 0x80 is U+0080 as a UTF-16 code unit but the euro sign as a lone byte.
		{name: "UTF16LE_no_BOM_minimum_length", input: []byte{0x80, 0x00, '!', 0x00}, wantExact: "\u0080!"},
		{name: "UTF16BE_no_BOM_minimum_length", input: []byte{0x00, 0x80, 0x00, '!'}, wantExact: "\u0080!"},
		// One code unit is a whole UTF-16 payload.
		{name: "UTF16LE_BOM_one_code_unit", input: []byte{0xFF, 0xFE, 'A', 0x00}, wantExact: "A"},
		{name: "UTF16BE_BOM_one_code_unit", input: []byte{0xFE, 0xFF, 0x00, 'A'}, wantExact: "A"},
		// A high surrogate followed by a single stray byte has no low surrogate
		// to pair with: the truncated pair becomes U+FFFD and the odd byte is
		// dropped, rather than the decoder reading past the end of the payload.
		{name: "UTF16BE_truncated_surrogate_pair", input: []byte{0xFE, 0xFF, 0xD8, 0x00, 0x41}, wantExact: "\uFFFD"},
		{name: "UTF16LE_truncated_surrogate_pair", input: []byte{0xFF, 0xFE, 0x00, 0xD8, 0x41}, wantExact: "\uFFFD"},
		{name: "Windows1252", input: []byte{'c', 'a', 'f', 0xE9}, wantExact: "café"},
		{name: "Windows1252_special_range", input: []byte{0x80}, wantExact: "€"},
		// 0x9F is the last byte the Windows-1252 table covers; 0xA0 is the first
		// one that falls through to its ISO-8859-1 code point.
		{name: "Windows1252_special_range_end", input: []byte{0x9F}, wantExact: "Ÿ"},
		{name: "Windows1252_first_latin1_byte", input: []byte{0xA0}, wantExact: "\u00a0"},
		// A NUL never appears in legitimate subtitle text, so it is dropped and
		// the surrounding text is kept.
		{name: "embedded_NUL_in_valid_UTF8", input: []byte("He\x00llo"), wantExact: "Hello"},
		{name: "UTF16LE_surrogate_pair", input: []byte{0xFF, 0xFE, 0x3D, 0xD8, 0x00, 0xDE}, wantExact: "😀"},
		{name: "UTF16BE_surrogate_pair", input: []byte{0xFE, 0xFF, 0xD8, 0x3D, 0xDE, 0x00}, wantExact: "😀"},
		// The two ends of the surrogate range are still surrogates: D800/DC00
		// pairs to the first supplementary code point and DBFF/DFFF to the last.
		{name: "UTF16LE_lowest_surrogate_pair", input: []byte{0xFF, 0xFE, 0x00, 0xD8, 0x00, 0xDC}, wantExact: "\U00010000"},
		{name: "UTF16BE_lowest_surrogate_pair", input: []byte{0xFE, 0xFF, 0xD8, 0x00, 0xDC, 0x00}, wantExact: "\U00010000"},
		{name: "UTF16LE_highest_surrogate_pair", input: []byte{0xFF, 0xFE, 0xFF, 0xDB, 0xFF, 0xDF}, wantExact: "\U0010FFFF"},
		{name: "UTF16BE_highest_surrogate_pair", input: []byte{0xFE, 0xFF, 0xDB, 0xFF, 0xDF, 0xFF}, wantExact: "\U0010FFFF"},
		{name: "UTF16LE_odd_byte_count", input: []byte{0xFF, 0xFE, 'H', 0x00, 0x42}, wantExact: "H"},
		{name: "UTF16BE_odd_byte_count", input: []byte{0xFE, 0xFF, 0x00, 'H', 0x42}, wantExact: "H"},
		{name: "UTF16LE_lone_high_surrogate", input: []byte{0xFF, 0xFE, 0x00, 0xD8, 'A', 0x00}, wantValidUTF: true, wantContains: "A"},
		{name: "UTF16BE_lone_high_surrogate", input: []byte{0xFE, 0xFF, 0xD8, 0x00, 0x00, 'A'}, wantValidUTF: true, wantContains: "A"},
		{name: "UTF16LE_lone_low_surrogate", input: []byte{0xFF, 0xFE, 0x00, 0xDC, 'A', 0x00}, wantValidUTF: true, wantContains: "A"},
		{name: "UTF16BE_lone_low_surrogate", input: []byte{0xFE, 0xFF, 0xDC, 0x00, 0x00, 'A'}, wantValidUTF: true, wantContains: "A"},
		{name: "UTF16LE_high_surrogate_at_end", input: []byte{0xFF, 0xFE, 0x00, 0xD8}, wantValidUTF: true},
		{name: "UTF16BE_high_surrogate_at_end", input: []byte{0xFE, 0xFF, 0xD8, 0x00}, wantValidUTF: true},
		{name: "Windows1252_undefined_byte", input: []byte{0x81}, wantExact: "\u0081"},
		{name: "short_input_under_4_bytes", input: []byte{0xC0, 0xC1, 0xFE}, wantExact: "ÀÁþ"},
		{name: "UTF8_BOM_only", input: []byte{0xEF, 0xBB, 0xBF}, wantEmpty: true},
		{name: "UTF16LE_BOM_only", input: []byte{0xFF, 0xFE}, wantEmpty: true},
		{name: "UTF16BE_BOM_only", input: []byte{0xFE, 0xFF}, wantEmpty: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeEncoding(tt.input)
			switch {
			case tt.wantEmpty:
				if len(got) != 0 {
					t.Fatalf("expected empty, got %d bytes: %x", len(got), got)
				}
			case tt.wantValidUTF:
				if !utf8.Valid(got) {
					t.Errorf("produced invalid UTF-8: %x", got)
				}
				if tt.wantContains != "" && !bytes.Contains(got, []byte(tt.wantContains)) {
					t.Fatalf("output %x missing %q", got, tt.wantContains)
				}
			case tt.wantExact != "":
				if string(got) != tt.wantExact {
					t.Fatalf("got %q, want %q", got, tt.wantExact)
				}
			}
		})
	}
}

// PBT: output is always valid UTF-8.
func TestNormalizeEncoding_always_valid_UTF8(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		data := rapid.SliceOf(rapid.Byte()).Draw(t, "data")
		got := NormalizeEncoding(data)
		if !utf8.Valid(got) {
			t.Fatalf("output is not valid UTF-8 for input %x", data)
		}
	})
}

// PBT: output never contains a UTF-8 BOM.
func TestNormalizeEncoding_never_contains_BOM(t *testing.T) {
	t.Parallel()
	bom := []byte{0xEF, 0xBB, 0xBF}
	rapid.Check(t, func(t *rapid.T) {
		data := rapid.SliceOf(rapid.Byte()).Draw(t, "data")
		got := NormalizeEncoding(data)
		if len(got) >= 3 && got[0] == bom[0] && got[1] == bom[1] && got[2] == bom[2] {
			t.Fatalf("output contains BOM for input %x", data)
		}
	})
}
