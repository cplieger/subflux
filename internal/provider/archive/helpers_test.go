package archive

import (
	"archive/zip"

	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/nwaples/rardecode/v2"
)

// The helpers here name one container's path through the shared selection policy,
// which is what the per-container extractors used to be before the two paths were
// merged. They exist so a test can still target a single container, and each is a
// thin call into the production functions rather than a copy of them.

func zipExtract(data []byte, want epmarker.Target) ([]byte, error) {
	return extractWith(zipMembers, data, want)
}

func rarExtract(data []byte, want epmarker.Target) ([]byte, error) {
	return extractWith(rarMembers, data, want)
}

// gateZipEntry and gateRAREntry run the real entry gate against a real container
// header, going through the production member mapping so the test cannot drift
// from how the walk actually describes an entry.

func gateZipEntry(f *zip.File) bool { return gate(zipMember(f)) }

func gateRAREntry(hdr *rardecode.FileHeader) bool { return gate(rarMember(nil, hdr)) }

// episode is the target for one episode, spelled with field names so a
// transposed call cannot compile into a silently wrong lookup.
func episode(season, ep int) epmarker.Target {
	return epmarker.For(epmarker.Marker{Season: season, Episode: ep})
}
