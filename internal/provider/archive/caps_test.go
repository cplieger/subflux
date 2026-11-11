package archive

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/cplieger/go/httpclient"
)

// The cap tests decode hostile headers in a child process: an uncapped
// decoder allocates gigabytes, so the child runs under limitAddressSpace and
// the parent survives its death and reports it.
const capsCaseEnv = "ARCHIVE_CAPS_CASE"

// capsCase is one hostile input, its decode, whether that decode refuses,
// and the peak heap it stays under: 80 MiB for a decoder's dictionary, and
// 4 MiB for a zip, whose refusal must come before archive/zip allocates per
// record (it takes 16 MiB for 70,000 records and 51 MiB for 196,609).
type capsCase struct {
	data    func() []byte
	decode  func([]byte) error
	maxHeap uint64
	refuses bool
}

var capsCases = map[string]capsCase{
	"rar_dictionary":  {hostileRAR, extractAny, 80 << 20, true},
	"xz_dictionary":   {hostileXZ, decompressAny, 80 << 20, true},
	"xz_hidden_block": {hiddenXZBlock, decompressAny, 80 << 20, true},
	"xz_dictionaries": {xzDictionaryPerBlock, decompressAny, 80 << 20, false},
	"zip_entries":     {manyZipEntries, extractAny, 4 << 20, true},
	"zip_count_lies":  {lyingZipCount, extractAny, 4 << 20, true},
	"zip_size_lies":   {understatedZipSize, extractAny, 4 << 20, true},
}

// capsVerdict is what a child reports: the bytes allocated while decoding,
// with the collector off so the count bounds the peak heap from above.
type capsVerdict struct {
	Allocated uint64
	Err       string
	Permanent bool
}

func TestMain(m *testing.M) {
	if name := os.Getenv(capsCaseEnv); name != "" {
		os.Exit(runCapsChild(name))
	}
	os.Exit(m.Run())
}

func runCapsChild(name string) int {
	c, ok := capsCases[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "no caps case %q\n", name)
		return 2
	}
	data := c.data()
	if err := limitAddressSpace(); err != nil {
		fmt.Fprintf(os.Stderr, "limit address space: %v\n", err)
		return 2
	}
	debug.SetGCPercent(-1)
	before := heapAllocated()
	err := c.decode(data)
	v := capsVerdict{Allocated: heapAllocated() - before, Permanent: httpclient.IsPermanent(err)}
	if err != nil {
		v.Err = err.Error()
	}
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		return 2
	}
	return 0
}

// heapAllocated reads TotalAlloc, which ReadMemStats makes exact by
// flushing every P's cached spans.
func heapAllocated() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.TotalAlloc
}

// runCaps runs one case in a child and requires its outcome, a permanent
// refusal or a decode, under the case's maxHeap.
func runCaps(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), capsCaseEnv+"="+name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("child for %s died: %v\n%s%s", name, err, out, stderr.Bytes())
	}
	var v capsVerdict
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("child for %s answered %q: %v", name, out, err)
	}
	c := capsCases[name]
	if c.refuses && (v.Err == "" || !v.Permanent) {
		t.Errorf("%s: err %q, permanent %v; want a permanent refusal", name, v.Err, v.Permanent)
	}
	if !c.refuses && v.Err != "" {
		t.Errorf("%s: err %q; want a decode", name, v.Err)
	}
	if limit := c.maxHeap; v.Allocated >= limit {
		t.Errorf("%s: allocated %d KiB decoding; want under %d KiB", name, v.Allocated>>10, limit>>10)
	}
	t.Logf("%s: %q after %d KiB", name, v.Err, v.Allocated>>10)
}

func TestRARDictionaryCapped(t *testing.T) { runCaps(t, "rar_dictionary") }

func TestXZDictionaryCapped(t *testing.T) {
	t.Run("declared", func(t *testing.T) { runCaps(t, "xz_dictionary") })
	t.Run("behind_a_lying_index", func(t *testing.T) { runCaps(t, "xz_hidden_block") })
}

func TestZipEntryPreflight(t *testing.T) { runCaps(t, "zip_entries") }

func TestZipEntryCountLies(t *testing.T) {
	t.Run("count", func(t *testing.T) { runCaps(t, "zip_count_lies") })
	t.Run("size", func(t *testing.T) { runCaps(t, "zip_size_lies") })
}

// TestXZDictionaryPerBlockBounded: each block's decoder takes no more
// dictionary than the block's indexed output, so blocks declaring 16 MiB
// each cost the dictionary their output needs.
func TestXZDictionaryPerBlockBounded(t *testing.T) { runCaps(t, "xz_dictionaries") }

