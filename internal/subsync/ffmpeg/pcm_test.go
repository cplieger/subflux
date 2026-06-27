package ffmpeg

import (
	"bytes"
	"io"
	"testing"
)

// chunkReader returns up to chunk bytes per Read (with a nil error), then
// (0, io.EOF) once exhausted — mirroring bytes.Reader's "deliver data first,
// signal EOF on the next call" contract. It forces readPCMSamples through more
// than one outer-loop iteration (a plain bytes.Reader hands over everything in
// the first Read).
type chunkReader struct {
	data  []byte
	chunk int
	pos   int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	end := min(r.pos+r.chunk, len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n
	return n, nil
}

// 4 bytes = 2 little-endian int16 samples; a generous cap reads them all.
func TestReadPCMSamples_readsAllSamples(t *testing.T) {
	got := readPCMSamples(bytes.NewReader([]byte{0x01, 0x00, 0x02, 0x00}), 100)
	if len(got) != 2 {
		t.Fatalf("readPCMSamples(4 bytes, max=100) len = %d, want 2", len(got))
	}
}

// An odd byte count yields whole int16 samples only; the trailing byte is
// ignored (5 bytes -> 2 samples).
func TestReadPCMSamples_oddByteCountDropsTrailingByte(t *testing.T) {
	got := readPCMSamples(bytes.NewReader([]byte{0x01, 0x00, 0x02, 0x00, 0x05}), 100)
	if len(got) != 2 {
		t.Fatalf("readPCMSamples(5 bytes, max=100) len = %d, want 2", len(got))
	}
}

// maxSamples is honored across reads: 8 bytes delivered in two 4-byte reads
// with max=2 stops once the first read fills the cap, so no second read runs.
func TestReadPCMSamples_stopsAtMaxAcrossReads(t *testing.T) {
	r := &chunkReader{
		data:  []byte{0x01, 0x00, 0x02, 0x00, 0x03, 0x00, 0x04, 0x00},
		chunk: 4,
	}
	got := readPCMSamples(r, 2)
	if len(got) != 2 {
		t.Fatalf("readPCMSamples(chunked 8 bytes, max=2) len = %d, want 2", len(got))
	}
}

// maxSamples is honored within a single read: 6 bytes (3 samples) with max=2
// breaks the inner loop the moment the cap is reached.
func TestReadPCMSamples_stopsAtMaxWithinRead(t *testing.T) {
	got := readPCMSamples(bytes.NewReader([]byte{0x01, 0x00, 0x02, 0x00, 0x03, 0x00}), 2)
	if len(got) != 2 {
		t.Fatalf("readPCMSamples(6 bytes, max=2) len = %d, want 2", len(got))
	}
}
