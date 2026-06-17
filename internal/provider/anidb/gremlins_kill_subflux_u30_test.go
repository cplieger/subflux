package anidb

import (
	"bytes"
	"testing"
)

// Kills the three mutants on anidb_mapping.go:21, the gzip-detection guard
//
//		len(data) < 2  ||  data[0] != 0x1f  ||  data[1] != 0x8b
//
//	  - 21:15 CONDITIONALS_BOUNDARY (< -> <=)
//	  - 21:30 CONDITIONALS_NEGATION (!= -> ==) on data[0]
//	  - 21:49 CONDITIONALS_NEGATION (!= -> ==) on data[1]
//
// The guard returns the bytes unchanged when the input is NOT a gzip stream.
// Each row pins the original behaviour so exactly one mutation flips it.
func Test_gk_subflux_u30_DecompressIfGzippedGuard(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		wantErr bool // true: original treats it as gzip and fails decoding; false: returned unchanged
	}{
		// Exactly the 2-byte gzip magic: len(data) < 2 is false and both magic
		// bytes match, so the original proceeds to decode an incomplete header
		// and errors. The <= mutant makes 2 <= 2 true and early-returns (data, nil).
		{name: "magic_two_bytes_truncated", in: []byte{0x1f, 0x8b}, wantErr: true},
		// First byte not the magic: the original short-circuits via the second
		// clause and returns the data unchanged. The != -> == mutant makes the
		// clause false, so it proceeds to decode and errors on a bad header.
		{name: "first_byte_not_magic", in: []byte{0x00, 0x8b}, wantErr: false},
		// Second byte not the magic: same, via the third clause.
		{name: "second_byte_not_magic", in: []byte{0x1f, 0x00}, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := decompressIfGzipped(tt.in, 1024)
			if tt.wantErr {
				if err == nil {
					t.Errorf("decompressIfGzipped(% x) err = nil, want non-nil", tt.in)
				}
				return
			}
			if err != nil {
				t.Errorf("decompressIfGzipped(% x) err = %v, want nil", tt.in, err)
			}
			if !bytes.Equal(out, tt.in) {
				t.Errorf("decompressIfGzipped(% x) = % x, want unchanged % x", tt.in, out, tt.in)
			}
		})
	}
}
