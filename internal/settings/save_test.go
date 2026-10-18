package settings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

var errActivate = errors.New("store update failed")

// fixture is a config.yaml path in a fresh directory holding text, or absent
// when text is "".
func fixture(t testing.TB, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if text != "" {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func serve(path string, activate func(context.Context, Active) error, check SaveCheck) *Service {
	return NewService(path, Registries{}, Load(path, Registries{}), activate, check)
}

// node parses text, one YAML document, into its root node.
func node(t testing.TB, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func fileText(t testing.TB, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func save(t testing.TB, s *Service, text string) (Snapshot, []formschema.FieldError, error) {
	t.Helper()
	return s.Save(t.Context(), node(t, text), s.Snapshot().Revision())
}

func logLevel(s Snapshot) slog.Level {
	srv, _ := s.Server()
	return srv.LogLevel()
}

func signalled(s *Service) bool {
	select {
	case <-s.Changed():
		return true
	default:
		return false
	}
}

func errPaths(errs []formschema.FieldError) []string {
	paths := make([]string, 0, len(errs))
	for _, e := range errs {
		paths = append(paths, e.Path)
	}
	return paths
}

func TestSaveSurvivesRequestCancel(t *testing.T) {
	t.Run("a cancelled request still persists and commits", func(t *testing.T) {
		path := fixture(t, "server: {log_level: info}\n")
		ctx, cancel := context.WithCancel(t.Context())
		var activeErr error
		s := serve(path, func(actx context.Context, _ Active) error {
			cancel()
			activeErr = actx.Err()
			return nil
		}, nil)
		snap, errs, err := s.Save(ctx, node(t, "server: {log_level: debug}\n"), s.Snapshot().Revision())
		if err != nil || errs != nil {
			t.Fatalf("Save = %v, %v", errs, err)
		}
		if activeErr != nil {
			t.Errorf("activate's context ended with the request: %v", activeErr)
		}
		if got := fileText(t, path); got != string(snap.p.file.data) {
			t.Errorf("file %q, live snapshot read %q", got, snap.p.file.data)
		}
		if logLevel(Load(path, Registries{})) != slog.LevelDebug || logLevel(s.Snapshot()) != slog.LevelDebug {
			t.Error("the saved level is not on disk and live")
		}
		if !signalled(s) {
			t.Error("a commit did not signal Changed")
		}
	})
	t.Run("a failing activate restores the bytes and keeps the snapshot", func(t *testing.T) {
		const before = "# kept as written\nserver: {log_level: info}\n"
		path := fixture(t, before)
		s := serve(path, func(context.Context, Active) error { return errActivate }, nil)
		old := s.Snapshot()
		got, errs, err := save(t, s, "server: {log_level: debug}\n")
		if !errors.Is(err, ErrActivation) || !errors.Is(err, errActivate) || errs != nil {
			t.Fatalf("Save = %v, %v; want ErrActivation wrapping the cause", errs, err)
		}
		if fileText(t, path) != before {
			t.Errorf("file %q, want the previous bytes", fileText(t, path))
		}
		if got.p != old.p || s.Snapshot().p != old.p {
			t.Error("a failed activation replaced the snapshot")
		}
	})
	t.Run("a failing first save leaves no file", func(t *testing.T) {
		path := fixture(t, "")
		s := serve(path, func(context.Context, Active) error { return errActivate }, nil)
		if _, _, err := save(t, s, "server: {log_level: debug}\n"); !errors.Is(err, ErrActivation) {
			t.Fatalf("Save error %v, want ErrActivation", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stat after a failed first save: %v, want not exist", err)
		}
		if m := Load(path, Registries{}).Mode(); m != Unconfigured {
			t.Errorf("reload mode %v, want Unconfigured", m)
		}
	})
	t.Run("a slow activate serialises a concurrent save", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			path := fixture(t, "server: {log_level: info}\n")
			release := make(chan struct{})
			var running, most atomic.Int32
			s := serve(path, func(context.Context, Active) error {
				n := running.Add(1)
				most.Store(max(most.Load(), n))
				<-release
				running.Add(-1)
				return nil
			}, nil)
			base := s.Snapshot().Revision()
			var first, second error
			done := make(chan struct{}, 2)
			go func() {
				_, _, first = s.Save(t.Context(), node(t, "server: {log_level: debug}\n"), base)
				done <- struct{}{}
			}()
			synctest.Wait()
			go func() {
				_, _, second = s.Save(t.Context(), node(t, "server: {log_level: warn}\n"), base)
				done <- struct{}{}
			}()
			synctest.Wait()
			if len(done) != 0 {
				t.Fatal("a save returned while the first was activating")
			}
			close(release)
			<-done
			<-done
			if first != nil || !errors.Is(second, ErrConflict) {
				t.Errorf("first %v, second %v; want nil then ErrConflict", first, second)
			}
			if most.Load() != 1 {
				t.Errorf("%d activations ran at once", most.Load())
			}
			if logLevel(Load(path, Registries{})) != slog.LevelDebug {
				t.Error("the file does not hold the first save")
			}
		})
	})
}

func TestSaveRefusesFileChangedBeforeSave(t *testing.T) {
	cases := []struct{ name, before, edit string }{
		{"an edited file", "server: {log_level: info}\n", "server: {log_level: error}\n"},
		{"a file created after an absent load", "", "server: {log_level: error}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := fixture(t, c.before)
			s := serve(path, nil, nil)
			old := s.Snapshot()
			if err := os.WriteFile(path, []byte(c.edit), 0o600); err != nil {
				t.Fatal(err)
			}
			_, errs, err := save(t, s, "server: {log_level: debug}\n")
			if err != nil || len(errs) != 1 || errs[0].Path != "" || errs[0].Message != msgChangedOnDisk {
				t.Fatalf("Save = %v, %v; want the root changed-on-disk error", errs, err)
			}
			if fileText(t, path) != c.edit || s.Snapshot().p != old.p {
				t.Error("a refused save wrote the file or replaced the snapshot")
			}
		})
	}
	t.Run("a file unreadable at load is never overwritten", func(t *testing.T) {
		path := fixture(t, strings.Repeat("#", maxFileBytes+1))
		s := serve(path, nil, nil)
		_, errs, err := save(t, s, "server: {log_level: debug}\n")
		if err != nil || len(errs) != 1 || errs[0].Message != msgUnreadAtStart {
			t.Fatalf("Save = %v, %v; want the unread-at-start error", errs, err)
		}
	})
}

func TestSaveRefusesDocumentOverLimit(t *testing.T) {
	const stored = "server: {log_level: info}\n"
	path := fixture(t, stored)
	s := serve(path, nil, nil)
	old := s.Snapshot()
	var body strings.Builder
	body.WriteString("server:\n  allowed_hosts:\n")
	for i := 0; body.Len() <= maxFileBytes; i++ {
		fmt.Fprintf(&body, "    - host%07d.example.lan\n", i)
	}
	got, errs, err := save(t, s, body.String())
	if err != nil || len(errs) != 1 || errs[0].Path != "" || errs[0].Message != msgTooLarge {
		t.Fatalf("Save = %v, %v; want the root too-large error", errs, err)
	}
	if got.p != old.p || s.Snapshot().p != old.p {
		t.Error("a refused save answered or made live another snapshot")
	}
	if fileText(t, path) != stored || signalled(s) {
		t.Error("a refused save wrote the file or signalled a change")
	}
}

func TestFailedRestoreRecordsReread(t *testing.T) {
	path := fixture(t, "server: {log_level: info}\n")
	rec := capture.Default(t)
	failActivate := true
	s := serve(path, func(context.Context, Active) error {
		if failActivate {
			return errActivate
		}
		return nil
	}, nil)
	real := writeFile
	t.Cleanup(func() { writeFile = real })
	writes := 0
	writeFile = func(ctx context.Context, p string, data []byte) error {
		writes++
		if writes == 2 {
			return errors.New("disk full")
		}
		return real(ctx, p, data)
	}
	if _, _, err := save(t, s, "server: {log_level: debug}\n"); !errors.Is(err, ErrActivation) {
		t.Fatalf("Save error %v, want ErrActivation", err)
	}
	if sig := signalled(s); !s.Diverged() || !sig {
		t.Errorf("Diverged %v and Changed signalled %v after a failed restore; want both", s.Diverged(), sig)
	}
	if rec.CountLevel(slog.LevelError, "could not be restored") != 1 {
		t.Errorf("logged %q, want one ERROR naming the failed restore", rec.Messages())
	}
	failActivate = false
	if _, errs, err := save(t, s, "server: {log_level: warn}\n"); err != nil || errs != nil {
		t.Fatalf("the save after a failed restore = %v, %v; want it accepted", errs, err)
	}
	if s.Diverged() || logLevel(Load(path, Registries{})) != slog.LevelWarn {
		t.Error("a committed save did not clear Diverged or write the file")
	}
}

func TestShutdownCancelsRunningSave(t *testing.T) {
	t.Run("an overrun cancels the save, which restores the file", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			const before = "server: {log_level: info}\n"
			path := fixture(t, before)
			s := serve(path, func(ctx context.Context, _ Active) error {
				<-ctx.Done()
				return ctx.Err()
			}, nil)
			saved := make(chan error, 1)
			go func() {
				_, _, err := save(t, s, "server: {log_level: debug}\n")
				saved <- err
			}()
			synctest.Wait()
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("Shutdown = %v, want the deadline error", err)
			}
			if err := <-saved; !errors.Is(err, ErrActivation) {
				t.Errorf("the running save answered %v, want ErrActivation", err)
			}
			if fileText(t, path) != before {
				t.Errorf("file %q, want the baseline", fileText(t, path))
			}
			if _, _, err := save(t, s, "server: {log_level: warn}\n"); !errors.Is(err, ErrClosed) {
				t.Errorf("a later save answered %v, want ErrClosed", err)
			}
		})
	})
	t.Run("an idle service shuts down at once, twice", func(t *testing.T) {
		s := serve(fixture(t, ""), nil, nil)
		for range 2 {
			if err := s.Shutdown(t.Context()); err != nil {
				t.Errorf("Shutdown = %v", err)
			}
		}
		if _, _, err := save(t, s, "server: {}\n"); !errors.Is(err, ErrClosed) {
			t.Errorf("save after Shutdown answered %v, want ErrClosed", err)
		}
	})
	t.Run("an idle service shuts down cleanly on an expired context", func(t *testing.T) {
		expired, cancel := context.WithCancel(t.Context())
		cancel()
		path := fixture(t, "")
		for i := range 64 {
			s := serve(path, nil, nil)
			for call := range 2 {
				if err := s.Shutdown(expired); err != nil {
					t.Fatalf("service %d, Shutdown %d = %v; want nil with no Save running", i, call, err)
				}
			}
		}
	})
}

