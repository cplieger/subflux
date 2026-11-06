package timing

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/cplieger/subflux/internal/subtitleenc"
)

// CodecVersion names AppendTiming's and AppendCues' encodings and ParseCues'
// event rules. Storage folds it into every codec holding them, so a change
// to any of the three must bump it, or stored baselines re-parse to other
// events than the ones their timings were fitted to.
const CodecVersion uint16 = 1

// cueLayout is AppendCues' first byte; a layout change bumps CodecVersion too.
const cueLayout byte = 1

// AppendCues appends c's encoding to b: a layout byte, the format and
// encoding bytes, and the document as a uvarint length and its bytes. The
// zero Cues encodes as bytes DecodeCues refuses.
func AppendCues(b []byte, c Cues) []byte {
	b = append(b, cueLayout, byte(c.format), byte(c.enc))
	b = binary.AppendUvarint(b, uint64(len(c.doc)))
	return append(b, c.doc...)
}

// DecodeCues reads exactly one AppendCues encoding and re-parses the document
// under the stored encoding. It refuses, as ErrFormatUnsupported, truncated
// bytes, an encoding byte subtitleenc does not declare, a document over
// 32 MiB, one that no longer parses, and any bytes AppendCues would not
// write for the Cues they parse to: another layout, a stored format the
// document does not parse as, a wrong or overlong length, trailing bytes.
func DecodeCues(b []byte) (Cues, error) {
	c, err := decodeCues(b)
	if err != nil {
		return Cues{}, fmt.Errorf("decode cues: %w", err)
	}
	return c, nil
}

func decodeCues(b []byte) (Cues, error) {
	if len(b) < 3 {
		return Cues{}, fmt.Errorf("%w: %w", ErrFormatUnsupported, errTruncated)
	}
	enc := subtitleenc.Encoding(b[2])
	if !enc.Valid() {
		return Cues{}, fmt.Errorf("%w: undeclared encoding %d", ErrFormatUnsupported, b[2])
	}
	_, k := binary.Uvarint(b[3:])
	if k <= 0 {
		return Cues{}, fmt.Errorf("%w: %w", ErrFormatUnsupported, errTruncated)
	}
	doc := b[3+k:]
	if err := checkSize(doc, maxParsedBytes); err != nil {
		return Cues{}, err
	}
	// A clone: the caller's buffer (a bbolt value) may not outlive the call.
	c, err := parse(bytes.Clone(doc), enc)
	if err != nil {
		return Cues{}, err
	}
	if !bytes.Equal(AppendCues(nil, c), b) {
		return Cues{}, fmt.Errorf("%w: layout, format or length does not match the document", ErrFormatUnsupported)
	}
	return c, nil
}
