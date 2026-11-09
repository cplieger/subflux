package sonarr

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/cplieger/arrapi/v3"
	arrsonarr "github.com/cplieger/arrapi/v3/sonarr"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr"
)

// Capabilities are Episode roots, Changes, Rescan, Tags and Posters.
func (*Adapter) Capabilities() mediasource.Capabilities {
	return mediasource.Capabilities{Kinds: []media.Kind{media.Episode}, Changes: true, Rescan: true, Tags: true, Posters: true}
}

// Items reads the series list, then each series' episodes, in list order,
// through one pinned server version, yielding one root per series. A failed
// series list fails the call; a failed episode read reports that series to
// unread and the walk goes on, except a server change, which fails the call
// so no item read from another version is yielded.
func (a *Adapter) Items(ctx context.Context, yield func(mediasource.Root, []mediasource.Item) error, unread mediasource.Unread) error {
	p, err := a.c.Pin(ctx)
	if err != nil {
		return arr.Classify(err)
	}
	series, err := p.Series(ctx)
	if err != nil {
		return arr.Classify(err)
	}
	for i := range series {
		s := &series[i]
		eps, err := p.EpisodesWithFiles(ctx, s.ID)
		switch {
		case err == nil:
			if yerr := yield(a.root(s), items(s, eps)); yerr != nil {
				return yerr
			}
		case ctx.Err() != nil || errors.As(err, new(*arrapi.ServerChangedError)):
			return arr.Classify(err)
		default:
			if uerr := unread(mediasource.NativeRef{ID: arr.Native(s.ID), Kind: media.Episode}, arr.Classify(err)); uerr != nil {
				return uerr
			}
		}
	}
	return nil
}

// Root reads one series and its episodes fresh through one pinned server
// version; a series the server answers absent is false.
func (a *Adapter) Root(ctx context.Context, ref mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
	id, ok := arr.ID(ref, media.Episode)
	if !ok {
		return mediasource.Root{}, nil, false, nil
	}
	p, err := a.c.Pin(ctx)
	if err != nil {
		return mediasource.Root{}, nil, false, arr.Classify(err)
	}
	s, found, err := p.SeriesByID(ctx, id)
	switch {
	case err != nil:
		return mediasource.Root{}, nil, false, arr.Classify(err)
	case !found:
		return mediasource.Root{}, nil, false, nil
	}
	eps, err := p.EpisodesWithFiles(ctx, id)
	if err != nil {
		return mediasource.Root{}, nil, false, arr.Classify(err)
	}
	return a.root(&s), items(&s, eps), true, nil
}

// Changes answers the series whose files changed since c (arr.Changes).
func (a *Adapter) Changes(ctx context.Context, c mediasource.Cursor) ([]mediasource.Change, mediasource.Cursor, error) {
	return arr.Changes(ctx, a.history, c, a.log)
}

// changeKinds are the history events that change a series' files.
var changeKinds = []arrsonarr.EventType{
	arrsonarr.EventDownloadImported, arrsonarr.EventFolderImported,
	arrsonarr.EventFileRenamed, arrsonarr.EventFileDeleted,
}

func (a *Adapter) history(ctx context.Context, since time.Time) ([]arr.Record, error) {
	recs, err := a.c.HistorySince(ctx, since, changeKinds...)
	if err != nil {
		return nil, err
	}
	out := make([]arr.Record, 0, len(recs))
	for i := range recs {
		r := &recs[i]
		if r.SeriesID <= 0 {
			continue
		}
		out = append(out, arr.Record{Date: r.Date, ID: r.ID, Root: mediasource.NativeRef{ID: arr.Native(r.SeriesID), Kind: media.Episode}})
	}
	return out, nil
}

// Tags are Sonarr's tags by id.
func (a *Adapter) Tags(ctx context.Context) (map[mediasource.TagRef]string, error) {
	tags, err := a.c.Tags(ctx)
	if err != nil {
		return nil, arr.Classify(err)
	}
	return arr.Tags(tags), nil
}

