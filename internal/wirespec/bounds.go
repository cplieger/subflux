package wirespec

import (
	"encoding/base64"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
)

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

// MaxBytes is the base64 length of provider.MaxBody decoded bytes.
func (UploadBytes) MaxBytes() int { return (provider.MaxBody + 2) / 3 * 4 }

// Count bounds one resync batch.
func (ResyncFiles) Count() (lo, hi int) { return 1, 500 }

// Pattern is a superset of the names UnmarshalText accepts, for the schema:
// one path element ending in an extension. UnmarshalText is authoritative.
func (FileName) Pattern() string { return `^[^/\\\x00]*\.[^./\\\x00]+$` }

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

// UnmarshalText decodes standard padded base64, refusing an empty file and
// more than provider.MaxBody decoded bytes.
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
	case n > provider.MaxBody:
		return fmt.Errorf("wirespec: upload of %d bytes, over %d", n, provider.MaxBody)
	}
	*u = UploadBytes(out[:n])
	return nil
}

// BodyCap is an endpoint's request-body bound: 256 KiB for timing bodies,
// 1 MiB for resync bodies and settings documents, 14 MiB for files.upload,
// 64 KiB otherwise. 500 resync files with 255-byte names reach about 817 KB
// under encoding/json's HTML escaping, so 512 KiB would refuse a valid
// batch.
func BodyCap(name string) int64 {
	switch name {
	case "files.setTiming", "files.render":
		return 256 << 10
	case "resyncs.start", "settings.save", "settings.test":
		return 1 << 20
	case "files.upload":
		return 14 << 20 // provider.MaxBody in base64, with room for the other fields
	}
	return 64 << 10
}
