package filehandlers

// Orphan files (S7 / R3.4): subtitle files on disk with no store row were
// previously invisible AND un-deletable via the API (the listing was
// store-rows-only). The listing now walks the media item's directories and
// surfaces orphans with server-minted opaque handles from a bounded TTL
// table; deletion resolves a handle exactly once, revalidates metadata +
// extension + containment, consumes it, then deletes. Expired handles
// answer 410, unknown ones 404. No client-supplied path exists anywhere in
// the flow, and the all-orphan arr fallback only walks directories of an
// arr item whose identity provably matches the requested media_id. Handles
// are random 128-bit values, meaningless off-box, and never logged.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/subtitleext"
)

const (
	// orphanTTL bounds how long a minted handle stays redeemable;
	// re-listing re-mints fresh handles.
	orphanTTL = 15 * time.Minute
	// orphanTableCap bounds the table globally; at capacity the
	// oldest-expiring entries are evicted first.
	orphanTableCap = 4096
)

// orphanEntry is the metadata recorded at mint time and revalidated at
// redemption: a size or mtime change means the file was swapped after
// listing, and the delete is refused.
type orphanEntry struct {
	mtime   time.Time
	expires time.Time
	path    string
	size    int64
}

// orphanTable is the bounded in-memory TTL handle table.
type orphanTable struct {
	entries map[string]orphanEntry
	now     func() time.Time
	mu      sync.Mutex
}

func newOrphanTable() *orphanTable {
	return &orphanTable{entries: make(map[string]orphanEntry), now: time.Now}
}

// mint inserts a new random handle for path, evicting expired entries and —
// at capacity — the entries closest to expiry.
func (t *orphanTable) mint(path string, size int64, mtime time.Time) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	handle := hex.EncodeToString(buf)

	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for h, e := range t.entries {
		if now.After(e.expires) {
			delete(t.entries, h)
		}
	}
	for len(t.entries) >= orphanTableCap {
		oldest, oldestExp := "", time.Time{}
		for h, e := range t.entries {
			if oldest == "" || e.expires.Before(oldestExp) {
				oldest, oldestExp = h, e.expires
			}
		}
		delete(t.entries, oldest)
	}
	t.entries[handle] = orphanEntry{path: path, size: size, mtime: mtime, expires: now.Add(orphanTTL)}
	return handle, nil
}

// redeemResult is the three-state outcome of an orphan-handle redemption.
// Unknown and expired handles answer different statuses (404 vs 410), so the
// table distinguishes "never knew this handle" (never minted, already
// consumed, or evicted) from "knew it, but its TTL passed".
type redeemResult int

const (
	redeemOK redeemResult = iota
	redeemExpired
	redeemUnknown
)

// consume redeems a handle exactly once: the entry is removed whether or not
// the caller's subsequent delete succeeds (a second attempt re-lists, and
// the consumed handle is unknown from then on).
func (t *orphanTable) consume(handle string) (orphanEntry, redeemResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[handle]
	if !ok {
		return orphanEntry{}, redeemUnknown
	}
	delete(t.entries, handle)
	if t.now().After(e.expires) {
		return orphanEntry{}, redeemExpired
	}
	return e, redeemOK
}

// appendOrphans walks the media item's directories and appends an orphan
// FileEntry (with a freshly minted handle) for every on-disk subtitle file
// that has no store row. Walk roots are derived from the item's own rows
// (subtitle + video paths); when the item has no store-derivable directory
// (all-orphan edge) and the caller supplied the arr ID, the arr item's file
// paths provide the roots — after the arr item's identity is verified
// against the requested media_id (arrFallbackDirs). A failed binding is a
// client error returned for the handler's 4xx; walk and transient arr
// failures degrade to the store-rows-only listing (logged, never fatal).
func (h *Handler) appendOrphans(ctx context.Context, ls *LiveState, mediaType api.MediaType, mediaID string, arrID int, rows []api.SubtitleEntry, entries []FileEntry) ([]FileEntry, error) {
	known := make(map[string]bool, len(rows))
	dirs := make(map[string]bool)
	for i := range rows {
		row := &rows[i]
		if row.Path != "" {
			known[row.Path] = true
			dirs[filepath.Dir(row.Path)] = true
		}
		if row.VideoPath != "" {
			dirs[filepath.Dir(row.VideoPath)] = true
		}
	}
	if len(dirs) == 0 {
		if err := arrFallbackDirs(ctx, ls, mediaType, mediaID, arrID, dirs); err != nil {
			return nil, err
		}
	}

	for dir := range dirs {
		entries = h.walkOrphanDir(ctx, ls, mediaID, dir, known, entries)
	}
	return entries, nil
}

