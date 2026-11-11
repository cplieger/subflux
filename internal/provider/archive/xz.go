package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash/crc32"
	"hash/crc64"
	"io"

	"github.com/ulikunitz/xz/lzma"
)

// The xz container (https://tukaani.org/xz/xz-file-format.txt) is read here
// and only LZMA2 is left to ulikunitz/xz/lzma: xz.NewReader treats
// ReaderConfig.DictCap as a floor, allocating whatever dictionary each block
// header declares as it reaches the block, and checks the index against the
// blocks only after the last one. So a lying index could hide a block header
// from any walk ahead of it; reading the blocks in order bounds each one's
// dictionary before its decoder exists.
const (
	xzHeaderLen     = 12
	xzFooterLen     = 12
	xzLZMA2         = 0x21
	maxXZDictionary = 16 << 20
)

var (
	xzMagic   = []byte{0xFD, '7', 'z', 'X', 'Z', 0}
	crc64ECMA = crc64.MakeTable(crc64.ECMA)
)

type xzRecord struct{ unpadded, uncompressed int }

// decompressXZ decodes one xz stream that fills data exactly.
func decompressXZ(data []byte) ([]byte, error) {
	if len(data) < xzHeaderLen+xzFooterLen {
		return nil, refuse(nil, "xz stream is %d bytes", len(data))
	}
	flags := data[6:8]
	if flags[0] != 0 || flags[1]&0xF0 != 0 || crc32.ChecksumIEEE(flags) != binary.LittleEndian.Uint32(data[8:]) {
		return nil, refuse(nil, "xz stream header is invalid")
	}
	check, checkLen, ok := xzCheck(flags[1])
	if !ok {
		return nil, refuse(nil, "xz check type %d is unsupported", flags[1])
	}
	indexAt, err := xzFooter(data, flags)
	if err != nil {
		return nil, err
	}
	records, err := xzIndex(data[indexAt:len(data)-xzFooterLen], len(data))
	if err != nil {
		return nil, err
	}
	total := 0
	for _, r := range records {
		total += r.uncompressed
	}
	if total > maxMember {
		return nil, refuse(nil, "xz output is above the %d-byte cap", maxMember)
	}
	out := bytes.NewBuffer(make([]byte, 0, total))
	pos := xzHeaderLen
	for i, rec := range records {
		n, err := xzBlock(data[pos:indexAt], rec, check, checkLen, out)
		if err != nil {
			return nil, refuse(err, "xz block %d", i+1)
		}
		pos += n
	}
	if pos != indexAt {
		return nil, refuse(nil, "xz blocks end at %d, the index starts at %d", pos, indexAt)
	}
	return out.Bytes(), nil
}

// xzFooter checks the stream footer and answers where the index it locates
// starts.
func xzFooter(data, flags []byte) (int, error) {
	f := data[len(data)-xzFooterLen:]
	if string(f[10:]) != "YZ" || !bytes.Equal(f[8:10], flags) || crc32.ChecksumIEEE(f[4:10]) != binary.LittleEndian.Uint32(f) {
		return 0, refuse(nil, "xz stream footer is invalid")
	}
	size := (int64(binary.LittleEndian.Uint32(f[4:])) + 1) * 4
	if size > int64(len(data)-xzHeaderLen-xzFooterLen) {
		return 0, refuse(nil, "xz index size %d does not fit the stream", size)
	}
	return len(data) - xzFooterLen - int(size), nil
}

// xzIndex reads the index: at most maxEntries records, each unpadded size
// within the stream's and each output within maxMember.
func xzIndex(idx []byte, streamLen int) ([]xzRecord, error) {
	if idx[0] != 0 || crc32.ChecksumIEEE(idx[:len(idx)-4]) != binary.LittleEndian.Uint32(idx[len(idx)-4:]) {
		return nil, refuse(nil, "xz index is invalid")
	}
	b := idx[1 : len(idx)-4]
	count, ok := readVLI(&b, maxEntries)
	if !ok {
		return nil, refuse(nil, "xz index lists more than %d blocks", maxEntries)
	}
	records := make([]xzRecord, 0, count)
	for range count {
		u, ok1 := readVLI(&b, streamLen)
		c, ok2 := readVLI(&b, maxMember)
		if !ok1 || !ok2 || u == 0 {
			return nil, refuse(nil, "xz index record is invalid or above the caps")
		}
		records = append(records, xzRecord{unpadded: u, uncompressed: c})
	}
	if len(b) > 3 || !allZero(b) {
		return nil, refuse(nil, "xz index padding is invalid")
	}
	return records, nil
}

