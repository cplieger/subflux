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

// A huge maxSamples (the whole-file durationMs=0 path uses 100M) must not be
// preallocated up front: the buffer starts at initialPCMBufSamples and grows
// only as data arrives. Guards against reintroducing the ~200 MB prealloc.
func TestReadPCMSamples_largeMaxDoesNotPreallocateCap(t *testing.T) {
	got := readPCMSamples(bytes.NewReader([]byte{0x01, 0x00, 0x02, 0x00}), 100_000_000)
	if len(got) != 2 {
		t.Fatalf("readPCMSamples(4 bytes, max=100M) len = %d, want 2", len(got))
	}
	if cap(got) > initialPCMBufSamples {
		t.Errorf("readPCMSamples(4 bytes, max=100M) cap = %d, want <= %d (no cap-sized prealloc)",
			cap(got), initialPCMBufSamples)
	}
}

// A cap smaller than the initial buffer still reads and truncates exactly at
// the cap (segment extractions preallocate exactly as before).
func TestReadPCMSamples_smallCapUnchanged(t *testing.T) {
	data := make([]byte, 64) // 32 samples available
	got := readPCMSamples(bytes.NewReader(data), 8)
	if len(got) != 8 {
		t.Fatalf("readPCMSamples(64 bytes, max=8) len = %d, want 8", len(got))
	}
}
