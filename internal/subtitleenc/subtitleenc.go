// Package subtitleenc answers what encoding a subtitle's bytes are in, by
// ICU's charset-detection models (internal/subtitleenc/icu) narrowed by the
// subtitle's language, and reads and writes them as the Encoding Standard
// does, on golang.org/x/text's decode tables.
//
// Decoding computes a view and never decides whether it replaces the
// original bytes: subflux persists a converted subtitle only under
// post_processing.normalize_utf8, so a caller writing Decode's output
// without that setting has converted a user's file.
package subtitleenc

import (
	"errors"
	"strconv"
)

// Encoding values are stored in timing's baseline codec: append only, and a
// renumbering bumps timing.CodecVersion.
type Encoding uint8

// The encodings Detect names. The ISO-8859 names are x/text's charmap ones.
//
//nolint:revive // ISO8859_2 and its kin follow golang.org/x/text/encoding/charmap
const (
	Unknown Encoding = iota // bytes Detect could not name; never decoded
	UTF8
	UTF16LE
	UTF16BE
	ISO2022JP // stateful 7-bit, named only by its ESC sequences
	Windows1250
	Windows1251
	Windows1252
	Windows1253
	Windows1254
	Windows1255
	Windows1256
	ISO8859_2
	ISO8859_5
	ISO8859_6
	ISO8859_7
	ISO8859_8 // Hebrew, named only when windows-1255 reads a sample byte as undefined
	KOI8R
	KOI8U
	ShiftJIS
	EUCJP
	GB18030
	Big5
	EUCKR
)

var encodingNames = [...]string{
	Unknown: "Unknown", UTF8: "UTF8", UTF16LE: "UTF16LE", UTF16BE: "UTF16BE", ISO2022JP: "ISO2022JP",
	Windows1250: "Windows1250", Windows1251: "Windows1251", Windows1252: "Windows1252",
	Windows1253: "Windows1253", Windows1254: "Windows1254", Windows1255: "Windows1255",
	Windows1256: "Windows1256", ISO8859_2: "ISO8859_2", ISO8859_5: "ISO8859_5", ISO8859_6: "ISO8859_6",
	ISO8859_7: "ISO8859_7", ISO8859_8: "ISO8859_8", KOI8R: "KOI8R", KOI8U: "KOI8U",
	ShiftJIS: "ShiftJIS", EUCJP: "EUCJP", GB18030: "GB18030", Big5: "Big5", EUCKR: "EUCKR",
}

// Valid reports whether e is a declared Encoding, Unknown included: a byte
// read back from storage may not be.
func (e Encoding) Valid() bool { return int(e) < len(encodingNames) }

// String is the value's Go name, such as "Windows1250".
func (e Encoding) String() string {
	if int(e) < len(encodingNames) {
		return encodingNames[e]
	}
	return "Encoding(" + strconv.Itoa(int(e)) + ")"
}

// ErrUndecodable reports Unknown, or bytes the encoding cannot read or write.
var ErrUndecodable = errors.New("subtitleenc: bytes not decodable in this encoding")
