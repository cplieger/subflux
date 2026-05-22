package subsync

import (
	"bytes"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"
)

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
		{name: "Windows1252", input: []byte{'c', 'a', 'f', 0xE9}, wantExact: "café"},
		{name: "Windows1252_special_range", input: []byte{0x80}, wantExact: "€"},
		{name: "UTF16LE_surrogate_pair", input: []byte{0xFF, 0xFE, 0x3D, 0xD8, 0x00, 0xDE}, wantExact: "😀"},
		{name: "UTF16BE_surrogate_pair", input: []byte{0xFE, 0xFF, 0xD8, 0x3D, 0xDE, 0x00}, wantExact: "😀"},
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
					t.Fatalf("produced invalid UTF-8: %x", got)
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
