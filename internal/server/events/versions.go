package events

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"

	"github.com/cplieger/keyenc"
	"github.com/cplieger/sse"
	"github.com/cplieger/subflux/internal/subflux"
)

// The digest subject kinds. One writer mints each: Publish for the seven
// event-fed kinds (switching on the event type), the sync-job registry for
// jobs. A client holds a version per (kind, ref) it read from a stamped GET
// and asks the digest which ones moved.
const (
	SubjectSeries    = "series"    // ref "": GET /api/coverage/series
	SubjectMovies    = "movies"    // ref "": GET /api/coverage/movies
	SubjectDetail    = "detail"    // ref tvdb-N | tmdb-N: the per-root detail GETs
	SubjectHistory   = "history"   // ref "": GET /api/state
	SubjectActivity  = "activity"  // ref "": GET /api/activity
	SubjectAlerts    = "alerts"    // ref "": GET /api/alerts
	SubjectProviders = "providers" // ref "": GET /api/providers/timeout
	SubjectJobs      = "jobs"      // ref "": GET /api/sync/jobs
)

// detailRootRe is the server twin of the client's positive media-id parser
// (coverage-heal.ts): a zero or leading-zero id, an imdb fallback or a
// malformed id names no root and bumps nothing.
var detailRootRe = regexp.MustCompile(`^(tvdb|tmdb)-([1-9]\d*)(?:-s\d+e\d+)?$`)

// ErrUnknownSubject is returned by Resolve for a held key outside the
// registry (an unknown kind, a non-empty ref on a ref-less kind, or a detail
// ref that is not a root); the digest answers must_refetch.
var ErrUnknownSubject = errors.New("events: unknown digest subject")

// Versions is the per-subject version table the digest compares against.
// Counters live in process memory and restart from zero with the epoch:
// a restart clears every client's map, so no persistence is needed.
// Safe for concurrent use.
type Versions struct {
	n     map[string]uint64
	epoch string
	mu    sync.Mutex
}

func newVersions(epoch string) *Versions {
	return &Versions{epoch: epoch, n: make(map[string]uint64)}
}

func versionKey(kind, ref string) string {
	return keyenc.Join(kind, ref)
}

// Bump advances the subject's version by one.
func (v *Versions) Bump(kind, ref string) {
	v.mu.Lock()
	v.n[versionKey(kind, ref)]++
	v.mu.Unlock()
}

// Current returns the subject's version as the decimal string the wire
// carries; "0" for a subject never bumped, which is also what a client that
// read a stamp before the first bump holds.
func (v *Versions) Current(kind, ref string) string {
	v.mu.Lock()
	n := v.n[versionKey(kind, ref)]
	v.mu.Unlock()
	return strconv.FormatUint(n, 10)
}

// Stamp is the subject version a stamped GET carries in its Subject-Stamp
// response header, JSON-encoded: the version the counter held when the
// request was admitted and the epoch it was minted under. A client observes
// it into its version map after applying the payload.
type Stamp struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Version string `json:"version"`
	Epoch   string `json:"epoch"`
}

// Stamp returns the subject's current stamp, or false for a (kind, ref)
// outside the registry, which is then served unstamped.
func (v *Versions) Stamp(kind, ref string) (Stamp, bool) {
	if !validSubject(kind, ref) {
		return Stamp{}, false
	}
	return Stamp{Kind: kind, Ref: ref, Version: v.Current(kind, ref), Epoch: v.epoch}, true
}

// Resolve is the sse.Resolver over this table: one current State per held
// subject, in order, or ErrUnknownSubject for a key outside the registry.
// It takes no I/O lock, so a vanished detail root is never reported gone
// here; its refetch answers 404 and the client already treats that as gone.
func (v *Versions) Resolve(_ context.Context, held []sse.Held) ([]sse.State, error) {
	out := make([]sse.State, 0, len(held))
	for _, h := range held {
		if !validSubject(h.Kind, h.Ref) {
			return nil, fmt.Errorf("%w: %s", ErrUnknownSubject, h.Kind)
		}
		out = append(out, sse.State{Subject: h.Subject, Version: v.Current(h.Kind, h.Ref), Status: sse.StatusCurrent})
	}
	return out, nil
}

func validSubject(kind, ref string) bool {
	switch kind {
	case SubjectDetail:
		return detailRoot(ref) == ref && ref != ""
	case SubjectSeries, SubjectMovies, SubjectHistory, SubjectActivity, SubjectAlerts, SubjectProviders, SubjectJobs:
		return ref == ""
	default:
		return false
	}
}

// detailRoot returns the series or movie root a media id belongs to, or ""
// when the id names none.
func detailRoot(mediaID string) string {
	m := detailRootRe.FindStringSubmatch(mediaID)
	if m == nil {
		return ""
	}
	return m[1] + "-" + m[2]
}

// bumpFor mints the versions an event's mutation moved. The bump precedes
// the frame on the wire and follows the store commit that produced the
// event; a GET reading its counter FIRST (the stamp middleware) can therefore
// label a new payload with an old version, which costs one spurious changed,
// never a false unchanged. Notify, the scan markers and sync:done mint
// nothing: a toast is not state, scan state rides the activity entry, and
// jobs has its own writer.
func (v *Versions) bumpFor(e Event) {
	switch e.Type {
	case CoverageUpdate:
		ev, ok := e.Data.(CoverageEvent)
		if !ok {
			return
		}
		v.bumpCoverage(&ev)
	case ActivityDelta:
		v.Bump(SubjectActivity, "")
	case AlertDelta:
		v.Bump(SubjectAlerts, "")
	case ProviderDelta:
		v.Bump(SubjectProviders, "")
	case Notify, ScanStart, ScanDone, SyncDone, Epoch:
	}
}

func (v *Versions) bumpCoverage(ev *CoverageEvent) {
	switch ev.MediaType {
	case subflux.MediaTypeEpisode:
		v.Bump(SubjectSeries, "")
	case subflux.MediaTypeMovie:
		v.Bump(SubjectMovies, "")
	}
	if root := detailRoot(ev.MediaID); root != "" && rootMatchesType(root, ev.MediaType) {
		v.Bump(SubjectDetail, root)
	}
	v.Bump(SubjectHistory, "")
}

// rootMatchesType mirrors the client parser's type check: every publisher
// pairs a tvdb id with an episode and a tmdb id with a movie, so a mismatch
// is a malformed event and bumps no detail.
func rootMatchesType(root string, mt subflux.MediaType) bool {
	switch mt {
	case subflux.MediaTypeEpisode:
		return root[:4] == "tvdb"
	case subflux.MediaTypeMovie:
		return root[:4] == "tmdb"
	default:
		return false
	}
}