// walkOrphanDir appends an orphan FileEntry (with a freshly minted handle)
// for every on-disk-capability subtitle file in dir that has no store row.
// Failures degrade to skipping the directory or file (logged, never fatal).
func (h *Handler) walkOrphanDir(ctx context.Context, ls *LiveState, mediaID, dir string, known map[string]bool, entries []FileEntry) []FileEntry {
	// Defense-in-depth: every walk root is server-derived (store rows or
	// arr items) and must sit inside the media roots.
	if err := ls.Cfg.ValidatePath(ctx, dir); err != nil {
		slog.Error("orphan walk: server-derived directory failed containment validation (invariant breach)",
			"dir", dir, "error", err)
		return entries
	}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		slog.Debug("orphan walk: directory unreadable", "dir", dir, "error", err)
		return entries
	}
	for _, de := range dirEntries {
		if de.IsDir() || !subtitleext.OnDisk(de.Name()) {
			continue
		}
		full := filepath.Join(dir, de.Name())
		if known[full] {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		handle, err := h.orphans.mint(full, fi.Size(), fi.ModTime())
		if err != nil {
			slog.Warn("orphan walk: handle mint failed", "error", err)
			continue
		}
		entries = append(entries, FileEntry{
			MediaID:      mediaID,
			Source:       string(api.SourceExternal),
			Name:         de.Name(),
			OrphanHandle: handle,
			Size:         fi.Size(),
		})
	}
	return entries
}

// Orphan-fallback binding errors: the fallback walks a directory derived
// from the client-supplied arr_id, so that identity must be PROVEN to be the
// requested media_id's own arr item before any directory is derived from it.
// A caller can otherwise request media A while supplying media B's arr ID
// and mint (then redeem) orphan handles for B's files under A.
var (
	// errArrBindingMismatch marks an arr_id whose arr item does not
	// correspond to the requested media_id (or a media_id whose format
	// cannot be bound at all). Handlers answer 400: the two client-supplied
	// identifiers contradict each other.
	errArrBindingMismatch = errors.New("arr_id does not correspond to media_id")
	// errArrItemNotFound marks an arr_id that addresses no arr item at all.
	// Handlers answer 404 media_not_found.
	errArrItemNotFound = errors.New("arr item not found")
)

// writeArrBindingError maps an orphan-fallback binding failure onto the JSON
// error envelope (mismatch -> 400, unknown arr item -> 404).
func writeArrBindingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errArrBindingMismatch):
		api.BadRequestC(w, r, api.CodeBadRequest, "arr_id does not correspond to media_id")
	case errors.Is(err, errArrItemNotFound):
		api.NotFoundC(w, r, api.CodeMediaNotFound, "arr_id addresses no known arr item")
	default:
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "orphan fallback")
	}
}

// arrFallbackDirs fills dirs from the arr item's file paths for the
// all-orphan edge (no store-derivable directory), binding the walk to the
// requested media identity: the arr item's external IDs must match the
// media_id (else errArrBindingMismatch), and an episode-level media_id
// narrows the walk to that one episode file's directory. A transient arr
// failure degrades to the store-rows-only listing (no dirs, nil error); an
// unknown arr_id answers errArrItemNotFound.
func arrFallbackDirs(ctx context.Context, ls *LiveState, mediaType api.MediaType, mediaID string, arrID int, dirs map[string]bool) error {
	if arrID <= 0 {
		return nil
	}
	switch mediaType {
	case api.MediaTypeMovie:
		return movieFallbackDirs(ctx, ls, mediaID, arrID, dirs)
	case api.MediaTypeEpisode:
		return episodeFallbackDirs(ctx, ls, mediaID, arrID, dirs)
	}
	return nil
}

// movieFallbackDirs resolves the movie arm of the bound fallback: the arr
// movie whose TMDB (or IMDB) identity matches the requested media_id
// contributes its movie file's directory.
func movieFallbackDirs(ctx context.Context, ls *LiveState, mediaID string, arrID int, dirs map[string]bool) error {
	if ls.Radarr == nil {
		return nil
	}
	m, err := ls.Radarr.GetMovieByID(ctx, arrID)
	if err != nil {
		if arrapi.IsNotFound(err) {
			return fmt.Errorf("movie %d: %w", arrID, errArrItemNotFound)
		}
		slog.Warn("orphan walk: movie lookup failed", "arr_id", arrID, "error", err)
		return nil
	}
	if !movieBindingMatches(mediaID, &m) {
		return fmt.Errorf("movie %d is not the requested media item: %w", arrID, errArrBindingMismatch)
	}
	if m.MovieFile != nil && m.MovieFile.Path != "" {
		dirs[filepath.Dir(m.MovieFile.Path)] = true
	}
	return nil
}