// xzBlock decodes the block opening region into out and returns the bytes it
// spans with its padding and check. Its header must declare one LZMA2
// filter with a dictionary of at most maxXZDictionary; its decoder gets no
// more dictionary than the block's indexed output needs, since LZMA2 resets
// its dictionary at each block.
func xzBlock(region []byte, rec xzRecord, check func([]byte) []byte, checkLen int, out *bytes.Buffer) (int, error) {
	if len(region) == 0 || region[0] == 0 {
		return 0, refuse(nil, "the index lists a block the stream does not hold")
	}
	hlen := (int(region[0]) + 1) * 4
	if hlen > len(region) {
		return 0, refuse(nil, "header is cut short")
	}
	h := region[:hlen]
	if crc32.ChecksumIEEE(h[:hlen-4]) != binary.LittleEndian.Uint32(h[hlen-4:]) {
		return 0, refuse(nil, "header checksum is wrong")
	}
	declared, dict, err := xzBlockHeader(h[1:hlen-4], len(region))
	if err != nil {
		return 0, err
	}
	r := bytes.NewReader(region[hlen:])
	lr, err := lzma.Reader2Config{DictCap: max(min(dict, rec.uncompressed), lzma.MinDictCap)}.NewReader2(r)
	if err != nil {
		return 0, refuse(err, "LZMA2")
	}
	start := out.Len()
	if _, err := io.Copy(out, io.LimitReader(lr, int64(rec.uncompressed)+1)); err != nil {
		return 0, refuse(err, "LZMA2")
	}
	got := out.Bytes()[start:]
	compressed := len(region) - hlen - r.Len()
	if len(got) != rec.uncompressed || !declared.agrees(compressed, len(got)) {
		return 0, refuse(nil, "sizes contradict its header or the index")
	}
	pad := (4 - (hlen+compressed)%4) % 4
	end := hlen + compressed + pad + checkLen
	if end > len(region) || !allZero(region[hlen+compressed:hlen+compressed+pad]) {
		return 0, refuse(nil, "padding is invalid")
	}
	if hlen+compressed+checkLen != rec.unpadded {
		return 0, refuse(nil, "size contradicts the index")
	}
	if checkLen > 0 && !bytes.Equal(check(got), region[end-checkLen:end]) {
		return 0, refuse(nil, "check is wrong")
	}
	return end, nil
}

// xzSizes are a block header's optional sizes, -1 when absent.
type xzSizes struct{ compressed, uncompressed int }

func (s xzSizes) agrees(compressed, uncompressed int) bool {
	return (s.compressed < 0 || s.compressed == compressed) &&
		(s.uncompressed < 0 || s.uncompressed == uncompressed)
}

// xzBlockHeader reads a block header's fields after its size byte, refusing
// any filter chain but a lone LZMA2 and any dictionary above
// maxXZDictionary; regionLen bounds the compressed size it may declare.
func xzBlockHeader(b []byte, regionLen int) (xzSizes, int, error) {
	flags := b[0]
	b = b[1:]
	if flags&0x3C != 0 {
		return xzSizes{}, 0, refuse(nil, "header flags are invalid")
	}
	sizes, ok := xzHeaderSizes(&b, flags, regionLen)
	if !ok {
		return xzSizes{}, 0, refuse(nil, "header size fields are invalid or above the caps")
	}
	if flags&0x03 != 0 {
		return xzSizes{}, 0, refuse(nil, "filter chain of %d is not a lone LZMA2", flags&0x03+1)
	}
	id, ok1 := readVLI(&b, xzLZMA2)
	props, ok2 := readVLI(&b, 1)
	if !ok1 || !ok2 || id != xzLZMA2 || props != 1 || len(b) < 1 {
		return xzSizes{}, 0, refuse(nil, "filter is not LZMA2")
	}
	d := b[0]
	if d&0xC0 != 0 || d > 40 || !allZero(b[1:]) {
		return xzSizes{}, 0, refuse(nil, "LZMA2 properties are invalid")
	}
	if d > xzDictMaxProp {
		return xzSizes{}, 0, refuse(nil, "LZMA2 dictionary of %d bytes is above the %d-byte cap", lzma2Dict(d), maxXZDictionary)
	}
	return sizes, (2 | int(d&1)) << (d/2 + 11), nil
}

// xzDictMaxProp is the LZMA2 dictionary property naming maxXZDictionary.
const xzDictMaxProp = 24

// lzma2Dict is the dictionary an LZMA2 property byte names
// (xz-file-format 5.3.1); xzBlockHeader computes the ones it accepts as int.
func lzma2Dict(d byte) uint64 {
	if d >= 40 {
		return 0xFFFFFFFF
	}
	return uint64(2|d&1) << (d/2 + 11)
}

func xzHeaderSizes(b *[]byte, flags byte, regionLen int) (xzSizes, bool) {
	sizes := xzSizes{compressed: -1, uncompressed: -1}
	ok := true
	if flags&0x40 != 0 {
		sizes.compressed, ok = readVLI(b, regionLen)
	}
	if ok && flags&0x80 != 0 {
		sizes.uncompressed, ok = readVLI(b, maxMember)
	}
	return sizes, ok
}

// xzCheck answers how a supported check type is stored for a block's
// output, and its length: the CRCs little-endian, SHA-256 as is.
func xzCheck(t byte) (stored func([]byte) []byte, n int, ok bool) {
	switch t {
	case 0x00:
		return nil, 0, true
	case 0x01:
		return func(b []byte) []byte { return binary.LittleEndian.AppendUint32(nil, crc32.ChecksumIEEE(b)) }, crc32.Size, true
	case 0x04:
		return func(b []byte) []byte { return binary.LittleEndian.AppendUint64(nil, crc64.Checksum(b, crc64ECMA)) }, crc64.Size, true
	case 0x0A:
		return func(b []byte) []byte { s := sha256.Sum256(b); return s[:] }, sha256.Size, true
	}
	return nil, 0, false
}

// readVLI reads one of xz's variable-length integers off the front of b,
// refusing a non-minimal or over-long encoding and a value above limit.
func readVLI(b *[]byte, limit int) (int, bool) {
	v := 0
	for i := 0; i < 9 && i < len(*b); i++ {
		c := (*b)[i]
		digit := int(c & 0x7F)
		if digit > (limit-v)>>(7*i) {
			return 0, false
		}
		v += digit << (7 * i)
		if c&0x80 == 0 {
			if i > 0 && c == 0 {
				return 0, false
			}
			*b = (*b)[i+1:]
			return v, true
		}
	}
	return 0, false
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
