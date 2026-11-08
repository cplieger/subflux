package statedb

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/timing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return mustOpen(t, filepath.Join(t.TempDir(), "subflux.bolt"), productionConfig())
}

// seededStore has one revision, so Since after its head is a partial
// answer rather than the Full every after of 0 gets.
func seededStore(t *testing.T) *Store {
	t.Helper()
	s := newStore(t)
	update(t, s, putItem(s, movieKey(t, 999999)))
	return s
}

func update(t *testing.T, s *Store, fn func(*bolttable.Tx) error) {
	t.Helper()
	if err := s.db.Update(t.Context(), fn); err != nil {
		t.Fatalf("update: %v", err)
	}
}

func headRev(t testing.TB, s *Store) uint64 {
	t.Helper()
	var rev uint64
	if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) (err error) {
		rev, err = r.Rev()
		return err
	}); err != nil {
		t.Fatalf("Rev: %v", err)
	}
	return rev
}

func since(t *testing.T, s *Store, after uint64) Delta {
	t.Helper()
	d, err := s.Since(t.Context(), s.db.Epoch(), after)
	if err != nil {
		t.Fatalf("Since(%d): %v", after, err)
	}
	return d
}

// putItem writes key's item record, a live row whose subject is key's root,
// and the no-video sighting it names.
func putItem(s *Store, k media.Key) func(*bolttable.Tx) error {
	return func(tx *bolttable.Tx) error {
		ref := itemRef{Source: "sonarr", Root: mediasource.NativeRef{ID: "1", Kind: media.Episode}, Item: mediasource.NativeID(k.String())}
		if err := s.t.sightings.Put(tx, ref, &sightingRow{Key: &rowKey{k: k}}); err != nil {
			return err
		}
		return s.t.items.Put(tx, k, new(recordOf(ref, 0)))
	}
}

func putInstance(s *Store, id InstanceID, keys ...media.Key) func(*bolttable.Tx) error {
	return func(tx *bolttable.Tx) error {
		row := instanceRow{Owner: "sonarr", VideoPath: "/tv/" + id.String() + ".mkv"}
		for _, k := range keys {
			row.Items = append(row.Items, rowKey{k: k})
		}
		return s.t.instances.Put(tx, id, &row)
	}
}

