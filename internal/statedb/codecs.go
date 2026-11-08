package statedb

import (
	"encoding/json"
	"errors"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// taggedVersion is the Version of a codec holding a tag: own times 2^16 plus
// language.FormatVersion, so a change to any tag's canonical text changes
// core's digest and resets it (DECISIONS 4).
func taggedVersion(own uint16) uint32 { return uint32(own)<<16 | uint32(language.FormatVersion) }

// timedVersion is the Version of a codec holding timing data: own in the top
// byte, timing.CodecVersion in the next, language.FormatVersion in the low 16
// bits, so a change to either format resets core.
func timedVersion(own uint8) uint32 {
	return uint32(own)<<24 | uint32(timingCodec)<<16 | uint32(language.FormatVersion)
}

// timingCodec fails to compile once timing.CodecVersion passes 255, the width
// timedVersion gives it.
const timingCodec = uint8(timing.CodecVersion)

// The conversions in taggedVersion and timedVersion accept any integer type,
// so this pins FormatVersion's declared width.
var _ uint16 = language.FormatVersion

// keyCodec is a bolttable.KeyCodec over the component appenders and
// keyReader of keys.go.
type keyCodec[K any] struct {
	put     func(dst []byte, k K) []byte
	parse   func(r *keyReader) K
	version uint32
}

func (c keyCodec[K]) Version() uint32 { return c.version }

func (c keyCodec[K]) AppendKey(dst []byte, k K) []byte { return c.put(dst, k) }

func (c keyCodec[K]) ParseKey(b []byte) (K, error) {
	r := keyReader{b: b}
	k := c.parse(&r)
	if err := r.end(); err != nil {
		var zero K
		return zero, err
	}
	return k, nil
}

// constKey is the one key of a single-row table; bbolt refuses an empty key.
type constKey struct{}

var constKeys = keyCodec[constKey]{
	version: 1,
	put:     func(dst []byte, _ constKey) []byte { return append(dst, 0) },
	parse: func(r *keyReader) constKey {
		if b := r.fixed(1, "constant"); len(b) == 1 && b[0] != 0 {
			r.fail("constant")
		}
		return constKey{}
	},
}

var instKeys = keyCodec[InstanceID]{
	version: 1,
	put:     func(dst []byte, id InstanceID) []byte { return appendUint64(dst, uint64(id)) },
	parse:   (*keyReader).inst,
}

var mediaKeys = keyCodec[media.Key]{
	version: 1,
	put:     func(dst []byte, k media.Key) []byte { return append(dst, k.Bytes()...) },
	parse:   (*keyReader).key,
}

var rootKeys = keyCodec[media.RootKey]{
	version: 1,
	put:     func(dst []byte, k media.RootKey) []byte { return append(dst, k.Bytes()...) },
	parse:   (*keyReader).rootKey,
}

var sourceKeys = keyCodec[media.SourceID]{
	version: 1,
	put:     func(dst []byte, s media.SourceID) []byte { return appendText(dst, string(s)) },
	parse:   (*keyReader).source,
}

// textKeys keys a table by one opaque string: a provider or a job id.
var textKeys = keyCodec[string]{
	version: 1,
	put:     appendText,
	parse:   func(r *keyReader) string { return r.text("text") },
}

var jobKeys = keyCodec[status.JobID]{
	version: 1,
	put:     func(dst []byte, id status.JobID) []byte { return appendText(dst, string(id)) },
	parse:   func(r *keyReader) status.JobID { return status.JobID(r.text("job id")) },
}

// rootRef names one root of one source: the roots and root_evidence key.
type rootRef struct {
	Source media.SourceID
	Root   mediasource.NativeRef
}

func appendRootRef(dst []byte, k rootRef) []byte {
	return appendNativeRef(appendText(dst, string(k.Source)), k.Root)
}

func readRootRef(r *keyReader) rootRef { return rootRef{Source: r.source(), Root: r.nativeRef()} }

var rootRefKeys = keyCodec[rootRef]{version: 1, put: appendRootRef, parse: readRootRef}

// itemRef names one item of a source's root: the sightings, stamps and
// overlaps key. A stamp's Item is empty for the root's own stamp.
type itemRef struct {
	Item mediasource.NativeID
	rootRef
}

var itemRefKeys = keyCodec[itemRef]{
	version: 1,
	put:     func(dst []byte, k itemRef) []byte { return appendText(appendRootRef(dst, k.rootRef), string(k.Item)) },
	parse: func(r *keyReader) itemRef {
		return itemRef{rootRef: readRootRef(r), Item: mediasource.NativeID(r.text("item id"))}
	},
}

// fileKey names one managed file of an instance: files, baselines, intents.
type fileKey struct {
	Name string
	Inst InstanceID
}

var fileKeys = keyCodec[fileKey]{
	version: 1,
	put:     func(dst []byte, k fileKey) []byte { return appendText(appendUint64(dst, uint64(k.Inst)), k.Name) },
	parse:   func(r *keyReader) fileKey { return fileKey{Inst: r.inst(), Name: r.text("name")} },
}

// lockKey names one locked pair of one item.
type lockKey struct {
	Tag     language.Tag
	Key     media.Key
	Variant language.Variant
}

var lockKeys = keyCodec[lockKey]{
	version: taggedVersion(1),
	put: func(dst []byte, k lockKey) []byte {
		return append(appendTag(append(dst, k.Key.Bytes()...), k.Tag), byte(k.Variant))
	},
	parse: func(r *keyReader) lockKey { return lockKey{Key: r.key(), Tag: r.tag(), Variant: r.variant()} },
}

var ordinalKeys = keyCodec[OrdinalKey]{
	version: taggedVersion(1),
	put: func(dst []byte, k OrdinalKey) []byte {
		return appendFlags(appendTag(appendUint64(dst, uint64(k.Inst)), k.Tag), k.Flags)
	},
	parse: func(r *keyReader) OrdinalKey { return OrdinalKey{Inst: r.inst(), Tag: r.tag(), Flags: r.flags()} },
}

// noResultKey names one item's no-result memory for one target and provider.
type noResultKey struct {
	Provider string
	Tag      language.Tag
	Key      media.Key
}

var noResultKeys = keyCodec[noResultKey]{
	version: taggedVersion(1),
	put: func(dst []byte, k noResultKey) []byte {
		return appendText(appendTag(append(dst, k.Key.Bytes()...), k.Tag), k.Provider)
	},
	parse: func(r *keyReader) noResultKey {
		return noResultKey{Key: r.key(), Tag: r.tag(), Provider: r.text("provider")}
	},
}

// rowTag is a tag in a row value. It decodes through language.Parse, so a
// spelling an older release wrote reads as its current representative; text
// Parse refuses fails the decode, which bolttable reports as ErrCorrupt.
// language.Tag's strict UnmarshalText serves wire and config only.
type rowTag struct{ t language.Tag }

var errRowTag = errors.New("statedb: stored tag names no language")

func (r rowTag) MarshalJSON() ([]byte, error) { return json.Marshal(r.t.String()) }

func (r *rowTag) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	t, ok := language.Parse(s)
	if !ok {
		return errRowTag
	}
	r.t = t
	return nil
}
