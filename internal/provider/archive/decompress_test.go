package archive

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/ulikunitz/xz"
)

// TestDecompress covers the public detect-and-decompress behavior: gzip and xz
// payloads round-trip back to the original bytes, plain data passes through
// unchanged, and a truncated/invalid compressed header falls back to returning
// the input untouched.
func TestDecompress(t *testing.T) {
	t.Parallel()
	payload := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello world\n")

	tests := []struct {
		name  string
		input []byte
		want  []byte
	}{
		{"plain text passthrough", payload, payload},
		{"gzip round-trip", gzipCompress(t, payload), payload},
		{"xz round-trip", xzCompress(t, payload), payload},
		{"truncated gzip header returns input", []byte{0x1f, 0x8b, 0x08, 0x00}, []byte{0x1f, 0x8b, 0x08, 0x00}},
		{"truncated xz header returns input", []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00, 0xFF}, []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00, 0xFF}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Decompress(tt.input)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("Decompress() = %q, want %q", got, tt.want)
			}
		})
	}
}

func gzipCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func xzCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatalf("xz writer: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("xz write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("xz close: %v", err)
	}
	return buf.Bytes()
}

// TestIsXZ pins the xz magic detection: the full 6-byte magic plus at least one
// trailing byte is required (the length guard is strictly greater than 6), and
// corrupting any magic byte must reject the data.
func TestIsXZ(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"full magic with trailing byte", []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00, 0x00}, true},
		{"exactly six magic bytes rejected", []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}, false},
		{"wrong byte 0", []byte{0xFF, 0x37, 0x7A, 0x58, 0x5A, 0x00, 0x00}, false},
		{"wrong byte 1", []byte{0xFD, 0xFF, 0x7A, 0x58, 0x5A, 0x00, 0x00}, false},
		{"wrong byte 2", []byte{0xFD, 0x37, 0xFF, 0x58, 0x5A, 0x00, 0x00}, false},
		{"wrong byte 3", []byte{0xFD, 0x37, 0x7A, 0xFF, 0x5A, 0x00, 0x00}, false},
		{"wrong byte 4", []byte{0xFD, 0x37, 0x7A, 0x58, 0xFF, 0x00, 0x00}, false},
		{"wrong byte 5", []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0xFF, 0x00}, false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isXZ(tt.data); got != tt.want {
				t.Errorf("isXZ(%x) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

// TestIsGzip pins the gzip magic detection: the 2-byte magic plus at least one
// trailing byte is required (length strictly greater than 2), and either magic
// byte being wrong must reject the data.
func TestIsGzip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"full magic with trailing byte", []byte{0x1f, 0x8b, 0x00}, true},
		{"exactly two magic bytes rejected", []byte{0x1f, 0x8b}, false},
		{"wrong byte 0", []byte{0x00, 0x8b, 0x00}, false},
		{"wrong byte 1", []byte{0x1f, 0x00, 0x00}, false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isGzip(tt.data); got != tt.want {
				t.Errorf("isGzip(%x) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}
