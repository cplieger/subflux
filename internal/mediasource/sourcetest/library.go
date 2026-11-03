// Package sourcetest is the contract suite every mediasource adapter passes,
// the fake library a harness serves, and Fake, an in-memory adapter for Set
// and scan tests.
package sourcetest

import (
	"maps"
	"slices"
	"sync"

	"github.com/cplieger/subflux/internal/mediasource"
)

// Library is the state a fake upstream serves; every Put and Remove appends
// one Event naming its root, which Changes must then report. Its zero value
// is empty and ready.
type Library struct {
	tags    map[mediasource.TagRef]string
	roots   []LibRoot
	folders []string
	events  []Event
	mu      sync.Mutex
}

// LibRoot is one root and its items as the upstream lists them. ReadFails
// makes the upstream fail this root's own read: Items reports it unread and
// goes on, and Root answers the failure.
type LibRoot struct {
	Items     []mediasource.Item
	Root      mediasource.Root
	ReadFails bool
}

// Event is one library mutation, numbered from 1.
type Event struct {
	Root mediasource.NativeRef
	Seq  uint64
}

// Put adds r, or replaces the root with r's Native, and records an Event.
// A source reports no evidence, so r's Root.Evidence is dropped.
func (l *Library) Put(r LibRoot) { //nolint:gocritic // hugeParam: the root is stored as a copy
	r = cloneLibRoot(r)
	r.Root.Evidence = nil
	l.mu.Lock()
	defer l.mu.Unlock()
	if i := l.index(r.Root.Native); i >= 0 {
		l.roots[i] = r
	} else {
		l.roots = append(l.roots, r)
	}
	l.record(r.Root.Native)
}

// Remove deletes the root ref, if listed, and records an Event either way.
func (l *Library) Remove(ref mediasource.NativeRef) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i := l.index(ref); i >= 0 {
		l.roots = slices.Delete(l.roots, i, i+1)
	}
	l.record(ref)
}

// SetTags replaces the upstream's tags.
func (l *Library) SetTags(tags map[mediasource.TagRef]string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tags = maps.Clone(tags)
}

// SetFolders replaces the upstream's root folders.
func (l *Library) SetFolders(folders []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.folders = slices.Clone(folders)
}

// Snapshot returns deep copies, for a harness rendering the upstream's
// answers: the roots in insertion order, the tags, the folders and every
// event so far.
func (l *Library) Snapshot() (roots []LibRoot, tags map[mediasource.TagRef]string, folders []string, events []Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	roots = make([]LibRoot, len(l.roots))
	for i := range l.roots {
		roots[i] = cloneLibRoot(l.roots[i])
	}
	return roots, maps.Clone(l.tags), slices.Clone(l.folders), slices.Clone(l.events)
}

func (l *Library) index(ref mediasource.NativeRef) int {
	return slices.IndexFunc(l.roots, func(r LibRoot) bool { return r.Root.Native == ref })
}

func (l *Library) record(ref mediasource.NativeRef) {
	l.events = append(l.events, Event{Root: ref, Seq: uint64(len(l.events)) + 1})
}

func cloneLibRoot(r LibRoot) LibRoot { //nolint:gocritic // hugeParam: the copy is the result
	r.Root.AltTitles = slices.Clone(r.Root.AltTitles)
	items := make([]mediasource.Item, len(r.Items))
	for i := range r.Items {
		items[i] = r.Items[i]
		items[i].AudioLangs = slices.Clone(r.Items[i].AudioLangs)
		items[i].Tags = slices.Clone(r.Items[i].Tags)
	}
	r.Items = items
	return r
}
