package boltstore

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	"pgregory.net/rapid"
)

// This file holds the standalone key-encoding and codec PROPERTIES required by
// task 9.3 (Requirements 8.1, 8.2, 8.3, 13.4): key encode/parse + be64 ordering
// as one property, and codec round-trip vs malformed-decode as a separate
// property. They complement the example-based keys_test.go / codec_test.go and
// the FuzzDecode seed corpus by exercising the same invariants across many
// randomly generated inputs.

// genComponent draws a NUL-free key component. The 0x00 byte is the composite-
// key separator, so every real component (media type/id, language, provider,
// variant, source, path) is NUL-free text; the charset here stays within that
// contract while still admitting the empty string and shared-prefix values
// (e.g. "tt1" vs "tt12") that stress component-boundary safety.
func genComponent(rt *rapid.T, label string) string {
	return rapid.StringMatching(`[a-zA-Z0-9_./-]{0,12}`).Draw(rt, label)
}

// TestProp_keyEncodeParse asserts the boltstore composite-key builders round-
// trip through their parsers and that be64-derived keys sort in numeric order
// (Requirements 8.1, 8.2, 8.3). Components are drawn from a NUL-free charset
// that includes shared prefixes and the empty string.
func TestProp_keyEncodeParse(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		mt := api.MediaType(rapid.SampledFrom([]string{"movie", "episode"}).Draw(rt, "mt"))
		mid := genComponent(rt, "mid")
		lang := genComponent(rt, "lang")
		provider := api.ProviderID(genComponent(rt, "provider"))
		variant := api.Variant(rapid.SampledFrom([]string{"standard", "hi", "forced"}).Draw(rt, "variant"))
		source := api.SubtitleSource(rapid.SampledFrom([]string{"external", "embedded"}).Draw(rt, "source"))
		path := genComponent(rt, "path")
		id := rapid.Int64Range(1, 1<<55).Draw(rt, "id")

		// attemptKey: mt 0x00 mid 0x00 lang 0x00 provider, Split round-trips.
		if got := kv.Split(attemptKey(mt, mid, lang, provider)); !slices.Equal(
			got, []string{string(mt), mid, lang, string(provider)}) {
			rt.Errorf("Split(attemptKey) = %q, want %q", got, []string{string(mt), mid, lang, string(provider)})
		}

		// subtitleFileKey: six components, Split round-trips.
		if got := kv.Split(subtitleFileKey(mt, mid, lang, variant, source, path)); !slices.Equal(
			got, []string{string(mt), mid, lang, string(variant), string(source), path}) {
			rt.Errorf("Split(subtitleFileKey) = %q, want 6-component round-trip", got)
		}

		// scanStateKey: mt 0x00 mid.
		if got := kv.Split(scanStateKey(mt, mid)); !slices.Equal(got, []string{string(mt), mid}) {
			rt.Errorf("Split(scanStateKey) = %q, want %q", got, []string{string(mt), mid})
		}

		// stateKey: parse round-trips the surrogate id.
		if gotID, ok := parseStateKey(stateKey(id)); !ok || gotID != id {
			rt.Errorf("parseStateKey(stateKey(%d)) = (%d, %v), want (%d, true)", id, gotID, ok, id)
		}

		// stateQuadKey carries the quad AND triple prefixes, its components
		// split back, and the trailing id parses back.
		qk := stateQuadKey(mt, mid, lang, variant, id)
		if !bytes.HasPrefix(qk, quadPrefix(mt, mid, lang, variant)) {
			rt.Errorf("stateQuadKey missing its quadPrefix")
		}
		if !bytes.HasPrefix(qk, triplePrefix(mt, mid, lang)) {
			rt.Errorf("stateQuadKey missing its triplePrefix (all-variant scans)")
		}
		if gotID, ok := stateQuadKeyID(qk); !ok || gotID != id {
			rt.Errorf("stateQuadKeyID = (%d, %v), want (%d, true)", gotID, ok, id)
		}
		gquad, gid, ok := splitStateQuadKey(qk)
		if !ok || gquad.mt != mt || gquad.mid != mid || gquad.lang != lang || gquad.variant != variant || gid != id {
			rt.Errorf("splitStateQuadKey = (%+v, %d, %v), want ({%s %q %q %q}, %d, true)",
				gquad, gid, ok, mt, mid, lang, variant, id)
		}

		// stateVideoKey splits back into (videoPath, id) and carries videoPrefix.
		vk := stateVideoKey(path, id)
		gotPath, gotID, ok := splitStateVideoKey(vk)
		if !ok || gotPath != path || gotID != id {
			rt.Errorf("splitStateVideoKey = (%q, %d, %v), want (%q, %d, true)", gotPath, gotID, ok, path, id)
		}
		if !bytes.HasPrefix(vk, videoPrefix(path)) {
			rt.Errorf("stateVideoKey missing its videoPrefix")
		}
	})
}

