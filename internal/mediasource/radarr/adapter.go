package radarr

import (
	"context"
	"errors"
	"io"
	"time"

	arrradarr "github.com/cplieger/arrapi/v3/radarr"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr"
)

// Capabilities are Movie roots, Changes, Rescan, Tags and Posters.
func (*Adapter) Capabilities() mediasource.Capabilities {
	return mediasource.Capabilities{Kinds: []media.Kind{media.Movie}, Changes: true, Rescan: true, Tags: true, Posters: true}
}

// Items reads the movie list once and yields one root per movie. One read
// covers every movie, so the adapter itself never calls unread; the Set
// does, for a movie breaking a per-root rule.
func (a *Adapter) Items(ctx context.Context, yield func(mediasource.Root, []mediasource.Item) error, _ mediasource.Unread) error {
	movies, err := a.c.Movies(ctx)
	if err != nil {
		return arr.Classify(err)
	}
	for i := range movies {
		r, it := a.movie(&movies[i])
		if err := yield(r, []mediasource.Item{it}); err != nil {
			return err
		}
	}
	return nil
}

// Root reads one movie fresh; a movie the server answers absent is false.
func (a *Adapter) Root(ctx context.Context, ref mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
	id, ok := arr.ID(ref, media.Movie)
	if !ok {
		return mediasource.Root{}, nil, false, nil
	}
	m, found, err := a.c.MovieByID(ctx, id)
	switch {
	case err != nil:
		return mediasource.Root{}, nil, false, arr.Classify(err)
	case !found:
		return mediasource.Root{}, nil, false, nil
	}
	r, it := a.movie(&m)
	return r, []mediasource.Item{it}, true, nil
}

// Changes answers the movies whose files changed since c (arr.Changes).
func (a *Adapter) Changes(ctx context.Context, c mediasource.Cursor) ([]mediasource.Change, mediasource.Cursor, error) {
	return arr.Changes(ctx, a.history, c, a.log)
}

// changeKinds are the history events that change a movie's files.
var changeKinds = []arrradarr.EventType{
	arrradarr.EventDownloadImported, arrradarr.EventFolderImported,
	arrradarr.EventFileRenamed, arrradarr.EventFileDeleted,
}

func (a *Adapter) history(ctx context.Context, since time.Time) ([]arr.Record, error) {
	recs, err := a.c.HistorySince(ctx, since, changeKinds...)
	if err != nil {
		return nil, err
	}
	out := make([]arr.Record, 0, len(recs))
	for i := range recs {
		r := &recs[i]
		if r.MovieID <= 0 {
			continue
		}
		out = append(out, arr.Record{Date: r.Date, ID: r.ID, Root: mediasource.NativeRef{ID: arr.Native(r.MovieID), Kind: media.Movie}})
	}
	return out, nil
}

// Tags are Radarr's tags by id.
func (a *Adapter) Tags(ctx context.Context) (map[mediasource.TagRef]string, error) {
	tags, err := a.c.Tags(ctx)
	if err != nil {
		return nil, arr.Classify(err)
	}
	return arr.Tags(tags), nil
}

// RootFolders are Radarr's root folders (arr.RootFolders).
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

var errNoMovie = errors.New("no such movie")

// Rescan queues RescanMovie, which arrapi sends at most once.
func (a *Adapter) Rescan(ctx context.Context, ref mediasource.NativeRef) error {
	id, ok := arr.ID(ref, media.Movie)
	if !ok {
		return &mediasource.SourceError{Cause: mediasource.NotFound, Err: errNoMovie}
	}
	if err := a.c.RescanMovie(ctx, id); err != nil {
		return arr.Classify(err)
	}
	return nil
}

// Poster fetches the URL MediaCoverURL builds for poster.jpg through
// arr.Poster.
func (a *Adapter) Poster(ctx context.Context, ref mediasource.NativeRef) (body io.ReadCloser, contentType string, found bool, err error) {
	id, ok := arr.ID(ref, media.Movie)
	if !ok {
		return nil, "", false, nil
	}
	u, err := a.c.MediaCoverURL(ctx, id, "poster.jpg")
	if err != nil {
		return nil, "", false, arr.Classify(err)
	}
	return a.poster.Fetch(ctx, u, a.v.APIKey)
}

// movie maps a movie to its root and its one item.
func (a *Adapter) movie(m *arrradarr.Movie) (mediasource.Root, mediasource.Item) {
	native := arr.Native(m.ID)
	ids := media.ExternalIDs{TMDB: arr.External(m.IDs.TMDB), IMDb: arr.IMDb(m.IDs.IMDb)}
	r := mediasource.Root{
		Native:    mediasource.NativeRef{ID: native, Kind: media.Movie},
		Title:     m.Title,
		AltTitles: arr.AltTitles(m.Title, m.AlternateTitles),
		Link:      m.WebURL(a.v.LinkBase()),
		External:  ids,
		Year:      max(m.Year, 0),
	}
	it := mediasource.Item{Native: native, Title: m.Title, External: ids, OriginalLang: arr.OriginalLang(m.OriginalLanguage)}
	for _, t := range m.Tags {
		it.Tags = append(it.Tags, mediasource.TagRef(arr.Native(t)))
	}
	if m.File != nil {
		it.VideoPath, it.SceneName = m.File.Path, m.File.SceneName
		it.AudioLangs = arr.AudioLangs(m.File.MediaInfo)
	}
	return r, it
}
