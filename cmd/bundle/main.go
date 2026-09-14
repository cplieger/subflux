// Command bundle builds subflux's browser client: it bundles the SSE worker
// and the two page entrypoints (app.ts, login.ts) with esbuild (a Go library
// — no Node, no npm, per the fleet's no-Node-in-the-builder doctrine),
// assembles the CSS bundles from the manifest files, copies
// @cplieger/ui-primitives' base stylesheet. (Precompressed .gz siblings are
// no longer emitted: the server's webhttp.StaticHandler gzips the embedded
// originals at construction, at the same BestCompression level.)
//
// It replaces the previous tsc-emit pipeline (per-module JS served over the
// pages' importmaps: dozens of uncached module fetches per page load) with
// cacheable artifacts: /app.js and /login.js plus hashed shared chunks under
// /chunks/ (code both pages import — the @cplieger libs, the wire decoders —
// is split out once and cached across the login → app transition). tsc
// remains the TYPE gate (run with --noEmit in CI and the Docker build);
// esbuild does not typecheck.
//
// Usage: go run ./cmd/bundle   (from the repo root; also run by the
// Dockerfile builder stage). Inputs are internal/server/static-src/ plus the
// library sources under its node_modules/ (npm install locally; registry
// tarballs in the Docker build). Outputs land in internal/server/static/,
// which go:embed ships.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/apipaths"
	"github.com/evanw/esbuild/pkg/api"
)

const (
	srcDir = "internal/server/static-src"
	outDir = "internal/server/static"

	// workerEntry is the SharedWorker script; workerURLDefine is the page-side
	// identifier its hashed URL replaces (declared in static-src/globals.d.ts).
	workerEntry     = "sse-worker.ts"
	workerURLDefine = "__SSE_WORKER_URL__"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bundle:", err)
		os.Exit(1)
	}
}

func run() error {
	if _, err := os.Stat(filepath.Join(srcDir, "app.ts")); err != nil {
		return fmt.Errorf("run from the repo root: %w", err)
	}
	if err := cleanOutputs(outDir); err != nil {
		return err
	}
	// The worker builds first: its content-hashed URL is a page-bundle input.
	workerURL, err := bundleWorker(srcDir, outDir)
	if err != nil {
		return err
	}
	if err := bundleEntries(srcDir, outDir, workerURL); err != nil {
		return err
	}
	return buildCSS()
}

