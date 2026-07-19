// Package resolve is the one home for typed-reference resolution (S7): it
// maps the wire references — FileRef (one stored subtitle file) and MediaRef
// (the arr-known video of a media item) — to absolute filesystem paths using
// server-derived facts only. No handler on the file/preview/sync/download
// verbs accepts a client-supplied path; every path a handler touches comes
// out of this package (or, for orphans, out of the filehandlers TTL handle
// table).
//
// Failure taxonomy (mapped by handlers):
//
//   - ErrMediaNotFound / ErrSubtitleNotFound -> 404 with the machine code of
//     the same name: the reference does not resolve to a known row/item.
//   - ErrAmbiguous -> 500-class internal invariant: a FileRef matching more
//     than one store row means the store's uniqueness assumption broke;
//     resolution never guesses.
//   - ErrPathInvariant -> 500-class: a server-derived path failed the
//     media-roots containment check that runs as defense-in-depth on every
//     resolved value. Server-derived paths should never fail it, so a hit is
//     logged at ERROR as an invariant breach, never answered as a 4xx.
package resolve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
)

// Sentinel errors; see the package doc for the handler status mapping.
var (
	// ErrMediaNotFound means the MediaRef (or a FileRef's surrounding media)
	// does not resolve to a known media item with a video file.
	ErrMediaNotFound = errors.New("media not found")
	// ErrSubtitleNotFound means the FileRef matches no stored subtitle row.
	ErrSubtitleNotFound = errors.New("subtitle not found")
	// ErrAmbiguous means the FileRef matched more than one stored row — an
	// internal invariant breach, never a guess.
	ErrAmbiguous = errors.New("ambiguous file reference")
	// ErrPathInvariant means a server-derived path failed the containment
	// validation that runs as defense-in-depth.
	ErrPathInvariant = errors.New("resolved path failed containment validation")
)

// FileRef addresses exactly one stored subtitle file: the store row identity
// (media_type, media_id, language, variant, source) plus the manual-sibling
// ordinal parsed from the row's filename (api.ManualOrdinal; 0 = the
// unnumbered auto file). A bare quad is NOT unique — manual ordinals share a
// quad, and sync offsets are path-keyed in the store, so ambiguous
// resolution would corrupt offset bookkeeping.
//
// MediaID is the store media identifier (e.g. "tmdb-1271",
// "tvdb-121361-s01e05"), matching the /api/files wire fields.
type FileRef struct {
	MediaType api.MediaType `json:"media_type"`
	MediaID   string        `json:"media_id"`
	Language  string        `json:"language"`
	Variant   string        `json:"variant"`
	Source    string        `json:"source"`
	Ordinal   int           `json:"ordinal,omitempty"`
}

// Validate checks the reference's field shape (not its existence). Only
// external rows are file-addressable: embedded tracks live inside the video
// container and have no per-file path.
func (ref *FileRef) Validate() error {
	if !ref.MediaType.Valid() {
		return errors.New("invalid media_type")
	}
	if ref.MediaID == "" || ref.Language == "" {
		return errors.New("media_id and language are required")
	}
	if ref.Source != string(api.SourceExternal) {
		return errors.New("source must be external (embedded tracks are not file-addressable)")
	}
	if ref.Ordinal < 0 {
		return errors.New("ordinal must be non-negative")
	}
	return nil
}

// MediaRef addresses the VIDEO of a media item by arr identity: the arr
// internal ID (Radarr movie ID, Sonarr series ID) plus season/episode for
// episodes. Resolution asks the arr for the current file path, so it never
// returns a stale store path. The json field names match the existing
// download wire contract (media_id = arr ID).
type MediaRef struct {
	MediaType api.MediaType `json:"media_type"`
	MediaID   int           `json:"media_id"`
	Season    int           `json:"season,omitempty"`
	Episode   int           `json:"episode,omitempty"`
}

