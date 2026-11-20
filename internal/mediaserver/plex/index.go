package plex

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/plexapi/v3"
)

const (
	indexMaxAge      = 15 * time.Minute
	missRebuildEvery = time.Hour // a path no Plex section holds misses on every call
	buildTimeout     = 5 * time.Minute
	buildRetry       = time.Minute // a failed build is not restarted sooner
	deltaMargin      = time.Minute
	deltaReuse       = 10 * time.Second
	pageSize         = 500
)

// pathKey is an index key: the first 16 bytes of SHA-256 of a server path,
// about 4 MB of keys at 60,000 items.
type pathKey [16]byte

func keyOf(serverPath string) pathKey {
	sum := sha256.Sum256([]byte(serverPath))
	return pathKey(sum[:16])
}

// section is one movie or show section and the leaf type its items are.
type section struct {
	key          plexapi.RatingKey
	metadataType int
}

// index maps part paths to the rating keys holding them, and owns the
// adapter's lifecycle state: every lookup, build start, merge commit,
// correction and Shutdown reads and changes it under mu. One goroutine at a
// time builds a fresh index; the old maps serve until a build succeeds.
type index struct {
	cur *built // nil until the first build succeeds
	// gone holds each rating key Plex answered not-found for until a build
	// that started after that answer succeeds, so a walk that listed the key
	// before Plex removed it cannot put it back.
	gone map[string]goneKey
	// highWater is each section's updatedAt bound for the next merge, in Unix
	// seconds: seeded from buildStarted less deltaMargin and moved only after
	// a merge's index update commits, so a failed merge re-reads its items.
	highWater    map[plexapi.RatingKey]int64
	building     chan struct{} // closed when the running build ends; nil when none runs
	buildErr     error         // the latest build's failure, nil after a success
	buildStarted time.Time     // the latest successful build's start
	merged       time.Time     // the latest committed merge's end
	missRebuild  time.Time     // the start of the latest miss-driven rebuild, cleared when it fails
	failedAt     time.Time
	gen          uint64 // counts successful builds; a merge snapshot from an older one is dropped
	mu           sync.Mutex
	stopped      bool // set by Shutdown
}

// goneKey is when Plex answered a rating key not-found, and floor, the
// highest updatedAt the index held for it: a body at or below it was served
// before Plex removed the item.
type goneKey struct {
	at    time.Time
	floor int64
}

// errIndexBuilding answers a caller whose deadline ends before the build it
// waits on; transient, so mediarefresh retries.
var errIndexBuilding error = transientError("plex: the path index is still being built")

type transientError string

func (e transientError) Error() string   { return string(e) }
func (transientError) IsTransient() bool { return true }

// unbuiltError is a lookup with no index because the latest build failed;
// transient, since the next build may succeed.
type unbuiltError struct{ err error }

func (e unbuiltError) Error() string {
	return "plex: the path index could not be built: " + e.err.Error()
}
func (e unbuiltError) Unwrap() error   { return e.err }
func (unbuiltError) IsTransient() bool { return true }

// holders calls f with each located item Plex confirms holds a part over
// serverPath, in index order, until f answers stop. Every answer Plex gives
// for a located key corrects the index, a not-found from f included; when no
// located key holds the part, the path is located once more, so an item Plex
// re-added under a new key is found through the merge. A path still absent
// calls f never and answers nil.
func (a *adapter) holders(ctx context.Context, serverPath string, f func(key string, p *plexapi.Part) (stop bool, err error)) error {
	k := keyOf(serverPath)
	for range 2 {
		keys, err := a.locate(ctx, k)
		if err != nil || len(keys) == 0 {
			return err
		}
		if held, err := a.visit(ctx, keys, serverPath, f); held || err != nil {
			return err
		}
	}
	return nil
}

// visit calls f with each of keys whose item Plex confirms holds a part over
// serverPath, answering whether one did; f answering stop ends it there.
func (a *adapter) visit(ctx context.Context, keys []string, serverPath string, f func(key string, p *plexapi.Part) (stop bool, err error)) (held bool, err error) {
	for _, key := range keys {
		p, err := a.confirm(ctx, key, serverPath)
		if err != nil {
			return false, err
		}
		if p == nil {
			continue
		}
		stop, err := f(key, p)
		switch {
		case plexapi.IsNotFound(err):
			a.idx.correct(key, nil, time.Now())
			continue
		case err != nil:
			return false, err
		case stop:
			return true, nil
		}
		held = true
	}
	return held, nil
}

