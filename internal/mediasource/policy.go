package mediasource

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cplieger/go/textsafe"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

// Bounds the Set enforces on every adapter answer and ParseConfigs on every
// entry.
const (
	maxSources      = 16
	maxNativeID     = 64
	maxTitleBytes   = 512
	maxOrderBytes   = 64
	maxAltTitles    = 32
	maxAudioLangs   = 16
	maxItemTags     = 64
	maxPathBytes    = 4096
	maxMappings     = 32
	maxExcludeTags  = 32
	maxTagLabel     = 64
	tagCacheMaxAge  = 5 * time.Minute
	rootFoldersWait = 10 * time.Second
)

// The unread bounds of one Items walk: the walkFailBudget-th consecutive
// unread root ends it as an outage, and so does the unread root past
// maxUnreadRoots, the most storage's keep list holds.
const (
	walkFailBudget = 3
	maxUnreadRoots = 32
)

const cutMarker = "…"

// quoteMax bounds an untrusted id or path an error names.
const quoteMax = 128

// answer checks one Set call's adapter answer before any consumer sees it,
// logging at most one WARN for the call's cut counts.
type answer struct {
	src *source
	log *slog.Logger
	op  string
	cut bool
}

func (a *answer) refuse(format string, args ...any) *SourceError {
	return &SourceError{Source: a.src.id, Op: a.op, Cause: Protocol, Err: fmt.Errorf(format, args...)}
}

// adapterError gives an adapter's error the call's Source and Op; any error
// that is not a *SourceError, or one with no Cause, is Protocol.
func (a *answer) adapterError(err error) *SourceError {
	var se *SourceError
	if !errors.As(err, &se) || se == nil {
		return &SourceError{Source: a.src.id, Op: a.op, Cause: Protocol, Err: err}
	}
	out := *se
	out.Source, out.Op = a.src.id, a.op
	if out.Cause == 0 {
		out.Cause = Protocol
	}
	return &out
}

// ref checks a root reference by the identity rule: a kind the source lists
// and a valid id.
func (a *answer) ref(r NativeRef) *SourceError {
	if !validNativeID(r.ID) {
		return a.refuse("root %q: id is not 1 to %d characters of A-Z, a-z, 0-9, '.', '_', ':' and '-'", quote(string(r.ID)), maxNativeID)
	}
	if !slices.Contains(a.src.caps.Kinds, r.Kind) {
		return a.refuse("root %s: kind %d is not one this source lists", r.ID, r.Kind)
	}
	return nil
}

// items checks and bounds one root's items, answering the first per-root
// violation, and marks each item carrying an excluded tag; it returns new
// slices, never the adapter's.
func (a *answer) items(r NativeRef, in []Item, excluded map[TagRef]struct{}) ([]Item, *SourceError) {
	out := make([]Item, 0, len(in))
	ids := make(map[NativeID]struct{}, len(in))
	for i := range in {
		it := in[i]
		if !validNativeID(it.Native) {
			return nil, a.refuse("root %s: item %q: id is not 1 to %d characters of A-Z, a-z, 0-9, '.', '_', ':' and '-'", r.ID, quote(string(it.Native)), maxNativeID)
		}
		if _, dup := ids[it.Native]; dup {
			return nil, a.refuse("root %s: item %s is listed twice", r.ID, it.Native)
		}
		ids[it.Native] = struct{}{}
		if it.VideoPath != "" {
			local, err := a.src.m.toLocal(it.VideoPath)
			if err != nil {
				return nil, a.refuse("root %s: item %s: video path %q %w", r.ID, it.Native, quote(it.VideoPath), err)
			}
			it.VideoPath = local
		}
		if !validSeriesNumbering(r.Kind, it.SeriesNumbering) {
			return nil, a.refuse("root %s: item %s: numbering is not a TVDB season and episode of 0 to 65535", r.ID, it.Native)
		}
		if !validAirDate(it.AirDate) {
			return nil, a.refuse("root %s: item %s: air date %q is not YYYY-MM-DD", r.ID, it.Native, quote(it.AirDate))
		}
		it.Title = line(it.Title, maxTitleBytes)
		it.SceneName = line(it.SceneName, maxTitleBytes)
		it.AudioLangs = a.langs(it.AudioLangs)
		// Read before the cut: an excluded tag past maxItemTags still excludes.
		it.Excluded = slices.ContainsFunc(it.Tags, func(t TagRef) bool {
			_, ok := excluded[t]
			return ok
		})
		it.Tags = a.tags(it.Tags)
		out = append(out, it)
	}
	return out, nil
}

// root bounds a checked root's text: one line each, counts cut, a Link that
// is not an absolute http or https URL dropped.
func (a *answer) root(r Root) Root { //nolint:gocritic // hugeParam: the bounded copy is the result
	r.Title = line(r.Title, maxTitleBytes)
	alt := r.AltTitles
	if len(alt) > maxAltTitles {
		alt = alt[:maxAltTitles]
		a.warnCut("alt_titles")
	}
	r.AltTitles = make([]string, len(alt))
	for i, t := range alt {
		r.AltTitles[i] = line(t, maxTitleBytes)
	}
	r.Link = link(r.Link)
	r.Order.Type = line(r.Order.Type, maxOrderBytes)
	r.Order.Name = line(r.Order.Name, maxOrderBytes)
	return r
}

func (a *answer) langs(in []language.Tag) []language.Tag {
	out := make([]language.Tag, 0, min(len(in), maxAudioLangs))
	for _, t := range in {
		if t == (language.Tag{}) {
			continue
		}
		if len(out) == maxAudioLangs {
			a.warnCut("audio_langs")
			break
		}
		out = append(out, t)
	}
	return out
}

func (a *answer) tags(in []TagRef) []TagRef {
	out := make([]TagRef, 0, min(len(in), maxItemTags))
	for _, t := range in {
		if !validNativeID(NativeID(t)) {
			continue
		}
		if len(out) == maxItemTags {
			a.warnCut("tags")
			break
		}
		out = append(out, t)
	}
	return out
}

func (a *answer) warnCut(field string) {
	if a.cut {
		return
	}
	a.cut = true
	a.log.Warn("source answer cut to its bounds", "source", a.src.id, "op", a.op, "field", field)
}

// validSeriesNumbering: an episode's place is a TVDB season and episode; a
// movie's is zero.
func validSeriesNumbering(k media.Kind, n media.Numbering) bool {
	if k != media.Episode {
		return n == media.Numbering{}
	}
	return n.Scheme == media.TVDB && media.ValidEpisodeNumber(n.Season) && media.ValidEpisodeNumber(n.Episode)
}

func line(s string, n int) string {
	out, _ := textsafe.SanitizeSingleLineCapped(s, n, cutMarker)
	return out
}

// link keeps an absolute http or https URL that is one safe line within
// maxTitleBytes; cutting or cleaning a URL would change where it points.
func link(s string) string {
	if s == "" || len(s) > maxTitleBytes || strings.ContainsFunc(s, textsafe.IsUnsafeSingleLine) {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return s
}

func quote(s string) string { return textsafe.CapBytes(s, quoteMax) }
