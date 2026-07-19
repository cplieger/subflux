// Command bundle builds subflux's browser client: it bundles the two page
// entrypoints (app.ts, login.ts) with esbuild (a Go library — no Node, no
// npm, per the fleet's no-Node-in-the-builder doctrine), assembles the CSS
// bundles from the manifest files, copies @cplieger/ui-primitives' base
// stylesheet, and writes precompressed .gz siblings for every emitted text
// asset.
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
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

const (
	srcDir = "internal/server/static-src"
	outDir = "internal/server/static"
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
	if err := cleanOutputs(); err != nil {
		return err
	}
	if err := bundleEntries(); err != nil {
		return err
	}
	if err := buildCSS(); err != nil {
		return err
	}
	return gzipOutputs()
}

// cleanOutputs removes previous build artifacts from static/ so stale
// modules from older builds (or the pre-bundler tsc-emit layout: loose
// per-module .js files, vendor/, wire/, lib/) never linger into the embed.
// Committed assets (index.html, login.html, favicon.svg, icons/) are
// untouched: only the patterns the bundle owns are removed.
func cleanOutputs() error {
	for _, dir := range []string{"chunks", "vendor", "wire", "lib", "actions"} {
		if err := os.RemoveAll(filepath.Join(outDir, dir)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(outDir)
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
			if err := os.Remove(filepath.Join(outDir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// bundleEntries bundles both page entrypoints in one esbuild call as ESM
// with code splitting: modules both pages share (the @cplieger libs, the
// wire decoders, api-client) become hashed chunks under /chunks/ that the
// browser caches across the login → app transition. The entries keep their
// stable /app.js and /login.js names — the committed HTML never needs
// rewriting; cache correctness comes from the server's ETag revalidation
// (assets are no-cache, HTML is no-store).
func bundleEntries() error {
	result := api.Build(api.BuildOptions{
		EntryPoints: []string{
			filepath.Join(srcDir, "app.ts"),
			filepath.Join(srcDir, "login.ts"),
		},
		Outdir:            outDir,
		Bundle:            true,
		Format:            api.FormatESModule,
		Splitting:         true,
		EntryNames:        "[name]",
		ChunkNames:        "chunks/[name]-[hash]",
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		Sourcemap:         api.SourceMapLinked,
		Charset:           api.CharsetUTF8,
		LogLevel:          api.LogLevelWarning,
		Write:             true,
	})
	if len(result.Errors) > 0 {
		msgs := api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage, Color: false})
		return fmt.Errorf("bundle failed:\n%s", strings.Join(msgs, "\n"))
	}
	return nil
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

// gzipOutputs writes a maximum-compression .gz sibling for every emitted
// .js/.css/.map file (recursively, so chunks/ is covered). The server
// serves the sibling to Accept-Encoding: gzip clients; a bundle this size
// compresses ~3-4x, and precompressing at build beats per-request
// compression on every axis (CPU, latency, simplicity).
func gzipOutputs() error {
	return filepath.WalkDir(outDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".js") && !strings.HasSuffix(p, ".css") && !strings.HasSuffix(p, ".map") {
			return nil
		}
		return gzipFile(p)
	})
}

// gzipFile writes a BestCompression .gz sibling next to p.
func gzipFile(p string) error {
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	f, err := os.Create(p + ".gz")
	if err != nil {
		return err
	}
	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		f.Close()
		return err
	}
	if _, err := zw.Write(data); err != nil {
		zw.Close()
		f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