// TestProp_be64Ordering is the central big-endian property: byte-lexicographic
// order equals numeric order, both for raw be64 values and for the surrogate-id
// stateKey / time-index keys built on top of it (Requirement 8.2). This is what
// makes ordered cursor walks and seek-to-cutoff exact.
func TestProp_be64Ordering(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a := rapid.Uint64().Draw(rt, "a")
		b := rapid.Uint64().Draw(rt, "b")
		byteCmp := bytes.Compare(kv.Be64(a), kv.Be64(b))
		if sign(byteCmp) != cmpUint64(a, b) {
			rt.Errorf("Be64 order mismatch for (%d, %d): byteSign=%d numSign=%d", a, b, sign(byteCmp), cmpUint64(a, b))
		}

		// Surrogate ids (positive) sort by numeric value through stateKey.
		ia := rapid.Int64Range(0, 1<<60).Draw(rt, "ia")
		ib := rapid.Int64Range(0, 1<<60).Draw(rt, "ib")
		if sign(bytes.Compare(stateKey(ia), stateKey(ib))) != cmpInt64(ia, ib) {
			rt.Errorf("stateKey order mismatch for (%d, %d)", ia, ib)
		}

		// Time-index keys sort chronologically by their timestamp prefix.
		ta := time.Unix(0, rapid.Int64Range(0, 1<<55).Draw(rt, "ta"))
		tb := time.Unix(0, rapid.Int64Range(0, 1<<55).Draw(rt, "tb"))
		primary := stateKey(ia)
		ka := kv.TimeIndexKey(ta, primary)
		kb := kv.TimeIndexKey(tb, primary)
		// With the same primary, key order must match timestamp order.
		if sign(bytes.Compare(ka, kb)) != cmpInt64(ta.UnixNano(), tb.UnixNano()) {
			rt.Errorf("TimeIndexKey order mismatch for (%d, %d)", ta.UnixNano(), tb.UnixNano())
		}
	})
}

// sign maps an int comparison result to -1/0/1.
func sign(c int) int {
	switch {
	case c < 0:
		return -1
	case c > 0:
		return 1
	default:
		return 0
	}
}