type ctxKey struct{}

func TestSaveCheckRefuses(t *testing.T) {
	refusal := []formschema.FieldError{{Path: "server.log_level", Message: "refused by the check"}}
	t.Run("a refusal writes nothing", func(t *testing.T) {
		const before = "server: {log_level: info}\n"
		path := fixture(t, before)
		var gotCtx context.Context
		var prev Active
		s := serve(path, nil, func(ctx context.Context, p, _ Active) []formschema.FieldError {
			gotCtx, prev = ctx, p
			return refusal
		})
		old := s.Snapshot()
		ctx := context.WithValue(t.Context(), ctxKey{}, "save")
		_, errs, err := s.Save(ctx, node(t, "server: {log_level: debug}\n"), old.Revision())
		if err != nil || !slices.Equal(errs, refusal) {
			t.Fatalf("Save = %v, %v; want the check's errors", errs, err)
		}
		if gotCtx != ctx {
			t.Error("the check did not run under the Save's context")
		}
		if prev.Server.LogLevel() != slog.LevelInfo || prev.Server.p == nil {
			t.Errorf("prev %+v, want the live Active", prev)
		}
		if fileText(t, path) != before || s.Snapshot().p != old.p {
			t.Error("a refused save wrote the file or replaced the snapshot")
		}
	})
	t.Run("load and a refused parse never call it", func(t *testing.T) {
		calls := 0
		s := serve(fixture(t, "server: {}\n"), nil, func(context.Context, Active, Active) []formschema.FieldError {
			calls++
			return nil
		})
		if _, errs, _ := save(t, s, "bogus: 1\n"); len(errs) == 0 {
			t.Fatal("an unknown section was accepted")
		}
		if calls != 0 {
			t.Errorf("check ran %d times", calls)
		}
	})
	t.Run("from Unconfigured prev is the zero Active", func(t *testing.T) {
		called := false
		s := serve(fixture(t, ""), nil, func(_ context.Context, p, _ Active) []formschema.FieldError {
			called = true
			if p.Server.p != nil {
				t.Errorf("prev %+v, want the zero Active", p)
			}
			return nil
		})
		if _, errs, err := save(t, s, "server: {}\n"); err != nil || errs != nil || !called {
			t.Errorf("Save = %v, %v, check called %v", errs, err, called)
		}
	})
	t.Run("a nil check checks nothing", func(t *testing.T) {
		s := serve(fixture(t, ""), nil, nil)
		if _, errs, err := save(t, s, "server: {log_level: debug}\n"); err != nil || errs != nil {
			t.Errorf("Save = %v, %v", errs, err)
		}
	})
}

