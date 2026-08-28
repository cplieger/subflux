package provider

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/httpwire"
)

// srtInZip packs one subtitle file into a zip archive, the shape most providers
// hand back for a download.
func srtInZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, err := w.Create(name)
	if err != nil {
		t.Fatalf("zip.Create(%q): %v", name, err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("zip.Write(%q): %v", name, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// TestExtractAndValidate_returnsTheArchivedSubtitle asserts an archived payload
// yields the subtitle from INSIDE the archive: the raw fallback is for bare
// subtitle bodies, and returning the container bytes would hand a binary blob to
// the writer.
func TestExtractAndValidate_returnsTheArchivedSubtitle(t *testing.T) {
	t.Parallel()
	want := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	archived := srtInZip(t, "movie.srt", want)

	got, err := ExtractAndValidate(archived, epmarker.Any())
	if err != nil {
		t.Fatalf("ExtractAndValidate(zip) unexpected error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("ExtractAndValidate(zip) = %q, want the archived subtitle %q", got, want)
	}
}

// TestExtractAndValidate_fallsBackToTheRawBody asserts a bare subtitle body (no
// archive) passes through unchanged.
func TestExtractAndValidate_fallsBackToTheRawBody(t *testing.T) {
	t.Parallel()
	want := []byte("1\n00:00:01,000 --> 00:00:02,000\nBare\n")

	got, err := ExtractAndValidate(want, epmarker.Any())
	if err != nil {
		t.Fatalf("ExtractAndValidate(raw) unexpected error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("ExtractAndValidate(raw) = %q, want %q", got, want)
	}
}

// TestExtractAndValidate_reportsTheArchivesOwnRefusal asserts a zip that opened
// and had nothing for this episode is reported as such. subtitlefile.Validate
// can only answer "detected zip archive" for those bytes, which reads as "this
// build cannot open archives" for a payload the extractor unpacked and walked
// member by member, and that misreading is what the message has to avoid.
func TestExtractAndValidate_reportsTheArchivesOwnRefusal(t *testing.T) {
	t.Parallel()
	archived := srtInZip(t, "Show.S03E02.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"))

	got, err := ExtractAndValidate(archived, epmarker.For(epmarker.Marker{Season: 3, Episode: 9}))
	if got != nil {
		t.Errorf("ExtractAndValidate(pack without S03E09) = %q, want nil", got)
	}
	if err == nil {
		t.Fatal("ExtractAndValidate(pack without S03E09) err = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "S03E09") {
		t.Errorf("err = %q, want the requested episode named", err)
	}
	if !strings.Contains(err.Error(), "S03E02.srt") {
		t.Errorf("err = %q, want the member it did hold named", err)
	}
	if strings.Contains(err.Error(), "binary archive data") {
		t.Errorf("err = %q, want the extractor's refusal rather than Validate's "+
			"container verdict", err)
	}
}

// TestExtractAndValidate_unreadableContainerStillDescribesTheBytes asserts the
// ErrNotArchive fallback survives: when nothing here can open the payload, the
// raw bytes are the whole story and Validate is what names them.
func TestExtractAndValidate_unreadableContainerStillDescribesTheBytes(t *testing.T) {
	t.Parallel()
	// 7z magic: a real container, and one this package deliberately does not
	// unpack, so Validate's format name is the most useful thing anyone can say.
	sevenZip := append([]byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}, bytes.Repeat([]byte{0x00, 0x81}, 300)...)

	got, err := ExtractAndValidate(sevenZip, epmarker.For(epmarker.Marker{Season: 1, Episode: 1}))
	if got != nil {
		t.Errorf("ExtractAndValidate(7z) = %d bytes, want nil", len(got))
	}
	if err == nil {
		t.Fatal("ExtractAndValidate(7z) err = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "7z") {
		t.Errorf("err = %q, want the detected container format named", err)
	}
}

// unreadableZip builds a zip whose members claim the episode and cannot be read,
// in the two shapes that reach the retry classifier differently.
//
// An LZMA member (method 14) is the DIAGNOSIS: Go's archive/zip implements Store
// and Deflate only, so it reads its central directory and fails at Open with
// zip.ErrAlgorithm, which is not transient.
//
// A member whose deflate stream is cut short is the HAZARD: the header records
// the real uncompressed size, so the reader runs out of input and returns
// io.ErrUnexpectedEOF, which httpx.IsTransient reads as a reason to re-download.
// A real pack can carry both, and then the second member is what would have made
// the whole archive look retryable.
//
// Two fixture constraints, both learned by getting them wrong. The bodies must
// resist compression, because the entry gate refuses a member declaring an
// expansion above bombRatio and a repetitive body sails past it. And the LZMA
// member's bytes must not survive into the archive verbatim, because Extract
// falls back to the raw payload when it looks like a subtitle, and a plaintext
// cue in the container made the whole archive read as one.
func unreadableZip(t *testing.T, lzmaName, truncatedName string) []byte {
	t.Helper()
	const lzma = 14
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	w.RegisterCompressor(lzma, func(out io.Writer) (io.WriteCloser, error) {
		return scrambler{out}, nil
	})
	w.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return &clippedDeflate{out: out}, nil
	})
	body := incompressibleCue(4096)
	for _, m := range []struct {
		name   string
		method uint16
	}{{lzmaName, lzma}, {truncatedName, zip.Deflate}} {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: m.name, Method: m.method})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", m.name, err)
		}
		if _, err := fw.Write(body); err != nil {
			t.Fatalf("zip.Write(%q): %v", m.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// incompressibleCue builds n bytes of printable, high-entropy text from a fixed
// sequence, so a deflated copy is about the same size as the original and the
// member's declared expansion stays under the entry gate's bomb ratio.
func incompressibleCue(n int) []byte {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, n)
	x := uint32(0x2545F491)
	for i := range out {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		out[i] = alphabet[x%uint32(len(alphabet))]
	}
	return out
}

// scrambler inverts every byte, so the member's plaintext never appears in the
// container and Extract cannot mistake the archive itself for a subtitle. The
// reader refuses method 14 before decompressing, so the transform is never undone.
type scrambler struct{ w io.Writer }

func (s scrambler) Write(p []byte) (int, error) {
	flipped := make([]byte, len(p))
	for i, b := range p {
		flipped[i] = ^b
	}
	if _, err := s.w.Write(flipped); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (scrambler) Close() error { return nil }

// clippedDeflate drops the last few bytes of the real deflate stream, so the
// member's recorded size and CRC stay honest while the data runs out early. It
// clips a fixed tail rather than half the stream, which keeps the declared
// expansion close to the honest one and under the entry gate's bomb ratio.
type clippedDeflate struct {
	out io.Writer
	buf bytes.Buffer
}

func (c *clippedDeflate) Write(p []byte) (int, error) { return c.buf.Write(p) }

func (c *clippedDeflate) Close() error {
	var full bytes.Buffer
	fw, err := flate.NewWriter(&full, flate.DefaultCompression)
	if err != nil {
		return err
	}
	if _, err := fw.Write(c.buf.Bytes()); err != nil {
		return err
	}
	if err := fw.Close(); err != nil {
		return err
	}
	b := full.Bytes()
	const clip = 24
	if len(b) <= clip {
		return errors.New("clippedDeflate: stream too short to clip")
	}
	_, err = c.out.Write(b[:len(b)-clip])
	return err
}

// TestExtractAndValidate_a_member_read_failure_is_not_transient is the guard on
// the archive chain being reachable at all.
//
// memberReadError wraps its per-member causes so a caller can find
// zip.ErrAlgorithm with errors.Is. It collects EVERY matched member's failure, so
// one truncated member contributes an io.ErrUnexpectedEOF that httpx.IsTransient
// reads as a reason to re-download, even when another member holds the real
// diagnosis. The archive bytes are identical on every attempt, so a retry burns
// two more provider requests and delays the fall-through to the next candidate,
// which is the recovery that works.
//
// ExtractAndValidate marks these permanent on the way out, and IsPermanent is
// answered before any transient test runs. Drop that mark and this test goes red.
func TestExtractAndValidate_a_member_read_failure_is_not_transient(t *testing.T) {
	t.Parallel()
	data := unreadableZip(t, "Show.S01E08.PROPER.srt", "Show.S01E08.REPACK.srt")

	_, err := ExtractAndValidate(data, epmarker.For(epmarker.Marker{Season: 1, Episode: 8}))
	if err == nil {
		t.Fatal("ExtractAndValidate(unreadable members) = nil error, want a read failure")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want a truncated member in the chain: "+
			"without one this test cannot see the retry hazard it guards", err)
	}
	if httpwire.IsTransient(err) {
		t.Errorf("IsTransient(%v) = true, want false: retrying refetches the same bytes", err)
	}
	if !errors.Is(err, zip.ErrAlgorithm) {
		t.Errorf("err = %v, want the chain to still reach zip.ErrAlgorithm", err)
	}
	if !strings.Contains(err.Error(), "failed to read") {
		t.Errorf("err = %q, want the operator-facing wording preserved", err)
	}
}

// TestExtractAndValidate_a_wrong_pack_is_not_transient covers the other refusal
// from an archive that DID open. It carries no wrapped cause today, so nothing
// makes it transient by accident; the assertion exists because the permanent
// mark is applied to the whole default arm rather than to one error type, and a
// future refusal that gains a %w cause must stay non-retryable too.
func TestExtractAndValidate_a_wrong_pack_is_not_transient(t *testing.T) {
	t.Parallel()
	data := srtInZip(t, "Show.S03E01.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"))

	_, err := ExtractAndValidate(data, epmarker.For(epmarker.Marker{Season: 1, Episode: 8}))
	if err == nil {
		t.Fatal("ExtractAndValidate(wrong pack) = nil error, want a refusal")
	}
	if httpwire.IsTransient(err) {
		t.Errorf("IsTransient(%v) = true, want false: the archive holds no such episode", err)
	}
}
