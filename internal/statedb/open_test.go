package statedb

import (
	"bytes"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/bolttable"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/media"
)

// copyFixture copies testdata/name to a fresh directory as subflux.bolt.
func copyFixture(t *testing.T, name string) (path string, fixture []byte) {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	path = filepath.Join(t.TempDir(), "subflux.bolt")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path, fixture
}

func mustOpen(t *testing.T, path string, cfg config) *Store {
	t.Helper()
	s, err := open(path, &cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func withTables(ts *tables) config {
	cfg := productionConfig()
	cfg.tables = ts
	return cfg
}

// bumped is the production schema with one more table in namespace ns, so
// that namespace's digest differs from production's.
func bumped(t *testing.T, ns string) *tables {
	t.Helper()
	s := bolttable.NewSchema(schemaName)
	coreNS := bolttable.Must(s.Namespace("core"))
	c, err := defineCoreIn(coreNS)
	if err != nil {
		t.Fatalf("defineCoreIn: %v", err)
	}
	authNS := bolttable.Must(s.Namespace("auth"))
	a, err := defineAuthIn(authNS)
	if err != nil {
		t.Fatalf("defineAuthIn: %v", err)
	}
	target := map[string]*bolttable.Namespace{"core": coreNS, "auth": authNS}[ns]
	if _, err := bolttable.Define(target, 0x7F, "bump", bolttable.BytesKey{}, bolttable.JSON[struct{}](1), bolttable.TableOptions{}); err != nil {
		t.Fatalf("Define bump in %s: %v", ns, err)
	}
	return &tables{schema: s, core: c, auth: a}
}

// coreOnly is the production schema without auth: today's core declarations alone.
func coreOnly(t *testing.T) *tables {
	t.Helper()
	s := bolttable.NewSchema(schemaName)
	c, err := defineCore(s)
	if err != nil {
		t.Fatalf("defineCore: %v", err)
	}
	return &tables{schema: s, core: c}
}

func oldCopies(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var olds []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".old") {
			olds = append(olds, filepath.Join(dir, e.Name()))
		}
	}
	return olds
}

// assertDeclared proves the file's top-level buckets are exactly
// bolttable, core and auth: a read-only open under the production schema
// refuses an undeclared namespace and a missing one.
func assertDeclared(t *testing.T, path string) {
	t.Helper()
	rdb, err := bolttable.OpenReadOnly(path, production.schema, bolttable.ReadOptions{})
	if err != nil {
		t.Fatalf("OpenReadOnly under the production schema: %v", err)
	}
	if err := rdb.Close(); err != nil {
		t.Fatalf("Close read-only: %v", err)
	}
}

func coreRows(t *testing.T, s *Store) int {
	t.Helper()
	st, err := s.db.Stats(t.Context())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	n := 0
	for _, ns := range st.Namespaces {
		if ns.Name == "core" {
			for _, tb := range ns.Tables {
				n += tb.Rows
			}
		}
	}
	return n
}

func TestTodaysFileResetsToCore(t *testing.T) {
	compactable := productionConfig()
	compactable.opts.Compact = bolttable.CompactPolicy{MinFreeRatio: 1e-9, MinFreeBytes: 1}
	path, fixture := copyFixture(t, "today-640a639.bolt")
	s := mustOpen(t, path, compactable)
	r := s.db.OpenReport()

	t.Run("kept_whole", func(t *testing.T) {
		olds := oldCopies(t, filepath.Dir(path))
		if len(olds) != 1 || r.Kept != olds[0] {
			t.Fatalf(".old copies %v, report Kept %q", olds, r.Kept)
		}
		if !strings.HasPrefix(filepath.Base(r.Kept), "subflux.bolt.00000000.") {
			t.Errorf("kept %q: today's file has no stored digest, so its copy is named by 8 zeros", r.Kept)
		}
		got, err := os.ReadFile(r.Kept)
		if err != nil {
			t.Fatalf("read kept: %v", err)
		}
		if !bytes.Equal(got, fixture) {
			t.Error("kept copy differs from the fixture")
		}
	})
	t.Run("declares_core_and_auth", func(t *testing.T) {
		st, err := s.db.Stats(t.Context())
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		var names []string
		for _, ns := range st.Namespaces {
			names = append(names, ns.Name)
		}
		slices.Sort(names)
		if !slices.Equal(names, []string{"auth", "core"}) {
			t.Errorf("namespaces %v, want auth and core", names)
		}
	})
	t.Run("compacted", func(t *testing.T) {
		if !r.Compacted || r.CompactErr != nil {
			t.Errorf("Compacted %v, CompactErr %v; want true, nil", r.Compacted, r.CompactErr)
		}
		zeroPath, _ := copyFixture(t, "today-640a639.bolt")
		zero := productionConfig()
		zero.opts.Compact = bolttable.CompactPolicy{}
		uncompacted := mustOpen(t, zeroPath, zero)
		if uncompacted.db.OpenReport().Compacted {
			t.Fatal("a zero CompactPolicy compacted")
		}
		got, err := s.db.Space()
		if err != nil {
			t.Fatalf("Space: %v", err)
		}
		want, err := uncompacted.db.Space()
		if err != nil {
			t.Fatalf("Space: %v", err)
		}
		if got.FreeBytes >= want.FreeBytes {
			t.Errorf("FreeBytes %d after compaction, %d without; want fewer", got.FreeBytes, want.FreeBytes)
		}
	})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertDeclared(t, path)
}

func TestOpenHoldsLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := bolttable.LockPath(path, 0); !errors.Is(err, bolttable.ErrLocked) {
		t.Errorf("LockPath while open: %v, want ErrLocked", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	lock, err := bolttable.LockPath(path, 0)
	if err != nil {
		t.Fatalf("LockPath after Close: %v", err)
	}
	defer func() { _ = lock.Release() }()
	before := dirState(t, filepath.Dir(path))
	start := time.Now()
	s2, err := Open(path)
	elapsed := time.Since(start)
	if err == nil {
		_ = s2.Close()
		t.Fatal("Open succeeded while another holder had the lock")
	}
	if !errors.Is(err, ErrLocked) {
		t.Errorf("Open under a held lock: %v, want ErrLocked", err)
	}
	if elapsed < 5*time.Second || elapsed >= 6*time.Second {
		t.Errorf("Open failed after %v, want at least 5 s and under 6 s", elapsed)
	}
	if after := dirState(t, filepath.Dir(path)); !maps.Equal(before, after) {
		t.Errorf("directory changed under a held lock: before %v, after %v", before, after)
	}
}

// dirState maps each file name in dir to its bytes.
func dirState(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	m := map[string]string{}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		m[e.Name()] = string(b)
	}
	return m
}

func TestOtherLayoutNumberResets(t *testing.T) {
	path, fixture := copyFixture(t, "other-layout.bolt")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r := s.db.OpenReport()
	var names []string
	for _, n := range r.Reset {
		names = append(names, n.Name)
	}
	if !slices.Equal(names, []string{"bolttable", "core"}) {
		t.Errorf("reset %v, want [bolttable core]", names)
	}
	olds := oldCopies(t, filepath.Dir(path))
	if len(olds) != 1 || olds[0] != r.Kept {
		t.Fatalf(".old copies %v, Kept %q; want one, the kept copy", olds, r.Kept)
	}
	if got, err := os.ReadFile(olds[0]); err != nil || !bytes.Equal(got, fixture) {
		t.Errorf("kept copy differs from the fixture (read error %v)", err)
	}
	if n := coreRows(t, s); n != 0 {
		t.Errorf("core holds %d rows after the reset, want 0", n)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer func() { _ = again.Close() }()
	if r := again.db.OpenReport(); len(r.Reset) != 0 || r.Kept != "" {
		t.Errorf("second Open reset %v kept %q, want nothing", r.Reset, r.Kept)
	}
	if olds := oldCopies(t, filepath.Dir(path)); len(olds) != 1 {
		t.Errorf(".old copies after the second Open: %v, want the one", olds)
	}
}

func TestOpenReportLogs(t *testing.T) {
	stored, want := [32]byte{0x0a, 0x1b, 0x2c, 0x3d}, [32]byte{0xde, 0xad, 0xbe, 0xef}
	reset := []bolttable.NamespaceDigest{{Name: "bolttable"}, {Name: "core", Stored: stored, Want: want}}
	compactErr := errors.New("directory fsync failed")
	type lines struct{ info, warn int }
	cases := []struct {
		name string
		r    bolttable.OpenReport
		want lines
	}{
		{"zero", bolttable.OpenReport{}, lines{}},
		{"compacted", bolttable.OpenReport{Compacted: true}, lines{info: 1}},
		{"compact_failed", bolttable.OpenReport{CompactErr: compactErr}, lines{warn: 1}},
		{"compacted_then_dir_fsync_failed", bolttable.OpenReport{Compacted: true, CompactErr: compactErr}, lines{info: 1, warn: 1}},
		{"reset", bolttable.OpenReport{Reset: reset, Kept: "/config/subflux.bolt.0a1b2c3d.x.old"}, lines{warn: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := capture.Default(t)
			logOpenReport(&tc.r)
			got := lines{info: rec.CountLevel(slog.LevelInfo, ""), warn: rec.CountLevel(slog.LevelWarn, "")}
			if got != tc.want || rec.Len() != tc.want.info+tc.want.warn {
				t.Errorf("logged %d records (%+v), want %+v", rec.Len(), got, tc.want)
			}
			if tc.r.CompactErr != nil && !rec.AttrContains("compaction failed", "error", "directory fsync failed") {
				t.Error("compaction WARN does not carry the error")
			}
			if len(tc.r.Reset) > 0 {
				for _, sub := range []string{"bolttable stored none recreated none", "core stored 0a1b2c3d recreated deadbeef"} {
					if !rec.AttrContains("reset", "namespaces", sub) {
						t.Errorf("reset WARN lacks %q", sub)
					}
				}
				if v, ok := rec.AttrValue("reset", "kept"); !ok || v != tc.r.Kept {
					t.Errorf("reset WARN kept %q, want %q", v, tc.r.Kept)
				}
			}
		})
	}
}

func TestOpenEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subflux.bolt")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if r := s.db.OpenReport(); len(r.Reset) != 0 || r.Kept != "" || r.Compacted {
		t.Errorf("new file report %+v, want zero", r)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var names []string
	for name := range dirState(t, dir) {
		names = append(names, name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"subflux.bolt", "subflux.bolt.lock"}) {
		t.Errorf("directory holds %v", names)
	}
	assertDeclared(t, path)
}