// merged runs s.Merge over entry, answering the expanded copy parse saw (nil
// when parse never ran) and Merge's errors.
func merged(t *testing.T, s *Service, entry string) (*yaml.Node, []formschema.FieldError) {
	t.Helper()
	var seen *yaml.Node
	calls := 0
	errs := s.Merge("sources", node(t, entry), func(n *yaml.Node) []formschema.FieldError {
		calls++
		seen = deepCopy(n)
		return nil
	})
	if calls > 1 {
		t.Errorf("parse ran %d times", calls)
	}
	return seen, errs
}

func TestMergeTakesStoredSecret(t *testing.T) {
	const stored = "sources:\n" +
		"  - {id: a, url: 'http://x', api_key: k1, auth: {token: t1}}\n" +
		"  - {id: b, url: 'http://y', api_key: '${SUBFLUX_KEY_B}'}\n"
	t.Setenv("SUBFLUX_KEY_B", "from-env")
	path := fixture(t, stored)
	s := serve(path, nil, nil)
	rev := s.Snapshot().Revision()
	value := func(t *testing.T, n *yaml.Node, key string) string {
		t.Helper()
		v := mappingValue(n, key)
		if v == nil {
			t.Fatalf("merged entry has no %s", key)
		}
		return v.Value
	}
	t.Run("a stored id gives its key", func(t *testing.T) {
		got, errs := merged(t, s, "{id: a, url: 'http://z', api_key: !unchanged '', auth: {token: !unchanged ''}}")
		if errs != nil || got == nil {
			t.Fatalf("Merge errors %v, parse saw %v", errs, got)
		}
		if value(t, got, "api_key") != "k1" || value(t, got, "url") != "http://z" || value(t, mappingValue(got, "auth"), "token") != "t1" {
			t.Errorf("parse saw %+v, want the stored key and token at the new url", got)
		}
	})
	t.Run("a stored reference is expanded for parse", func(t *testing.T) {
		got, errs := merged(t, s, "{id: b, url: 'http://y', api_key: !unchanged ''}")
		if errs != nil || got == nil || value(t, got, "api_key") != "from-env" {
			t.Errorf("parse saw %+v, errors %v; want the expanded key", got, errs)
		}
	})
	t.Run("parse's errors are Merge's", func(t *testing.T) {
		want := []formschema.FieldError{{Path: "sources.url", Message: "unreachable"}}
		errs := s.Merge("sources", node(t, "{id: a, url: 'http://z'}"), func(*yaml.Node) []formschema.FieldError { return want })
		if !slices.Equal(errs, want) {
			t.Errorf("Merge = %v, want parse's %v", errs, want)
		}
	})
	refusals := []struct{ name, entry, path string }{
		{"an id the file lacks", "{id: c, api_key: !unchanged ''}", "sources"},
		{"no id", "{api_key: !unchanged ''}", "sources"},
		{"a key the stored entry lacks", "{id: a, password: !unchanged ''}", "sources.password"},
		{"an unresolved reference", "{id: c, api_key: '${SUBFLUX_MISSING}'}", "sources.api_key"},
		{"a forged mark", "{id: a, api_key: !plain k}", "sources.api_key"},
		{"an anchor", "{id: a, url: &u 'http://z'}", "sources.url"},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			got, errs := merged(t, s, c.entry)
			if got != nil || !slices.Equal(errPaths(errs), []string{c.path}) {
				t.Errorf("parse saw %+v, errors %v; want no parse and one error at %s", got, errs, c.path)
			}
		})
	}
	t.Run("merging writes nothing", func(t *testing.T) {
		if fileText(t, path) != stored || s.Snapshot().Revision() != rev {
			t.Error("Merge changed the file or the snapshot")
		}
	})
}