// Validate checks the reference's field shape (not its existence).
func (ref *MediaRef) Validate() error {
	if !ref.MediaType.Valid() {
		return errors.New("invalid media_type")
	}
	if ref.MediaID <= 0 {
		return errors.New("media_id (arr id) is required")
	}
	if ref.MediaType == api.MediaTypeEpisode && (ref.Season < 0 || ref.Episode <= 0) {
		return errors.New("season and episode are required for episodes")
	}
	return nil
}

// FileStore is the store surface resolution needs: the per-media subtitle
// rows (key-derived identity incl. path; satisfied by api.Store).
type FileStore interface {
	GetSubtitleFiles(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.SubtitleEntry, error)
}

// PathValidator is the containment check run on every resolved path as
// defense-in-depth (satisfied by api.ConfigProvider).
type PathValidator interface {
	ValidatePath(ctx context.Context, path string) error
}

// SonarrEpisodes is the Sonarr surface MediaRef resolution needs.
type SonarrEpisodes interface {
	GetEpisodes(ctx context.Context, seriesID int) ([]arrapi.Episode, error)
}

// RadarrMovie is the Radarr surface MediaRef resolution needs.
type RadarrMovie interface {
	GetMovieByID(ctx context.Context, id int) (arrapi.Movie, error)
}

// State is the per-request snapshot of hot-reloadable dependencies. Arr
// clients are nil when the corresponding arr is not configured.
type State struct {
	Cfg    PathValidator
	Sonarr SonarrEpisodes
	Radarr RadarrMovie
}

// Resolver resolves typed references against the store and the arrs.
type Resolver struct {
	Store FileStore
	State func() *State
}

// SubtitleRow resolves a FileRef to its single store row. It filters the
// media item's rows by the full reference identity; zero matches answer
// ErrSubtitleNotFound, more than one ErrAmbiguous (see package doc).
func (r *Resolver) SubtitleRow(ctx context.Context, ref *FileRef) (*api.SubtitleEntry, error) {
	return r.subtitleRow(ctx, r.State(), ref)
}

// subtitleRow is SubtitleRow against one already-captured State snapshot.
// Every public resolution call captures State exactly once and threads the
// snapshot through, so a hot reload between lookup and validation can never
// validate an old-generation path against new-generation media roots.
func (r *Resolver) subtitleRow(ctx context.Context, st *State, ref *FileRef) (*api.SubtitleEntry, error) {
	if err := ref.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSubtitleNotFound, err)
	}
	rows, err := r.Store.GetSubtitleFiles(ctx, ref.MediaType, ref.MediaID)
	if err != nil {
		return nil, fmt.Errorf("subtitle rows for %s/%s: %w", ref.MediaType, ref.MediaID, err)
	}
	var match *api.SubtitleEntry
	for i := range rows {
		row := &rows[i]
		if row.MediaID != ref.MediaID ||
			row.Language != ref.Language ||
			row.Variant != ref.Variant ||
			row.Source != ref.Source ||
			row.Path == "" {
			continue
		}
		if api.ManualOrdinal(row.Path) != ref.Ordinal {
			continue
		}
		if match != nil {
			slog.Error("file reference resolves to multiple store rows",
				"media_type", ref.MediaType, "media_id", ref.MediaID,
				"language", ref.Language, "variant", ref.Variant,
				"ordinal", ref.Ordinal, "path_a", match.Path, "path_b", row.Path)
			return nil, ErrAmbiguous
		}
		match = row
	}
	if match == nil {
		return nil, ErrSubtitleNotFound
	}
	if err := validateResolved(ctx, st, match.Path); err != nil {
		return nil, err
	}
	return match, nil
}

// SubtitlePath resolves a FileRef to the row's absolute subtitle path.
func (r *Resolver) SubtitlePath(ctx context.Context, ref *FileRef) (string, error) {
	row, err := r.subtitleRow(ctx, r.State(), ref)
	if err != nil {
		return "", err
	}
	return row.Path, nil
}