// seedBoth writes one row of each namespace through a store over ts and
// closes it: an item (core), and a user and the auth secrets (auth). It
// returns the secrets.
func seedBoth(t *testing.T, path string, ts *tables) AuthSecrets {
	t.Helper()
	s := mustOpen(t, path, withTables(ts))
	err := s.write(t.Context(), "seed", s.db.Update, func(tx *bolttable.Tx) error {
		return s.t.items.Put(tx, mustKey(t, "tmdb-603"), &itemRow{Source: "radarr", Item: "1", Root: nativeRefRow{ID: "1", Kind: uint8(media.Movie)}})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.Accounts().CreateUser(t.Context(), &access.User{Username: "ada", Role: access.RoleAdmin, Enabled: true}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	secrets, err := s.AuthSecrets(t.Context())
	if err != nil {
		t.Fatalf("AuthSecrets: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return secrets
}

// heldRows reports whether the seeded core row, and the seeded user and
// secrets, are in the store.
func heldRows(t *testing.T, s *Store, secrets AuthSecrets) (core, auth bool) {
	t.Helper()
	err := s.db.View(t.Context(), func(r *bolttable.ReadTx) error {
		_, item, err := s.t.items.Get(r, mustKey(t, "tmdb-603"))
		if err != nil {
			return err
		}
		sec, stored, err := s.a.secrets.Get(r, constKey{})
		if err != nil {
			return err
		}
		n, err := s.a.userCount.Value(r)
		if err != nil {
			return err
		}
		core, auth = item, stored && n == 1 && sec.FailureKey == sum32(secrets.FailureKey) && sec.DeviceKey == sum32(secrets.DeviceKey)
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	return core, auth
}

func TestCoreBumpKeepsOtherNamespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	secrets := seedBoth(t, path, production)
	s := mustOpen(t, path, withTables(bumped(t, "core")))
	if r := s.db.OpenReport(); len(r.Reset) != 1 || r.Reset[0].Name != "core" {
		t.Errorf("reset %v, want core alone", r.Reset)
	}
	core, auth := heldRows(t, s, secrets)
	if core || !auth {
		t.Errorf("after a core bump: core row held %v, auth rows and secrets held %v; want false, true", core, auth)
	}
}

func TestAuthBumpKeepsCore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	secrets := seedBoth(t, path, production)
	s := mustOpen(t, path, withTables(bumped(t, "auth")))
	if r := s.db.OpenReport(); len(r.Reset) != 1 || r.Reset[0].Name != "auth" {
		t.Errorf("reset %v, want auth alone", r.Reset)
	}
	core, auth := heldRows(t, s, secrets)
	if !core || auth {
		t.Errorf("after an auth bump: core row held %v, auth rows held %v; want true, false", core, auth)
	}
}

func TestOpenAddsAuthToCoreOnlyFile(t *testing.T) {
	path := tempPath(t)
	core := coreOnly(t)
	seed := mustOpen(t, path, withTables(core))
	err := seed.write(t.Context(), "seed", seed.db.Update, func(tx *bolttable.Tx) error {
		return seed.t.items.Put(tx, mustKey(t, "tmdb-603"), &itemRow{Source: "radarr", Item: "1", Root: nativeRefRow{ID: "1", Kind: uint8(media.Movie)}})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rdb, err := bolttable.OpenReadOnly(path, core.schema, bolttable.ReadOptions{})
	if err != nil {
		t.Fatalf("OpenReadOnly under core alone: %v", err)
	}
	before, err := rdb.Stats(t.Context())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if err := rdb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if before.Rev == 0 || len(before.Namespaces) != 1 {
		t.Fatalf("the seeded core-only file holds rev %d and namespaces %+v, want a written core alone", before.Rev, before.Namespaces)
	}

	s, err := open(path, new(productionConfig()))
	if err != nil {
		t.Fatalf("open under ST-2's schema: %v, want no *FormatError", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if r := s.db.OpenReport(); len(r.Reset) != 0 || r.Kept != "" {
		t.Errorf("open reset %v and kept %q, want neither", r.Reset, r.Kept)
	}
	after, err := s.db.Stats(t.Context())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if after.Rev != before.Rev {
		t.Errorf("Rev %d after the open, %d before", after.Rev, before.Rev)
	}
	var names []string
	for _, ns := range after.Namespaces {
		names = append(names, ns.Name)
		if ns.Name == "core" && !reflect.DeepEqual(ns, before.Namespaces[0]) {
			t.Errorf("core after the open %+v, before %+v", ns, before.Namespaces[0])
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"auth", "core"}) {
		t.Errorf("namespaces %v, want auth and core", names)
	}
}

func TestOldCopiesABAB(t *testing.T) {
	a, b := production, bumped(t, "core")
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	var kept []string
	for i, c := range []*tables{a, b, a, b} {
		cfg := withTables(c)
		s, err := open(path, &cfg)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		r := s.db.OpenReport()
		if err := s.Close(); err != nil {
			t.Fatalf("Close %d: %v", i, err)
		}
		if i == 0 {
			if r.Kept != "" {
				t.Fatalf("a new file kept %q", r.Kept)
			}
			continue
		}
		if r.Kept == "" || slices.Contains(kept, r.Kept) {
			t.Fatalf("open %d kept %q after %v; want a new copy per reset", i, r.Kept, kept)
		}
		kept = append(kept, r.Kept)
	}
	got := oldCopies(t, filepath.Dir(path))
	slices.Sort(got)
	want := slices.Clone(kept[1:])
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf(".old copies %v, want the two newest %v", got, want)
	}
}

func TestStrandedCopyTmpRemoved(t *testing.T) {
	for _, name := range []string{"subflux.bolt.copy-tmp", "subflux.bolt.compact-tmp"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "subflux.bolt")
			s := mustOpen(t, path, productionConfig())
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte("stranded"), 0o600); err != nil {
				t.Fatal(err)
			}
			mustOpen(t, path, productionConfig())
			if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s after Open: %v, want gone", name, err)
			}
		})
	}
}

func TestSecondCloseNil(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "subflux.bolt"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v, want nil", err)
	}
}