// TestExpandedSecretStaysInParse pins where a resolved ${SUBFLUX_...} value
// may appear: only in the copy an owner's parse reads. Merge returns no
// document, its errors, the caller's entry, the file, the Document and every
// log line keep the reference text.
func TestExpandedSecretStaysInParse(t *testing.T) {
	const secret = "resolved-secret-value"
	t.Setenv("SUBFLUX_SRC_KEY", secret)
	rec := capture.Default(t)
	const stored = "sources: [{id: a, url: 'http://x', api_key: '${SUBFLUX_SRC_KEY}'}]\n"
	s := serve(fixture(t, stored), nil, nil)
	leaks := func(t *testing.T, where, text string) {
		t.Helper()
		if strings.Contains(text, secret) {
			t.Errorf("%s holds the resolved value: %s", where, text)
		}
	}
	t.Run("Merge hands the value to parse only", func(t *testing.T) {
		entry := node(t, "{id: a, url: 'http://y', api_key: '${SUBFLUX_SRC_KEY}', mode: bogus}")
		sawValue := false
		errs := s.Merge("sources", entry, func(n *yaml.Node) []formschema.FieldError {
			sawValue = mappingValue(n, "api_key").Value == secret
			return []formschema.FieldError{{Path: "sources.mode", Message: "must be one of a, b"}}
		})
		if !sawValue {
			t.Error("parse did not see the resolved value")
		}
		leaks(t, "Merge's errors", fmt.Sprint(errs))
		leaks(t, "the caller's entry", encoded(t, entry))
	})
	t.Run("an unresolved name is an error naming the variable", func(t *testing.T) {
		errs := s.Merge("sources", node(t, "{id: a, api_key: '${SUBFLUX_UNSET_KEY}'}"), func(*yaml.Node) []formschema.FieldError {
			t.Error("parse ran over an unresolved reference")
			return nil
		})
		if len(errs) != 1 || errs[0].Path != "sources.api_key" || !strings.Contains(errs[0].Message, "${SUBFLUX_UNSET_KEY}") {
			t.Errorf("errors %v, want one at sources.api_key naming the variable", errs)
		}
	})
	t.Run("Save and Document keep the reference text", func(t *testing.T) {
		values, _ := testSection(t)
		tpath := fixture(t, "t: {s: '${SUBFLUX_SRC_KEY}', a: x}\n")
		ts := serve(tpath, nil, nil)
		snap := ts.Snapshot()
		next, errs, err := ts.Save(t.Context(), snap.Document(), snap.Revision())
		if err != nil || errs != nil {
			t.Fatalf("Save = %v, %v", errs, err)
		}
		if v, _ := values.Secret("s"); v != secret {
			t.Errorf("the section parsed %q, want the resolved value", v)
		}
		text := fileText(t, tpath)
		leaks(t, "the file", text)
		if !strings.Contains(text, "${SUBFLUX_SRC_KEY}") {
			t.Errorf("file %q lost the reference", text)
		}
		leaks(t, "the Document", encoded(t, next.Document()))
	})
	for _, r := range rec.Records() {
		line := r.Message
		r.Attrs(func(a slog.Attr) bool {
			line += " " + a.String()
			return true
		})
		leaks(t, "a log line", line)
	}
}