// VideoPathForFile resolves the video path belonging to a FileRef's media:
// first the resolved row's own subtitle_state join (VideoPath), then any
// sibling row of the same media item that carries one. A media item whose
// rows carry no video path (nothing was ever downloaded for it) answers
// ErrMediaNotFound.
func (r *Resolver) VideoPathForFile(ctx context.Context, ref *FileRef) (string, error) {
	st := r.State()
	row, err := r.subtitleRow(ctx, st, ref)
	if err != nil {
		return "", err
	}
	if row.VideoPath != "" {
		if vErr := validateResolved(ctx, st, row.VideoPath); vErr != nil {
			return "", vErr
		}
		return row.VideoPath, nil
	}
	rows, err := r.Store.GetSubtitleFiles(ctx, ref.MediaType, ref.MediaID)
	if err != nil {
		return "", fmt.Errorf("subtitle rows for %s/%s: %w", ref.MediaType, ref.MediaID, err)
	}
	for i := range rows {
		if rows[i].MediaID == ref.MediaID && rows[i].VideoPath != "" {
			if vErr := validateResolved(ctx, st, rows[i].VideoPath); vErr != nil {
				return "", vErr
			}
			return rows[i].VideoPath, nil
		}
	}
	return "", fmt.Errorf("%w: no video path recorded for %s/%s", ErrMediaNotFound, ref.MediaType, ref.MediaID)
}

// VideoPath resolves a MediaRef to the arr-known video file path: Radarr's
// movie file for movies, the season/episode's episode file for episodes. An
// unconfigured arr, an unknown item, or an item without a file answers
// ErrMediaNotFound.
func (r *Resolver) VideoPath(ctx context.Context, ref *MediaRef) (string, error) {
	if err := ref.Validate(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrMediaNotFound, err)
	}
	st := r.State()
	path, err := arrVideoPath(ctx, st, ref)
	if err != nil {
		return "", err
	}
	if err := validateResolved(ctx, st, path); err != nil {
		return "", err
	}
	return path, nil
}

// arrVideoPath performs the arr lookup half of VideoPath: a two-arm
// dispatcher over the media type.
func arrVideoPath(ctx context.Context, st *State, ref *MediaRef) (string, error) {
	switch ref.MediaType {
	case api.MediaTypeMovie:
		return movieVideoPath(ctx, st, ref)
	case api.MediaTypeEpisode:
		return episodeVideoPath(ctx, st, ref)
	default:
		return "", fmt.Errorf("%w: unsupported media type %q", ErrMediaNotFound, ref.MediaType)
	}
}

// movieVideoPath resolves the movie arm: Radarr's current movie file path.
func movieVideoPath(ctx context.Context, st *State, ref *MediaRef) (string, error) {
	if st == nil || st.Radarr == nil {
		return "", fmt.Errorf("%w: radarr not configured", ErrMediaNotFound)
	}
	m, err := st.Radarr.GetMovieByID(ctx, ref.MediaID)
	if err != nil {
		return "", fmt.Errorf("%w: movie %d: %w", ErrMediaNotFound, ref.MediaID, err)
	}
	if m.MovieFile == nil || m.MovieFile.Path == "" {
		return "", fmt.Errorf("%w: movie %d has no file", ErrMediaNotFound, ref.MediaID)
	}
	return m.MovieFile.Path, nil
}

