package plex

import (
	"context"

	"github.com/cplieger/plexapi/v3"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediaserver"
)

// maxLabels bounds one file's labels; the Set bounds each string.
const maxLabels = 64

// Labels lists the sidecar subtitle streams, the ones carrying a key, of the
// media part at serverPath, at most 64, read from the first located item
// Plex confirms holds that part. Every episode of a multi-episode file holds
// a part over the same file and streams, so one part is read. File stays "",
// since a Plex stream names no file.
func (a *adapter) Labels(ctx context.Context, serverPath string) ([]mediaserver.Label, error) {
	return run(ctx, a, func(ctx context.Context) ([]mediaserver.Label, error) {
		var out []mediaserver.Label
		err := a.holders(ctx, serverPath, func(_ string, p *plexapi.Part) (bool, error) {
			out = sidecars(p.Stream)
			return true, nil
		})
		return out, err
	})
}

// partAt answers the item's first media part over serverPath, nil when none.
func partAt(it *plexapi.Item, serverPath string) *plexapi.Part {
	for i := range it.Media {
		for j := range it.Media[i].Part {
			if p := &it.Media[i].Part[j]; p.File == serverPath {
				return p
			}
		}
	}
	return nil
}

// sidecars answers each sidecar subtitle stream's label, at most maxLabels.
func sidecars(streams []plexapi.Stream) []mediaserver.Label {
	var out []mediaserver.Label
	for i := range streams {
		s := &streams[i]
		if !s.IsSubtitle() || s.Key == "" {
			continue
		}
		if len(out) == maxLabels {
			break
		}
		out = append(out, mediaserver.Label{
			LanguageTag: s.LanguageTag,
			Display:     s.DisplayTitle,
			Flags:       language.Flags{HI: s.HearingImpaired, Forced: s.Forced},
		})
	}
	return out
}