func cmpUint64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// TestProp_codecRoundTrip generates random values of every core-domain record
// type, encodes them, decodes them back under FailClosed, and asserts the
// re-encoded bytes are byte-stable (Requirement 8.1 codec half). It is the
// round-trip companion to FuzzDecode's malformed-input coverage.
func TestProp_codecRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		which := rapid.IntRange(0, 3).Draw(rt, "recType")
		switch which {
		case 0:
			rec := attemptRec{
				LastTried: genTime(rt, "lastTried"),
				NextRetry: genTime(rt, "nextRetry"),
				Failures:  rapid.IntRange(0, 1000).Draw(rt, "failures"),
			}
			assertRecStable(rt, &rec)
		case 1:
			rec := stateRec{
				ID:            rapid.Int64Range(1, 1<<55).Draw(rt, "id"),
				MediaType:     api.MediaType(genComponent(rt, "mt")),
				MediaID:       genComponent(rt, "mid"),
				Language:      genComponent(rt, "lang"),
				Variant:       api.Variant(genComponent(rt, "variant")),
				Provider:      api.ProviderID(genComponent(rt, "provider")),
				ReleaseName:   genComponent(rt, "release"),
				Path:          genComponent(rt, "path"),
				Title:         genComponent(rt, "title"),
				ImdbID:        genComponent(rt, "imdb"),
				ReleaseTag:    genComponent(rt, "tag"),
				Score:         rapid.IntRange(-10, 100).Draw(rt, "score"),
				Season:        rapid.IntRange(0, 50).Draw(rt, "season"),
				Episode:       rapid.IntRange(0, 500).Draw(rt, "episode"),
				Manual:        rapid.Bool().Draw(rt, "manual"),
				VideoPath:     genComponent(rt, "video"),
				MediaImported: genTime(rt, "imported"),
			}
			assertRecStable(rt, &rec)
		case 2:
			rec := fileRec{
				Codec:     genComponent(rt, "codec"),
				UpdatedAt: genTime(rt, "updated"),
			}
			assertRecStable(rt, &rec)
		case 3:
			rec := scanRec{
				Title:     genComponent(rt, "title"),
				AudioLang: genComponent(rt, "audio"),
				Season:    rapid.IntRange(0, 50).Draw(rt, "season"),
				Episode:   rapid.IntRange(0, 500).Draw(rt, "episode"),
				ScannedAt: genTime(rt, "scanned"),
			}
			assertRecStable(rt, &rec)
		}
	})
}

// genTime draws a UTC timestamp. time.Unix with a post-epoch nanosecond offset
// avoids the monotonic-clock reading that would defeat byte-stable JSON
// round-tripping.
func genTime(rt *rapid.T, label string) time.Time {
	return time.Unix(0, rapid.Int64Range(0, 1<<60).Draw(rt, label)).UTC()
}

// assertRecStable encodes v, decodes it back under FailClosed, re-encodes, and
// asserts the two encodings are byte-identical (a stable round-trip).
func assertRecStable[T any](rt *rapid.T, v *T) {
	rt.Helper()
	enc, err := encodeRecord(v)
	if err != nil {
		rt.Fatalf("encodeRecord: %v", err)
	}
	var got T
	skip, derr := decodeRecord(kv.FailClosed, "prop", []byte("k"), enc, &got)
	if skip {
		rt.Fatalf("FailClosed decode must never skip")
	}
	if derr != nil {
		rt.Fatalf("decodeRecord: %v", derr)
	}
	reEnc, err := encodeRecord(&got)
	if err != nil {
		rt.Fatalf("re-encode: %v", err)
	}
	if !bytes.Equal(enc, reEnc) {
		rt.Errorf("round-trip not byte-stable:\n have %s\n want %s", reEnc, enc)
	}
}

// TestProp_malformedDecode asserts the decode policy holds across arbitrary
// bytes (Requirement 13.4): the decoder never panics, FailClosed errors on
// invalid JSON and never skips, and TolerantSkip skips invalid JSON without
// erroring. This is the property form of the FuzzDecode contract.
func TestProp_malformedDecode(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		data := []byte(rapid.String().Draw(rt, "data"))
		valid := json.Valid(data)

		var v stateRec
		skip, err := decodeRecord(kv.FailClosed, bucketSubtitleState, []byte("k"), data, &v)
		if skip {
			rt.Fatalf("FailClosed must never skip")
		}
		if !valid && err == nil {
			rt.Fatalf("FailClosed: invalid JSON %q decoded without error", data)
		}

		var vs stateRec
		skip, serr := decodeRecord(kv.TolerantSkip, bucketSubtitleState, []byte("k"), data, &vs)
		if serr != nil {
			rt.Fatalf("TolerantSkip returned error: %v", serr)
		}
		if !valid && !skip {
			rt.Fatalf("TolerantSkip: invalid JSON %q not skipped", data)
		}
	})
}