// hostileRAR is a solid RAR 5 file header declaring a 4 GiB dictionary
// (compression information: method 1, solid, dictionary 128 KiB << 15).
func hostileRAR() []byte {
	const info = 1<<7 | 0x40 | 15<<10
	return rar5(true, rarFile{name: "a.srt", content: bytes.Repeat([]byte{0x55}, 64), compInfo: info})
}

// hostileXZ is one block whose LZMA2 header declares a 2 GiB dictionary.
func hostileXZ() []byte {
	return xzBuild(capsTB{}, nil, xzBlockSpec{text: []byte(srt), props: xzDict2GiB})
}

// hiddenXZBlock holds a small block, then a block declaring a 2 GiB
// dictionary, under an index listing one block that spans both: a walk led
// by the index sees only the first header.
func hiddenXZBlock() []byte {
	a := xzBuild(capsTB{}, nil, xzBlockSpec{text: []byte(srt), props: xzDict8MiB})
	b := xzBuild(capsTB{}, nil, xzBlockSpec{text: []byte(srt), props: xzDict2GiB})
	blockA := a[xzHeaderLen:xzIndexAt(a)]
	blockB := b[xzHeaderLen:xzIndexAt(b)]
	spanned := xzBuild(capsTB{}, []xzRecord{{unpadded: len(blockA) + len(blockB), uncompressed: len(srt)}})
	out := append(bytes.Clone(spanned[:xzHeaderLen]), blockA...)
	out = append(out, blockB...)
	return append(out, spanned[xzHeaderLen:]...)
}

func xzIndexAt(stream []byte) int {
	backward := (int(binary.LittleEndian.Uint32(stream[len(stream)-8:])) + 1) * 4
	return len(stream) - xzFooterLen - backward
}

// manyZipEntries is a real ZIP64 archive of 70,000 stored entries, under
// the 10 MiB download cap.
func manyZipEntries() []byte {
	files := make([]zipEntry, 70_000)
	for i := range files {
		files[i] = zipEntry{name: "a"}
	}
	return makeZip(capsTB{}, files...)
}

// lyingZipCount is 196,609 empty central-directory records under an end
// record declaring 1: archive/zip compares the count modulo 65,536 only.
func lyingZipCount() []byte {
	const n = 196_609
	out := make([]byte, 0, n*zipDirRecordLen+zipEndLen)
	for range n {
		rec := make([]byte, zipDirRecordLen)
		binary.LittleEndian.PutUint32(rec, zipDirRecordSig)
		out = append(out, rec...)
	}
	end := make([]byte, zipEndLen)
	copy(end, "PK\x05\x06")
	binary.LittleEndian.PutUint16(end[8:], 1)
	binary.LittleEndian.PutUint16(end[10:], 1)
	binary.LittleEndian.PutUint32(end[12:], uint32(len(out)))
	return append(out, end...)
}

// xzDictionaryPerBlock is 1,024 blocks of one byte each, every header
// declaring a 16 MiB dictionary: decoded with the dictionary each header
// declares, it allocates 16 GiB.
func xzDictionaryPerBlock() []byte {
	blocks := make([]xzBlockSpec, 1024)
	for i := range blocks {
		blocks[i] = xzBlockSpec{text: []byte{'a'}, props: xzDict16MiB}
	}
	return xzBuild(capsTB{}, nil, blocks...)
}

// understatedZipSize is 196,608 central-directory records (zero modulo
// 65,536) at offset 0 under an end record declaring none and a directory
// size of 0: archive/zip, finding a record at the declared offset, reads
// all of them from there.
func understatedZipSize() []byte {
	data := lyingZipCount()
	recs := data[:196_608*zipDirRecordLen]
	end := bytes.Clone(data[len(data)-zipEndLen:])
	binary.LittleEndian.PutUint16(end[8:], 0)
	binary.LittleEndian.PutUint16(end[10:], 0)
	binary.LittleEndian.PutUint32(end[12:], 0)
	return append(bytes.Clone(recs), end...)
}

func extractAny(data []byte) error {
	_, err := Extract(data, first)
	return err
}

func decompressAny(data []byte) error {
	_, err := Decompress(data)
	return err
}

// capsTB lets the builders run in a child, where no *testing.T exists: a
// builder failing there is a broken test, so it panics.
type capsTB struct{ testing.TB }

func (capsTB) Helper() {}

func (capsTB) Fatal(args ...any) { panic(fmt.Sprint(args...)) }

func (capsTB) Fatalf(format string, args ...any) { panic(fmt.Sprintf(format, args...)) }
