package provider

import (
	"errors"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/provider/archive"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/timing"
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

// ExtractAndValidate returns the subtitle in a download body: the member for
// want when the body is an archive, else the body itself, refused unless
// timing.ParseCues accepts it. The bytes returned are the bytes that arrived.
// A refusal from an archive that opened is httpclient.Permanent: its bytes
// fail the same way on every retry, and a truncated member's
// io.ErrUnexpectedEOF would otherwise read as transient.
func ExtractAndValidate(data []byte, want epmarker.Target) ([]byte, error) {
	extracted, err := archive.Extract(data, want)
	switch {
	case err == nil:
		if _, verr := timing.ParseCues(extracted); verr != nil {
			return nil, verr
		}
		return extracted, nil
	case errors.Is(err, archive.ErrNotArchive):
		if _, verr := timing.ParseCues(data); verr != nil {
			return nil, verr
		}
		return data, nil
	default:
		return nil, httpclient.Permanent(err)
	}
}