// episodeVideoPath resolves the episode arm: the season/episode's current
// episode file path within the Sonarr series.
func episodeVideoPath(ctx context.Context, st *State, ref *MediaRef) (string, error) {
	if st == nil || st.Sonarr == nil {
		return "", fmt.Errorf("%w: sonarr not configured", ErrMediaNotFound)
	}
	episodes, err := st.Sonarr.GetEpisodes(ctx, ref.MediaID)
	if err != nil {
		return "", fmt.Errorf("%w: series %d: %w", ErrMediaNotFound, ref.MediaID, err)
	}
	for i := range episodes {
		ep := &episodes[i]
		if ep.SeasonNumber != ref.Season || ep.EpisodeNumber != ref.Episode {
			continue
		}
		if ep.EpisodeFile == nil || ep.EpisodeFile.Path == "" {
			return "", fmt.Errorf("%w: s%02de%02d of series %d has no file", ErrMediaNotFound, ref.Season, ref.Episode, ref.MediaID)
		}
		return ep.EpisodeFile.Path, nil
	}
	return "", fmt.Errorf("%w: s%02de%02d not found in series %d", ErrMediaNotFound, ref.Season, ref.Episode, ref.MediaID)
}

// validateResolved runs the media-roots containment check on a resolved
// path against the resolution's one State snapshot. Server-derived paths
// should never fail it, so a failure logs at ERROR (invariant breach) and
// maps to a 500-class error, never a 4xx.
func validateResolved(ctx context.Context, st *State, path string) error {
	if st == nil || st.Cfg == nil {
		slog.Error("resolve: no config available for containment validation", "path", path)
		return ErrPathInvariant
	}
	if err := st.Cfg.ValidatePath(ctx, path); err != nil {
		slog.Error("resolve: server-derived path failed containment validation (invariant breach)",
			"path", path, "error", err)
		return fmt.Errorf("%w: %w", ErrPathInvariant, err)
	}
	return nil
}

// WriteError maps a resolution error onto the JSON error envelope: the two
// not-found sentinels answer 404 with their machine codes; everything else
// (ambiguity, path invariant, store failure) is a 500-class internal error.
// One mapping site so every reference-taking handler answers identically.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrMediaNotFound):
		api.NotFoundC(w, r, api.CodeMediaNotFound, "media not found")
	case errors.Is(err, ErrSubtitleNotFound):
		api.NotFoundC(w, r, api.CodeSubtitleNotFound, "subtitle not found")
	default:
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "reference resolution")
	}
}

// FileRefFromQuery parses a FileRef from GET query parameters
// (media_type, media_id, language, variant, source, ordinal). Variant
// defaults to standard and source to external; ordinal defaults to 0.
// Shape errors are returned for the handler's 400.
func FileRefFromQuery(q url.Values) (*FileRef, error) {
	ref := &FileRef{
		MediaType: api.MediaType(q.Get("media_type")),
		MediaID:   q.Get("media_id"),
		Language:  q.Get("language"),
		Variant:   q.Get("variant"),
		Source:    q.Get("source"),
	}
	if ref.Variant == "" {
		ref.Variant = string(api.VariantStandard)
	}
	if ref.Source == "" {
		ref.Source = string(api.SourceExternal)
	}
	if o := q.Get("ordinal"); o != "" {
		n, err := strconv.Atoi(o)
		if err != nil || n < 0 {
			return nil, errors.New("invalid ordinal")
		}
		ref.Ordinal = n
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	return ref, nil
}

// MediaRefFromQuery parses a MediaRef from GET query parameters
// (media_type, media_id [arr id], season, episode). Shape errors are
// returned for the handler's 400.
func MediaRefFromQuery(q url.Values) (*MediaRef, error) {
	ref := &MediaRef{MediaType: api.MediaType(q.Get("media_type"))}
	var err error
	if ref.MediaID, err = strconv.Atoi(q.Get("media_id")); err != nil {
		return nil, errors.New("media_id (arr id) must be an integer")
	}
	if s := q.Get("season"); s != "" {
		if ref.Season, err = strconv.Atoi(s); err != nil {
			return nil, errors.New("invalid season")
		}
	}
	if e := q.Get("episode"); e != "" {
		if ref.Episode, err = strconv.Atoi(e); err != nil {
			return nil, errors.New("invalid episode")
		}
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	return ref, nil
}