// TestEditorSaveAfterInterleavedCommitConflicts pins that a document and its
// revision are one reading: a commit between an editor's read and its save
// makes that save a conflict, never an overwrite.
func TestEditorSaveAfterInterleavedCommitConflicts(t *testing.T) {
	path := fixture(t, "server: {log_level: info}\n")
	s := serve(path, nil, nil)
	read := s.Snapshot()
	doc := read.Document()
	if _, errs, err := save(t, s, "server: {log_level: warn}\n"); err != nil || errs != nil {
		t.Fatalf("the interleaved Save = %v, %v", errs, err)
	}
	doc.Content[1].Content[1].Value = "debug"
	if _, _, err := s.Save(t.Context(), doc, read.Revision()); !errors.Is(err, ErrConflict) {
		t.Errorf("the editor's Save answered %v, want ErrConflict", err)
	}
	if logLevel(Load(path, Registries{})) != slog.LevelWarn {
		t.Errorf("file %q, want the interleaved commit kept", fileText(t, path))
	}
}

func TestSaveRefusesStaleBase(t *testing.T) {
	path := fixture(t, "server: {log_level: info}\n")
	s := serve(path, nil, nil)
	base := s.Snapshot().Revision()
	first, errs, err := s.Save(t.Context(), node(t, "server: {log_level: debug}\n"), base)
	if err != nil || errs != nil {
		t.Fatalf("first Save = %v, %v", errs, err)
	}
	if first.Revision() == base || first.Revision() == "" {
		t.Errorf("revision %q after a commit from %q; want a new one", first.Revision(), base)
	}
	if err := os.WriteFile(path, []byte("server: {log_level: error}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errs, err = s.Save(t.Context(), node(t, "server: {log_level: warn}\n"), base)
	if !errors.Is(err, ErrConflict) || errs != nil {
		t.Errorf("stale Save = %v, %v; want ErrConflict before the file is read", errs, err)
	}
	if fileText(t, path) != "server: {log_level: error}\n" || s.Snapshot().p != first.p {
		t.Error("a stale save wrote the file or replaced the snapshot")
	}
}

func TestSaveBodyShapes(t *testing.T) {
	forged := node(t, "server: {log_level: debug}\n")
	forged.Content[1].Content[1].Tag = formschema.PlainTag
	binary := node(t, "server: {log_level: debug}\n")
	binary.Content[1].Content[1].Tag, binary.Content[1].Content[1].Value = "!!binary", "not base64"
	cases := []struct {
		name  string
		body  *yaml.Node
		paths []string
		msg   string
	}{
		{"a written mark", node(t, "server: {log_level: !plain debug}\n"), []string{"server.log_level"}, ""},
		{"a written core tag", node(t, "server: {log_level: !!str debug}\n"), []string{"server.log_level"}, msgTagged},
		{"a mark set in code", forged, []string{"server.log_level"}, ""},
		{"a key held twice", node(t, "server: {log_level: debug, log_level: warn}\n"), []string{"server.log_level"}, ""},
		{"an anchor and alias", node(t, "server: {log_level: &l debug, log_format: *l}\n"), []string{"server.log_level", "server.log_format"}, ""},
		{"a merge key", node(t, "server: {<<: {log_level: debug}}\n"), []string{"server.<<"}, ""},
		{"an unchanged value with text", node(t, "server: {log_level: !unchanged debug}\n"), []string{"server.log_level"}, ""},
		{"an unchanged key", node(t, "server: {!unchanged '': x}\n"), []string{"server."}, ""},
		{"a binary scalar", node(t, "server: {log_level: !!binary ZGVidWc=}\n"), []string{"server.log_level"}, ""},
		{"a binary scalar set in code", binary, []string{"server.log_level"}, msgTagged},
		{"invalid UTF-8", &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "server"},
			{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "log_level"}, {Kind: yaml.ScalarNode, Value: "\xff"}}},
		}}, []string{"server.log_level"}, msgUTF8},
		{"a list at the root", node(t, "[a]\n"), []string{""}, ""},
		{"no document", nil, []string{""}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			const before = "server: {log_level: info}\n"
			path := fixture(t, before)
			s := serve(path, nil, nil)
			_, errs, err := s.Save(t.Context(), c.body, s.Snapshot().Revision())
			if err != nil || !slices.Equal(errPaths(errs), c.paths) {
				t.Fatalf("Save = %v, %v; want errors at %q", errs, err, c.paths)
			}
			if c.msg != "" && errs[0].Message != c.msg {
				t.Errorf("message %q, want %q", errs[0].Message, c.msg)
			}
			if fileText(t, path) != before {
				t.Error("a refused body was written")
			}
		})
	}
}

func TestNewServiceOfZeroSnapshotRefusesSaves(t *testing.T) {
	path := fixture(t, "server: {}\n")
	s := NewService(path, Registries{}, Snapshot{}, nil, nil)
	if s.Snapshot().Mode() != Failed {
		t.Errorf("mode %v, want Failed", s.Snapshot().Mode())
	}
	if _, errs, err := save(t, s, "server: {}\n"); err != nil || len(errs) != 1 || errs[0].Message != msgUnreadAtStart {
		t.Errorf("Save = %v, %v; want the unread-at-start error", errs, err)
	}
}
