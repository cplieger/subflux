package testrig

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/cplieger/subflux"

// importRule confines a dependency to the packages allowed to import it
// directly. imported and each allowed entry match a path or, ending in
// "/...", a path and everything beneath it. everyPackage extends the check
// from this module's packages to every non-stdlib package in the build.
type importRule struct {
	imported     string
	allowed      []string
	everyPackage bool
}

// importRules is the architecture's confinement table; each row lands with
// the landing that turns it on.
var importRules []importRule

// providerHTTPRoots are trees whose subpackages may not build a raw net/http
// client or transport; the provider boundary owns the one client.
var providerHTTPRoots []string

func TestImportConfinement(t *testing.T) {
	pkgs := goList(t, absDir(t, moduleRoot), "./...")
	for _, v := range importViolations(pkgs, modulePath, importRules) {
		t.Error(v)
	}
}

func TestProviderHTTP(t *testing.T) {
	pkgs := goList(t, absDir(t, moduleRoot), "./...")
	vs, err := rawHTTPViolations(pkgs, modulePath, providerHTTPRoots)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func TestConfinementChecker(t *testing.T) {
	const fixture = "example.com/confinement"
	pkgs := goList(t, absDir(t, "testdata/confinement"), "./...")

	t.Run("planted import", func(t *testing.T) {
		rules := []importRule{{imported: fixture + "/b", allowed: []string{fixture + "/c"}}}
		got := importViolations(pkgs, fixture, rules)
		want := []string{fixture + "/a imports " + fixture + "/b, allowed only in [" + fixture + "/c]"}
		if !slices.Equal(got, want) {
			t.Errorf("violations = %q, want %q", got, want)
		}
	})
	t.Run("allowed importer", func(t *testing.T) {
		rules := []importRule{{imported: fixture + "/b", allowed: []string{fixture + "/..."}}}
		if got := importViolations(pkgs, fixture, rules); len(got) != 0 {
			t.Errorf("violations = %q, want none", got)
		}
	})
	t.Run("every package", func(t *testing.T) {
		rule := importRule{imported: fixture + "/b"}
		if got := importViolations(pkgs, "example.com/other", []importRule{rule}); len(got) != 0 {
			t.Errorf("module-only rule outside its module: violations = %q, want none", got)
		}
		rule.everyPackage = true
		if got := importViolations(pkgs, "example.com/other", []importRule{rule}); len(got) != 1 {
			t.Errorf("every-package rule: violations = %q, want the one planted import", got)
		}
	})
	t.Run("raw client", func(t *testing.T) {
		got, err := rawHTTPViolations(pkgs, fixture, []string{""})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"a/a.go:10: raw http.Client",
			"a/a.go:11: raw http.DefaultTransport",
			"a/a.go:15: raw http.Get",
		}
		if !slices.Equal(got, want) {
			t.Errorf("violations = %q, want %q", got, want)
		}
	})
}

// importViolations reports each direct import a rule forbids, once per
// importer and imported path, test variants folded into their package.
func importViolations(pkgs []listedPackage, module string, rules []importRule) []string {
	var out []string
	for _, p := range pkgs {
		if p.Standard || p.Module == nil {
			continue
		}
		importer := basePackage(p.ImportPath)
		for _, r := range rules {
			if !r.everyPackage && p.Module.Path != module {
				continue
			}
			for _, imp := range p.Imports {
				imp = basePackage(imp)
				if !matchPath(r.imported, imp) || slices.ContainsFunc(r.allowed, func(a string) bool { return matchPath(a, importer) }) {
					continue
				}
				v := fmt.Sprintf("%s imports %s, allowed only in %v", importer, imp, r.allowed)
				if !slices.Contains(out, v) {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// rawHTTPViolations parses the non-test files of every module package beneath
// a root (relative to module; "" is the module root) and reports each raw
// net/http client, transport or default-client call, as dir/file:line.
func rawHTTPViolations(pkgs []listedPackage, module string, roots []string) ([]string, error) {
	var out []string
	for _, p := range pkgs {
		if p.Module == nil || p.Module.Path != module || p.ImportPath != basePackage(p.ImportPath) {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, module), "/")
		if !slices.ContainsFunc(roots, func(root string) bool { return under(root, rel) }) {
			continue
		}
		for _, name := range p.GoFiles {
			vs, err := rawHTTPInFile(filepath.Join(p.Dir, name), filepath.ToSlash(filepath.Join(rel, name)))
			if err != nil {
				return nil, err
			}
			out = append(out, vs...)
		}
	}
	return out, nil
}

// rawHTTPSelectors are the net/http names that build or use a client outside
// the provider boundary's.
var rawHTTPSelectors = []string{"Client", "Transport", "DefaultClient", "DefaultTransport", "Get", "Head", "Post", "PostForm"}

func rawHTTPInFile(path, shown string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	local := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == "net/http" {
			local = "http"
			if imp.Name != nil {
				local = imp.Name.Name
			}
		}
	}
	if local == "" || local == "_" {
		return nil, nil
	}
	if local == "." {
		return []string{fmt.Sprintf("%s: net/http dot import", shown)}, nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == local && slices.Contains(rawHTTPSelectors, sel.Sel.Name) {
			out = append(out, fmt.Sprintf("%s:%d: raw http.%s", shown, fset.Position(sel.Pos()).Line, sel.Sel.Name))
		}
		return true
	})
	return out, nil
}

// basePackage folds a test variant ("p [p.test]", "p_test [p.test]") into p.
func basePackage(importPath string) string {
	path, _, _ := strings.Cut(importPath, " ")
	return strings.TrimSuffix(path, "_test")
}

func matchPath(pattern, path string) bool {
	if tree, ok := strings.CutSuffix(pattern, "/..."); ok {
		return path == tree || strings.HasPrefix(path, tree+"/")
	}
	return path == pattern
}

// under reports whether rel is a package strictly beneath root.
func under(root, rel string) bool {
	if root == "" {
		return rel != ""
	}
	return strings.HasPrefix(rel, root+"/")
}
