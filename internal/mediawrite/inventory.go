package mediawrite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subtitleext"
	"github.com/cplieger/subflux/internal/subtitlefile"
)

// maxSubtitleBytes bounds every expectation read, and so every baseline and
// rendered file a write may create.
const maxSubtitleBytes = 16 << 20

// Found is one subtitle file of an instance as List read it.
type Found struct {
	File FileRef
	statedb.LocalFile
	Expect  Expectation
	Ordinal int
	Managed bool
}

// Inventory is every subtitle name an instance's video owns: Files the
// names the grammar reads, Unparsed the owned names it does not (or a name
// two videos sharing a stem both claim). Unwritable is set while the video
// directory is remembered as refusing stage creation under a root whose
// write probe passed.
type Inventory struct {
	Files      []Found
	Unparsed   []string
	Unwritable bool
}

// ListOptions is List's argument. With UnmanagedExpect false an unmanaged
// Found carries the zero Expectation, which every write refuses, and no
// unmanaged byte is read; with it set, so does one that cannot be read or
// is over 16 MiB.
type ListOptions struct{ UnmanagedExpect bool }

// List reads the instance's video directory once under the instance lease
// and reconciles it: a managed name whose file is gone or changed loses its
// record and baseline, a changed file becoming the user's; a name with a
// pending write is skipped. A managed file confirms by size and mtime, else
// by its hash; one that cannot be read keeps its record and is listed with
// the record's expectation. A directory that cannot be read or no longer
// holds the video answers an error and changes nothing.
func (w *Writer) List(ctx context.Context, inst statedb.InstanceID, opts ListOptions) (Inventory, error) {
	in, err := w.instance(ctx, inst)
	if err != nil {
		return Inventory{}, err
	}
	b, err := w.borrow(ctx, in.VideoPath)
	if err != nil {
		return Inventory{}, err
	}
	defer b.release()
	unlock, err := w.lease(ctx, inst, b.state.path)
	if err != nil {
		return Inventory{}, err
	}
	defer unlock()
	l, err := w.readListing(ctx, b, inst)
	if err != nil {
		return Inventory{}, err
	}
	defer l.dir.Close()
	inv, drop := l.inventory(w, opts)
	if err := w.reconcile(ctx, b, inst, drop); err != nil {
		return Inventory{}, err
	}
	if info, err := w.fs.DirStat(l.dir); err == nil {
		id, ok := dirIDOf(info)
		inv.Unwritable = ok && w.unwritable.holds(dirKey{root: b.state.path, dir: l.dirRel}, id, time.Now())
	}
	return inv, nil
}

// listing is one read of a video directory with the instance's records and
// pending names.
type listing struct {
	dir     *os.Root
	b       *borrowed
	records map[string]*statedb.FileRecord
	pending map[string]bool
	dirRel  string
	stem    string
	entries []fs.DirEntry
	stems   []string
	inst    statedb.InstanceID
}

func (w *Writer) readListing(ctx context.Context, b *borrowed, inst statedb.InstanceID) (*listing, error) {
	l := &listing{b: b, inst: inst, dirRel: filepath.Dir(b.rel), stem: subtitlefile.Stem(filepath.Base(b.rel))}
	dir, err := w.fs.OpenDir(b.h.root, l.dirRel)
	if err != nil {
		return nil, fmt.Errorf("mediawrite: open video directory of instance %d: %w", inst, err)
	}
	l.dir = dir
	if l.entries, err = w.fs.ReadDir(dir); err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("mediawrite: read video directory of instance %d: %w", inst, err)
	}
	video := filepath.Base(b.rel)
	held := false
	for _, e := range l.entries {
		held = held || e.Name() == video
		if subtitlefile.IsVideo(e.Name()) {
			l.stems = append(l.stems, subtitlefile.Stem(e.Name()))
		}
	}
	if !held {
		_ = dir.Close()
		return nil, fmt.Errorf("mediawrite: video of instance %d is gone from its directory: %w", inst, fs.ErrNotExist)
	}
	recs, err := w.store.Files(ctx, inst)
	if err == nil {
		l.pending, err = w.pendingNames(ctx, inst)
	}
	if err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("mediawrite: read records of instance %d: %w", inst, err)
	}
	l.records = make(map[string]*statedb.FileRecord, len(recs))
	for i := range recs {
		l.records[recs[i].Name] = &recs[i]
	}
	return l, nil
}

