package statedb

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// RootRow is one listed root: its display facts come from the source of its
// lowest item that is owned or has no instance.
type RootRow struct {
	Source     media.SourceID
	Root       mediasource.Root
	Key        media.RootKey
	HasHistory bool
}

// ItemRow is one listed item: its facts from the sighting its record names,
// its instance (0: no video) and its configured pairs.
type ItemRow struct {
	Coverage   []CoverageRow
	Item       mediasource.Item
	Key        media.Key
	Inst       InstanceID
	HasHistory bool
}

// UnidentifiedRow is a sighting no key could be chosen for, with its root.
type UnidentifiedRow struct {
	Source media.SourceID
	Root   mediasource.Root
	Item   mediasource.Item
}

// Instance is one video file; an empty Source is ownerless.
type Instance struct {
	Source    media.SourceID
	VideoPath string
	SceneName string
	Items     []media.Key
	ID        InstanceID
}

// RootTarget is one source root owning instances under a root key, with
// those instances' video paths.
type RootTarget struct {
	Root       mediasource.NativeRef
	Source     media.SourceID
	VideoPaths []string
}

func rootsList() listScope {
	return listScope{list: listRoots, valid: func(last []byte) bool {
		_, err := media.RootKeyFromBytes(last)
		return err == nil
	}}
}

func itemsList(root media.RootKey) listScope {
	return listScope{list: listItems, prefix: root.Bytes(), valid: func(last []byte) bool {
		_, err := media.KeyFromBytes(last)
		return err == nil
	}}
}

func unidentifiedList() listScope {
	return listScope{list: listUnidentified, valid: func(last []byte) bool {
		_, err := itemRefKeys.ParseKey(last)
		return err == nil
	}}
}

// Roots returns up to limit roots in key order that have an item owned or
// with no instance, and the cursor of the next page, "" after the last.
// limit outside 1 to 500 is an error; a cursor of another list is
// ErrCursorInvalid.
func (s *Store) Roots(ctx context.Context, c ListCursor, limit int) ([]RootRow, ListCursor, error) {
	if err := checkLimit(limit); err != nil {
		return nil, "", err
	}
	l := rootsList()
	last, err := l.position(c)
	if err != nil {
		return nil, "", err
	}
	p := newPage[RootRow](&l, limit)
	err = s.db.View(ctx, func(r *bolttable.ReadTx) error {
		for from := pastRoot(last); ; from = pastRoot(p.last) {
			k, rec, found, ferr := s.firstListed(r, bolttable.Range{From: from})
			if ferr != nil || !found || p.full() {
				return ferr
			}
			row, rerr := s.rootRow(r, k.Root(), &rec)
			if rerr != nil {
				return rerr
			}
			p.add(row, row.Key.Bytes())
		}
	})
	if err != nil {
		return nil, "", err
	}
	return p.rows, p.next, nil
}

// pastRoot is the first item key after every item of the root whose ten
// bytes are root; nil for the first page.
func pastRoot(root []byte) []byte {
	if root == nil {
		return nil
	}
	highest := bytes.Repeat([]byte{0xff}, len(media.Key{}.Bytes())-len(root))
	return resumeAt(append(slices.Clip(root), highest...))
}

// Root returns root's row as Roots lists it; false when no item of root is
// owned or has no instance.
func (s *Store) Root(ctx context.Context, root media.RootKey) (RootRow, bool, error) {
	var row RootRow
	var found bool
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		_, rec, listed, err := s.firstListed(r, bolttable.Range{Prefix: root.Bytes()})
		if err != nil || !listed {
			return err
		}
		row, err = s.rootRow(r, root, &rec)
		found = err == nil
		return err
	})
	return row, found, err
}

// firstListed is the first item record in rg that is owned or has no
// instance.
func (s *Store) firstListed(r *bolttable.ReadTx, rg bolttable.Range) (media.Key, itemRow, bool, error) { //nolint:gocritic // hugeParam: callers build a Range inline
	var key media.Key
	var rec itemRow
	var found bool
	err := s.t.items.Scan(r, rg, func(k media.Key, v *itemRow) (bool, error) {
		listed, err := s.listed(r, k, v)
		if err != nil || !listed {
			return err == nil, err
		}
		key, rec, found = k, *v, true
		return false, nil
	})
	return key, rec, found, err
}