func movieKey(t *testing.T, id int) media.Key {
	t.Helper()
	return mustKey(t, "tmdb-"+strconv.Itoa(id))
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestChangedWakesEveryObserver(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	t.Run("one_revision_wakes_both", func(t *testing.T) {
		var woke sync.WaitGroup
		got := make(chan int, 2)
		for i := range 2 {
			ch := s.Changed()
			since(t, s, headRev(t, s))
			woke.Go(func() {
				select {
				case <-ch:
					got <- i
				case <-time.After(time.Second):
				}
			})
		}
		update(t, s, putItem(s, movieKey(t, 1)))
		woke.Wait()
		if len(got) != 2 {
			t.Errorf("%d of 2 observers woke within 1 s", len(got))
		}
	})
	t.Run("a_burst_wakes_each_and_reaches_the_head", func(t *testing.T) {
		start := headRev(t, s)
		target := start + 100
		var observers sync.WaitGroup
		wakes := make([]int, 2)
		reached := make([]uint64, 2)
		for i := range 2 {
			observers.Go(func() {
				after := start
				for after < target {
					ch := s.Changed()
					d, err := s.Since(ctx, s.db.Epoch(), after)
					if err != nil {
						t.Errorf("observer %d Since: %v", i, err)
						return
					}
					if after = d.Rev; after >= target {
						break
					}
					select {
					case <-ch:
						wakes[i]++
					case <-time.After(5 * time.Second):
						t.Errorf("observer %d slept past 5 s at rev %d", i, after)
						return
					}
				}
				reached[i] = after
			})
		}
		for i := range 100 {
			update(t, s, putItem(s, movieKey(t, 100+i)))
		}
		observers.Wait()
		for i := range 2 {
			if wakes[i] < 1 || reached[i] != target {
				t.Errorf("observer %d: %d wakes, reached rev %d, want at least 1 and %d", i, wakes[i], reached[i], target)
			}
		}
	})
	t.Run("a_channel_read_between_commits_closes_on_the_next", func(t *testing.T) {
		update(t, s, putItem(s, movieKey(t, 2)))
		ch := s.Changed()
		if isClosed(ch) {
			t.Fatal("the channel read after a commit is already closed")
		}
		update(t, s, putItem(s, movieKey(t, 3)))
		if !isClosed(ch) {
			t.Error("the next commit did not close it")
		}
	})
	t.Run("a_silent_only_commit_closes_nothing", func(t *testing.T) {
		ch := s.Changed()
		update(t, s, putCursor(s, "sonarr"))
		if isClosed(ch) {
			t.Error("a silent-only commit closed Changed")
		}
	})
}

func TestChangedNeverBlocksCommit(t *testing.T) {
	s := newStore(t)
	before := headRev(t, s)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 1000 {
			if err := s.db.Update(t.Context(), putItem(s, movieKey(t, i+1))); err != nil {
				t.Errorf("commit %d: %v", i, err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("1,000 commits with no observer did not complete in 60 s")
	}
	if got := headRev(t, s); got != before+1000 {
		t.Errorf("head %d, want %d", got, before+1000)
	}
}

func TestSinceFullCases(t *testing.T) {
	s := newStore(t)
	update(t, s, putItem(s, movieKey(t, 1)))
	head := headRev(t, s)
	full := func(t *testing.T, d Delta, wantMarks []string) {
		t.Helper()
		if !d.Full || d.Rev != headRev(t, s) || d.Epoch != s.db.Epoch() || d.Roots != nil || d.Written != nil || !slices.Equal(d.Marks, wantMarks) {
			t.Errorf("got %+v, want Full at the head with marks %v", d, wantMarks)
		}
	}
	t.Run("another_epoch", func(t *testing.T) {
		d, err := s.Since(t.Context(), [16]byte{1}, head)
		if err != nil {
			t.Fatal(err)
		}
		full(t, d, nil)
	})
	t.Run("after_zero", func(t *testing.T) { full(t, since(t, s, 0), nil) })
	t.Run("after_above_the_head", func(t *testing.T) { full(t, since(t, s, head+1), nil) })
	t.Run("after_below_config_rev", func(t *testing.T) {
		update(t, s, func(tx *bolttable.Tx) error {
			tx.Mark(MarkConfig)
			return s.t.activation.Put(tx, constKey{}, &activationRow{Sources: []media.SourceID{"sonarr"}})
		})
		cfgRev := headRev(t, s)
		var act activationRow
		if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) (err error) {
			act, _, err = s.t.activation.Get(r, constKey{})
			return err
		}); err != nil || act.ConfigRev != cfgRev {
			t.Fatalf("stored config_rev %d (%v), want %d", act.ConfigRev, err, cfgRev)
		}
		full(t, since(t, s, cfgRev-1), []string{MarkConfig})
		if d := since(t, s, cfgRev); d.Full || d.Rev != cfgRev || len(d.Roots) != 0 || len(d.Marks) != 0 {
			t.Errorf("Since(config_rev) = %+v, want an empty partial answer", d)
		}
	})
	t.Run("trimmed", func(t *testing.T) {
		cfg := productionConfig()
		cfg.opts.Retention = 2
		small := mustOpen(t, filepath.Join(t.TempDir(), "subflux.bolt"), cfg)
		for i := range 5 {
			update(t, small, putItem(small, movieKey(t, i+1)))
		}
		if d := since(t, small, 1); !d.Full {
			t.Errorf("Since(1) over a log retaining 2 of 5: %+v, want Full", d)
		}
		if d := since(t, small, 3); d.Full || len(d.Roots) != 2 {
			t.Errorf("Since(3): %+v, want the two retained roots", d)
		}
	})
}

func TestSinceCollapsesRoots(t *testing.T) {
	s := seededStore(t)
	before := headRev(t, s)
	e1, e2, movie := mustKey(t, "tvdb-5-s01e01"), mustKey(t, "tvdb-5-s01e02"), movieKey(t, 9)
	update(t, s, putItem(s, e1))
	update(t, s, putItem(s, e2))
	update(t, s, func(tx *bolttable.Tx) error {
		return s.t.coverage.Put(tx, e1, &coverageRow{})
	})
	update(t, s, putItem(s, movie))
	d := since(t, s, before)
	if want := []media.RootKey{e1.Root(), movie.Root()}; d.Full || !slices.Equal(d.Roots, want) || d.Rev != before+4 {
		t.Errorf("got %+v, want Roots %v at rev %d", d, want, before+4)
	}
}

func TestSinceSpansPages(t *testing.T) {
	s := seededStore(t)
	before := headRev(t, s)
	var writers sync.WaitGroup
	for i := range 2500 {
		writers.Go(func() {
			if err := s.db.Batch(t.Context(), putItem(s, movieKey(t, i+1))); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		})
	}
	writers.Wait()
	d := since(t, s, before)
	if d.Full || len(d.Roots) != 2500 || d.Rev != before+2500 {
		t.Errorf("got Full %v, %d roots at rev %d; want 2500 at rev %d", d.Full, len(d.Roots), d.Rev, before+2500)
	}
}

// TestRootSubjectsAreSynthetic writes each live core table once and checks
// that the recorded subjects are synthetic roots Since sorts by kind.
func TestRootSubjectsAreSynthetic(t *testing.T) {
	s := seededStore(t)
	ep := mustKey(t, "tvdb-121361-s01e02")
	en := mustTag(t, "en")
	src := media.SourceID("sonarr")
	ref := itemRef{Source: src, Root: mediasource.NativeRef{Kind: media.Episode, ID: "73"}, Item: "4021"}
	update(t, s, putInstance(s, 7, ep))
	update(t, s, func(tx *bolttable.Tx) error {
		return s.t.sightings.Put(tx, ref, &sightingRow{Key: &rowKey{k: ep}})
	})
	cases := []struct {
		write   func(*bolttable.Tx) error
		name    string
		written bool
	}{
		{name: "items", write: putItem(s, ep)},
		{name: "coverage", write: func(tx *bolttable.Tx) error { return s.t.coverage.Put(tx, ep, &coverageRow{}) }},
		{name: "locks", write: func(tx *bolttable.Tx) error {
			return s.t.locks.Put(tx, lockKey{Key: ep, Tag: en, Variant: language.Standard}, &lockRow{At: fixtureAt})
		}},
		{name: "instances", write: func(tx *bolttable.Tx) error {
			return s.t.instances.Put(tx, 7, &instanceRow{Owner: "sonarr", VideoPath: "/tv/7.mkv", SceneName: "renamed", Items: []rowKey{{k: ep}}})
		}},
		{name: "files", written: true, write: func(tx *bolttable.Tx) error {
			return s.t.files.Put(tx, fileKey{Inst: 7, Name: "a.en.srt"}, &fileRow{Tag: rowTag{t: en}, Timing: []byte{0}})
		}},
		{name: "local", write: func(tx *bolttable.Tx) error { return s.t.local.Put(tx, 7, &localRow{}) }},
		{name: "embedded", write: func(tx *bolttable.Tx) error { return s.t.embedded.Put(tx, 7, &embeddedRow{}) }},
		{name: "roots", write: func(tx *bolttable.Tx) error {
			return s.t.roots.Put(tx, ref.rootRef, &rootRow{Title: "Show"})
		}},
		{name: "sightings", write: func(tx *bolttable.Tx) error {
			return s.t.sightings.Put(tx, ref, &sightingRow{Key: &rowKey{k: ep}, Title: "Two"})
		}},
		{name: "downloads", write: func(tx *bolttable.Tx) error {
			return s.t.downloads.Put(tx, 1, &downloadRow{Target: rowTag{t: en}, Items: []rowKey{{k: ep}}, Variant: 1, Provenance: provenanceRow{MatchedBy: 1, Fit: 1}})
		}},
		{name: "touch_written", written: true, write: func(tx *bolttable.Tx) error {
			tx.Touch(rootSubject(subjectWritten, ep.Root()))
			return nil
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := headRev(t, s)
			update(t, s, c.write)
			var subs []bolttable.Subject
			if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) error {
				page, err := r.Since(before, 10)
				for _, ch := range page.Changes {
					subs = append(subs, ch.Subjects...)
				}
				return err
			}); err != nil {
				t.Fatalf("raw Since: %v", err)
			}
			want := []bolttable.Subject{rootSubject(subjectRoot, ep.Root())}
			if c.written {
				want = []bolttable.Subject{rootSubject(subjectWritten, ep.Root()), want[0]}
			}
			if !slices.EqualFunc(subs, want, slices.Equal) {
				t.Errorf("recorded subjects %x, want %x", subs, want)
			}
			d := since(t, s, before)
			if !slices.Equal(d.Roots, []media.RootKey{ep.Root()}) || (len(d.Written) == 1) != c.written {
				t.Errorf("Since: Roots %v Written %v, want the root (and written %v)", d.Roots, d.Written, c.written)
			}
		})
	}
}

