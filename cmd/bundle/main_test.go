package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanw/esbuild/pkg/api"
)

// Anything bundleOptions writes into the output directory is embedded by
// go:embed and served by handleUI's public asset branch, so a sourcemap in
// there is a published artifact: it hands the TypeScript sources to any
// unauthenticated caller and rides along in every binary and image. Drive the
// real option set over a fixture entrypoint and assert both halves of the
// linked-map shape are absent - the .map file, and the comment that points a
// browser at it.
func TestBundleOptionsEmitNoSourcemap(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()
	entry := filepath.Join(src, "probe.ts")
	if err := os.WriteFile(entry, []byte("export const probe: number = 1;\nconsole.log(probe);\n"), 0o600); err != nil {
		t.Fatalf("Setup: writing the fixture entrypoint: %v", err)
	}

	result := api.Build(bundleOptions([]string{entry}, out))
	if len(result.Errors) > 0 {
		msgs := api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage, Color: false})
		t.Fatalf("Setup: api.Build(bundleOptions(%q, %q)) failed: %s", entry, out, strings.Join(msgs, "\n"))
	}

	// Precondition: the build really produced the entry bundle. An empty output
	// directory would satisfy every assertion below for the wrong reason.
	bundle, err := os.ReadFile(filepath.Join(out, "probe.js"))
	if err != nil {
		t.Fatalf("Setup: api.Build(bundleOptions(...)) wrote no probe.js: %v", err)
	}
	if len(bundle) == 0 {
		t.Fatal("Setup: api.Build(bundleOptions(...)) wrote an empty probe.js")
	}

	emitted, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("Setup: reading the output directory: %v", err)
	}
	for _, e := range emitted {
		if strings.HasSuffix(e.Name(), ".map") {
			t.Errorf("api.Build(bundleOptions(...)) emitted %q; a sourcemap in the embedded tree is served unauthenticated", e.Name())
		}
	}
	if strings.Contains(string(bundle), "sourceMappingURL") {
		t.Errorf("probe.js references a sourcemap, want none; got tail %q", tail(string(bundle), 120))
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