// listed is whether rec's item shows in lists: it has no instance or an
// owned one. An ownerless instance's items wait for the source that claims
// it.
func (s *Store) listed(r *bolttable.ReadTx, k media.Key, rec *itemRow) (bool, error) {
	if rec.Inst == 0 {
		return true, nil
	}
	inst, held, err := s.t.instances.Get(r, InstanceID(rec.Inst))
	switch {
	case err != nil:
		return false, err
	case !held:
		return false, fmt.Errorf("statedb: item %s names instance %d, which is gone", k, rec.Inst)
	}
	return inst.Owner != "", nil
}

func (s *Store) rootRow(r *bolttable.ReadTx, root media.RootKey, rec *itemRow) (RootRow, error) {
	ref := rec.ref().rootRef
	rr, found, err := s.t.roots.Get(r, ref)
	switch {
	case err != nil:
		return RootRow{}, err
	case !found:
		return RootRow{}, fmt.Errorf("statedb: root %s names %s's root %s, which is gone", root, ref.Source, ref.Root.ID)
	}
	has, err := s.hasHistory(r, RootHistory(root))
	if err != nil {
		return RootRow{}, err
	}
	return RootRow{Key: root, Source: ref.Source, Root: rr.root(ref.Root), HasHistory: has}, nil
}

// Items returns up to limit of root's items in key order, skipping those of
// ownerless instances, and the cursor of the next page, "" after the last.
// limit outside 1 to 500 is an error; a cursor of another list or root is
// ErrCursorInvalid.
func (s *Store) Items(ctx context.Context, root media.RootKey, c ListCursor, limit int) ([]ItemRow, ListCursor, error) {
	if err := checkLimit(limit); err != nil {
		return nil, "", err
	}
	l := itemsList(root)
	last, err := l.position(c)
	if err != nil {
		return nil, "", err
	}
	p := newPage[ItemRow](&l, limit)
	err = s.db.View(ctx, func(r *bolttable.ReadTx) error {
		return s.t.items.Scan(r, bolttable.Range{Prefix: l.prefix, From: resumeAt(last)}, func(k media.Key, v *itemRow) (bool, error) {
			listed, lerr := s.listed(r, k, v)
			if lerr != nil || !listed {
				return lerr == nil, lerr
			}
			if p.full() {
				return false, nil
			}
			row, rerr := s.itemRow(r, k, v)
			p.add(row, k.Bytes())
			return rerr == nil, rerr
		})
	})
	if err != nil {
		return nil, "", err
	}
	return p.rows, p.next, nil
}

// ItemRow returns k's row as Items lists it; false when k has no record or
// its instance is ownerless.
func (s *Store) ItemRow(ctx context.Context, k media.Key) (ItemRow, bool, error) {
	var row ItemRow
	var found bool
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		rec, stored, err := s.t.items.Get(r, k)
		if err != nil || !stored {
			return err
		}
		if found, err = s.listed(r, k, &rec); err != nil || !found {
			return err
		}
		row, err = s.itemRow(r, k, &rec)
		found = err == nil
		return err
	})
	return row, found, err
}

func (s *Store) itemRow(r *bolttable.ReadTx, k media.Key, rec *itemRow) (ItemRow, error) {
	ref := rec.ref()
	sg, err := s.namedSighting(r, k, ref)
	if err != nil {
		return ItemRow{}, err
	}
	cov, err := s.coverageRows(r, k)
	if err != nil {
		return ItemRow{}, err
	}
	has, err := s.hasHistory(r, ItemHistory(k))
	if err != nil {
		return ItemRow{}, err
	}
	return ItemRow{Key: k, Item: sg.item(ref.Item), Inst: InstanceID(rec.Inst), Coverage: cov, HasHistory: has}, nil
}

// namedSighting is the sighting k's item record names, which a live record
// always has.
func (s *Store) namedSighting(r *bolttable.ReadTx, k media.Key, ref itemRef) (sightingRow, error) {
	sg, found, err := s.t.sightings.Get(r, ref)
	if err == nil && !found {
		err = fmt.Errorf("statedb: item %s names %s's sighting %s, which is gone", k, ref.Source, ref.Item)
	}
	return sg, err
}

