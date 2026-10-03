package mediawrite_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const atomicfilePath = "github.com/cplieger/atomicfile/v4"

var atomicfileWriters = map[string]bool{
	"WriteFile": true, "WriteReader": true, "NewPendingFile": true,
	"WriteFileInRoot": true, "WriteReaderInRoot": true, "NewPendingFileInRoot": true,
}

// TestAtomicfileWrites_outside_mediawrite_pass_WithMode keeps every media
// write behind the folder guard and every other write's mode explicit: an
// atomicfile write without WithMode outside this package is either a
// subtitle write that bypasses the guard or a private file that lost its
// owner-only mode.
func TestAtomicfileWrites_outside_mediawrite_pass_WithMode(t *testing.T) {
	moduleRoot := filepath.Join("..", "..")
	here, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	err = filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "testdata", "vendor", ".git":
				return filepath.SkipDir
			}
			if abs, _ := filepath.Abs(path); abs == here {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		found, err := unguardedWrites(path)
		violations = append(violations, found...)
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", moduleRoot, err)
	}
	for _, v := range violations {
		t.Errorf("%s: atomicfile write without atomicfile.WithMode outside internal/mediawrite", v)
	}
}

func unguardedWrites(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	pkgName := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == atomicfilePath {
			pkgName = "atomicfile"
			if imp.Name != nil {
				pkgName = imp.Name.Name
			}
		}
	}
	if pkgName == "" {
		return nil, nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isPkgCall(call.Fun, pkgName, atomicfileWriters) {
			return true
		}
		for _, arg := range call.Args {
			if c, ok := arg.(*ast.CallExpr); ok && isPkgCall(c.Fun, pkgName, map[string]bool{"WithMode": true}) {
				return true
			}
		}
		out = append(out, fset.Position(call.Pos()).String())
		return true
	})
	return out, nil
}

func isPkgCall(fun ast.Expr, pkg string, names map[string]bool) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && names[sel.Sel.Name]
}
