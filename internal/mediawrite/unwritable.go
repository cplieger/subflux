package mediawrite

import (
	"errors"
	"io/fs"
	"sync"
	"syscall"
	"time"
)

// unwritableSize and unwritableTTL bound the unwritable-directory memory: a
// dropped or expired directory costs one provider pass to learn again.
const (
	unwritableSize = 4096
	unwritableTTL  = 24 * time.Hour
)

// dirKey is a video directory: a root-relative path under one root.
type dirKey struct{ root, dir string }

// dirID is a directory as fstat sees it; a chmod, chown or replacement
// changes it, and with it what the directory refuses.
type dirID struct {
	ctimeNs        int64
	dev, ino       uint64
	mode, uid, gid uint32
}

func dirIDOf(info fs.FileInfo) (dirID, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return dirID{}, false
	}
	return dirID{ctimeNs: st.Ctim.Nano(), dev: st.Dev, ino: st.Ino, mode: st.Mode, uid: st.Uid, gid: st.Gid}, true
}

func denied(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EROFS)
}

type unwritableEntry struct {
	at  time.Time
	id  dirID
	seq uint64 // record order, so eviction never ties on a coarse clock
}

// unwritableDirs remembers the directories whose stage creation was refused
// while their root took the write probe, so automatic passes stop spending
// provider quota on a folder no write can land in. It lives in memory, so a
// restart clears it.
type unwritableDirs struct {
	dirs map[dirKey]unwritableEntry
	seq  uint64
	mu   sync.Mutex
}

func newUnwritableDirs() *unwritableDirs {
	return &unwritableDirs{dirs: map[dirKey]unwritableEntry{}}
}

// record remembers k as id, dropping the oldest entry past unwritableSize.
func (u *unwritableDirs) record(k dirKey, id dirID, now time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.seq++
	u.dirs[k] = unwritableEntry{at: now, id: id, seq: u.seq}
	if len(u.dirs) <= unwritableSize {
		return
	}
	oldest, first := dirKey{}, u.seq
	for dk, e := range u.dirs {
		if e.seq < first {
			oldest, first = dk, e.seq
		}
	}
	delete(u.dirs, oldest)
}

// holds reports whether k is remembered as id and younger than
// unwritableTTL, forgetting it when it is not.
func (u *unwritableDirs) holds(k dirKey, id dirID, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	e, ok := u.dirs[k]
	if !ok {
		return false
	}
	if e.id != id || now.Sub(e.at) >= unwritableTTL {
		delete(u.dirs, k)
		return false
	}
	return true
}

func (u *unwritableDirs) clear(k dirKey) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.dirs, k)
}

// clearRoot forgets every directory under root.
func (u *unwritableDirs) clearRoot(root string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for k := range u.dirs {
		if k.root == root {
			delete(u.dirs, k)
		}
	}
}
