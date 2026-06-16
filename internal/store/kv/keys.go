package boltkv

import (
	"bytes"
	"encoding/binary"
	"time"
)

// Be64 encodes v as an 8-byte big-endian value. Big-endian preserves numeric
// ordering under byte-lexicographic comparison, so be64-encoded keys sort by
// their numeric value: surrogate ids sort in insertion order and unixnano
// timestamps sort chronologically. A forward cursor therefore walks oldest to
// newest and a Seek jumps to a numeric cutoff exactly.
func Be64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

// DecodeBe64 decodes an 8-byte big-endian value produced by [Be64]. It returns
// (value, true) for an 8-byte input and (0, false) otherwise.
func DecodeBe64(b []byte) (uint64, bool) {
	if len(b) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(b), true
}

// Join builds a composite key by joining the components with a single [Sep]
// (0x00) byte. Components must be NUL-free UTF-8 text so the separator
// unambiguously delimits boundaries; this keeps prefix scans correct (a scan
// for "mt\x00mid\x00" never matches a value that merely shares a prefix of the
// next component). Pass at least one component for a clean [Split] round-trip.
func Join(parts ...string) []byte {
	if len(parts) == 0 {
		return []byte{}
	}
	n := len(parts) - 1 // separators
	for _, p := range parts {
		n += len(p)
	}
	buf := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			buf = append(buf, Sep)
		}
		buf = append(buf, p...)
	}
	return buf
}

// Split parses a composite key built by [Join] back into its components,
// splitting on the [Sep] byte. It is the inverse of Join for keys built from
// one or more components.
func Split(key []byte) []string {
	parts := bytes.Split(key, []byte{Sep})
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(p)
	}
	return out
}

// TimeIndexKey builds a time-ordered secondary-index key of the form
// be64(unixnano) 0x00 primary. The big-endian timestamp makes a forward cursor
// walk oldest to newest and a Seek to be64(cutoff) land exactly at a time
// cutoff; the primary key is appended (after a [Sep]) so the index entry is
// unique per primary and dereferences back to it. The primary component may
// itself contain NUL bytes (e.g. a composite key) because the fixed 8-byte
// timestamp prefix makes the boundary unambiguous.
//
// The timestamp is t.UnixNano() reinterpreted as uint64; ordering is correct
// for the post-epoch timestamps this store deals with.
func TimeIndexKey(t time.Time, primary []byte) []byte {
	buf := make([]byte, 0, 8+1+len(primary))
	buf = append(buf, Be64(uint64(t.UnixNano()))...)
	buf = append(buf, Sep)
	buf = append(buf, primary...)
	return buf
}

// SplitTimeIndexKey parses a key built by [TimeIndexKey] into its unixnano
// timestamp and primary key. It returns ok=false if the key is too short or is
// missing the separator. The returned primary slice aliases key; copy it if it
// must outlive the enclosing bbolt transaction.
func SplitTimeIndexKey(key []byte) (unixNano int64, primary []byte, ok bool) {
	if len(key) < 9 || key[8] != Sep {
		return 0, nil, false
	}
	unixNano = int64(binary.BigEndian.Uint64(key[:8])) //nolint:gosec // G115: inverse of TimeIndexKey
	primary = key[9:]
	return unixNano, primary, true
}
