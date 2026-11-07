package ffmpeg

import "bytes"

// maxStderrBytes is how much of a child's stderr the runner keeps: the last
// this many bytes, the only stderr an error carries.
const maxStderrBytes = 64 << 10

// tail is an io.Writer keeping the last maxStderrBytes written to it, so a
// child flooding stderr costs a fixed buffer, never the flood.
type tail struct {
	buf  [maxStderrBytes]byte
	next int // where the next byte goes
	full bool
}

func (t *tail) Write(p []byte) (int, error) {
	n := len(p)
	if n >= len(t.buf) {
		copy(t.buf[:], p[n-len(t.buf):])
		t.next, t.full = 0, true
		return n, nil
	}
	if c := copy(t.buf[t.next:], p); c < n {
		copy(t.buf[:], p[c:])
		t.full = true
	}
	t.next = (t.next + n) % len(t.buf)
	if t.next == 0 {
		t.full = true
	}
	return n, nil
}

// bytes returns the kept bytes in the order they were written.
func (t *tail) bytes() []byte {
	if !t.full {
		return bytes.Clone(t.buf[:t.next])
	}
	return append(bytes.Clone(t.buf[t.next:]), t.buf[:t.next]...)
}
