package wirespec

import (
	"encoding/base64"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/mediawrite"
)

// UploadMax is files.upload's decoded cap: a decimal 10 MB, the figure a
// client prints for it.
const UploadMax = 10_000_000

// FromMs is a preview's start in milliseconds.
type FromMs int64

// FileName is a subtitle file's name in its instance's folder.
type FileName string

// UploadName is files.upload's name, recorded as Provenance.Release.
type UploadName string

// UploadBytes is files.upload's file or archive, base64 on the wire.
type UploadBytes []byte

// ResyncFiles is one resync batch.
type ResyncFiles []ResyncFile

// Range bounds a preview's start to one day.
func (FromMs) Range() (lo, hi int64) { return 0, 86_400_000 }

// MaxBytes is a file system's name limit.
func (FileName) MaxBytes() int { return 255 }

// MaxBytes bounds the release name an upload records.
func (UploadName) MaxBytes() int { return 255 }

// MaxBytes is the base64 length of UploadMax decoded bytes.
func (UploadBytes) MaxBytes() int { return (UploadMax + 2) / 3 * 4 }

// Count bounds one resync batch.
func (ResyncFiles) Count() (lo, hi int) { return 1, 500 }

// MarshalText answers the name as UnmarshalText took it.
func (n FileName) MarshalText() ([]byte, error) { return []byte(n), nil }

// UnmarshalText refuses more than MaxBytes bytes, then anything
// mediawrite.ValidName refuses, so a bad name is refused before a handler
// runs.
func (n *FileName) UnmarshalText(b []byte) error {
	if len(b) > FileName("").MaxBytes() {
		return fmt.Errorf("wirespec: file name of %d bytes, over %d", len(b), FileName("").MaxBytes())
	}
	if err := mediawrite.ValidName(string(b)); err != nil {
		return err
	}
	*n = FileName(b)
	return nil
}

// MarshalText answers the name as UnmarshalText took it.
func (n UploadName) MarshalText() ([]byte, error) { return []byte(n), nil }

// UnmarshalText refuses an empty name, more than MaxBytes bytes, invalid
// UTF-8 and any control character (a line break included), so the name is
// one line before Engine.Upload sanitizes it like a Release.
func (n *UploadName) UnmarshalText(b []byte) error {
	switch {
	case len(b) == 0:
		return errors.New("wirespec: empty upload name")
	case len(b) > UploadName("").MaxBytes():
		return fmt.Errorf("wirespec: upload name of %d bytes, over %d", len(b), UploadName("").MaxBytes())
	case !utf8.Valid(b):
		return errors.New("wirespec: upload name is not valid UTF-8")
	}
	for _, r := range string(b) {
		if unicode.IsControl(r) {
			return fmt.Errorf("wirespec: upload name holds control character %U", r)
		}
	}
	*n = UploadName(b)
	return nil
}

// MarshalText answers standard padded base64, the form UnmarshalText takes.
func (u UploadBytes) MarshalText() ([]byte, error) {
	return base64.StdEncoding.AppendEncode(nil, u), nil
}

// UnmarshalText decodes standard padded base64, refusing an empty file and
// more than UploadMax decoded bytes.
func (u *UploadBytes) UnmarshalText(b []byte) error {
	if len(b) > UploadBytes(nil).MaxBytes() {
		return fmt.Errorf("wirespec: upload of %d base64 bytes, over %d", len(b), UploadBytes(nil).MaxBytes())
	}
	out := make([]byte, base64.StdEncoding.DecodedLen(len(b)))
	n, err := base64.StdEncoding.Strict().Decode(out, b)
	if err != nil {
		return fmt.Errorf("wirespec: upload is not standard base64: %w", err)
	}
	switch {
	case n == 0:
		return errors.New("wirespec: empty upload")
	case n > UploadMax:
		return fmt.Errorf("wirespec: upload of %d bytes, over %d", n, UploadMax)
	}
	*u = UploadBytes(out[:n])
	return nil
}