func TestDownloadSetsHistoryMark(t *testing.T) {
	s := seededStore(t)
	before := headRev(t, s)
	e1, e2 := mustKey(t, "tvdb-5-s01e01"), mustKey(t, "tvdb-6-s01e01")
	update(t, s, func(tx *bolttable.Tx) error {
		seq, err := s.t.downloads.NextID(tx)
		if err != nil {
			return err
		}
		return s.t.downloads.Put(tx, seq, &downloadRow{Target: rowTag{t: mustTag(t, "en")}, Items: []rowKey{{k: e1}, {k: e2}}, Variant: 1, Provenance: provenanceRow{MatchedBy: 1, Fit: 1}})
	})
	d := since(t, s, before)
	if !slices.Equal(d.Marks, []string{MarkHistory}) || !slices.Equal(d.Roots, []media.RootKey{e1.Root(), e2.Root()}) {
		t.Errorf("got %+v, want the history mark and both items' roots", d)
	}
}

// TestMarksAreExportedConstants checks each mark a writer reaches; the
// account cases join with the auth namespace.
func TestMarksAreExportedConstants(t *testing.T) {
	s := seededStore(t)
	t.Run("download", func(t *testing.T) {
		before := headRev(t, s)
		update(t, s, func(tx *bolttable.Tx) error {
			return s.t.downloads.Put(tx, 1, &downloadRow{Target: rowTag{t: mustTag(t, "en")}, Variant: 1, Provenance: provenanceRow{MatchedBy: 1, Fit: 1}})
		})
		if d := since(t, s, before); !slices.Equal(d.Marks, []string{MarkHistory}) {
			t.Errorf("marks %v, want [%s]", d.Marks, MarkHistory)
		}
	})
	t.Run("unidentified_sighting_appearing", func(t *testing.T) {
		before := headRev(t, s)
		update(t, s, func(tx *bolttable.Tx) error {
			tx.Mark(MarkUnidentified)
			return s.t.sightings.Put(tx, itemRef{Source: "sonarr", Root: mediasource.NativeRef{Kind: media.Movie, ID: "1"}, Item: "1"}, &sightingRow{})
		})
		if d := since(t, s, before); !slices.Equal(d.Marks, []string{MarkUnidentified}) || len(d.Roots) != 0 {
			t.Errorf("got %+v, want only [%s]", d, MarkUnidentified)
		}
	})
	t.Run("changed_activation", func(t *testing.T) {
		before := headRev(t, s)
		update(t, s, func(tx *bolttable.Tx) error {
			tx.Mark(MarkConfig)
			return s.t.activation.Put(tx, constKey{}, &activationRow{})
		})
		if d := since(t, s, before); !d.Full || !slices.Equal(d.Marks, []string{MarkConfig}) {
			t.Errorf("got %+v, want Full with [%s]", d, MarkConfig)
		}
	})
	t.Run("no_literal_mark_in_the_package", func(t *testing.T) {
		fset := token.NewFileSet()
		files, err := filepath.Glob("*.go")
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Mark" {
					for _, a := range call.Args {
						if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							t.Errorf("%s: Mark(%s) passes a literal, not a Mark constant", fset.Position(lit.Pos()), lit.Value)
						}
					}
				}
				return true
			})
		}
	})
}

