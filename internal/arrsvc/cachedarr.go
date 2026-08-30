package arrsvc

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/keyenc"
)

// Cache keys, one namespace per arr instance (each CachedSonarr/CachedRadarr
// owns its own table, so sonarr and radarr never share an entry).
const (
	keySeriesList = "series"
	keyMovieList  = "movies"
)

func episodesKey(seriesID int) string { return "episodes/" + strconv.Itoa(seriesID) }

// tagsKey keys exclude-tag resolution by the tag NAME SET (sorted, so order
// is immaterial). logMissing stays out of the key: the hint is caller-side,
// logged from the cached unmatched names.
func tagsKey(names []string) string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	return "tags/" + keyenc.Join(sorted...)
}

// seriesSnapshot is the series-list entry payload: the rows plus the
// tvdb→row index, derived once per entry refresh, inside the entry value.
type seriesSnapshot struct {
	byTvdb map[int]int
	rows   []arrapi.Series
}

func newSeriesSnapshot(rows []arrapi.Series) seriesSnapshot {
	idx := make(map[int]int, len(rows))
	for i := range rows {
		if id := rows[i].TvdbID; id > 0 {
			idx[id] = i
		}
	}
	return seriesSnapshot{rows: rows, byTvdb: idx}
}

// hasID reports whether the snapshot holds the series with this arr id (the
// ordered membership gate's check).
func (s seriesSnapshot) hasID(seriesID int) bool {
	for i := range s.rows {
		if s.rows[i].ID == seriesID {
			return true
		}
	}
	return false
}

// movieSnapshot is the movie-list entry payload: the rows plus the tmdb→row
// index, derived once per entry refresh, inside the entry value.
type movieSnapshot struct {
	byTmdb map[int]int
	rows   []arrapi.Movie
}

func newMovieSnapshot(rows []arrapi.Movie) movieSnapshot {
	idx := make(map[int]int, len(rows))
	for i := range rows {
		if id := rows[i].TmdbID; id > 0 {
			idx[id] = i
		}
	}
	return movieSnapshot{rows: rows, byTmdb: idx}
}

// tagSnapshot is the exclude-tag entry payload: both halves of the
// resolution, so the caller-side hint can log the unmatched NAMES verbatim
// on cache hits too (names are not derivable from an id set).
type tagSnapshot struct {
	ids       map[int]struct{}
	unmatched []string
}

// tagResolveFn is arrapi's ResolveTagIDs shape.
type tagResolveFn func(ctx context.Context, labels ...string) (map[int]struct{}, []string, error)

func tagFetch(resolve tagResolveFn, names []string) fetchFn {
	return func(ctx context.Context) (any, error) {
		ids, unmatched, err := resolve(ctx, names...)
		if err != nil {
			return nil, err
		}
		return tagSnapshot{ids: ids, unmatched: unmatched}, nil
	}
}

// payloadAs asserts an entry payload's family type. The key namespace pins
// each key's payload type, so a mismatch is a programming bug (an invariant
// check, not error handling).
func payloadAs[T any](v any) T {
	p, ok := v.(T)
	if !ok {
		panic(fmt.Sprintf("arrsvc: cache payload has type %T, want %T", v, p))
	}
	return p
}

// resolveTags is the shared cached exclude-tag read: only err == nil results
// are cached (a fail-open nil is NEVER cached), and the hint logs the cached
// unmatched names verbatim, cache hit or not.
func (t *readTable) resolveTags(ctx context.Context, names []string, logMissing bool, plain, wave tagResolveFn) (map[int]struct{}, error) {
	if len(names) == 0 {
		return nil, nil
	}
	v, err := t.read(ctx, tagsKey(names), tagFetch(plain, names), tagFetch(wave, names))
	if err != nil {
		return nil, err
	}
	snap := payloadAs[tagSnapshot](v)
	if logMissing {
		logUnmatchedTags(snap.unmatched)
	}
	return snap.ids, nil
}

// sonarrReads is the Sonarr read surface the wrapper coalesces: the three
// Sonarr read families plus raw tag resolution. Satisfied by *arrsvc.Sonarr
// (the shipped 3-attempt client) and by the wrapper's own single-attempt
// *arrapi.Sonarr wave client.
type sonarrReads interface {
	Series(ctx context.Context) ([]arrapi.Series, error)
	Episodes(ctx context.Context, seriesID int) ([]arrapi.Episode, error)
	ResolveTagIDs(ctx context.Context, labels ...string) (map[int]struct{}, []string, error)
}