// RootFolders are Sonarr's root folders (arr.RootFolders).
func (a *Adapter) RootFolders(ctx context.Context) ([]string, error) {
	folders, err := a.c.RootFolders(ctx)
	if err != nil {
		return nil, arr.Classify(err)
	}
	return arr.RootFolders(folders), nil
}

// Ping is arrapi's Info: a fresh detection, which fails for another
// application or an unreadable API.
func (a *Adapter) Ping(ctx context.Context) error {
	info, err := a.c.Info(ctx)
	if err != nil {
		return arr.Classify(err)
	}
	a.version.Note(a.log, info.Version, info.API)
	return nil
}

// Rescan queues RescanSeries, which arrapi sends at most once.
func (a *Adapter) Rescan(ctx context.Context, ref mediasource.NativeRef) error {
	id, ok := arr.ID(ref, media.Episode)
	if !ok {
		return &mediasource.SourceError{Cause: mediasource.NotFound, Err: errNoSeries}
	}
	if err := a.c.RescanSeries(ctx, id); err != nil {
		return arr.Classify(err)
	}
	return nil
}

// Poster fetches the URL MediaCoverURL builds for poster.jpg through
// arr.Poster.
func (a *Adapter) Poster(ctx context.Context, ref mediasource.NativeRef) (body io.ReadCloser, contentType string, found bool, err error) {
	id, ok := arr.ID(ref, media.Episode)
	if !ok {
		return nil, "", false, nil
	}
	u, err := a.c.MediaCoverURL(ctx, id, "poster.jpg")
	if err != nil {
		return nil, "", false, arr.Classify(err)
	}
	return a.poster.Fetch(ctx, u, a.v.APIKey)
}

var errNoSeries = errors.New("no such series")

// root maps a series.
func (a *Adapter) root(s *arrsonarr.Series) mediasource.Root {
	alts := make([]string, 0, len(s.AlternateTitles)+2)
	for _, tr := range s.Translations {
		if tr.Language.Name == "English" {
			alts = append(alts, tr.Title)
			break
		}
	}
	alts = append(append(alts, s.OriginalTitle), s.AlternateTitles...)
	return mediasource.Root{
		Native:    mediasource.NativeRef{ID: arr.Native(s.ID), Kind: media.Episode},
		Title:     s.Title,
		AltTitles: arr.AltTitles(s.Title, alts),
		Link:      s.WebURL(a.v.LinkBase()),
		Language:  arr.OriginalLang(s.Language),
		Order:     mediasource.Order{Type: s.Order.Type, Name: s.Order.Name, Aired: s.Order.Official},
		External:  media.ExternalIDs{TVDB: arr.External(s.IDs.TVDB), TMDB: arr.External(s.IDs.TMDB), IMDb: arr.IMDb(s.IDs.IMDb)},
		Year:      max(s.Year, 0),
	}
}

// items maps every episode Sonarr lists, file or not, at its place in the
// series' own order; the Set fills Numbering.
func items(s *arrsonarr.Series, eps []arrsonarr.Episode) []mediasource.Item {
	tags := make([]mediasource.TagRef, len(s.Tags))
	for i, t := range s.Tags {
		tags[i] = mediasource.TagRef(arr.Native(t))
	}
	orig := arr.OriginalLang(s.OriginalLanguage)
	out := make([]mediasource.Item, len(eps))
	for i := range eps {
		e := &eps[i]
		it := mediasource.Item{
			Native:          arr.Native(e.ID),
			Title:           e.Title,
			AirDate:         e.AirDate,
			SeriesNumbering: media.Numbering{Scheme: media.TVDB, Season: e.Season, Episode: e.Number},
			Absolute:        e.Absolute,
			OriginalLang:    orig,
			Tags:            tags,
		}
		if e.File != nil {
			it.VideoPath, it.SceneName = e.File.Path, e.File.SceneName
			it.AudioLangs = arr.AudioLangs(e.File.MediaInfo)
		}
		out[i] = it
	}
	return out
}
