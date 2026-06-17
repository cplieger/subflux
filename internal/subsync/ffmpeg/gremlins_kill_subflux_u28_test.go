package ffmpeg

import (
	"bytes"
	"io"
	"testing"
)

// gk_subflux_u28_chunkReader returns up to chunk bytes per Read (with a nil
// error), then (0, io.EOF) once exhausted — mirroring bytes.Reader's
// "deliver data first, signal EOF on the next call" contract. Used to force
// readPCMSamples through more than one outer-loop iteration (a plain
// bytes.Reader hands over everything in the first Read).
type gk_subflux_u28_chunkReader struct {
	data  []byte
	chunk int
	pos   int
}

func (r *gk_subflux_u28_chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	end := min(r.pos+r.chunk, len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n
	return n, nil
}

// Test_gk_subflux_u28_readPCMSamples_readsAllSamples feeds 4 bytes (2 int16
// samples) with a high cap. Original returns 2.
// Kills pcm.go:110:19 NEGATION (`len(samples) < maxSamples` -> `>=`: the
// outer loop never runs -> 0), pcm.go:112:19 NEGATION (`i+1 < n` -> `>=`:
// the inner loop never runs -> 0), and pcm.go:112:16 ARITHMETIC (`i+1` ->
// `i-1`: the inner loop over-reads one extra pair -> 3).
func Test_gk_subflux_u28_readPCMSamples_readsAllSamples(t *testing.T) {
	got := readPCMSamples(bytes.NewReader([]byte{0x01, 0x00, 0x02, 0x00}), 100)
	if len(got) != 2 {
		t.Fatalf("readPCMSamples(4 bytes, max=100) len = %d, want 2", len(got))
	}
}

// Test_gk_subflux_u28_readPCMSamples_oddByteCount feeds 5 bytes (an odd
// count) with a high cap. Original consumes two full int16 pairs and ignores
// the trailing byte -> 2 samples.
// Kills pcm.go:112:19 BOUNDARY (`i+1 < n` -> `i+1 <= n`: processes a 3rd,
// out-of-data pair -> 3) and pcm.go:112:16 ARITHMETIC (`i-1 < n` over-reads
// -> 3).
func Test_gk_subflux_u28_readPCMSamples_oddByteCount(t *testing.T) {
	got := readPCMSamples(bytes.NewReader([]byte{0x01, 0x00, 0x02, 0x00, 0x05}), 100)
	if len(got) != 2 {
		t.Fatalf("readPCMSamples(5 bytes, max=100) len = %d, want 2", len(got))
	}
}

// Test_gk_subflux_u28_readPCMSamples_outerLoopBoundary delivers 8 bytes in
// two 4-byte reads with maxSamples=2. Original fills exactly 2 on the first
// read; the outer guard `len(samples) < maxSamples` is then false (2 < 2),
// so no second read happens -> 2 samples.
// Kills pcm.go:110:19 BOUNDARY (`<` -> `<=`: 2 <= 2 triggers one more read
// that appends a 3rd sample -> 3).
func Test_gk_subflux_u28_readPCMSamples_outerLoopBoundary(t *testing.T) {
	r := &gk_subflux_u28_chunkReader{
		data:  []byte{0x01, 0x00, 0x02, 0x00, 0x03, 0x00, 0x04, 0x00},
		chunk: 4,
	}
	got := readPCMSamples(r, 2)
	if len(got) != 2 {
		t.Fatalf("readPCMSamples(chunked 8 bytes, max=2) len = %d, want 2", len(got))
	}
}

// Test_gk_subflux_u28_readPCMSamples_innerBreakAtMaxSamples feeds 6 bytes
// (3 samples) in a single read with maxSamples=2. Original breaks the inner
// loop the moment `len(samples) >= maxSamples` is reached -> 2 samples.
// Kills pcm.go:115:20 BOUNDARY (`>=` -> `>`: allows a 3rd sample before the
// break -> 3) and pcm.go:115:20 NEGATION (`>=` -> `<`: `1 < 2` breaks after
// the first append -> 1).
func Test_gk_subflux_u28_readPCMSamples_innerBreakAtMaxSamples(t *testing.T) {
	got := readPCMSamples(bytes.NewReader([]byte{0x01, 0x00, 0x02, 0x00, 0x03, 0x00}), 2)
	if len(got) != 2 {
		t.Fatalf("readPCMSamples(6 bytes, max=2) len = %d, want 2", len(got))
	}
}

// Test_gk_subflux_u28_NormalizeFFprobeLang_leadingDashBoundary covers
// helpers.go:112 `if i := strings.IndexByte(lang, '-'); i > 0`. For a tag
// that begins with '-', IndexByte returns 0, so the original `i > 0` is
// false and no truncation happens (the tag is returned verbatim).
// Kills helpers.go:112:42 BOUNDARY (`> 0` -> `>= 0`: 0 >= 0 truncates
// lang[:0] -> "").
func Test_gk_subflux_u28_NormalizeFFprobeLang_leadingDashBoundary(t *testing.T) {
	if got := NormalizeFFprobeLang("-en", nil); got != "-en" {
		t.Errorf("NormalizeFFprobeLang(%q, nil) = %q, want %q", "-en", got, "-en")
	}
	// Sanity: a normal BCP47 tag truncates at the dash regardless of > vs >=.
	if got := NormalizeFFprobeLang("en-US", nil); got != "en" {
		t.Errorf("NormalizeFFprobeLang(%q, nil) = %q, want %q", "en-US", got, "en")
	}
}

// Test_gk_subflux_u28_ParseProbeOutput_sizeBoundary builds exactly
// maxProbeOutputBytes of valid JSON (an empty object padded with internal
// whitespace). probe.go:141 `if len(data) > maxProbeOutputBytes` is false at
// len == max, so the parse proceeds and succeeds.
// Kills probe.go:141:15 BOUNDARY (`>` -> `>=`: len == max is rejected as
// "ffprobe output too large").
func Test_gk_subflux_u28_ParseProbeOutput_sizeBoundary(t *testing.T) {
	n := maxProbeOutputBytes
	data := bytes.Repeat([]byte(" "), n)
	data[0] = '{'
	data[n-1] = '}'

	tracks, err := ParseProbeOutput(data)
	if err != nil {
		t.Fatalf("ParseProbeOutput(len==maxProbeOutputBytes) error = %v, want nil", err)
	}
	if len(tracks) != 0 {
		t.Errorf("ParseProbeOutput(empty object) tracks = %d, want 0", len(tracks))
	}
}
