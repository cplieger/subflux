package archive

import (
	"bytes"
	"errors"
	"testing"

	"github.com/cplieger/go/httpclient"
)

// FuzzExtract_bounded: any bytes and any choice give a member of at most
// maxMember bytes, ErrNotArchive unmarked, or a permanent refusal, and
// choose is called at most once with names it may index.
func FuzzExtract_bounded(f *testing.F) {
	for _, n := range []string{"test.rar", "test_hidden.rar", "test_with_dir.rar"} {
		f.Add(readFixture(f, n), uint8(0))
	}
	f.Add(makeZip(f, zipEntry{name: "a.srt", content: []byte(srt)}, zipEntry{name: "b.srt", content: []byte(srt)}), uint8(1))
	f.Add(rar5(false, rarFile{name: "a.srt", content: []byte(srt)}), uint8(0))
	f.Add(append(bytes.Repeat([]byte{0x90}, 64), makeZip(f, zipEntry{name: "a.srt", content: []byte(srt)})...), uint8(0))
	f.Add(lyingZipCount()[len(lyingZipCount())-zipEndLen-zipDirRecordLen*3:], uint8(0))
	f.Add([]byte(srt), uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, pick uint8) {
		calls := 0
		got, err := Extract(data, func(names []string) (int, error) {
			calls++
			if len(names) == 0 {
				t.Fatal("choose called with no names")
			}
			return int(pick) % (len(names) + 1), nil
		})
		if calls > 1 {
			t.Fatalf("choose called %d times", calls)
		}
		switch {
		case err == nil:
			if len(got) == 0 || len(got) > maxMember {
				t.Fatalf("Extract gave %d bytes", len(got))
			}
		case errors.Is(err, ErrNotArchive):
			if got != nil || httpclient.IsPermanent(err) || calls != 0 {
				t.Fatalf("ErrNotArchive with %d bytes, permanent %v, %d calls", len(got), httpclient.IsPermanent(err), calls)
			}
		case !httpclient.IsPermanent(err) || got != nil:
			t.Fatalf("refusal %v with %d bytes is not permanent", err, len(got))
		}
	})
}

// FuzzDecompress_bounded: any bytes decompress to at most maxMember bytes or
// refuse permanently, and bytes with no gzip or xz magic come back as they
// were.
func FuzzDecompress_bounded(f *testing.F) {
	for _, n := range []string{"sample-crc32.srt.xz", "sample-blocks.srt.xz", "sample-delta.srt.xz"} {
		f.Add(readFixture(f, n))
	}
	f.Add(gzipped(f, []byte(srt)))
	f.Add(xzBuild(f, nil, xzBlockSpec{text: []byte(srt), props: xzDict8MiB, withSizes: true}))
	f.Add([]byte(srt))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := Decompress(data)
		if err != nil {
			if got != nil || !httpclient.IsPermanent(err) {
				t.Fatalf("refusal %v with %d bytes is not permanent", err, len(got))
			}
			return
		}
		if !bytes.HasPrefix(data, xzMagic) && !bytes.HasPrefix(data, gzipMagic) {
			if !bytes.Equal(got, data) {
				t.Fatal("plain bytes changed")
			}
			return
		}
		if len(got) > maxMember {
			t.Fatalf("Decompress gave %d bytes", len(got))
		}
	})
}