// Item returns k's facts from the sighting its item record names, and that
// sighting's root; false when k has no record or its instance is ownerless.
func (s *Store) Item(ctx context.Context, k media.Key) (mediasource.Item, mediasource.Root, bool, error) {
	var it mediasource.Item
	var root mediasource.Root
	var found bool
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		rec, stored, err := s.t.items.Get(r, k)
		if err != nil || !stored {
			return err
		}
		if found, err = s.listed(r, k, &rec); err != nil || !found {
			return err
		}
		ref := rec.ref()
		sg, err := s.namedSighting(r, k, ref)
		if err != nil {
			return err
		}
		rr, stored, err := s.t.roots.Get(r, ref.rootRef)
		if err == nil && !stored {
			err = fmt.Errorf("statedb: item %s names %s's root %s, which is gone", k, ref.Source, ref.Root.ID)
		}
		it, root = sg.item(ref.Item), rr.root(ref.Root)
		found = err == nil
		return err
	})
	return it, root, found, err
}

// Unidentified returns up to limit sightings no key could be chosen for, by
// source, root and item, and the cursor of the next page, "" after the
// last. limit outside 1 to 500 is an error; a cursor of another list is
// ErrCursorInvalid.
func (s *Store) Unidentified(ctx context.Context, c ListCursor, limit int) ([]UnidentifiedRow, ListCursor, error) {
	if err := checkLimit(limit); err != nil {
		return nil, "", err
	}
	l := unidentifiedList()
	last, err := l.position(c)
	if err != nil {
		return nil, "", err
	}
	p := newPage[UnidentifiedRow](&l, limit)
	err = s.db.View(ctx, func(r *bolttable.ReadTx) error {
		return s.t.unidentified.Scan(r, bolttable.Range{From: resumeAt(last)}, func(ikey []byte, ref itemRef, _ []byte) (bool, error) {
			if p.full() {
				return false, nil
			}
			row, rerr := s.unidentifiedRow(r, ref)
			p.add(row, ikey)
			return rerr == nil, rerr
		})
	})
	if err != nil {
		return nil, "", err
	}
	return p.rows, p.next, nil
}

func (s *Store) unidentifiedRow(r *bolttable.ReadTx, ref itemRef) (UnidentifiedRow, error) {
	sg, found, err := s.t.sightings.Get(r, ref)
	if err == nil && !found {
		err = fmt.Errorf("%w: the unidentified index names %s's sighting %s, which is gone", bolttable.ErrCorrupt, ref.Source, ref.Item)
	}
	if err != nil {
		return UnidentifiedRow{}, err
	}
	rr, found, err := s.t.roots.Get(r, ref.rootRef)
	if err == nil && !found {
		err = fmt.Errorf("statedb: %s's sighting %s names root %s, which is gone", ref.Source, ref.Item, ref.Root.ID)
	}
	return UnidentifiedRow{Source: ref.Source, Root: rr.root(ref.Root), Item: sg.item(ref.Item)}, err
}

// Instance returns any stored instance, ownerless ones included; false for
// an unknown id.
func (s *Store) Instance(ctx context.Context, id InstanceID) (Instance, bool, error) {
	var row instanceRow
	var found bool
	if err := s.db.View(ctx, func(r *bolttable.ReadTx) (err error) {
		row, found, err = s.t.instances.Get(r, id)
		return err
	}); err != nil || !found {
		return Instance{}, false, err
	}
	items := make([]media.Key, 0, len(row.Items))
	for _, x := range row.Items {
		items = append(items, x.k)
	}
	return Instance{ID: id, Source: row.Owner, VideoPath: row.VideoPath, SceneName: row.SceneName, Items: items}, true, nil
}

