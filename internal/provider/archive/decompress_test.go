package archive

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"hash/crc32"
	"testing"

	"github.com/ulikunitz/xz"
)

func gzipped(t testing.TB, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func xzWritten(t testing.TB, c xz.WriterConfig, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := c.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestDecompress: gzip and xz unwrap to their text, the xz files as XZ Utils
// 5.8.1 and ulikunitz/xz write them (every check type, one block or several,
// sizes in the block headers or not), and other bytes pass through.
func TestDecompress(t *testing.T) {
	t.Parallel()
	sample := readFixture(t, "sample.srt")
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"plain", []byte(srt), []byte(srt)},
		{"empty", []byte{}, []byte{}},
		{"gzip", gzipped(t, sample), sample},
		{"xz_utils_check_none", readFixture(t, "sample-none.srt.xz"), sample},
		{"xz_utils_crc32", readFixture(t, "sample-crc32.srt.xz"), sample},
		{"xz_utils_crc64_sizes_in_header", readFixture(t, "sample-crc64.srt.xz"), sample},
		{"xz_utils_sha256", readFixture(t, "sample-sha256.srt.xz"), sample},
		{"xz_utils_five_blocks", readFixture(t, "sample-blocks.srt.xz"), sample},
		{"xz_utils_16MiB_dictionary", readFixture(t, "sample-dict16.srt.xz"), sample},
		{"ulikunitz", xzWritten(t, xz.WriterConfig{}, sample), sample},
		{"ulikunitz_blocks", xzWritten(t, xz.WriterConfig{BlockSize: 1000, CheckSum: xz.SHA256}, sample), sample},
		{"at_the_cap", xzWritten(t, xz.WriterConfig{}, bytes.Repeat([]byte("a"), maxMember)), bytes.Repeat([]byte("a"), maxMember)},
		{"built_sizes_in_header", xzBuild(t, nil, xzBlockSpec{text: sample, props: xzDict16MiB, withSizes: true}), sample},
		{"empty_block", xzBuild(t, nil, xzBlockSpec{props: xzDict8MiB}, xzBlockSpec{text: sample, props: xzDict8MiB}), sample},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Decompress(tt.in)
			if err != nil || !bytes.Equal(got, tt.want) {
				t.Errorf("Decompress = %d bytes, %v; want %d bytes", len(got), err, len(tt.want))
			}
		})
	}
}

// TestDecompressRefuses: past the cap, a filter or dictionary the decoder
// must not build, and any stream that does not decode exactly, each refuse
// on one permanent line.
func TestDecompressRefuses(t *testing.T) {
	t.Parallel()
	sample := readFixture(t, "sample.srt")
	over := bytes.Repeat([]byte("a"), maxMember+1)
	corrupt := bytes.Clone(readFixture(t, "sample-crc64.srt.xz"))
	corrupt[40] ^= 0xFF
	good := xzBuild(t, nil, xzBlockSpec{text: sample, props: xzDict8MiB})
	manyBlocks := make([]xzBlockSpec, maxEntries+1)
	for i := range manyBlocks {
		manyBlocks[i] = xzBlockSpec{text: []byte{'a'}, props: xzDict8MiB}
	}
	tests := []struct {
		name string
		in   []byte
	}{
		{"gzip_over_cap", gzipped(t, over)},
		{"gzip_corrupt", append(gzipped(t, sample)[:20], 0xFF, 0xFF, 0xFF)},
		{"xz_over_cap", xzWritten(t, xz.WriterConfig{}, over)},
		{"xz_index_over_cap", xzBuild(t, []xzRecord{{unpadded: 1, uncompressed: maxMember + 1}}, xzBlockSpec{text: []byte(srt), props: xzDict8MiB})},
		{"xz_delta_filter", readFixture(t, "sample-delta.srt.xz")},
		{"xz_24MiB_dictionary", readFixture(t, "sample-dict24.srt.xz")},
		{"xz_24MiB_dictionary_built", xzBuild(t, nil, xzBlockSpec{text: sample, props: xzDict24MiB})},
		{"xz_corrupt_block", corrupt},
		{"xz_truncated", good[:len(good)-1]},
		{"xz_trailing_bytes", append(bytes.Clone(good), 0, 0, 0, 0)},
		{"xz_second_stream", append(bytes.Clone(good), good...)},
		{"xz_header_only", good[:xzHeaderLen]},
		{"xz_index_size_lies", xzBuild(t, []xzRecord{{unpadded: 16, uncompressed: len(sample)}}, xzBlockSpec{text: sample, props: xzDict8MiB})},
		{"xz_index_output_lies", xzBuild(t, []xzRecord{{unpadded: len(good), uncompressed: 10}}, xzBlockSpec{text: sample, props: xzDict8MiB})},
		{"xz_index_lists_a_missing_block", xzBuild(t, []xzRecord{{unpadded: 16, uncompressed: 0}})},
		{"xz_header_size_lies", xzBuild(t, nil, xzBlockSpec{text: sample, props: xzDict8MiB, withSizes: true, sizeSkew: 1})},
		{"xz_too_many_blocks", xzBuild(t, nil, manyBlocks...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Decompress(tt.in)
			if got != nil || err == nil {
				t.Fatalf("Decompress = %d bytes, %v; want a refusal", len(got), err)
			}
			assertRefusal(t, err)
		})
	}
}

