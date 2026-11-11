package statedb

import (
	"context"
	"fmt"
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
)

// The marks a Delta carries. Every writer and BeforeCommit sets a mark only
// through these, so a rename is a compile error at every reader.
const (
	// MarkHistory: a download attempt was recorded.
	MarkHistory = "history"
	// MarkAccounts: a user, invite, session, passkey or API key changed.
	MarkAccounts = "accounts"
	// MarkUnidentified: an unidentified sighting appeared, changed or went.
	MarkUnidentified = "unidentified"
	// MarkConfig: the activation changed; every reader re-reads in full.
	MarkConfig = "config"
)

// Delta is what changed after a revision. Roots are the library roots whose
// rows changed, Written those whose managed files changed (each also in
// Roots), Marks the marks set. Full means the feed cannot answer from the
// reader's revision, so the reader re-reads everything; it carries
// MarkConfig when an activation change is why.
type Delta struct {
	Roots   []media.RootKey
	Written []media.RootKey
	Marks   []string
	Epoch   [16]byte
	Rev     uint64
	Full    bool
}

// The kinds of a synthetic root subject, the byte after SyntheticPrefix.
const (
	subjectRoot    byte = 'R'
	subjectWritten byte = 'W'
)

func rootSubject(kind byte, r media.RootKey) bolttable.Subject {
	return bolttable.Synthetic(append([]byte{kind}, r.Bytes()...))
}

// sinceLimit is how many changes Since reads per page.
const sinceLimit = 1000

// Since answers what changed after revision after of the store opened with
// epoch: Full with the head revision for another epoch, an after of 0 or
// below the last activation change, or a range the feed no longer retains;
// otherwise every root, written root and mark since after, deduplicated.
func (s *Store) Since(ctx context.Context, epoch [16]byte, after uint64) (Delta, error) {
	d := Delta{Epoch: s.db.Epoch()}
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		head, err := r.Rev()
		if err != nil {
			return err
		}
		d.Rev = head
		act, _, err := s.t.activation.Get(r, constKey{})
		if err != nil {
			return err
		}
		switch {
		case epoch != d.Epoch || after == 0:
			d.Full = true
		case after < act.ConfigRev:
			d.Full, d.Marks = true, []string{MarkConfig}
		default:
			return collect(r, &d, after)
		}
		return nil
	})
	if err != nil {
		return Delta{}, err
	}
	return d, nil
}

// collect reads the change log after after in pages of one View, so the
// answer is the one an unpaged read gives.
func collect(r *bolttable.ReadTx, d *Delta, after uint64) error {
	b := deltaBuilder{d: d, roots: map[string]struct{}{}, written: map[string]struct{}{}, marks: map[string]struct{}{}}
	for {
		page, err := r.Since(after, sinceLimit)
		if err != nil {
			return err
		}
		if page.Full {
			d.Full, d.Roots, d.Written, d.Marks = true, nil, nil, nil
			return nil
		}
		for i := range page.Changes {
			if err := b.add(&page.Changes[i]); err != nil {
				return err
			}
			after = page.Changes[i].Rev
		}
		if !page.More {
			return nil
		}
	}
}

// deltaBuilder folds changes into a Delta, each root and mark once.
type deltaBuilder struct {
	d                     *Delta
	roots, written, marks map[string]struct{}
}

func (b *deltaBuilder) add(c *bolttable.Change) error {
	for _, sub := range c.Subjects {
		kind, root, err := parseRootSubject(sub)
		if err != nil {
			return err
		}
		if kind == subjectWritten {
			b.d.Written = addOnce(b.written, b.d.Written, string(sub[2:]), root)
		} else {
			b.d.Roots = addOnce(b.roots, b.d.Roots, string(sub[2:]), root)
		}
	}
	for _, m := range c.Marks {
		b.d.Marks = addOnce(b.marks, b.d.Marks, m, m)
	}
	return nil
}

func addOnce[T any](seen map[string]struct{}, list []T, key string, v T) []T {
	if _, dup := seen[key]; dup {
		return list
	}
	seen[key] = struct{}{}
	return append(list, v)
}

func parseRootSubject(sub bolttable.Subject) (byte, media.RootKey, error) {
	if len(sub) < 2 || sub[0] != bolttable.SyntheticPrefix || (sub[1] != subjectRoot && sub[1] != subjectWritten) {
		return 0, media.RootKey{}, fmt.Errorf("%w: change subject %x", ErrCorrupt, []byte(sub))
	}
	root, err := media.RootKeyFromBytes(sub[2:])
	if err != nil {
		return 0, media.RootKey{}, fmt.Errorf("%w: change subject %x: %w", ErrCorrupt, []byte(sub), err)
	}
	return sub[1], root, nil
}

// Changed returns the current generation's channel, closed by the next
// commit that allocates a revision. An observer reads Changed before Since
// and waits on the channel it read, so one revision wakes every observer.
func (s *Store) Changed() <-chan struct{} {
	s.sig.Lock()
	defer s.sig.Unlock()
	return s.changed
}

func (s *Store) onCommit(bolttable.Change) {
	s.sig.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.sig.Unlock()
}