// cleanOutputs removes previous build artifacts from outdir so stale
// modules from older builds (or the pre-bundler tsc-emit layout: loose
// per-module .js files, vendor/, wire/, lib/) never linger into the embed.
// Committed assets (index.html, login.html, favicon.svg, icons/) are
// untouched: only the patterns the bundle owns are removed. The .map pattern
// stays load-bearing even though bundleOptions emits none: it is what keeps a
// sourcemap left by an older build out of the embed.
func cleanOutputs(outdir string) error {
	for _, dir := range []string{"chunks", "vendor", "wire", "lib", "actions"} {
		if err := os.RemoveAll(filepath.Join(outdir, dir)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(outdir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".css") ||
			strings.HasSuffix(name, ".map") || strings.HasSuffix(name, ".gz") {
			if err := os.Remove(filepath.Join(outdir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// bundleWorker bundles the SharedWorker script as a self-contained IIFE
// under outdir/chunks/ and returns its served URL (/chunks/sse-worker-<hash>.js).
// The hash is the worker's identity: a tab constructs the worker by URL, so a
// redeploy's tabs cannot attach to a stale worker still running the old
// bundle, and cleanOutputs removes the previous hash with the directory.
func bundleWorker(srcdir, outdir string) (string, error) {
	result := api.Build(workerOptions(filepath.Join(srcdir, workerEntry), outdir))
	if len(result.Errors) > 0 {
		msgs := api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage, Color: false})
		return "", fmt.Errorf("worker bundle failed:\n%s", strings.Join(msgs, "\n"))
	}
	if len(result.OutputFiles) != 1 {
		return "", fmt.Errorf("worker bundle wrote %d files, want 1", len(result.OutputFiles))
	}
	out := result.OutputFiles[0]
	if err := os.MkdirAll(filepath.Dir(out.Path), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(out.Path, out.Contents, 0o600); err != nil {
		return "", err
	}
	// esbuild reports absolute output paths; outdir is relative when run()
	// passes the repo-relative constant.
	absOut, err := filepath.Abs(outdir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absOut, out.Path)
	if err != nil {
		return "", err
	}
	return "/" + filepath.ToSlash(rel), nil
}

// workerOptions is the esbuild configuration bundleWorker runs. The API paths
// the worker fetches are injected from the generated Go constants, because
// the generated TS client imports DOM-bound modules the worker cannot load.
// Write is off so the emitted path can be read back from OutputFiles.
func workerOptions(entry, outdir string) api.BuildOptions {
	opts := baseOptions(outdir)
	opts.EntryPoints = []string{entry}
	opts.Format = api.FormatIIFE
	opts.EntryNames = "chunks/[name]-[hash]"
	opts.Define = map[string]string{
		"__PATH_EVENTS__":       strconv.Quote(apipaths.PathEvents),
		"__PATH_EVENTS_ALIVE__": strconv.Quote(apipaths.PathEventsAlive),
	}
	opts.Write = false
	return opts
}

// bundleEntries bundles both page entrypoints in one esbuild call as ESM
// with code splitting: modules both pages share (the @cplieger libs, the
// wire decoders, api-client) become hashed chunks under /chunks/ that the
// browser caches across the login → app transition. The entries keep their
// stable /app.js and /login.js names — the committed HTML never needs
// rewriting; cache correctness comes from the server's ETag revalidation
// (assets are no-cache, HTML is no-store).
func bundleEntries(srcdir, outdir, workerURL string) error {
	result := api.Build(bundleOptions([]string{
		filepath.Join(srcdir, "app.ts"),
		filepath.Join(srcdir, "login.ts"),
	}, outdir, workerURL))
	if len(result.Errors) > 0 {
		msgs := api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage, Color: false})
		return fmt.Errorf("bundle failed:\n%s", strings.Join(msgs, "\n"))
	}
	return nil
}

// bundleOptions is the esbuild configuration bundleEntries runs, taken as
// arguments so a test can drive the same option set over a fixture tree.
// workerURL replaces the page's __SSE_WORKER_URL__ identifier; empty leaves
// it undefined, for a fixture that never spawns the worker.
func bundleOptions(entryPoints []string, outdir, workerURL string) api.BuildOptions {
	opts := baseOptions(outdir)
	opts.EntryPoints = entryPoints
	opts.Format = api.FormatESModule
	opts.Splitting = true
	opts.EntryNames = "[name]"
	opts.ChunkNames = "chunks/[name]-[hash]"
	if workerURL != "" {
		opts.Define = map[string]string{workerURLDefine: strconv.Quote(workerURL)}
	}
	return opts
}

// baseOptions is what every bundle shares: minification, UTF-8 output and
// no sourcemap.
func baseOptions(outdir string) api.BuildOptions {
	return api.BuildOptions{
		Outdir:            outdir,
		Bundle:            true,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		// No sourcemap: go:embed ships everything this writes into static/ and
		// the server serves those assets to unauthenticated callers, so a map
		// would publish the TypeScript sources to anyone who can reach the
		// port and carry them in every binary and image.
		Sourcemap: api.SourceMapNone,
		Charset:   api.CharsetUTF8,
		LogLevel:  api.LogLevelWarning,
		Write:     true,
	}
}

// buildCSS assembles the served stylesheets exactly as the former
// Dockerfile shell loop did: static-src/css/MANIFEST concatenates to
// style.css and login.MANIFEST to login.css (both include
// _shared-tokens.css so the two pages stay visually consistent). The
// @cplieger/ui-primitives base stylesheet stays a standalone file served at
// /ui-primitives.css via its own <link> (loaded before the page bundle so
// the 16-uip-skin.css split layers on top) — copied here from the package
// source, cached once, shared by both pages.
func buildCSS() error {
	cssDir := filepath.Join(srcDir, "css")
	if err := concatCSS(filepath.Join(cssDir, "MANIFEST"), cssDir, "style.css"); err != nil {
		return err
	}
	if err := concatCSS(filepath.Join(cssDir, "login.MANIFEST"), cssDir, "login.css"); err != nil {
		return err
	}
	uip, err := os.ReadFile(filepath.Join(srcDir, "node_modules", "@cplieger", "ui-primitives", "css", "ui-primitives.css"))
	if err != nil {
		return fmt.Errorf("ui-primitives.css: %w", err)
	}
	//nolint:gosec // G703 false positive: both path components are compile-time constants.
	return os.WriteFile(filepath.Join(outDir, "ui-primitives.css"), uip, 0o600)
}

// concatCSS writes outDir/<outName> from the manifest's ordered file list
// (paths relative to baseDir; blank lines and #-comments skipped).
// Library-before-consumer source order is the override mechanism — see the
// manifest headers.
func concatCSS(manifestPath, baseDir, outName string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("css manifest: %w", err)
	}
	var out strings.Builder
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		part, err := os.ReadFile(filepath.Join(baseDir, line))
		if err != nil {
			return fmt.Errorf("css part: %w", err)
		}
		out.Write(part)
	}
	return os.WriteFile(filepath.Join(outDir, outName), []byte(out.String()), 0o600)
}