// radarrReads is the Radarr counterpart of sonarrReads.
type radarrReads interface {
	Movies(ctx context.Context) ([]arrapi.Movie, error)
	ResolveTagIDs(ctx context.Context, labels ...string) (map[int]struct{}, []string, error)
}

// CachedSonarr is the Sonarr half of the arr-read wrapper (A4): the series
// list, episodes-by-series and exclude-tag read families route through one
// cache with plain-read coalescing and recovery waves; every other method
// passes through to the shipped client. Plain reads fetch on the shipped
// 3-attempt client; admitted wave passes run single-attempt on the wrapper's
// own wave client. Returned slices are shared cache state: read-only.
type CachedSonarr struct {
	*Sonarr
	shipped   sonarrReads
	wave      sonarrReads
	waveClose func()
	table     *readTable
}

// NewCachedSonarr builds the wrapped Sonarr service: the shipped client plus
// the wrapper's own wave client (WithMaxAttempts(1), WithTimeout at the
// execution budget), constructed together so activation publishes and closes
// them atomically. The wrapper's cache starts empty, so a reload revokes the
// previous instance's in-flight wave writes.
func NewCachedSonarr(baseURL string, apiKey APIKey, gate *ReadGate) (*CachedSonarr, error) {
	s, err := NewSonarr(baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	w, err := arrapi.NewSonarr(baseURL, apiKey,
		arrapi.WithMaxAttempts(1), arrapi.WithTimeout(executionBudget))
	if err != nil {
		s.Close()
		return nil, err
	}
	c := newCachedSonarr(s, w, gate)
	c.waveClose = w.Close
	return c, nil
}

// newCachedSonarr wires the wrapper around explicit read surfaces (the seam
// the tests use with fake upstreams).
func newCachedSonarr(shipped *Sonarr, wave sonarrReads, gate *ReadGate) *CachedSonarr {
	return &CachedSonarr{Sonarr: shipped, shipped: shipped, wave: wave, table: newReadTable(gate)}
}

// Close releases both clients' transports.
func (c *CachedSonarr) Close() {
	c.Sonarr.Close()
	if c.waveClose != nil {
		c.waveClose()
	}
}

// Series returns the cached series list. The returned slice is shared and
// read-only.
func (c *CachedSonarr) Series(ctx context.Context) ([]arrapi.Series, error) {
	v, err := c.table.read(ctx, keySeriesList, fetchSeries(c.shipped), fetchSeries(c.wave))
	if err != nil {
		return nil, err
	}
	return payloadAs[seriesSnapshot](v).rows, nil
}

// SeriesByTvdbID returns the cached series row for one TVDB id, resolved
// through the tvdb→row index carried inside the entry value. Absence is a
// normal answer: an id the list does not hold (only positive ids are ever
// indexed) reports found == false with a nil error. The returned row shares
// cache state: read-only.
func (c *CachedSonarr) SeriesByTvdbID(ctx context.Context, tvdbID int) (arrapi.Series, bool, error) {
	v, err := c.table.read(ctx, keySeriesList, fetchSeries(c.shipped), fetchSeries(c.wave))
	if err != nil {
		return arrapi.Series{}, false, err
	}
	snap := payloadAs[seriesSnapshot](v)
	i, ok := snap.byTvdb[tvdbID]
	if !ok {
		return arrapi.Series{}, false, nil
	}
	return snap.rows[i], true, nil
}

func fetchSeries(src sonarrReads) fetchFn {
	return func(ctx context.Context) (any, error) {
		rows, err := src.Series(ctx)
		if err != nil {
			return nil, err
		}
		return newSeriesSnapshot(rows), nil
	}
}

// Episodes returns the cached episode list for one series. A MARKED read
// passes the ordered membership gate first: an id missing from the cached
// series list awaits/joins the same recovery's series-list wave and
// re-checks; only a post-wave miss answers ErrUnknownSeries, with no
// upstream episodes call. A plain read keeps today's behavior — a miss falls
// through to the cached upstream call.
func (c *CachedSonarr) Episodes(ctx context.Context, seriesID int) ([]arrapi.Episode, error) {
	key := episodesKey(seriesID)
	rec, marked := recoveryFrom(ctx)
	if !marked {
		v, err := c.table.plainRead(ctx, key, fetchEpisodes(c.shipped, seriesID))
		if err != nil {
			return nil, err
		}
		return payloadAs[[]arrapi.Episode](v), nil
	}
	if !c.cachedSeriesHas(seriesID) {
		v, err := c.table.waveRead(ctx, rec, keySeriesList, fetchSeries(c.wave))
		if err != nil {
			return nil, err
		}
		if !payloadAs[seriesSnapshot](v).hasID(seriesID) {
			return nil, fmt.Errorf("%w: series %d", ErrUnknownSeries, seriesID)
		}
	}
	v, err := c.table.waveRead(ctx, rec, key, fetchEpisodes(c.wave, seriesID))
	if err != nil {
		return nil, err
	}
	return payloadAs[[]arrapi.Episode](v), nil
}

func fetchEpisodes(src sonarrReads, seriesID int) fetchFn {
	return func(ctx context.Context) (any, error) {
		rows, err := src.Episodes(ctx, seriesID)
		if err != nil {
			return nil, err
		}
		return rows, nil
	}
}

// cachedSeriesHas is the gate's fast-path check against the current cached
// series list; a missing or expired entry reads as a miss.
func (c *CachedSonarr) cachedSeriesHas(seriesID int) bool {
	e, ok := c.table.cache.Get(keySeriesList)
	if !ok {
		return false
	}
	return payloadAs[seriesSnapshot](e.payload).hasID(seriesID)
}

// ResolveExcludeTagIDsErr is the error-returning exclude-tag resolution:
// cached beneath the fail-open, so only err == nil results are stored, and a
// marked read's wave failure or refusal PROPAGATES (a typed leg failure at
// the HTTP response, never a silent empty-exclusion 200).
func (c *CachedSonarr) ResolveExcludeTagIDsErr(ctx context.Context, names []string, logMissing bool) (map[int]struct{}, error) {
	return c.table.resolveTags(ctx, names, logMissing, c.shipped.ResolveTagIDs, c.wave.ResolveTagIDs)
}

// ResolveExcludeTagIDs is the shipped fail-open projection over
// ResolveExcludeTagIDsErr, kept by plain reads and the scan path.
func (c *CachedSonarr) ResolveExcludeTagIDs(ctx context.Context, names []string, logMissing bool) map[int]struct{} {
	return failOpenTagIDs(c.ResolveExcludeTagIDsErr(ctx, names, logMissing))
}

// WantedEpisodes is the scan engine's entry point: its fetches bypass the
// cache (always fresh) and register as write-through waves.
func (c *CachedSonarr) WantedEpisodes(ctx context.Context, excludeTagIDs map[int]struct{}, fn func(arrapi.Series, arrapi.Episode) error) error {
	return wantedEpisodes(ctx, sonarrWriteThrough{c}, excludeTagIDs, fn)
}

// sonarrWriteThrough is the scan bypass: each fetch runs on the shipped
// client and registers afterwards as an already-begun wave (start = its own
// read-begin; newest-write-wins; resets the floor clock; never passes the
// admission queue or budgets).
type sonarrWriteThrough struct{ c *CachedSonarr }

func (s sonarrWriteThrough) Series(ctx context.Context) ([]arrapi.Series, error) {
	begin := time.Now()
	rows, err := s.c.shipped.Series(ctx)
	if err != nil {
		return nil, err
	}
	s.c.table.writeThrough(keySeriesList, readEntry{payload: newSeriesSnapshot(rows), readBegin: begin})
	return rows, nil
}

func (s sonarrWriteThrough) Episodes(ctx context.Context, seriesID int) ([]arrapi.Episode, error) {
	begin := time.Now()
	rows, err := s.c.shipped.Episodes(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	s.c.table.writeThrough(episodesKey(seriesID), readEntry{payload: rows, readBegin: begin})
	return rows, nil
}

// CachedRadarr is the Radarr half of the arr-read wrapper; see CachedSonarr.
type CachedRadarr struct {
	*Radarr
	shipped   radarrReads
	wave      radarrReads
	waveClose func()
	table     *readTable
}

// NewCachedRadarr builds the wrapped Radarr service; see NewCachedSonarr.
func NewCachedRadarr(baseURL string, apiKey APIKey, gate *ReadGate) (*CachedRadarr, error) {
	r, err := NewRadarr(baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	w, err := arrapi.NewRadarr(baseURL, apiKey,
		arrapi.WithMaxAttempts(1), arrapi.WithTimeout(executionBudget))
	if err != nil {
		r.Close()
		return nil, err
	}
	c := newCachedRadarr(r, w, gate)
	c.waveClose = w.Close
	return c, nil
}

// newCachedRadarr wires the wrapper around explicit read surfaces (the test
// seam).
func newCachedRadarr(shipped *Radarr, wave radarrReads, gate *ReadGate) *CachedRadarr {
	return &CachedRadarr{Radarr: shipped, shipped: shipped, wave: wave, table: newReadTable(gate)}
}

// Close releases both clients' transports.
func (c *CachedRadarr) Close() {
	c.Radarr.Close()
	if c.waveClose != nil {
		c.waveClose()
	}
}

// Movies returns the cached movie list. The returned slice is shared and
// read-only.
func (c *CachedRadarr) Movies(ctx context.Context) ([]arrapi.Movie, error) {
	v, err := c.table.read(ctx, keyMovieList, fetchMovies(c.shipped), fetchMovies(c.wave))
	if err != nil {
		return nil, err
	}
	return payloadAs[movieSnapshot](v).rows, nil
}

// MovieByTmdbID returns the cached movie row for one TMDB id; see
// SeriesByTvdbID.
func (c *CachedRadarr) MovieByTmdbID(ctx context.Context, tmdbID int) (arrapi.Movie, bool, error) {
	v, err := c.table.read(ctx, keyMovieList, fetchMovies(c.shipped), fetchMovies(c.wave))
	if err != nil {
		return arrapi.Movie{}, false, err
	}
	snap := payloadAs[movieSnapshot](v)
	i, ok := snap.byTmdb[tmdbID]
	if !ok {
		return arrapi.Movie{}, false, nil
	}
	return snap.rows[i], true, nil
}

func fetchMovies(src radarrReads) fetchFn {
	return func(ctx context.Context) (any, error) {
		rows, err := src.Movies(ctx)
		if err != nil {
			return nil, err
		}
		return newMovieSnapshot(rows), nil
	}
}

// ResolveExcludeTagIDsErr is the Radarr-side error-returning exclude-tag
// resolution; see the CachedSonarr method.
func (c *CachedRadarr) ResolveExcludeTagIDsErr(ctx context.Context, names []string, logMissing bool) (map[int]struct{}, error) {
	return c.table.resolveTags(ctx, names, logMissing, c.shipped.ResolveTagIDs, c.wave.ResolveTagIDs)
}

// ResolveExcludeTagIDs is the fail-open projection; see the CachedSonarr
// method.
func (c *CachedRadarr) ResolveExcludeTagIDs(ctx context.Context, names []string, logMissing bool) map[int]struct{} {
	return failOpenTagIDs(c.ResolveExcludeTagIDsErr(ctx, names, logMissing))
}

// WantedMovies is the scan engine's Radarr entry point; see WantedEpisodes.
func (c *CachedRadarr) WantedMovies(ctx context.Context, excludeTagIDs map[int]struct{}, fn func(arrapi.Movie) error) error {
	return wantedMovies(ctx, radarrWriteThrough{c}, excludeTagIDs, fn)
}

// radarrWriteThrough is the Radarr scan bypass; see sonarrWriteThrough.
type radarrWriteThrough struct{ c *CachedRadarr }

func (r radarrWriteThrough) Movies(ctx context.Context) ([]arrapi.Movie, error) {
	begin := time.Now()
	rows, err := r.c.shipped.Movies(ctx)
	if err != nil {
		return nil, err
	}
	r.c.table.writeThrough(keyMovieList, readEntry{payload: newMovieSnapshot(rows), readBegin: begin})
	return rows, nil
}

// logUnmatchedTags is the caller-side hint: each configured tag name that
// matched no arr tag, logged verbatim.
func logUnmatchedTags(unmatched []string) {
	for _, name := range unmatched {
		slog.Info("exclude_tag not found in arr, create it in Settings > Tags", "tag", name)
	}
}

// failOpenTagIDs is the fail-open projection over an error-returning
// exclude-tag resolution: a failure logs and disables exclusion (nil).
func failOpenTagIDs(ids map[int]struct{}, err error) map[int]struct{} {
	if err != nil {
		slog.Warn("failed to fetch tags, exclude_arr_tags will not work", "error", err)
		return nil
	}
	return ids
}
