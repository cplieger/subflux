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
// directly or, when forbidden is set, keeps it out of those importers
// alone. imported and each allowed or forbidden entry match a path or,
// ending in "/...", a path and everything beneath it. everyPackage extends
// the check from this module's packages to every non-stdlib package in the
// build.
type importRule struct {
	imported     string
	allowed      []string
	forbidden    []string
	everyPackage bool
}

// importRules is the architecture's confinement table; each row lands with
// the landing that turns it on.
var importRules = []importRule{releaseRule(modulePath)}

// releaseRule keeps release.Parsed inside search's packages: rank, the
// provider adapters and the engine.
func releaseRule(module string) importRule {
	return importRule{
		imported: module + "/internal/release",
		allowed:  []string{module + "/internal/rank", module + "/internal/provider/...", module + "/internal/search"},
	}
}

// providerHTTPRoots are trees whose subpackages may not build a raw net/http
// client or transport; the provider boundary owns the one client.
var providerHTTPRoots []string

// scheduleRule keeps scan out of settings, which holds the scan section's
// Schedule from mediasource: scan imports status, so settings importing scan
// would make a cycle once status is built over settings.
var scheduleRule = importRule{imported: modulePath + "/internal/scan", forbidden: []string{modulePath + "/internal/settings/..."}}

func TestScheduleLivesInMediasource(t *testing.T) {
	pkgs := goList(t, absDir(t, moduleRoot), "./...")
	for _, v := range importViolations(pkgs, modulePath, []importRule{scheduleRule}) {
		t.Error(v)
	}
}

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

// TestConfinementRulesSetOneList: violation reads forbidden alone when it is
// set, so a rule setting both lists would check only half of what it says.
func TestConfinementRulesSetOneList(t *testing.T) {
	for _, r := range append(slices.Clone(importRules), scheduleRule) {
		if len(r.allowed) > 0 && len(r.forbidden) > 0 {
			t.Errorf("rule for %s sets both allowed and forbidden", r.imported)
		}
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
	t.Run("forbidden importer", func(t *testing.T) {
		rules := []importRule{{imported: fixture + "/b", forbidden: []string{fixture + "/a"}}}
		want := []string{fixture + "/a imports " + fixture + "/b, forbidden in [" + fixture + "/a]"}
		if got := importViolations(pkgs, fixture, rules); !slices.Equal(got, want) {
			t.Errorf("violations = %q, want %q", got, want)
		}
		rules[0].forbidden = []string{fixture + "/c/..."}
		if got := importViolations(pkgs, fixture, rules); len(got) != 0 {
			t.Errorf("another importer forbidden: violations = %q, want none", got)
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
	t.Run("release row", func(t *testing.T) {
		rule := releaseRule(fixture)
		got := importViolations(pkgs, fixture, []importRule{rule})
		want := []string{fmt.Sprintf("%s/internal/other imports %s/internal/release, allowed only in %v", fixture, fixture, rule.allowed)}
		if !slices.Equal(got, want) {
			t.Errorf("violations = %q, want %q", got, want)
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
// importer and imported path, test variants folded into their package, so a
// package's own tests may import it.
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
				if imp == importer || !matchPath(r.imported, imp) {
					continue
				}
				v := r.violation(importer, imp)
				if v != "" && !slices.Contains(out, v) {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// violation describes importer's import of imp if r refuses it, else "".
func (r importRule) violation(importer, imp string) string {
	matches := func(p string) bool { return matchPath(p, importer) }
	if len(r.forbidden) > 0 {
		if slices.ContainsFunc(r.forbidden, matches) {
			return fmt.Sprintf("%s imports %s, forbidden in %v", importer, imp, r.forbidden)
		}
		return ""
	}
	if slices.ContainsFunc(r.allowed, matches) {
		return ""
	}
	return fmt.Sprintf("%s imports %s, allowed only in %v", importer, imp, r.allowed)
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

// basePackage folds a test variant ("p [p.test]", "p_test [p.test]") and
// the generated test main ("p.test") into p.
func basePackage(importPath string) string {
	path, _, _ := strings.Cut(importPath, " ")
	return strings.TrimSuffix(strings.TrimSuffix(path, ".test"), "_test")
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
