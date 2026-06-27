package search

import (
	"bytes"
	"testing"
)

// --- Synthetic MKV builder for embedded subtitle tests ---

type testMKVCue struct {
	text       string
	startMs    int64
	durationMs int64
}

// buildTestMKV constructs a minimal MKV file with one SRT subtitle track.
func buildTestMKV(t *testing.T, lang string, cues []testMKVCue) []byte {
	t.Helper()
	var buf bytes.Buffer

	// EBML header (empty).
	mkvWriteElement(&buf, 0x1A45DFA3, nil)

	// Segment content.
	var seg bytes.Buffer

	// Tracks element with one subtitle track.
	var tracks bytes.Buffer
	var entry bytes.Buffer
	mkvWriteUint(&entry, 0xD7, 1)
	mkvWriteUint(&entry, 0x83, 17)
	mkvWriteString(&entry, 0x86, "S_TEXT/UTF8")
	mkvWriteString(&entry, 0x22B59C, lang)
	mkvWriteElement(&tracks, 0xAE, entry.Bytes())
	mkvWriteElement(&seg, 0x1654AE6B, tracks.Bytes())

	// One cluster per cue.
	for _, c := range cues {
		var cluster bytes.Buffer
		mkvWriteUint(&cluster, 0xE7, uint64(c.startMs)) // Cluster Timestamp

		// BlockGroup: BlockDuration + Block.
		var bg bytes.Buffer
		mkvWriteUint(&bg, 0x9B, uint64(c.durationMs))

		var block bytes.Buffer
		block.WriteByte(0x81)
		block.Write([]byte{0x00, 0x00})
		block.WriteByte(0x00)
		block.WriteString(c.text)
		mkvWriteElement(&bg, 0xA1, block.Bytes())

		mkvWriteElement(&cluster, 0xA0, bg.Bytes())
		mkvWriteElement(&seg, 0x1F43B675, cluster.Bytes())
	}

	mkvWriteElement(&buf, 0x18538067, seg.Bytes())
	return buf.Bytes()
}

// mkvWriteElement writes an EBML element (ID + size + data).
func mkvWriteElement(buf *bytes.Buffer, id uint32, data []byte) {
	mkvWriteID(buf, id)
	mkvWriteSize(buf, int64(len(data)))
	buf.Write(data)
}

// mkvWriteID writes an EBML element ID as raw bytes.
func mkvWriteID(buf *bytes.Buffer, id uint32) {
	switch {
	case id <= 0xFF:
		buf.WriteByte(byte(id))
	case id <= 0xFFFF:
		buf.WriteByte(byte(id >> 8))
		buf.WriteByte(byte(id))
	case id <= 0xFFFFFF:
		buf.WriteByte(byte(id >> 16))
		buf.WriteByte(byte(id >> 8))
		buf.WriteByte(byte(id))
	default:
		buf.WriteByte(byte(id >> 24))
		buf.WriteByte(byte(id >> 16))
		buf.WriteByte(byte(id >> 8))
		buf.WriteByte(byte(id))
	}
}

// mkvWriteSize writes an EBML VINT size value.
func mkvWriteSize(buf *bytes.Buffer, size int64) {
	switch {
	case size < 0x7F:
		buf.WriteByte(byte(size) | 0x80)
	case size < 0x3FFF:
		buf.WriteByte(byte(size>>8) | 0x40)
		buf.WriteByte(byte(size))
	case size < 0x1FFFFF:
		buf.WriteByte(byte(size>>16) | 0x20)
		buf.WriteByte(byte(size >> 8))
		buf.WriteByte(byte(size))
	default:
		buf.WriteByte(byte(size>>24) | 0x10)
		buf.WriteByte(byte(size >> 16))
		buf.WriteByte(byte(size >> 8))
		buf.WriteByte(byte(size))
	}
}

// mkvWriteUint writes an EBML uint element.
func mkvWriteUint(buf *bytes.Buffer, id uint32, val uint64) {
	var data [8]byte
	data[0] = byte(val >> 56)
	data[1] = byte(val >> 48)
	data[2] = byte(val >> 40)
	data[3] = byte(val >> 32)
	data[4] = byte(val >> 24)
	data[5] = byte(val >> 16)
	data[6] = byte(val >> 8)
	data[7] = byte(val)
	// Trim leading zeros.
	start := 0
	for start < 7 && data[start] == 0 {
		start++
	}
	mkvWriteElement(buf, id, data[start:])
}

// mkvWriteString writes an EBML string element.
func mkvWriteString(buf *bytes.Buffer, id uint32, s string) {
	mkvWriteElement(buf, id, []byte(s))
}
