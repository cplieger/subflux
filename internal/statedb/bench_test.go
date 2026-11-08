package statedb

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// The production scale design section 13 benchmarks at: 63,000 items under
// three targets, one root large enough for a full 500-row page.
const (
	benchItems     = 63_000
	benchBigRoot   = 1_000
	benchRootItems = 100
	benchTargets   = "default:\n  - tag: en\n  - tag: fr\n  - tag: de\n"
)

// benchLibrary is a store holding benchItems episodes, each in its own
// instance, observed by one complete pass of sonarr under three targets.
func benchLibrary(b *testing.B) (*Store, language.Policy) {
	b.Helper()
	p := pol(b, benchTargets)
	s, err := open(filepath.Join(b.TempDir(), "subflux.bolt"), new(productionConfig()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	ctx := b.Context()
	if err := s.Activate(ctx, Activation{Sources: []media.SourceID{"sonarr"}, Languages: p}); err != nil {
		b.Fatal(err)
	}
	pass, err := s.BeginPass(ctx, "sonarr", PassScope{})
	if err != nil {
		b.Fatal(err)
	}
	for root, left := 0, benchItems; left > 0; root++ {
		n := benchRootItems
		if root == 0 {
			n = benchBigRoot
		}
		items := make([]mediasource.Item, 0, n)
		for i := range n {
			season, ep := 1+i/100, 1+i%100
			items = append(items, episode(fmt.Sprintf("r%d-e%d", root, i), season, ep, fmt.Sprintf("/tv/%d/S%02dE%02d.mkv", root, season, ep)))
		}
		if err := s.ObserveItems(ctx, "sonarr", pass, series(fmt.Sprintf("r%d", root), uint64(10_000+root)), items); err != nil {
			b.Fatal(err)
		}
		left -= n
	}
	if _, err := s.FinishPass(ctx, "sonarr", pass, true, mediasource.Cursor{}, nil); err != nil {
		b.Fatal(err)
	}
	return s, p
}

func bigRoot(b *testing.B) media.RootKey {
	b.Helper()
	k, ok := media.Choose(media.Episode, media.ExternalIDs{TVDB: 10_000}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1})
	if !ok {
		b.Fatal("no key for the big root")
	}
	return k.Root()
}

func BenchmarkItemsPage(b *testing.B) {
	s, _ := benchLibrary(b)
	root := bigRoot(b)
	b.ReportAllocs()
	for b.Loop() {
		rows, _, err := s.Items(b.Context(), root, "", maxListLimit)
		if err != nil || len(rows) != maxListLimit {
			b.Fatalf("Items: %d rows, %v; want %d", len(rows), err, maxListLimit)
		}
	}
}

func BenchmarkSince20000Changes(b *testing.B) {
	s, _ := benchLibrary(b)
	from := headRev(b, s)
	const changes = 20_000
	var wg sync.WaitGroup
	errs := make(chan error, changes)
	sem := make(chan struct{}, 256)
	for i := range changes {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			k, _ := media.Choose(media.Episode, media.ExternalIDs{TVDB: uint64(10_001 + i%620)}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1 + i/620})
			errs <- s.db.Batch(b.Context(), func(tx *bolttable.Tx) error {
				return s.t.locks.Put(tx, lockKey{Key: k, Tag: mustTag(b, "en"), Variant: language.Standard}, &lockRow{At: time.Unix(int64(i), 0).UTC()})
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			b.Fatal(err)
		}
	}
	epoch := s.db.Epoch()
	b.ReportAllocs()
	for b.Loop() {
		d, err := s.Since(b.Context(), epoch, from)
		if err != nil || d.Full || len(d.Roots) != 620 {
			b.Fatalf("Since: full %v, %d roots, %v; want 620 roots", d.Full, len(d.Roots), err)
		}
	}
}

func BenchmarkActivatePolicyChange(b *testing.B) {
	s, p := benchLibrary(b)
	policies := []language.Policy{pol(b, "default:\n  - tag: en\n  - tag: fr\n  - tag: es\n"), p}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if err := s.Activate(b.Context(), Activation{Sources: []media.SourceID{"sonarr"}, Languages: policies[i%2]}); err != nil {
			b.Fatal(err)
		}
		i++
	}
}