// beforeCommit replaces the touched rows with the library roots they change
// (design section 3), read as stored after the write, sets the history mark
// for a download and recounts every such root (CR-UX-1). A change marked
// config records no subjects, since every reader answers it in full, and
// stores its revision as config_rev.
func (s *Store) beforeCommit(tx *bolttable.Tx, p *bolttable.Pending) ([]bolttable.Subject, error) {
	out := subjects{seen: map[string]struct{}{}}
	for _, sub := range p.Touched {
		if err := s.mapSubject(tx, &out, sub); err != nil {
			return nil, err
		}
	}
	if err := s.recount(tx, out.roots); err != nil {
		return nil, err
	}
	if slices.Contains(p.Marks, MarkConfig) {
		return nil, s.storeConfigRev(tx, p.Rev)
	}
	return out.list, nil
}

type subjects struct {
	seen  map[string]struct{}
	list  []bolttable.Subject
	roots []media.RootKey
}

func (o *subjects) add(kind byte, r media.RootKey) {
	sub := rootSubject(kind, r)
	if _, dup := o.seen[string(sub)]; !dup {
		o.seen[string(sub)] = struct{}{}
		o.list = append(o.list, sub)
		if kind == subjectRoot {
			o.roots = append(o.roots, r)
		}
	}
	if kind == subjectWritten {
		o.add(subjectRoot, r)
	}
}

// mapSubject adds sub's roots to out. A table with no case here fails the
// commit, so a new non-silent table cannot publish nothing unnoticed.
func (s *Store) mapSubject(tx *bolttable.Tx, out *subjects, sub bolttable.Subject) error {
	r, b := tx.Read(), []byte(sub[1:])
	switch sub[0] {
	case bolttable.SyntheticPrefix:
		kind, root, err := parseRootSubject(sub)
		if err == nil {
			out.add(kind, root)
		}
		return err
	case pItems, pCoverage:
		return addKeyRoot(out, mediaKeys, b, func(k media.Key) media.Key { return k })
	case pLocks:
		return addKeyRoot(out, lockKeys, b, func(k lockKey) media.Key { return k.Key })
	case pInstances, pLocal, pEmbedded:
		id, err := instKeys.ParseKey(b)
		if err != nil {
			return err
		}
		return s.addInstanceRoots(r, out, id, subjectRoot)
	case pFiles:
		k, err := fileKeys.ParseKey(b)
		if err != nil {
			return err
		}
		return s.addInstanceRoots(r, out, k.Inst, subjectWritten)
	case pRoots:
		return s.addRootFactsRoots(r, out, b)
	case pSightings:
		return s.addSightingRoot(r, out, b)
	case pDownloads:
		tx.Mark(MarkHistory)
		return s.addDownloadRoots(r, out, b)
	case pUsers, pSessions, pPasskeys, pKeys, pAttributions:
		tx.Mark(MarkAccounts)
		return nil
	}
	return fmt.Errorf("statedb: table %#x has no change mapping", sub[0])
}

func addKeyRoot[K any](out *subjects, c keyCodec[K], b []byte, key func(K) media.Key) error {
	k, err := c.ParseKey(b)
	if err == nil {
		out.add(subjectRoot, key(k).Root())
	}
	return err
}

// addRootFactsRoots adds the roots of a source root's identified sightings.
func (s *Store) addRootFactsRoots(r *bolttable.ReadTx, out *subjects, b []byte) error {
	ref, err := rootRefKeys.ParseKey(b)
	if err != nil {
		return err
	}
	return s.t.sightings.Scan(r, bolttable.Range{Prefix: appendRootRef(nil, ref)}, func(_ itemRef, v *sightingRow) (bool, error) {
		if v.Key != nil {
			out.add(subjectRoot, v.Key.k.Root())
		}
		return true, nil
	})
}

func (s *Store) addSightingRoot(r *bolttable.ReadTx, out *subjects, b []byte) error {
	ref, err := itemRefKeys.ParseKey(b)
	if err != nil {
		return err
	}
	v, found, err := s.t.sightings.Get(r, ref)
	if found && v.Key != nil {
		out.add(subjectRoot, v.Key.k.Root())
	}
	return err
}

func (s *Store) addDownloadRoots(r *bolttable.ReadTx, out *subjects, b []byte) error {
	seq, err := downloadSeqCodec.ParseKey(b)
	if err != nil {
		return err
	}
	v, _, err := s.t.downloads.Get(r, seq)
	for _, k := range v.Items {
		out.add(subjectRoot, k.k.Root())
	}
	return err
}

// addInstanceRoots adds the roots of instance id's items as stored; a
// deleted instance adds none, since every removal rewrites the item records
// it detaches and deleteInstance touches its roots first.
func (s *Store) addInstanceRoots(r *bolttable.ReadTx, out *subjects, id InstanceID, kind byte) error {
	v, _, err := s.t.instances.Get(r, id)
	if err != nil {
		return err
	}
	for _, k := range v.Items {
		out.add(kind, k.k.Root())
	}
	return nil
}

func (s *Store) storeConfigRev(tx *bolttable.Tx, rev uint64) error {
	act, _, err := s.t.activation.Get(tx.Read(), constKey{})
	if err != nil {
		return err
	}
	act.ConfigRev = rev
	return s.t.activation.Put(tx, constKey{}, &act)
}
