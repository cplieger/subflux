package statedb

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Key components (design section 4). Every component is fixed-width or
// length-prefixed, its last included, so a prefix scan over one component
// never reaches a longer one: session s1 never prefixes s10.

// signFlip maps a signed or time value onto uint64 so big-endian byte order
// is numeric order: BypassOwner (-1) sorts before SystemOwner (0).
const signFlip = 1 << 63

func appendUint64(dst []byte, v uint64) []byte { return binary.BigEndian.AppendUint64(dst, v) }

func ownerBytes(owner int64) []byte {
	return appendUint64(nil, uint64(owner)^signFlip) //nolint:gosec // G115: the flip is the encoding
}

// appendTime encodes t's UnixNano, so instants a nanosecond apart order as
// they happened across every second and the epoch.
func appendTime(dst []byte, t time.Time) []byte {
	return appendUint64(dst, uint64(t.UnixNano())^signFlip)
}

func appendText(dst []byte, s string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(s)))
	return append(dst, s...)
}

func appendTag(dst []byte, t language.Tag) []byte { return appendText(dst, t.String()) }

func appendNativeRef(dst []byte, r mediasource.NativeRef) []byte {
	return appendText(append(dst, byte(r.Kind)), string(r.ID))
}

func appendFlags(dst []byte, f language.Flags) []byte { return append(dst, flagsByte(f)) }

// flagsByte is f as the one byte keys and rows store.
func flagsByte(f language.Flags) byte {
	var b byte
	if f.HI {
		b |= flagHI
	}
	if f.Forced {
		b |= flagForced
	}
	return b
}

// flagsOf is flagsByte's inverse for a byte inside flagsAll.
func flagsOf(b byte) language.Flags {
	return language.Flags{HI: b&flagHI != 0, Forced: b&flagForced != 0}
}

const (
	flagHI byte = 1 << iota
	flagForced
	flagsAll = flagHI | flagForced
)

// keyReader decodes components in order; the first failure sticks, so a
// caller reads every component and checks err once, at end.
type keyReader struct {
	err error
	b   []byte
}

func (r *keyReader) fail(what string) {
	if r.err == nil {
		r.err = fmt.Errorf("%w: key %s", bolttable.ErrCorrupt, what)
	}
	r.b = nil
}

func (r *keyReader) fixed(n int, what string) []byte {
	if r.err != nil || len(r.b) < n {
		r.fail(what)
		return nil
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}

func (r *keyReader) uint64(what string) uint64 {
	b := r.fixed(8, what)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func (r *keyReader) text(what string) string {
	if r.err != nil {
		return ""
	}
	n, w := binary.Uvarint(r.b)
	if w <= 0 || n > uint64(len(r.b)-w) { //nolint:gosec // G115: len(r.b) >= w > 0
		r.fail(what + " length")
		return ""
	}
	r.b = r.b[w:]
	return string(r.fixed(int(n), what)) //nolint:gosec // G115: n <= len(r.b), checked above
}

// tag refuses a component whose bytes are not the canonical text of the tag
// they parse to: SetLock and Activate address keys by canonical bytes, so an
// alias such as mo would be a row they never reach.
func (r *keyReader) tag() language.Tag {
	text := r.text("tag")
	if r.err != nil {
		return language.Tag{}
	}
	t, ok := language.Parse(text)
	if !ok || t.String() != text {
		r.fail("tag not canonical")
		return language.Tag{}
	}
	return t
}

func (r *keyReader) source() media.SourceID {
	text := r.text("source")
	var id media.SourceID
	if r.err == nil && id.UnmarshalText([]byte(text)) != nil {
		r.fail("source")
	}
	return id
}

func (r *keyReader) key() media.Key {
	b := r.fixed(len(media.Key{}.Bytes()), "media key")
	if b == nil {
		return media.Key{}
	}
	k, err := media.KeyFromBytes(b)
	if err != nil {
		r.fail("media key")
	}
	return k
}

func (r *keyReader) rootKey() media.RootKey {
	b := r.fixed(len(media.RootKey{}.Bytes()), "root key")
	if b == nil {
		return media.RootKey{}
	}
	k, err := media.RootKeyFromBytes(b)
	if err != nil {
		r.fail("root key")
	}
	return k
}

func (r *keyReader) nativeRef() mediasource.NativeRef {
	kind := r.fixed(1, "root kind")
	if kind == nil {
		return mediasource.NativeRef{}
	}
	ref := mediasource.NativeRef{Kind: media.Kind(kind[0]), ID: mediasource.NativeID(r.text("root id"))}
	if ref.Kind != media.Episode && ref.Kind != media.Movie {
		r.fail("root kind")
	}
	return ref
}

func (r *keyReader) inst() InstanceID {
	id := InstanceID(r.uint64("instance"))
	if r.err == nil && id == 0 {
		r.fail("instance zero")
	}
	return id
}

func (r *keyReader) variant() language.Variant {
	b := r.fixed(1, "variant")
	if b == nil {
		return 0
	}
	v := language.Variant(b[0])
	if v != language.Standard && v != language.HI && v != language.Forced {
		r.fail("variant")
	}
	return v
}

func (r *keyReader) flags() language.Flags {
	b := r.fixed(1, "flags")
	if b == nil {
		return language.Flags{}
	}
	if b[0]&^flagsAll != 0 {
		r.fail("flags")
	}
	return flagsOf(b[0])
}

// end refuses trailing bytes, so one stored key decodes to one key value.
func (r *keyReader) end() error {
	if r.err == nil && len(r.b) != 0 {
		r.fail("trailing bytes")
	}
	return r.err
}