// confirm reads the located key's item and corrects the index with Plex's
// answer, answering the item's part over serverPath, nil when the item is
// gone or no longer holds one.
func (a *adapter) confirm(ctx context.Context, key, serverPath string) (*plexapi.Part, error) {
	it, err := a.c.Metadata(ctx, plexapi.RatingKey(key))
	switch {
	case plexapi.IsNotFound(err):
		a.idx.correct(key, nil, time.Now())
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading item %s: %w", key, err)
	}
	a.idx.correct(key, it, time.Now())
	return partAt(it, serverPath), nil
}

// locate looks the path up in the index. A lookup with no index yet starts
// the first build and waits for it until ctx ends, then answers a transient
// "index building" error. A miss first merges the items Plex updated since
// each section's high-water and looks again; one still missing on an index
// older than 15 minutes starts a rebuild, at most one an hour after a
// rebuild that succeeded, and waits for it the same way. A path still absent
// answers empty.
func (a *adapter) locate(ctx context.Context, k pathKey) ([]string, error) {
	keys, built, err := a.lookup(k)
	if err != nil {
		return nil, err
	}
	if !built {
		if werr := await(ctx, a.idx.startBuild(a)); werr != nil {
			return nil, werr
		}
		if keys, built, err = a.lookup(k); err != nil {
			return nil, err
		}
		if !built {
			return nil, a.idx.unbuilt()
		}
	}
	if len(keys) > 0 {
		return keys, nil
	}
	if merr := a.merge(ctx); merr != nil {
		return nil, merr
	}
	if keys, _, err = a.lookup(k); err != nil || len(keys) > 0 {
		return keys, err
	}
	if werr := await(ctx, a.idx.staleRebuild(a)); werr != nil {
		return nil, werr
	}
	keys, _, err = a.lookup(k)
	return keys, err
}

// lookup answers k's rating keys and whether an index exists.
func (a *adapter) lookup(k pathKey) (keys []string, built bool, err error) {
	a.idx.mu.Lock()
	defer a.idx.mu.Unlock()
	if a.idx.stopped {
		return nil, false, errShutDown
	}
	if a.idx.cur == nil {
		return nil, false, nil
	}
	return slices.Clone(a.idx.cur.byPath[k]), true, nil
}

// await waits for the build behind done, nil when none was started.
func await(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errIndexBuilding
	}
}

// unbuilt answers a lookup that found no index after its wait.
func (ix *index) unbuilt() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.buildErr == nil {
		return errIndexBuilding
	}
	return unbuiltError{ix.buildErr}
}

// startBuild starts a build unless one runs, the adapter is shut down, or
// the latest failed within buildRetry; it answers the running build's
// channel, nil when none runs.
func (ix *index) startBuild(a *adapter) <-chan struct{} {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	done, _ := ix.startLocked(a)
	return done
}

// staleRebuild starts a rebuild for a miss when the index is older than
// indexMaxAge and no miss-driven rebuild that succeeded started within
// missRebuildEvery; it answers the build to wait for, nil when none.
func (ix *index) staleRebuild(a *adapter) <-chan struct{} {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	now := time.Now()
	if now.Sub(ix.buildStarted) <= indexMaxAge || !ix.missRebuild.IsZero() && now.Sub(ix.missRebuild) < missRebuildEvery {
		return nil
	}
	done, started := ix.startLocked(a)
	if started {
		ix.missRebuild = now
	}
	return done
}

// startLocked starts a build unless one runs, Shutdown began, or the latest
// failed within buildRetry. It answers the running build's channel, nil
// when none runs, and whether this call started it.
func (ix *index) startLocked(a *adapter) (done <-chan struct{}, started bool) {
	switch {
	case ix.stopped:
		return nil, false
	case ix.building != nil:
		return ix.building, false
	case !ix.failedAt.IsZero() && time.Since(ix.failedAt) < buildRetry:
		return nil, false
	}
	ch := make(chan struct{})
	ix.building = ch
	a.work.Go(func() { a.build(ch) })
	return ch, true
}

// build walks every movie and show section's leaves on a context detached
// from every caller, bounded by buildTimeout and cancelled by Shutdown.
func (a *adapter) build(done chan struct{}) {
	ctx, cancel := context.WithTimeout(a.life, buildTimeout)
	defer cancel()
	started := time.Now()
	fresh, err := a.walk(ctx)
	a.idx.finish(started, fresh, err)
	if err != nil && a.life.Err() == nil {
		a.log.Warn("plex: building the path index failed; the old index keeps serving", "error", err)
	}
	close(done)
}

// built is a fresh index's maps and sections.
type built struct {
	byPath   map[pathKey][]string
	byKey    map[string]entry
	sections []section
}

// entry is one rating key's part paths and the updatedAt Plex answered them
// with.
type entry struct {
	paths   []pathKey
	updated int64
}

