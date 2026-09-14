package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/evanw/esbuild/pkg/api"
)

// Anything bundleOptions writes into the output directory is embedded by the
// server's go:embed directive and served by handleUI's public asset branch, so
// a sourcemap in
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

	result := api.Build(bundleOptions([]string{entry}, out, ""))
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

// The worker bundle is served from the same embedded tree, so the same
// sourcemap rule binds it.
func TestWorkerOptionsEmitNoSourcemap(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()
	writeWorkerFixture(t, src, "1")

	url, err := bundleWorker(src, out)
	if err != nil {
		t.Fatalf("Setup: bundleWorker(%q, %q): %v", src, out, err)
	}
	bundle, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(url)))
	if err != nil {
		t.Fatalf("Setup: bundleWorker wrote no %s: %v", url, err)
	}
	if strings.Contains(string(bundle), "sourceMappingURL") {
		t.Errorf("%s references a sourcemap, want none; got tail %q", url, tail(string(bundle), 120))
	}
	names := chunkNames(t, out)
	for _, name := range names {
		if strings.HasSuffix(name, ".map") {
			t.Errorf("bundleWorker emitted %q; a sourcemap in the embedded tree is served unauthenticated", name)
		}
	}
}

// The worker's content hash is its identity: the page bundle names exactly
// the chunk the worker build wrote, and a changed worker source yields a new
// name with the old one gone, so no tab can attach to a stale worker after
// a redeploy.
func TestWorkerURL_isContentHashedAndInjectedIntoThePage(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()
	writeWorkerFixture(t, src, "1")
	for _, page := range []string{"app.ts", "login.ts"} {
		body := "console.log(" + workerURLDefine + ");\n"
		if err := os.WriteFile(filepath.Join(src, page), []byte(body), 0o600); err != nil {
			t.Fatalf("Setup: writing %s: %v", page, err)
		}
	}

	first, err := bundleWorker(src, out)
	if err != nil {
		t.Fatalf("bundleWorker(%q, %q): %v", src, out, err)
	}
	if !strings.HasPrefix(first, "/chunks/sse-worker-") || !strings.HasSuffix(first, ".js") {
		t.Fatalf("bundleWorker() = %q, want /chunks/sse-worker-<hash>.js", first)
	}
	if got := chunkNames(t, out); len(got) != 1 || got[0] != filepath.Base(first) {
		t.Errorf("chunks/ after bundleWorker = %q, want exactly [%q]", got, filepath.Base(first))
	}
	// The IIFE carries the injected API paths, not the identifiers.
	worker, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(first)))
	if err != nil {
		t.Fatalf("reading %s: %v", first, err)
	}
	if strings.Contains(string(worker), "__PATH_EVENTS") || !strings.Contains(string(worker), `"/api/events/alive"`) {
		t.Errorf("%s does not carry the injected API paths; got tail %q", first, tail(string(worker), 200))
	}

	if err := bundleEntries(src, out, first); err != nil {
		t.Fatalf("bundleEntries(%q, %q, %q): %v", src, out, first, err)
	}
	app, err := os.ReadFile(filepath.Join(out, "app.js"))
	if err != nil {
		t.Fatalf("bundleEntries wrote no app.js: %v", err)
	}
	if !strings.Contains(string(app), first) {
		t.Errorf("app.js does not name the worker URL %q; got %q", first, string(app))
	}
	if strings.Contains(string(app), workerURLDefine) {
		t.Errorf("app.js still carries the %s identifier; got %q", workerURLDefine, string(app))
	}

	// A one-byte change to the worker source, built the way run() builds:
	// clean, then bundle.
	writeWorkerFixture(t, src, "2")
	if err := cleanOutputs(out); err != nil {
		t.Fatalf("cleanOutputs(%q): %v", out, err)
	}
	second, err := bundleWorker(src, out)
	if err != nil {
		t.Fatalf("bundleWorker(%q, %q) after the source change: %v", src, out, err)
	}
	if second == first {
		t.Errorf("bundleWorker() after a source change = %q, same as before; the URL is not content-hashed", second)
	}
	if got := chunkNames(t, out); len(got) != 1 || got[0] != filepath.Base(second) {
		t.Errorf("chunks/ after the rebuild = %q, want exactly [%q]", got, filepath.Base(second))
	}
}

// run() passes the repo-relative constants, and esbuild reports absolute
// output paths, so the served URL has to come out the same either way.
func TestBundleWorker_servesTheSameURLFromARelativeOutdir(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("src", 0o750); err != nil {
		t.Fatalf("Setup: mkdir src: %v", err)
	}
	writeWorkerFixture(t, "src", "1")

	url, err := bundleWorker("src", "out")
	if err != nil {
		t.Fatalf("bundleWorker(%q, %q): %v", "src", "out", err)
	}
	if !strings.HasPrefix(url, "/chunks/sse-worker-") || !strings.HasSuffix(url, ".js") {
		t.Errorf("bundleWorker(relative outdir) = %q, want /chunks/sse-worker-<hash>.js", url)
	}
	if _, err := os.Stat(filepath.Join("out", filepath.FromSlash(url))); err != nil {
		t.Errorf("bundleWorker wrote no %s under the relative outdir: %v", url, err)
	}
}

// writeWorkerFixture writes a minimal worker source whose body carries
// `marker`, so two fixtures differ by content and hash differently.
func writeWorkerFixture(t *testing.T, src, marker string) {
	t.Helper()
	body := "declare const __PATH_EVENTS__: string;\ndeclare const __PATH_EVENTS_ALIVE__: string;\n" +
		"console.log(__PATH_EVENTS__, __PATH_EVENTS_ALIVE__, " + strconv.Quote(marker) + ");\n"
	if err := os.WriteFile(filepath.Join(src, workerEntry), []byte(body), 0o600); err != nil {
		t.Fatalf("Setup: writing the worker fixture: %v", err)
	}
}

// chunkNames lists outdir/chunks, empty when the directory is absent.
func chunkNames(t *testing.T, outdir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(outdir, "chunks"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading %s/chunks: %v", outdir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
