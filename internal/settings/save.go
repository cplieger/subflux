package settings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

const (
	msgChangedOnDisk = "config.yaml changed on disk; restart to load it"
	msgUnreadAtStart = "config.yaml could not be read when settings loaded; fix it by hand and restart"
	msgTooLarge      = "config.yaml would be larger than 1 MiB"
)

// fileState is the file as one read found it: absent, or present holding
// data. A read that failed knows neither.
type fileState struct {
	data    []byte
	present bool
	known   bool
}

func (f fileState) same(g fileState) bool {
	return f.known && g.known && f.present == g.present && bytes.Equal(f.data, g.data)
}

// writeFile is the one write seam: Save's persist and its restore both call it.
var writeFile = func(ctx context.Context, path string, data []byte) error {
	_, err := atomicfile.WriteFile(ctx, path, data, atomicfile.WithMode(0o600), atomicfile.WithMaxBytes(maxFileBytes))
	return err
}

// Save replaces the file with doc, a whole document as Document answers it,
// each unchanged scalar taken from the stored file, and makes it live. base
// is the Revision the document was read at; another is ErrConflict, checked
// first. A document that does not parse, a check that refuses it, or a file
// changed on disk since this service last read or wrote it answers the live
// snapshot and field errors, writing nothing. The write and activate run to
// the end whatever ctx does, and only Shutdown cancels them; a failed
// activate restores the file and answers ErrActivation.
func (s *Service) Save(ctx context.Context, doc *yaml.Node, base string) (Snapshot, []formschema.FieldError, error) {
	if err := s.lock(ctx); err != nil {
		return s.Snapshot(), nil, err
	}
	defer s.unlock()
	cur := s.Snapshot()
	if base != cur.Revision() {
		return cur, nil, ErrConflict
	}
	merged, errs := mergeDocument(cur.p.doc, doc)
	if len(errs) > 0 {
		return cur, errs, nil
	}
	data, err := encode(merged)
	if err != nil {
		return cur, nil, err
	}
	if len(data) > maxFileBytes {
		return cur, rootError(msgTooLarge), nil
	}
	if err := ctx.Err(); err != nil {
		return cur, nil, err
	}
	if baseline := readFile(s.path); !baseline.same(s.seen) {
		if !s.seen.known {
			return cur, rootError(msgUnreadAtStart), nil
		}
		return cur, rootError(msgChangedOnDisk), nil
	}
	next := fromBytes(data, s.r)
	if errs, failed := next.Failure(); failed {
		return cur, errs, nil
	}
	active, _ := next.Active()
	if s.check != nil {
		prev, ok := cur.Active()
		if !ok {
			prev = Active{}
		}
		if errs := s.check(ctx, prev, active); len(errs) > 0 {
			return cur, errs, nil
		}
	}
	return s.commit(ctx, cur, next, active)
}

// commit writes next's file, activates it and makes it live, on a context
// only Shutdown cancels: a disconnect then cannot leave the file and the live
// settings apart.
func (s *Service) commit(ctx context.Context, cur, next Snapshot, active Active) (Snapshot, []formschema.FieldError, error) {
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	defer context.AfterFunc(s.stop, cancel)()
	if err := writeFile(sctx, s.path, next.p.file.data); err != nil {
		return cur, nil, fmt.Errorf("settings: write config.yaml: %w", err)
	}
	if s.activate != nil {
		if err := s.activate(sctx, active); err != nil {
			s.restore()
			return cur, nil, fmt.Errorf("%w: %w", ErrActivation, err)
		}
	}
	s.seen = next.p.file
	s.cur.Store(next.p)
	s.diverged.Store(false)
	s.signal()
	return next, nil, nil
}

// restore puts back s.seen, the file before the write, taking no context so a
// cancelled Save still restores: the bytes rewritten, or the file removed and
// its directory synced. When that fails the file holds settings that are not
// live: the re-read file becomes the baseline, so the next Save is not
// refused as a hand edit, and Diverged reports it.
func (s *Service) restore() {
	var err error
	if s.seen.present {
		err = writeFile(context.Background(), s.path, s.seen.data)
	} else {
		err = removeFile(s.path)
	}
	if err == nil {
		return
	}
	slog.Error("settings: config.yaml could not be restored after a failed save; it holds settings that are not live", "error", err)
	s.seen = readFile(s.path)
	s.diverged.Store(true)
	s.signal()
}

func removeFile(path string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if rerr := atomicfile.RemoveFileInRoot(root, filepath.Base(path)); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return rerr
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

// readFile reads the file as Save's baseline.
func readFile(path string) fileState {
	data, err := atomicfile.ReadBounded(context.Background(), path, maxFileBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fileState{known: true}
	case err != nil:
		return fileState{}
	}
	return fileState{data: data, present: true, known: true}
}

func rootError(msg string) []formschema.FieldError {
	return []formschema.FieldError{{Message: msg}}
}

// encode is n as file text, two-space indented.
func encode(n *yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(n); err != nil {
		return nil, fmt.Errorf("settings: encode the document: %w", err)
	}
	if err := e.Close(); err != nil {
		return nil, fmt.Errorf("settings: encode the document: %w", err)
	}
	return b.Bytes(), nil
}

// reread is n as a file holding it would read back, its tags resolved from
// the text, so expansion and the owners' parsers see what a Save would load.
func reread(n *yaml.Node) (*yaml.Node, error) {
	data, err := encode(n)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("settings: reread the document: %w", err)
	}
	return doc.Content[0], nil
}