// episodeFallbackDirs resolves the episode arm of the bound fallback: the
// arr series whose TVDB (or IMDB) identity matches the requested media_id's
// series base contributes episode file directories — only the addressed
// episode's for an episode-level media_id ("tvdb-123-s01e05"), every
// episode's for a series-prefix media_id ("tvdb-123-", the series-level
// file manager, whose request scope IS the whole series).
func episodeFallbackDirs(ctx context.Context, ls *LiveState, mediaID string, arrID int, dirs map[string]bool) error {
	if ls.Sonarr == nil {
		return nil
	}
	base, season, episode, hasEpisode, ok := splitEpisodeMediaID(mediaID)
	if !ok {
		return fmt.Errorf("media_id %q has no bindable series identity: %w", mediaID, errArrBindingMismatch)
	}
	series, err := ls.Sonarr.GetSeriesByID(ctx, arrID)
	if err != nil {
		if arrapi.IsNotFound(err) {
			return fmt.Errorf("series %d: %w", arrID, errArrItemNotFound)
		}
		slog.Warn("orphan walk: series lookup failed", "arr_id", arrID, "error", err)
		return nil
	}
	if !seriesBindingMatches(base, &series) {
		return fmt.Errorf("series %d is not the requested media item: %w", arrID, errArrBindingMismatch)
	}
	episodes, err := ls.Sonarr.GetEpisodes(ctx, arrID)
	if err != nil {
		slog.Warn("orphan walk: episodes lookup failed", "arr_id", arrID, "error", err)
		return nil
	}
	for i := range episodes {
		ep := &episodes[i]
		if hasEpisode && (ep.SeasonNumber != season || ep.EpisodeNumber != episode) {
			continue
		}
		if ef := ep.EpisodeFile; ef != nil && ef.Path != "" {
			dirs[filepath.Dir(ef.Path)] = true
		}
	}
	return nil
}

// episodeIDSuffixRe matches the season/episode suffix of an episode-level
// store media ID as api.BuildEpisodeID emits it ("...-s01e05"; 3+ digits
// unpadded).
var episodeIDSuffixRe = regexp.MustCompile(`-s(\d+)e(\d+)$`)

// splitEpisodeMediaID splits an episode store media ID into its series base
// and the optional season/episode narrowing:
//
//	"tvdb-121361-s01e05" -> ("tvdb-121361", 1, 5, hasEpisode=true)
//	"tvdb-121361-"       -> ("tvdb-121361", 0, 0, hasEpisode=false)
//
// The second form is the series-prefix ID the series-level file manager
// lists with. Anything else is not bindable (ok=false).
func splitEpisodeMediaID(mediaID string) (base string, season, episode int, hasEpisode, ok bool) {
	if m := episodeIDSuffixRe.FindStringSubmatch(mediaID); m != nil {
		s, errS := strconv.Atoi(m[1])
		e, errE := strconv.Atoi(m[2])
		if errS != nil || errE != nil {
			return "", 0, 0, false, false
		}
		return mediaID[:len(mediaID)-len(m[0])], s, e, true, true
	}
	if b, found := strings.CutSuffix(mediaID, "-"); found && b != "" {
		return b, 0, 0, false, true
	}
	return "", 0, 0, false, false
}

// movieBindingMatches reports whether the arr movie's external identity is
// the requested store media_id (api.BuildMovieID's formats: "tmdb-<id>",
// else the raw IMDB ID).
func movieBindingMatches(mediaID string, m *arrapi.Movie) bool {
	if m.TmdbID != 0 && mediaID == "tmdb-"+strconv.Itoa(m.TmdbID) {
		return true
	}
	return m.ImdbID != "" && mediaID == m.ImdbID
}

// seriesBindingMatches reports whether the arr series' external identity is
// the requested media_id's series base (api.BuildEpisodeID's formats:
// "tvdb-<id>", else the raw IMDB ID).
func seriesBindingMatches(base string, s *arrapi.Series) bool {
	if s.TvdbID != 0 && base == "tvdb-"+strconv.Itoa(s.TvdbID) {
		return true
	}
	return s.ImdbID != "" && base == s.ImdbID
}

// deleteOrphan redeems an orphan handle and deletes the file it referenced.
// The handle is consumed exactly once; an expired handle answers 410 Gone,
// an unknown one (never minted, already consumed, or evicted) 404 — either
// way the client re-lists for fresh handles. Redemption revalidates
// containment, the delete extension capability, and the recorded size+mtime
// (a mismatch means the file was swapped after listing -> 409).
func (h *Handler) deleteOrphan(ctx context.Context, w http.ResponseWriter, r *http.Request, handle string) {
	entry, res := h.orphans.consume(handle)
	switch res {
	case redeemUnknown:
		api.NotFoundC(w, r, "orphan_handle_unknown",
			"unknown orphan handle; reload the file list")
		return
	case redeemExpired:
		api.JSONErrorWithCode(w, r, http.StatusGone, "orphan_handle_expired",
			"orphan handle expired; reload the file list")
		return
	case redeemOK:
	}

	ls := h.deps.StateFunc()
	if err := ls.Cfg.ValidatePath(ctx, entry.path); err != nil {
		slog.Error("orphan delete: recorded path failed containment validation (invariant breach)",
			"error", err)
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "orphan delete")
		return
	}
	fi, err := os.Stat(entry.path)
	if err != nil {
		api.NotFoundC(w, r, api.CodeSubtitleNotFound, "file no longer exists")
		return
	}
	if fi.Size() != entry.size || !fi.ModTime().Equal(entry.mtime) {
		slog.Warn("orphan delete: metadata changed since listing, refusing",
			"recorded_size", entry.size, "current_size", fi.Size())
		api.ConflictC(w, r, "orphan_changed",
			"file changed since listing; reload the file list")
		return
	}

	if !h.removeSubtitleFile(ctx, w, r, entry.path) {
		return
	}
	slog.Info("orphan subtitle file deleted", "path", entry.path)
	w.WriteHeader(http.StatusNoContent)
}
