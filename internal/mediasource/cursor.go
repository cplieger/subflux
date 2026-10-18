package mediasource

import "bytes"

// Cursor is a source's change position, opaque to all but the adapter that
// wrote it, which reads its own format version from the bytes and answers
// Expired for a version it cannot read. The zero Cursor asks for the head.
type Cursor struct{ b []byte }

// MarshalBinary returns a copy of the cursor's bytes, none for the zero Cursor.
func (c Cursor) MarshalBinary() ([]byte, error) { return bytes.Clone(c.b), nil }

// UnmarshalBinary keeps a copy of b, refusing nothing, so a stored cursor in
// an unreadable format reaches the adapter's Expired rather than failing every
// read of it. Empty b is the zero Cursor.
func (c *Cursor) UnmarshalBinary(b []byte) error {
	c.b = nil
	if len(b) > 0 {
		c.b = bytes.Clone(b)
	}
	return nil
}