// Files returns the instance's managed file records by name; none for an
// unknown instance.
func (s *Store) Files(ctx context.Context, id InstanceID) ([]FileRecord, error) {
	var out []FileRecord
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		out = nil
		return s.t.files.Scan(r, bolttable.Range{Prefix: appendUint64(nil, uint64(id))}, func(fk fileKey, v *fileRow) (bool, error) {
			rec, err := v.record(fk)
			out = append(out, rec)
			return err == nil, err
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RootTargets returns one target per source root owning instances under
// root, by source and root, each with its instances' video paths sorted.
func (s *Store) RootTargets(ctx context.Context, root media.RootKey) ([]RootTarget, error) {
	var out []RootTarget
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		out = nil
		seen := map[InstanceID]bool{}
		return s.t.items.Scan(r, bolttable.Range{Prefix: root.Bytes()}, func(k media.Key, v *itemRow) (bool, error) {
			id := InstanceID(v.Inst)
			if id == 0 || seen[id] {
				return true, nil
			}
			seen[id] = true
			inst, held, err := s.t.instances.Get(r, id)
			switch {
			case err != nil:
				return false, err
			case !held:
				return false, fmt.Errorf("statedb: item %s names instance %d, which is gone", k, id)
			case inst.Owner == "":
				return true, nil
			}
			out = addTarget(out, RootTarget{Source: v.Source, Root: v.Root.ref()}, inst.VideoPath)
			return true, nil
		})
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b RootTarget) int {
		return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.Root.Kind, b.Root.Kind), cmp.Compare(a.Root.ID, b.Root.ID))
	})
	for i := range out {
		slices.Sort(out[i].VideoPaths)
	}
	return out, nil
}

func addTarget(ts []RootTarget, t RootTarget, path string) []RootTarget {
	i := slices.IndexFunc(ts, func(x RootTarget) bool { return x.Source == t.Source && x.Root == t.Root })
	if i < 0 {
		ts, i = append(ts, t), len(ts)
	}
	ts[i].VideoPaths = append(ts[i].VideoPaths, path)
	return ts
}

// rootRowOf is r less its Evidence, which table 0x17 holds.
func rootRowOf(r *mediasource.Root) rootRow {
	return rootRow{
		Language: rowTag{t: r.Language}, Order: orderRow{Type: r.Order.Type, Name: r.Order.Name, Aired: r.Order.Aired},
		Title: r.Title, Link: r.Link, AltTitles: r.AltTitles, External: externalRowOf(r.External), Year: r.Year,
	}
}

func (r *rootRow) root(native mediasource.NativeRef) mediasource.Root {
	return mediasource.Root{
		Native: native, Language: r.Language.t, Order: mediasource.Order{Type: r.Order.Type, Name: r.Order.Name, Aired: r.Order.Aired},
		Title: r.Title, Link: r.Link, AltTitles: r.AltTitles, External: r.External.ids(), Year: r.Year,
	}
}

// sightingRowOf is it's facts with no key; the caller sets the claimed key.
func sightingRowOf(it *mediasource.Item) sightingRow {
	audio := make([]rowTag, 0, len(it.AudioLangs))
	for _, t := range it.AudioLangs {
		audio = append(audio, rowTag{t: t})
	}
	return sightingRow{
		OriginalLang: rowTag{t: it.OriginalLang}, VideoPath: it.VideoPath, SceneName: it.SceneName, Title: it.Title,
		AudioLangs: audio, Tags: it.Tags, External: externalRowOf(it.External),
		Numbering: numberingRowOf(it.Numbering), Series: numberingRowOf(it.SeriesNumbering), AirDate: it.AirDate,
		Absolute: it.Absolute, Excluded: it.Excluded,
	}
}

func (r *sightingRow) item(native mediasource.NativeID) mediasource.Item {
	audio := make([]language.Tag, 0, len(r.AudioLangs))
	for _, t := range r.AudioLangs {
		audio = append(audio, t.t)
	}
	return mediasource.Item{
		Native: native, OriginalLang: r.OriginalLang.t, VideoPath: r.VideoPath, SceneName: r.SceneName, Title: r.Title,
		AudioLangs: audio, Tags: r.Tags, External: r.External.ids(),
		Numbering: r.Numbering.numbering(), SeriesNumbering: r.Series.numbering(), AirDate: r.AirDate,
		Absolute: r.Absolute, Excluded: r.Excluded,
	}
}

func numberingRowOf(n media.Numbering) numberingRow {
	return numberingRow{Season: n.Season, Episode: n.Episode, Scheme: uint8(n.Scheme)}
}

func (n numberingRow) numbering() media.Numbering {
	return media.Numbering{Season: n.Season, Episode: n.Episode, Scheme: media.Scheme(n.Scheme)}
}

func externalRowOf(e media.ExternalIDs) externalRow {
	return externalRow{TVDB: e.TVDB, TMDB: e.TMDB, IMDb: e.IMDb}
}

func (e externalRow) ids() media.ExternalIDs {
	return media.ExternalIDs{TVDB: e.TVDB, TMDB: e.TMDB, IMDb: e.IMDb}
}
