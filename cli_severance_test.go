package main

// Severance proof (R6.3): the CLI is a pure remote client. The local
// search composition root (internal/clisearch) is gone, no CLI file
// constructs a store / provider registry / engine / config loader, and
// boltstore.Open callsites are exactly the two server-mode roots in
// main.go (runServer, runConfiguredServer).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeverance_clisearch_package_deleted(t *testing.T) {
	if _, err := os.Stat(filepath.Join("internal", "clisearch")); !os.IsNotExist(err) {
		t.Errorf("internal/clisearch still exists (err=%v); the local CLI search composition root must be deleted", err)
	}
}

// The CLI files must not import the local-execution stack: the CLI binary
// path no longer reads provider/arr/OIDC secrets or opens the database.
func TestSeverance_cli_files_import_no_local_stack(t *testing.T) {
	forbidden := []string{
		`"github.com/cplieger/subflux/internal/boltstore"`,
		`"github.com/cplieger/subflux/internal/provider"`,
		`"github.com/cplieger/subflux/internal/scorer"`,
		`"github.com/cplieger/subflux/internal/search"`,
		`"github.com/cplieger/subflux/internal/search/syncing"`,
		`"github.com/cplieger/subflux/internal/embedded"`,
		`"github.com/cplieger/subflux/internal/clisearch"`,
	}
	for _, file := range []string{"cli.go", "cli_remote.go", "cli_specs.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, imp := range forbidden {
			if strings.Contains(string(src), imp) {
				t.Errorf("%s imports %s; CLI runners must not construct the local search stack", file, imp)
			}
		}
	}
}

// boltstore.Open is a server-mode-only call: main.go's runServer and
// runConfiguredServer are the only permitted callsites in the whole tree.
func TestSeverance_boltstore_open_only_in_server_roots(t *testing.T) {
	var offenders []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// Skip VCS, frontend toolchain, and embedded/static assets.
			if name == ".git" || name == "node_modules" || name == "static" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(src), "boltstore.Open(") && path != "main.go" {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("boltstore.Open called outside main.go's server roots: %v", offenders)
	}
}