// inventory builds the instance's inventory and the records to drop.
func (l *listing) inventory(w *Writer, opts ListOptions) (Inventory, []*statedb.FileRecord) {
	var inv Inventory
	var drop []*statedb.FileRecord
	seen := map[string]bool{}
	for _, e := range l.entries {
		if !e.Type().IsRegular() || !subtitleext.OnDisk(e.Name()) || l.pending[e.Name()] {
			continue
		}
		r := l.read(w, opts, e)
		seen[e.Name()] = r.seen
		switch {
		case r.found != nil:
			inv.Files = append(inv.Files, *r.found)
		case r.unparsed:
			inv.Unparsed = append(inv.Unparsed, e.Name())
		}
		if r.stale != nil {
			drop = append(drop, r.stale)
		}
	}
	for name, rec := range l.records {
		if !seen[name] && !l.pending[name] {
			drop = append(drop, rec)
		}
	}
	return inv, drop
}

// held is every subtitle coverage counts beside the video: each record,
// and each regular unmanaged name the video owns that the grammar reads.
func (l *listing) held() []statedb.LocalFile {
	files := make([]statedb.LocalFile, 0, len(l.records))
	for _, rec := range l.records {
		files = append(files, statedb.LocalFile{Name: rec.Name, Tag: rec.Tag, Flags: rec.Flags})
	}
	for _, e := range l.entries {
		name := e.Name()
		if l.records[name] != nil || !e.Type().IsRegular() || !subtitleext.OnDisk(name) {
			continue
		}
		if mine, _ := claim(l.stems, l.stem, name); !mine {
			continue
		}
		if n, ok := subtitlefile.Parse(l.stem, name); ok && n.Tag != (language.Tag{}) {
			files = append(files, statedb.LocalFile{Name: name, Tag: n.Tag, Flags: n.Flags})
		}
	}
	return files
}

// listed is what one directory entry adds to an inventory: seen when the
// instance claims it or holds its record, stale for a record whose file
// changed.
type listed struct {
	found    *Found
	stale    *statedb.FileRecord
	seen     bool
	unparsed bool
}

// read classifies one regular on-disk subtitle entry.
func (l *listing) read(w *Writer, opts ListOptions, e fs.DirEntry) listed {
	name := e.Name()
	rec := l.records[name]
	mine, shared := claim(l.stems, l.stem, name)
	if !mine && !shared && rec == nil {
		return listed{}
	}
	r := listed{seen: true}
	info, err := e.Info()
	if err != nil {
		return r
	}
	var sum [32]byte
	hashed := false
	if rec != nil {
		var state recordState
		state, sum, hashed = l.recordState(w, name, info, rec)
		switch state {
		case recordHeld, recordUnread:
			r.found = &Found{
				Name: name, Tag: rec.Tag, Flags: rec.Flags, File: FileRef{inst: rec.Inst, name: name},
				Expect: Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256}, Ordinal: rec.Ordinal, Managed: true,
			}
			return r
		case recordGone:
			r.stale = rec
			return r
		case recordChanged:
			r.stale = rec
		}
	}
	n, parsed := subtitlefile.Parse(l.stem, name)
	if !mine || !parsed || n.Tag == (language.Tag{}) {
		r.unparsed = mine || shared
		return r
	}
	r.found = &Found{Name: name, Tag: n.Tag, Flags: n.Flags, File: FileRef{inst: l.inst, name: name}, Ordinal: n.Ordinal}
	if opts.UnmanagedExpect && !l.expect(w, r.found, info, sum, hashed) {
		r.found = nil
	}
	return r
}

// recordState is what the file at a managed name says of its record.
type recordState uint8

const (
	recordHeld    recordState = iota + 1 // the file holds the record's bytes
	recordUnread                         // the file could not be read: the record stays
	recordChanged                        // the file is the user's now
	recordGone
)

// recordState confirms the file at name against rec: equal size and mtime,
// else its hash, returned when read. A read that fails but for the file
// being gone or past maxSubtitleBytes proves no change, so the record stays.
func (l *listing) recordState(w *Writer, name string, info fs.FileInfo, rec *statedb.FileRecord) (recordState, [32]byte, bool) {
	switch {
	case info.Size() != rec.Identity.Size:
		return recordChanged, [32]byte{}, false
	case info.ModTime().UnixNano() == rec.Identity.MtimeNs:
		return recordHeld, [32]byte{}, false
	}
	sum, err := l.hash(w, name, info)
	switch {
	case err == nil && sum == rec.Identity.SHA256:
		return recordHeld, sum, true
	case err == nil:
		return recordChanged, sum, true
	case errors.Is(err, errTooLarge):
		return recordChanged, sum, false
	case errors.Is(err, fs.ErrNotExist):
		return recordGone, sum, false
	}
	w.log.Warn("mediawrite: a managed subtitle could not be read; its record stays",
		"dir", filepath.Join(l.b.state.path, l.dirRel), "name", name, "err", err)
	return recordUnread, sum, false
}

