package provider

import (
	"errors"

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/provider/archive"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subtitlefile"
)

// TargetOf returns the episode a subtitle download is looking for: the episode
// the subtitle names, or epmarker.Any when it names none, which is what a movie
// and a provider that reports no numbering both arrive with.
//
// It exists so the wire-to-target conversion happens once. Season and Episode are
// adjacent ints of the same type, so a transposed conversion would compile and
// then quietly extract the wrong episode; here it is written once and tested.
func TargetOf(sub *subflux.Subtitle) epmarker.Target {
	return epmarker.For(epmarker.Marker{Season: sub.Season, Episode: sub.Episode})
}

// ExtractAndValidate turns a provider's download body into usable subtitle
// bytes: extract from an archive when there is one, fall back to the raw body
// when nothing here could read it, and validate the result as subtitle content.
//
// The two failure paths report different things on purpose. When the archive
// opened and had no subtitle for this target, its own refusal is returned,
// because subtitlefile.Validate can only say "detected zip archive" — which reads
// as "archives are unsupported" for a payload this code unpacked and inspected
// member by member. Validate is the answer only when nothing opened the data,
// where the raw bytes really are the whole story.
//
// Nothing here converts encodings. Validate judges a decoded VIEW so a UTF-16
// subtitle is recognised as text, and the bytes returned are the bytes that
// arrived, because whether a conversion is PERSISTED belongs to
// post_processing.normalize_utf8 alone.
//
// Every refusal from an archive that DID open is marked permanent on the way
// out. Those errors wrap a cause so a caller can reach it with errors.Is, and
// one of the causes a truncated member produces is io.ErrUnexpectedEOF, which
// httpx.IsTransient reads as a reason to re-download. An unreadable or
// wrong-episode archive is a property of the bytes, so a retry fetches the same
// bytes and fails identically while delaying the fall-through to the next
// candidate. IsPermanent is consulted before every transient test, and
// httpx.Permanent adds no text and keeps the chain, so neither the operator's
// log line nor errors.Is changes.
func ExtractAndValidate(data []byte, want epmarker.Target) ([]byte, error) {
	extracted, err := archive.Extract(data, want)
	switch {
	case err == nil:
		if verr := subtitlefile.Validate(extracted); verr != nil {
			return nil, verr
		}
		return extracted, nil
	case errors.Is(err, archive.ErrNotArchive):
		if verr := subtitlefile.Validate(data); verr != nil {
			return nil, verr
		}
		return data, nil
	default:
		return nil, httpx.Permanent(err)
	}
}
