package mediawrite_test

import (
	"crypto/sha256"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

type code = struct {
	Err  error
	Code string
}

// failureArm is one way an api-called export refuses.
type failureArm struct {
	call func() error
	name string
}

// failureArms builds a library with one root of each state and drives
// every refusal of the api-called exports.
func failureArms(t *testing.T) []failureArm {
	t.Helper()
	dir := t.TempDir()
	up, down := filepath.Join(dir, "up"), filepath.Join(dir, "down")
	writeFile(t, filepath.Join(up, "keep"), "x")
	served := filepath.Join(up, "S01E03.mkv")
	writeFile(t, served, "video")
	writeFile(t, filepath.Join(up, "S01E03.en.srt"), srtOf("en"))
	writeFile(t, filepath.Join(up, "S01E03.en.sub"), "vobsub")
	writeFile(t, filepath.Join(up, "S01E03.fr.srt"), srtOf("fr"))
	store := openStore(t)
	insts := seed(t, store, filepath.Join(dir, "elsewhere", "S01E01.mkv"), filepath.Join(down, "S01E02.mkv"), served)
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(up, down)}, nil)
	run(t, w)
	if err := store.BeginDelete(t.Context(), insts[2], "S01E03.fr.srt", sha256.Sum256([]byte(srtOf("fr")))); err != nil {
		t.Fatal(err)
	}
	ref := func(inst statedb.InstanceID, name string) mediawrite.FileRef {
		r, err := mediawrite.DecodeRef(inst, name)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	en, sub, pending := ref(insts[2], "S01E03.en.srt"), ref(insts[2], "S01E03.en.sub"), ref(insts[2], "S01E03.fr.srt")
	hashed := mediawrite.Expectation{SHA256: sha256.Sum256([]byte(srtOf("en")))}
	subHash := mediawrite.Expectation{SHA256: sha256.Sum256([]byte("vobsub"))}
	retime := func(r mediawrite.FileRef, e mediawrite.Expectation, tm timing.Timing) func() error {
		return func() error { _, err := w.Retime(t.Context(), r, e, tm, nil); return err }
	}
	current := func(r mediawrite.FileRef, e mediawrite.Expectation) func() error {
		return func() error { _, err := w.Current(t.Context(), r, e); return err }
	}
	del := func(r mediawrite.FileRef, e mediawrite.Expectation) func() error {
		return func() error { return w.Delete(t.Context(), r, e) }
	}
	shift := timing.Shift{Ms: 1000}
	openVideo := func(inst statedb.InstanceID) func() error {
		return func() error {
			f, err := w.OpenVideo(t.Context(), inst)
			if err == nil {
				_ = f.Close()
			}
			return err
		}
	}
	list := func(inst statedb.InstanceID) func() error {
		return func() error {
			_, err := w.List(t.Context(), inst, mediawrite.ListOptions{})
			return err
		}
	}
	return []failureArm{
		{name: "OpenVideo unknown instance", call: openVideo(insts[1] + 100)},
		{name: "OpenVideo outside every root", call: openVideo(insts[0])},
		{name: "OpenVideo root unavailable", call: openVideo(insts[1])},
		{name: "List unknown instance", call: list(insts[1] + 100)},
		{name: "List outside every root", call: list(insts[0])},
		{name: "List root unavailable", call: list(insts[1])},
		{name: "DecodeRef zero instance", call: func() error { _, err := mediawrite.DecodeRef(0, "a.en.srt"); return err }},
		{name: "DecodeRef bad name", call: func() error { _, err := mediawrite.DecodeRef(insts[0], "../a.en.srt"); return err }},
		{name: "ValidName", call: func() error { return mediawrite.ValidName("a.vtt") }},
		{name: "Retime zero reference", call: retime(mediawrite.FileRef{}, hashed, shift)},
		{name: "Retime outside every root", call: retime(ref(insts[0], "S01E01.en.srt"), hashed, shift)},
		{name: "Retime root unavailable", call: retime(ref(insts[1], "S01E02.en.srt"), hashed, shift)},
		{name: "Retime zero expectation", call: retime(en, mediawrite.Expectation{}, shift)},
		{name: "Retime invalid timing", call: retime(en, hashed, timing.Shift{Ms: 7 * 3_600_000})},
		{name: "Retime pending", call: retime(pending, hashed, shift)},
		{name: "Retime .sub", call: retime(sub, subHash, shift)},
		{name: "Delete zero reference", call: del(mediawrite.FileRef{}, hashed)},
		{name: "Delete outside every root", call: del(ref(insts[0], "S01E01.en.srt"), hashed)},
		{name: "Delete root unavailable", call: del(ref(insts[1], "S01E02.en.srt"), hashed)},
		{name: "Delete zero expectation", call: del(en, mediawrite.Expectation{})},
		{name: "Delete pending", call: del(pending, hashed)},
		{name: "Delete .sub", call: del(sub, subHash)},
		{name: "Current zero reference", call: current(mediawrite.FileRef{}, hashed)},
		{name: "Current outside every root", call: current(ref(insts[0], "S01E01.en.srt"), hashed)},
		{name: "Current root unavailable", call: current(ref(insts[1], "S01E02.en.srt"), hashed)},
		{name: "Current zero expectation", call: current(en, mediawrite.Expectation{})},
		{name: "Current pending", call: current(pending, hashed)},
		{name: "Current .sub", call: current(sub, subHash)},
	}
}

func TestCodesCoverFailureTable(t *testing.T) {
	t.Parallel()
	for _, arm := range failureArms(t) {
		err := arm.call()
		if err == nil {
			t.Errorf("%s: succeeded, want a refusal", arm.name)
			continue
		}
		if !slices.ContainsFunc(mediawrite.Codes, func(c code) bool { return errors.Is(err, c.Err) }) {
			t.Errorf("%s: %v is no entry of mediawrite.Codes", arm.name, err)
		}
	}
}

var sentinels = map[string]error{
	"ErrInvalidRef":      mediawrite.ErrInvalidRef,
	"ErrExpectation":     mediawrite.ErrExpectation,
	"ErrPathPending":     mediawrite.ErrPathPending,
	"ErrNameContended":   mediawrite.ErrNameContended,
	"ErrOutsideRoot":     mediawrite.ErrOutsideRoot,
	"ErrRootUnavailable": mediawrite.ErrRootUnavailable,
}

// exempt lists sentinels deliberately left out of Codes, with the reason.
var exempt = map[string]string{
	"ErrNameContended": "only Deliver returns it, and only search calls Deliver",
}

// Go cannot enumerate package vars by reflect, so the source is parsed.
func TestEverySentinelCoded(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				for _, id := range spec.(*ast.ValueSpec).Names {
					if id.IsExported() && strings.HasPrefix(id.Name, "Err") {
						declared = append(declared, id.Name)
					}
				}
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no exported Err* var; the parse is broken")
	}
	for _, name := range declared {
		v, ok := sentinels[name]
		if !ok {
			t.Errorf("%s is missing from this test's sentinels table", name)
			continue
		}
		_, isExempt := exempt[name]
		coded := slices.ContainsFunc(mediawrite.Codes, func(c code) bool { return c.Err == v })
		if coded == isExempt {
			t.Errorf("%s: coded %v, exempt %v; want exactly one", name, coded, isExempt)
		}
	}
	for name := range sentinels {
		if !slices.Contains(declared, name) {
			t.Errorf("sentinels names %s, which the package does not declare", name)
		}
	}
}