func BenchmarkSingleRowCommit(b *testing.B) {
	s, _ := benchLibrary(b)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		if err := s.PutCredentialDisable(b.Context(), CredentialDisable{At: time.Unix(int64(i), 0).UTC(), Provider: "opensubtitles", Reason: "rejected"}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkViewDuringBackup reports the slowest Item read while a Backup
// runs beside a writer committing in a loop.
func BenchmarkViewDuringBackup(b *testing.B) {
	s, _ := benchLibrary(b)
	k, _ := media.Choose(media.Episode, media.ExternalIDs{TVDB: 10_001}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1})
	dir := b.TempDir()
	var slowest time.Duration
	n := 0
	for b.Loop() {
		n++
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.PutCredentialDisable(b.Context(), CredentialDisable{At: time.Unix(int64(i), 0).UTC(), Provider: "p", Reason: "r"}); err != nil {
					b.Error(err)
					return
				}
			}
		})
		done := make(chan error, 1)
		go func() { done <- s.Backup(b.Context(), filepath.Join(dir, fmt.Sprintf("b%d.bolt", n))) }()
	reads:
		for {
			select {
			case err := <-done:
				if err != nil {
					b.Fatal(err)
				}
				break reads
			default:
			}
			start := time.Now()
			if _, _, found, err := s.Item(b.Context(), k); err != nil || !found {
				b.Fatalf("Item: %v, %v", found, err)
			}
			slowest = max(slowest, time.Since(start))
		}
		close(stop)
		wg.Wait()
	}
	b.ReportMetric(float64(slowest.Microseconds()), "max-view-µs")
}

// The largest realistic root: a long-running series of 2,000 episodes, one
// file each, under three targets in all three variants.
const (
	recountItems   = 2_000
	recountTargets = "default:\n  - tag: en\n    variants: [standard, hi, forced]\n  - tag: fr\n    variants: [standard, hi, forced]\n  - tag: de\n    variants: [standard, hi, forced]\n"
)

// recountLibrary is a store holding one root of n episodes, each with an
// English file, every other one locked on en, under recountTargets.
func recountLibrary(b *testing.B, tvdb uint64, n int) (*Store, InstanceID) {
	b.Helper()
	s, err := open(filepath.Join(b.TempDir(), "subflux.bolt"), new(productionConfig()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	ctx := b.Context()
	if err := s.Activate(ctx, Activation{Sources: []media.SourceID{"sonarr"}, Languages: pol(b, "default:\n  - tag: en\n")}); err != nil {
		b.Fatal(err)
	}
	pass, err := s.BeginPass(ctx, "sonarr", PassScope{})
	if err != nil {
		b.Fatal(err)
	}
	items := make([]mediasource.Item, 0, n)
	for i := range n {
		season, ep := 1+i/100, 1+i%100
		items = append(items, episode(fmt.Sprintf("e%d", i), season, ep, fmt.Sprintf("/tv/long/S%02dE%02d.mkv", season, ep)))
	}
	if err := s.ObserveItems(ctx, "sonarr", pass, series("long", tvdb), items); err != nil {
		b.Fatal(err)
	}
	var first InstanceID
	if err := s.db.Update(ctx, func(tx *bolttable.Tx) error {
		return s.t.instances.Scan(tx.Read(), bolttable.Range{}, func(id InstanceID, v *instanceRow) (bool, error) {
			if first == 0 {
				first = id
			}
			err := s.t.local.Put(tx, id, &localRow{Files: []localFileRow{{Name: "x.en.srt", Tag: rowTag{t: mustTag(b, "en")}}}})
			if err == nil && id%2 == 0 {
				err = s.t.locks.Put(tx, lockKey{Key: v.Items[0].k, Tag: mustTag(b, "en"), Variant: language.Standard}, &lockRow{At: time.Unix(1, 0).UTC()})
			}
			return err == nil, err
		})
	}); err != nil {
		b.Fatal(err)
	}
	if err := s.Activate(ctx, Activation{Sources: []media.SourceID{"sonarr"}, Languages: pol(b, recountTargets)}); err != nil {
		b.Fatal(err)
	}
	return s, first
}

// BenchmarkRecountLargestRoot measures what CR-UX-1's recount adds to a
// commit: one lock toggle in the 2,000-episode root against the same toggle
// in a one-episode root, and the recount alone.
func BenchmarkRecountLargestRoot(b *testing.B) {
	toggle := func(b *testing.B, n int) {
		s, id := recountLibrary(b, 20_000, n)
		en := mustTag(b, "en")
		b.ReportAllocs()
		locked := false
		for b.Loop() {
			locked = !locked
			if err := s.SetLock(b.Context(), id, en, language.Standard, locked); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.Run("lock_toggle_2000_items", func(b *testing.B) { toggle(b, recountItems) })
	b.Run("lock_toggle_1_item", func(b *testing.B) { toggle(b, 1) })
	b.Run("recount_2000_items", func(b *testing.B) {
		s, _ := recountLibrary(b, 20_000, recountItems)
		root := epKeyB(b, 20_000).Root()
		b.ReportAllocs()
		for b.Loop() {
			if err := s.db.View(b.Context(), func(r *bolttable.ReadTx) error {
				row, err := s.countRoot(r, root)
				if err == nil && len(row.Pairs) != 9 {
					err = fmt.Errorf("%d pairs, want 9", len(row.Pairs))
				}
				return err
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func epKeyB(b *testing.B, tvdb uint64) media.Key {
	b.Helper()
	k, ok := media.Choose(media.Episode, media.ExternalIDs{TVDB: tvdb}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1})
	if !ok {
		b.Fatal("no key")
	}
	return k
}
