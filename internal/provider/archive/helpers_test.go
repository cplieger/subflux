package archive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"testing"

	"github.com/ulikunitz/xz/lzma"
)

const srt = "1\n00:00:01,000 --> 00:00:02,000\nHello\n"

// zipEntry is one file of a test zip, stored unless method names another.
type zipEntry struct {
	name    string
	content []byte
	method  uint16
}

// zipLZMA is method 14, whose directory archive/zip reads and whose members
// it cannot decode; makeZip writes such a member's bytes as they are.
const zipLZMA = 14

func makeZip(t testing.TB, files ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	w.RegisterCompressor(zipLZMA, func(out io.Writer) (io.WriteCloser, error) { return nopCloser{out}, nil })
	for _, f := range files {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: f.name, Method: f.method})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", f.name, err)
		}
		if _, err := fw.Write(f.content); err != nil {
			t.Fatalf("zip write %q: %v", f.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// zipDeclaring is a zip of one member whose header declares packed and
// unpacked as its sizes, with content written raw.
func zipDeclaring(t testing.TB, name string, packed, unpacked uint64, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, err := w.CreateRaw(&zip.FileHeader{
		Name: name, Method: zip.Store, CompressedSize64: packed, UncompressedSize64: unpacked,
		CRC32: crc32.ChecksumIEEE(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// rarFile is one file of a RAR 5 archive rar5 writes: compInfo is the
// header's compression information field, 0 for a stored file.
type rarFile struct {
	name     string
	content  []byte
	compInfo uint64
}

// rar5 writes a RAR 5 archive by the format's technical note
// (https://www.rarlab.com/technote.htm): signature, main header, one file
// header per file followed by its bytes, end header.
func rar5(solid bool, files ...rarFile) []byte {
	out := []byte("Rar!\x1a\x07\x01\x00")
	var arcFlags uint64
	if solid {
		arcFlags = 0x0004
	}
	out = rarBlock(out, 1, 0, nil, vints(arcFlags), nil)
	for _, f := range files {
		body := vints(0, uint64(len(f.content)), 0x20, f.compInfo, 1, uint64(len(f.name)))
		body = append(body, f.name...)
		out = rarBlock(out, 2, 0x0002, vints(uint64(len(f.content))), body, f.content)
	}
	return rarBlock(out, 5, 0, nil, vints(0), nil)
}

func rarBlock(out []byte, typ, flags uint64, sizes, body, data []byte) []byte {
	h := append(vints(typ, flags), sizes...)
	h = append(h, body...)
	h = append(vints(uint64(len(h))), h...)
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(h))
	out = append(out, h...)
	return append(out, data...)
}

func vints(vs ...uint64) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.AppendUvarint(b, v)
	}
	return b
}

// xzBlockSpec is one block xzBuild writes: an LZMA2 stream of text, under
// a header declaring props as its LZMA2 dictionary byte, with sizes in the
// header when withSizes is set.
type xzBlockSpec struct {
	text      []byte
	props     byte
	withSizes bool
	// sizeSkew is added to the compressed size the header declares.
	sizeSkew uint64
	// header, when set, edits the block header ahead of its CRC; part edits
	// the assembled block before it is written.
	header func(h []byte) []byte
	part   func(b *xzBlockParts)
}

// xzBlockParts is one block as xzBuild lays it out.
type xzBlockParts struct {
	header, compressed, padding, check []byte
}

// xzBuild assembles an xz stream by the format
// (https://tukaani.org/xz/xz-file-format.txt) with a CRC32 check, its index
// listing the blocks as written unless index overrides it.
func xzBuild(t testing.TB, index []xzRecord, blocks ...xzBlockSpec) []byte {
	t.Helper()
	flags := []byte{0, 0x01}
	out := append([]byte{}, xzMagic...)
	out = append(out, flags...)
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(flags))
	var written []xzRecord
	for _, b := range blocks {
		var c bytes.Buffer
		w, err := lzma.Writer2Config{DictCap: lzma.MinDictCap}.NewWriter2(&c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b.text); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		h := []byte{0, 0x00}
		if b.withSizes {
			h[1] = 0xC0
			h = binary.AppendUvarint(h, uint64(c.Len())+b.sizeSkew)
			h = binary.AppendUvarint(h, uint64(len(b.text)))
		}
		h = append(h, xzLZMA2, 1, b.props)
		if b.header != nil {
			h = b.header(h)
		}
		for (len(h)+4)%4 != 0 {
			h = append(h, 0)
		}
		h[0] = byte((len(h)+4)/4 - 1)
		h = binary.LittleEndian.AppendUint32(h, crc32.ChecksumIEEE(h))
		parts := xzBlockParts{
			header:     h,
			compressed: c.Bytes(),
			padding:    make([]byte, (4-(len(h)+c.Len())%4)%4),
			check:      binary.LittleEndian.AppendUint32(nil, crc32.ChecksumIEEE(b.text)),
		}
		if b.part != nil {
			b.part(&parts)
		}
		for _, p := range [][]byte{parts.header, parts.compressed, parts.padding, parts.check} {
			out = append(out, p...)
		}
		unpadded := len(parts.header) + len(parts.compressed) + len(parts.check)
		written = append(written, xzRecord{unpadded: unpadded, uncompressed: len(b.text)})
	}
	if index == nil {
		index = written
	}
	idx := []byte{0}
	idx = binary.AppendUvarint(idx, uint64(len(index)))
	for _, r := range index {
		idx = binary.AppendUvarint(idx, uint64(r.unpadded))
		idx = binary.AppendUvarint(idx, uint64(r.uncompressed))
	}
	for len(idx)%4 != 0 {
		idx = append(idx, 0)
	}
	idx = binary.LittleEndian.AppendUint32(idx, crc32.ChecksumIEEE(idx))
	out = append(out, idx...)
	foot := binary.LittleEndian.AppendUint32(nil, uint32(len(idx)/4-1))
	foot = append(foot, flags...)
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(foot))
	out = append(out, foot...)
	return append(out, 'Y', 'Z')
}

// Dictionary bytes of an LZMA2 filter property (xz-file-format 5.3.1).
const (
	xzDict8MiB  = 22
	xzDict16MiB = 24
	xzDict24MiB = 25
	xzDict2GiB  = 38
)

// chooser records each choose call and answers pick.
type chooser struct {
	calls [][]string
	pick  int
	err   error
}

func (c *chooser) choose(names []string) (int, error) {
	c.calls = append(c.calls, append([]string(nil), names...))
	return c.pick, c.err
}

func first([]string) (int, error) { return 0, nil }

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
