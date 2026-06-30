package kv

import (
	"bytes"
	"math"
	"slices"
	"testing"
	"time"
)

// TestBe64_orderingMatchesNumericOrder asserts the central property that makes
// big-endian keys useful: byte-lexicographic order equals numeric order.
func TestBe64_orderingMatchesNumericOrder(t *testing.T) {
	vals := []uint64{0, 1, 2, 255, 256, 1 << 16, 1 << 32, math.MaxUint64 - 1, math.MaxUint64}
	for i := range len(vals) - 1 {
		a, b := vals[i], vals[i+1]
		if cmp := bytes.Compare(Be64(a), Be64(b)); cmp >= 0 {
			t.Errorf("Be64(%d) vs Be64(%d): bytes.Compare = %d, want < 0", a, b, cmp)
		}
	}

	// Pairwise: the byte comparison sign must match the numeric comparison sign
	// for every ordered pair.
	for _, a := range vals {
		for _, b := range vals {
			gotByte := bytes.Compare(Be64(a), Be64(b))
			var wantNum int
			switch {
			case a < b:
				wantNum = -1
			case a > b:
				wantNum = 1
			}
			gotNum := 0
			if gotByte < 0 {
				gotNum = -1
			} else if gotByte > 0 {
				gotNum = 1
			}
			if gotNum != wantNum {
				t.Errorf("Be64 ordering mismatch for (%d,%d): byteSign=%d numSign=%d", a, b, gotNum, wantNum)
			}
		}
	}
}

func TestBe64_roundTrip(t *testing.T) {
	vals := []uint64{0, 1, 42, 1 << 20, math.MaxUint64}
	for _, v := range vals {
		got, ok := DecodeBe64(Be64(v))
		if !ok || got != v {
			t.Errorf("DecodeBe64(Be64(%d)) = (%d, %v), want (%d, true)", v, got, ok, v)
		}
	}
	if _, ok := DecodeBe64([]byte{1, 2, 3}); ok {
		t.Error("DecodeBe64 on a 3-byte slice: ok = true, want false")
	}
}

func TestJoinSplit_roundTrip(t *testing.T) {
	cases := [][]string{
		{"movie"},
		{"movie", "tmdb-123"},
		{"episode", "tvdb-99-s01e02", "fr"},
		{"episode", "tvdb-99-s01e02", "fr", "opensubtitles"},
		{"a", "", "b"}, // empty middle component round-trips
	}
	for _, parts := range cases {
		got := Split(Join(parts...))
		if !slices.Equal(got, parts) {
			t.Errorf("Split(Join(%q)) = %q, want %q", parts, got, parts)
		}
	}
}

// TestJoin_prefixDisambiguation guards prefix-scan correctness: a key for "tt1"
// must not be a byte-prefix of a key for "tt12" once the separator is present.
func TestJoin_prefixDisambiguation(t *testing.T) {
	short := Join("episode", "tt1", "fr")
	long := Join("episode", "tt12", "fr")
	prefix := append(Join("episode", "tt1"), Sep)
	if !bytes.HasPrefix(short, prefix) {
		t.Errorf("expected %q to have prefix %q", short, prefix)
	}
	if bytes.HasPrefix(long, prefix) {
		t.Errorf("did not expect %q to match the tt1 prefix %q", long, prefix)
	}
}

func TestTimeIndexKey_orderingAndSplit(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := base
	t2 := base.Add(time.Nanosecond)
	t3 := base.Add(time.Hour)
	primary := Join("episode", "tvdb-1-s01e01")

	k1 := TimeIndexKey(t1, primary)
	k2 := TimeIndexKey(t2, primary)
	k3 := TimeIndexKey(t3, primary)

	if bytes.Compare(k1, k2) >= 0 || bytes.Compare(k2, k3) >= 0 {
		t.Errorf("time-index keys not in chronological byte order: %x %x %x", k1, k2, k3)
	}

	// Round-trip: timestamp and primary recovered, even though primary itself
	// contains NUL separators.
	gotNano, gotPrimary, ok := SplitTimeIndexKey(k3)
	if !ok {
		t.Fatal("SplitTimeIndexKey returned ok = false")
	}
	if gotNano != t3.UnixNano() {
		t.Errorf("recovered unixnano = %d, want %d", gotNano, t3.UnixNano())
	}
	if !bytes.Equal(gotPrimary, primary) {
		t.Errorf("recovered primary = %x, want %x", gotPrimary, primary)
	}

	if _, _, ok := SplitTimeIndexKey([]byte{1, 2, 3}); ok {
		t.Error("SplitTimeIndexKey on a too-short key: ok = true, want false")
	}
}