func (a *adapter) walk(ctx context.Context) (*built, error) {
	all, err := a.c.Sections(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the library sections: %w", err)
	}
	b := &built{byPath: map[pathKey][]string{}, byKey: map[string]entry{}}
	for _, s := range all {
		typ, ok := leafType(s.Type)
		if !ok {
			continue
		}
		sec := section{key: plexapi.RatingKey(s.Key), metadataType: typ}
		b.sections = append(b.sections, sec)
		for it, err := range a.c.WalkSectionItems(ctx, sec.key, typ, plexapi.Page{Size: pageSize}, nil) {
			if err != nil {
				return nil, fmt.Errorf("reading section %s: %w", s.Key, err)
			}
			b.put(&it)
		}
	}
	return b, nil
}

// leafType is the metadata type of a section type's leaves: a movie
// section's movies, a show section's episodes.
func leafType(sectionType string) (int, bool) {
	switch sectionType {
	case "movie":
		return plexapi.MetadataTypeMovie, true
	case "show":
		return plexapi.MetadataTypeEpisode, true
	}
	return 0, false
}

// put indexes the item's part paths in place of any it held before, unless
// the index holds a newer answer for its key: of a walk and a read racing
// over one item, the later updatedAt wins in either order. An answer with no
// updatedAt cannot be ordered and replaces.
func (b *built) put(it *plexapi.Item) {
	if it.RatingKey == "" {
		return
	}
	if old, ok := b.byKey[it.RatingKey]; ok && it.UpdatedAt > 0 && old.updated > it.UpdatedAt {
		return
	}
	b.remove(it.RatingKey)
	var keys []pathKey
	for _, m := range it.Media {
		for _, p := range m.Part {
			if p.File == "" {
				continue
			}
			k := keyOf(p.File)
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
				b.byPath[k] = append(b.byPath[k], it.RatingKey)
			}
		}
	}
	b.byKey[it.RatingKey] = entry{paths: keys, updated: it.UpdatedAt}
}

// remove drops the key from every path holding it.
func (b *built) remove(key string) {
	for _, p := range b.byKey[key].paths {
		b.byPath[p] = slices.DeleteFunc(b.byPath[p], func(k string) bool { return k == key })
		if len(b.byPath[p]) == 0 {
			delete(b.byPath, p)
		}
	}
	delete(b.byKey, key)
}

// correct takes Plex's answer for a located key, read at at, into whatever
// index serves now. A nil item, a not-found, removes the key and keeps it out
// of later merges, unless the index was built from a walk started after the
// answer; a body for a removed key is taken only above its floor. An item
// replaces the key's parts unless the index holds a newer answer, so a key
// whose item no longer holds a path leaves that path. A read only holds keys
// a built index answered, and cur never returns to nil.
func (ix *index) correct(key string, it *plexapi.Item, at time.Time) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if it == nil {
		if at.Before(ix.buildStarted) {
			return
		}
		ix.gone[key] = goneKey{at: at, floor: max(ix.gone[key].floor, ix.cur.byKey[key].updated)}
		ix.cur.remove(key)
		return
	}
	if g, ok := ix.gone[key]; ok {
		if it.UpdatedAt <= g.floor {
			return
		}
		delete(ix.gone, key)
	}
	ix.cur.put(it)
}

// finish ends a build: a success replaces the index, re-seeds every
// section's high-water from started and keeps out each key Plex answered
// not-found for since started, raising its floor to the walk's listing and
// forgetting older ones, which its walk could not list. A failure keeps the old index and clears missRebuild, so
// buildRetry alone delays the next miss's rebuild.
func (ix *index) finish(started time.Time, fresh *built, err error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.building = nil
	if err != nil {
		ix.failedAt, ix.buildErr, ix.missRebuild = time.Now(), err, time.Time{}
		return
	}
	for key, g := range ix.gone {
		if g.at.Before(started) {
			delete(ix.gone, key)
			continue
		}
		g.floor = max(g.floor, fresh.byKey[key].updated)
		ix.gone[key] = g
		fresh.remove(key)
	}
	ix.cur = fresh
	ix.buildStarted, ix.failedAt, ix.buildErr = started, time.Time{}, nil
	ix.gen++
	seed := max(started.Add(-deltaMargin).Unix(), 0)
	ix.highWater = make(map[plexapi.RatingKey]int64, len(fresh.sections))
	for _, s := range fresh.sections {
		ix.highWater[s.key] = seed
	}
}

// snapshot answers the sections to merge, their high-waters and the build
// generation they belong to.
func (ix *index) snapshot() (sections []section, highWater map[plexapi.RatingKey]int64, gen uint64) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.cur == nil {
		return nil, nil, ix.gen
	}
	return slices.Clone(ix.cur.sections), maps.Clone(ix.highWater), ix.gen
}