// xzRefit rewrites a stream's index or footer bytes through edit, then
// refreshes the CRCs the edit invalidated, so only the edit is wrong.
func xzRefit(stream []byte, edit func(index, footer []byte)) []byte {
	out := bytes.Clone(stream)
	at := xzIndexAt(out)
	idx, foot := out[at:len(out)-xzFooterLen], out[len(out)-xzFooterLen:]
	edit(idx, foot)
	binary.LittleEndian.PutUint32(idx[len(idx)-4:], crc32.ChecksumIEEE(idx[:len(idx)-4]))
	binary.LittleEndian.PutUint32(foot, crc32.ChecksumIEEE(foot[4:10]))
	return out
}

// TestXZRefusesEachMalformedPart: every field the xz format checks
// (xz-file-format 1.2.1), broken alone with its CRC kept right, refuses.
func TestXZRefusesEachMalformedPart(t *testing.T) {
	t.Parallel()
	text := []byte(srt + "the padded block.")
	spec := xzBlockSpec{text: text, props: xzDict8MiB}
	good := xzBuild(t, nil, spec)
	if got, err := Decompress(good); err != nil || !bytes.Equal(got, text) {
		t.Fatalf("the unbroken stream: %q, %v", got, err)
	}
	var padLen int
	xzBuild(t, nil, xzBlockSpec{text: text, props: xzDict8MiB, part: func(b *xzBlockParts) { padLen = len(b.padding) }})
	if padLen == 0 {
		t.Fatal("the block needs padding for the padding case")
	}
	with := func(edit func(*xzBlockSpec)) []byte {
		s := spec
		edit(&s)
		return xzBuild(t, nil, s)
	}
	unsupported := bytes.Clone(good)
	unsupported[7] = 0x02
	binary.LittleEndian.PutUint32(unsupported[8:], crc32.ChecksumIEEE(unsupported[6:8]))
	unsupported = xzRefit(unsupported, func(_, foot []byte) { foot[9] = 0x02 })
	for _, tt := range []struct {
		name string
		in   []byte
	}{
		{"stream_header_crc", func() []byte { b := bytes.Clone(good); b[8] ^= 1; return b }()},
		{"check_type_unsupported", unsupported},
		{"footer_flags_differ", xzRefit(good, func(_, foot []byte) { foot[9] = 0x04 })},
		{"index_size_past_the_stream", xzRefit(good, func(_, foot []byte) { binary.LittleEndian.PutUint32(foot[4:], 1<<20) })},
		{"index_crc", func() []byte { b := bytes.Clone(good); b[len(b)-xzFooterLen-1] ^= 1; return b }()},
		{"index_indicator", xzRefit(good, func(idx, _ []byte) { idx[0] = 1 })},
		{"index_padding_nonzero", xzRefit(xzBuild(t, nil, xzBlockSpec{text: readFixture(t, "sample.srt"), props: xzDict8MiB}), func(idx, _ []byte) {
			idx[len(idx)-5] = 1
		})},
		{"index_record_unpadded_zero", xzBuild(t, []xzRecord{{unpadded: 0, uncompressed: len(text)}}, spec)},
		{"block_header_cut_short", xzCutHeader(t)},
		{"block_header_crc", with(func(s *xzBlockSpec) { s.part = func(b *xzBlockParts) { b.header[len(b.header)-1] ^= 1 } })},
		{"block_flags_reserved", with(func(s *xzBlockSpec) { s.header = func(h []byte) []byte { h[1] |= 0x04; return h } })},
		{"block_size_field_overlong", with(func(s *xzBlockSpec) {
			s.header = func(h []byte) []byte {
				return append([]byte{0, h[1] | 0x40, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}, h[2:]...)
			}
		})},
		{"filter_not_lzma2", with(func(s *xzBlockSpec) { s.header = func(h []byte) []byte { h[2] = 0x03; return h } })},
		{"lzma2_props_reserved_bits", with(func(s *xzBlockSpec) { s.props = 0x40 | xzDict8MiB })},
		{"lzma2_props_past_40", with(func(s *xzBlockSpec) { s.props = 41 })},
		{"lzma2_props_trailing_byte", with(func(s *xzBlockSpec) { s.header = func(h []byte) []byte { return append(h, 0, 0, 0, 7) } })},
		{"block_padding_nonzero", with(func(s *xzBlockSpec) { s.part = func(b *xzBlockParts) { b.padding[0] = 1 } })},
		{"block_check", with(func(s *xzBlockSpec) { s.part = func(b *xzBlockParts) { b.check[0] ^= 1 } })},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Decompress(tt.in)
			if got != nil || err == nil {
				t.Fatalf("Decompress = %d bytes, %v; want a refusal", len(got), err)
			}
			assertRefusal(t, err)
		})
	}
}

// TestReadMemberRefusesPastCap: a member longer than maxMember refuses
// whole, never truncated, whatever the container declared.
func TestReadMemberRefusesPastCap(t *testing.T) {
	t.Parallel()
	got, err := readMember("a.srt", bytes.NewReader(bytes.Repeat([]byte("a"), maxMember+1)))
	if got != nil || err == nil {
		t.Fatalf("readMember = %d bytes, %v; want a refusal", len(got), err)
	}
	assertRefusal(t, err)
	if got, err := readMember("a.srt", bytes.NewReader(bytes.Repeat([]byte("a"), maxMember))); err != nil || len(got) != maxMember {
		t.Errorf("readMember at the cap = %d bytes, %v", len(got), err)
	}
}

// xzCutHeader is a stream whose one block header claims 1,024 bytes where
// the stream holds far fewer before its index.
func xzCutHeader(t *testing.T) []byte {
	t.Helper()
	b := xzBuild(t, nil, xzBlockSpec{text: []byte(srt), props: xzDict8MiB})
	b[xzHeaderLen] = 0xFF
	return b
}