// bumpedCore's table 0x7F is live and has no change mapping.
func TestUnmappedTableFailsCommit(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "subflux.bolt"), withTables(bumpedCore(t)))
	before := headRev(t, s)
	err := s.db.Update(t.Context(), func(tx *bolttable.Tx) error {
		tx.Touch(bolttable.Subject{0x7F, 1})
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "no change mapping") || headRev(t, s) != before {
		t.Errorf("a touch of an unmapped table: %v, head %d; want the commit refused at %d", err, headRev(t, s), before)
	}
}

func TestPublishesRoot(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	k, id := showEpisode(t, s, "sonarr", epPath)
	root := []media.RootKey{k.Root()}
	publishes := func(t *testing.T, written bool, op func()) {
		t.Helper()
		before := headRev(t, s)
		op()
		d := since(t, s, before)
		if !slices.Equal(d.Roots, root) || slices.Equal(d.Written, root) != written {
			t.Errorf("Since = Roots %v Written %v; want Roots %v, written %v", d.Roots, d.Written, root, written)
		}
	}
	t.Run("inventory change", func(t *testing.T) {
		publishes(t, false, func() { putEmbedded(t, s, id, track(t, "en", "subrip")) })
		publishes(t, false, func() { putExternal(t, s, id, local(t, "S01E01.fr.srt", "fr")) })
	})
	t.Run("lock toggle", func(t *testing.T) {
		publishes(t, false, func() { setLock(t, s, id, "en", true) })
		publishes(t, false, func() { setLock(t, s, id, "en", false) })
	})
	t.Run("timing save", func(t *testing.T) {
		deliver(t, s, pendingDeliver(t, id, managedName, 1, k))
		rec := recordAt(id, managedName, enTag, 1)
		rec.Timing = timing.Shift{Ms: 500}
		publishes(t, true, func() {
			if err := s.BeginRetime(t.Context(), rec, nil); err != nil {
				t.Fatal(err)
			}
			if err := s.EndRetime(t.Context(), id, managedName, true); err != nil {
				t.Fatal(err)
			}
		})
	})
}

func TestSupersessionListsRootInWritten(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	k, id := showEpisode(t, s, "sonarr", epPath)
	deliver(t, s, pendingDeliver(t, id, managedName, 1, k))
	before := headRev(t, s)
	if _, moved := showEpisode(t, s, "sonarr", "/tv/Show/S01E01.v2.mkv"); moved == id {
		t.Fatal("the path change kept the old instance")
	}
	if d := since(t, s, before); !slices.Equal(d.Written, []media.RootKey{k.Root()}) {
		t.Errorf("Since after the supersession = Written %v, want %v", d.Written, k.Root())
	}
	if _, found := getRow(t, s, s.t.files, fileKey{Inst: id, Name: managedName}); found {
		t.Error("the superseded instance's file record survived")
	}
}
