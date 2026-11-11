package archive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"
)

const (
	zipDirRecordLen = 46 // + name, extra and comment
	zipEndLen       = 22 // + comment
	zip64LocatorLen = 20
	zip64EndLen     = 56
	zipEndSearch    = 65 * 1024
	zipDirRecordSig = 0x02014b50
	zip64EndSig     = 0x06064b50
	zip64LocatorSig = 0x07064b50
)

// zipMembers opens data as a zip, after walkZipDirectory bounds what
// zip.NewReader will read.
func zipMembers(data []byte) ([]member, error) {
	if err := walkZipDirectory(data); err != nil {
		return nil, err
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, refuse(err, "zip")
	}
	if len(r.File) > maxEntries {
		return nil, refuse(nil, "zip holds %d entries, above the %d cap", len(r.File), maxEntries)
	}
	ms := make([]member, 0, len(r.File))
	for _, f := range r.File {
		ms = append(ms, zipMember(f))
	}
	return ms, nil
}

func zipMember(f *zip.File) member {
	packed, packedOK := declaredSize(f.CompressedSize64)
	unpacked, unpackedOK := declaredSize(f.UncompressedSize64)
	return member{
		name:        f.Name,
		packed:      packed,
		unpacked:    unpacked,
		sizeUnknown: !packedOK || !unpackedOK,
		isDir:       f.FileInfo().IsDir(),
		read:        func() ([]byte, error) { return readZipMember(f) },
	}
}

// readZipMember reports a member archive/zip cannot decode (LZMA, PPMd:
// zip.ErrAlgorithm) as a refusal wrapping that cause.
func readZipMember(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, refuse(err, "open member %s", f.Name)
	}
	defer rc.Close()
	return readMember(f.Name, rc)
}

// walkZipDirectory counts the central-directory records zip.NewReader would
// read, from wherever it would start reading them, and refuses a count
// above maxEntries or one the end record contradicts. zip.NewReader stops at
// the first record without the record signature or cut short, as this walk
// does, so it reads no more records than the walk counted. It answers
// errNoContainer when data holds no end record.
func walkZipDirectory(data []byte) error {
	dir, ok, err := readZipEnd(data)
	if !ok {
		return errNoContainer
	}
	if err != nil {
		return err
	}
	// zip.NewReader reads from the end record less the directory's size,
	// which absorbs bytes prepended to the archive, unless the declared
	// offset is lower and a record already sits there; both starts are
	// walked, since this walk's record check is looser than the reader's.
	start := dir.endAt - dir.size
	if dir.size < 0 || start < 0 {
		return refuse(nil, "zip central directory does not fit the %d bytes", len(data))
	}
	starts := []int{start}
	if dir.offset >= 0 && dir.offset < start && zipRecordAt(data, dir.offset) {
		starts = []int{dir.offset, start}
	}
	for _, s := range starts {
		if n := countZipRecords(data, s); n > maxEntries {
			return refuse(nil, "zip central directory holds more than %d records", maxEntries)
		}
	}
	if n := countZipRecords(data, starts[0]); n != dir.records {
		return refuse(nil, "zip central directory holds %d records where its end record declares %s", n, dir.declared())
	}
	return nil
}

// zipEnd is the end record's fields, each -1 when it overflows the bytes
// it could describe (records past maxEntries+1, size or offset past the
// data).
type zipEnd struct {
	endAt   int // the end record, or the ZIP64 end record when there is one
	records int
	size    int
	offset  int
}

func (d zipEnd) declared() string {
	if d.records < 0 {
		return fmt.Sprintf("more than %d", maxEntries)
	}
	return strconv.Itoa(d.records)
}

// readZipEnd reads the end record as archive/zip finds it: the last
// signature in the final zipEndSearch bytes whose comment fits, then the
// ZIP64 end record its locator names when a classic field is saturated. ok
// is false when there is no end record at all.
func readZipEnd(data []byte) (dir zipEnd, ok bool, err error) {
	at := -1
	for i := len(data) - zipEndLen; i >= max(0, len(data)-zipEndSearch); i-- {
		if string(data[i:i+4]) == "PK\x05\x06" {
			if int(binary.LittleEndian.Uint16(data[i+20:]))+zipEndLen+i <= len(data) {
				at = i
			}
			break
		}
	}
	if at < 0 {
		return zipEnd{}, false, nil
	}
	b := data[at+8:]
	dir = zipEnd{
		endAt:   at,
		records: leWithin(b[2:4], maxEntries+1),
		size:    leWithin(b[4:8], len(data)),
		offset:  leWithin(b[8:12], len(data)),
	}
	if string(b[2:4]) == "\xff\xff" || string(b[4:8]) == "\xff\xff\xff\xff" || string(b[8:12]) == "\xff\xff\xff\xff" {
		if dir, err = readZip64End(data, dir); err != nil {
			return zipEnd{}, true, err
		}
	}
	return dir, true, nil
}

// readZip64End follows a valid ZIP64 locator before the end record, as
// archive/zip does, keeping the classic fields when there is none.
func readZip64End(data []byte, dir zipEnd) (zipEnd, error) {
	loc := dir.endAt - zip64LocatorLen
	if loc < 0 {
		return dir, nil
	}
	l := data[loc:]
	if binary.LittleEndian.Uint32(l) != zip64LocatorSig ||
		binary.LittleEndian.Uint32(l[4:]) != 0 || binary.LittleEndian.Uint32(l[16:]) != 1 {
		return dir, nil
	}
	p := leWithin(l[8:16], len(data)-zip64EndLen)
	if p < 0 {
		return zipEnd{}, refuse(nil, "zip64 end record is outside the %d bytes", len(data))
	}
	e := data[p:]
	if binary.LittleEndian.Uint32(e) != zip64EndSig {
		return zipEnd{}, refuse(nil, "zip64 end record at %d has no signature", p)
	}
	return zipEnd{
		endAt:   p,
		records: leWithin(e[32:40], maxEntries+1),
		size:    leWithin(e[40:48], len(data)),
		offset:  leWithin(e[48:56], len(data)),
	}, nil
}

// leWithin reads b as a little-endian unsigned integer, -1 when it is above
// limit.
func leWithin(b []byte, limit int) int {
	v := 0
	for _, c := range slices.Backward(b) {
		if v > limit>>8 {
			return -1
		}
		v = v<<8 | int(c)
	}
	if v > limit {
		return -1
	}
	return v
}

// countZipRecords counts the whole records from start, stopping one past
// maxEntries.
func countZipRecords(data []byte, start int) int {
	n := 0
	for p := start; zipRecordAt(data, p) && n <= maxEntries; n++ {
		p += zipRecordLen(data[p:])
	}
	return n
}

func zipRecordAt(data []byte, p int) bool {
	if p < 0 || len(data)-p < zipDirRecordLen {
		return false
	}
	r := data[p:]
	return binary.LittleEndian.Uint32(r) == zipDirRecordSig && zipRecordLen(r) <= len(r)
}

func zipRecordLen(r []byte) int {
	return zipDirRecordLen + int(binary.LittleEndian.Uint16(r[28:])) +
		int(binary.LittleEndian.Uint16(r[30:])) + int(binary.LittleEndian.Uint16(r[32:]))
}
