package mediasource

import (
	"bytes"
	"encoding"
	"testing"
)

var (
	_ encoding.BinaryMarshaler   = Cursor{}
	_ encoding.BinaryUnmarshaler = (*Cursor)(nil)
)

func TestCursorRoundTrip(t *testing.T) {
	t.Run("the zero cursor is no bytes, and no bytes is the zero cursor", func(t *testing.T) {
		b, err := Cursor{}.MarshalBinary()
		if err != nil || len(b) != 0 {
			t.Fatalf("zero MarshalBinary = %x, %v; want no bytes", b, err)
		}
		c := Cursor{b: []byte{9}}
		if err := c.UnmarshalBinary([]byte{}); err != nil || c.b != nil {
			t.Errorf("UnmarshalBinary(empty) = %v, holding %x; want the zero cursor", err, c.b)
		}
	})
	t.Run("bytes round-trip whatever their version byte", func(t *testing.T) {
		for _, in := range [][]byte{{1, 0x80, 0x01}, {0xff}, {0}} {
			var c Cursor
			if err := c.UnmarshalBinary(in); err != nil {
				t.Fatalf("UnmarshalBinary(%x) = %v; an adapter's Changes judges the version", in, err)
			}
			if out, err := c.MarshalBinary(); err != nil || !bytes.Equal(out, in) {
				t.Errorf("round trip of %x = %x, %v", in, out, err)
			}
		}
	})
	t.Run("neither side aliases the caller's bytes", func(t *testing.T) {
		in := []byte{1, 2, 3}
		var c Cursor
		if err := c.UnmarshalBinary(in); err != nil {
			t.Fatal(err)
		}
		in[0] = 9
		out, _ := c.MarshalBinary()
		out[1] = 9
		if again, _ := c.MarshalBinary(); !bytes.Equal(again, []byte{1, 2, 3}) {
			t.Errorf("cursor holds %x after its input and output were written; want 010203", again)
		}
	})
}