// expect sets unmanaged f's expectation to its file's hash, reusing sum
// when hashed; false when the file is gone. A file past maxSubtitleBytes or
// one that cannot be read keeps the zero Expectation every write refuses.
func (l *listing) expect(w *Writer, f *Found, info fs.FileInfo, sum [32]byte, hashed bool) bool {
	var err error
	if !hashed {
		sum, err = l.hash(w, f.Name, info)
	}
	switch {
	case err == nil:
		f.Expect = Expectation{SHA256: sum}
	case errors.Is(err, fs.ErrNotExist):
		return false
	case !errors.Is(err, errTooLarge):
		w.log.Warn("mediawrite: a subtitle could not be read; it is listed with no expectation",
			"dir", filepath.Join(l.b.state.path, l.dirRel), "name", f.Name, "err", err)
	}
	return true
}

// hash is the SHA-256 of the regular file now at name, through the cache
// keyed by info, which holds only a read of the very file info saw;
// errTooLarge past maxSubtitleBytes.
func (l *listing) hash(w *Writer, name string, info fs.FileInfo) ([32]byte, error) {
	if info.Size() > maxSubtitleBytes {
		return [32]byte{}, errTooLarge
	}
	key := hashKey{root: l.b.state.path, path: filepath.Join(l.dirRel, name), size: info.Size(), mtimeNs: info.ModTime().UnixNano(), inode: inodeOf(info)}
	if sum, ok := w.hashes.get(key); ok {
		return sum, nil
	}
	sum, opened, err := w.hashFile(l.dir, name)
	if err != nil {
		return [32]byte{}, err
	}
	if os.SameFile(opened, info) && opened.Size() == info.Size() && opened.ModTime().Equal(info.ModTime()) {
		w.hashes.put(key, sum)
	}
	return sum, nil
}

// errTooLarge is a file past maxSubtitleBytes, whose bytes no expectation
// or intent can name.
var errTooLarge = errors.New("mediawrite: the subtitle is over 16 MiB")

// hashFile reads at most maxSubtitleBytes of the regular file at name,
// refusing a symlink, and returns its hash with the stat of what it read;
// errTooLarge past the cap.
func (w *Writer) hashFile(dir *os.Root, name string) ([32]byte, fs.FileInfo, error) {
	f, info, err := w.fs.OpenRead(dir, name)
	if err != nil {
		return [32]byte{}, nil, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxSubtitleBytes+1))
	switch {
	case err != nil:
		return [32]byte{}, nil, err
	case n > maxSubtitleBytes:
		return [32]byte{}, nil, fmt.Errorf("%q: %w", name, errTooLarge)
	}
	return [32]byte(h.Sum(nil)), info, nil
}

func inodeOf(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

// claim is how the video with stem holds name among the folder's video
// stems: mine when it owns name, shared when its stem is the longest
// prefixing name but another video has the same stem.
func claim(stems []string, stem, name string) (mine, shared bool) {
	if owner, ok := subtitlefile.Owner(stems, name); ok {
		return owner == stem, false
	}
	if !strings.HasPrefix(name, stem+".") {
		return false, false
	}
	for _, s := range stems {
		if len(s) > len(stem) && strings.HasPrefix(name, s+".") {
			return false, false
		}
	}
	return false, true
}

// reconcile takes the path leases of drop's records and drops them.
func (w *Writer) reconcile(ctx context.Context, b *borrowed, inst statedb.InstanceID, drop []*statedb.FileRecord) error {
	if len(drop) == 0 {
		return nil
	}
	dirRel := filepath.Dir(b.rel)
	rels := make([]string, 0, len(drop))
	for _, rec := range drop {
		rels = append(rels, filepath.Join(dirRel, rec.Name))
	}
	unlock, err := w.lockPaths(ctx, b.state.path, rels...)
	if err != nil {
		return err
	}
	defer unlock()
	return w.dropRecords(ctx, inst, drop)
}

// dropRecords drops each record of drop, whose path leases the caller
// holds: BeginDelete and EndDelete landed, the file having been removed or
// changed outside subflux.
func (w *Writer) dropRecords(ctx context.Context, inst statedb.InstanceID, drop []*statedb.FileRecord) error {
	for _, rec := range drop {
		if err := w.store.BeginDelete(ctx, inst, rec.Name, rec.Identity.SHA256); err != nil {
			return fmt.Errorf("mediawrite: reconcile %q of instance %d: %w", rec.Name, inst, err)
		}
		if err := w.fs.End(ctx, statedb.Delete, inst, rec.Name, true); err != nil {
			return fmt.Errorf("mediawrite: reconcile %q of instance %d: %w", rec.Name, inst, err)
		}
		w.log.Info("mediawrite: a managed subtitle was removed or changed outside subflux; its record is dropped", "instance", inst, "name", rec.Name)
	}
	return nil
}

// pendingNames are the names of inst a pending intent holds.
func (w *Writer) pendingNames(ctx context.Context, inst statedb.InstanceID) (map[string]bool, error) {
	intents, err := w.store.PendingWrites(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for i := range intents {
		if intents[i].Inst == inst {
			names[intents[i].Name] = true
		}
	}
	return names, nil
}
