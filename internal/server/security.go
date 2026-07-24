// Package server — Content-Security-Policy construction.
//
// The CSP is computed once at startup from the embedded HTML entrypoints
// rather than hardcoded, so the inline <head> script — the anti-FOUC
// <script data-theme-init> IIFE (@cplieger/ui-primitives' themeInitSnippet
// output) — is allowed by an exact sha256 hash. Under default-src 'self' an
// inline script has no exception, so without a matching script-src hash the
// browser blocks it and the page flashes the wrong theme before the
// stylesheets load. Deriving the hash from the embedded file means an edit
// (even a whitespace-only reformat) needs no hand-updated constant. (The
// former second inline script, the importmap, is gone: cmd/bundle resolves
// every bare specifier at build time.)
package server

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/cplieger/webhttp"
)

// cspTemplate is the policy applied to every response, with a single %s
// placeholder for the space-joined sha256 tokens of the inline <head>
// theme-init scripts.
//
//	script-src 'self' <hashes>  the bundled entry modules + the hashed inline
//	                            anti-FOUC theme-init scripts
//	style-src  'unsafe-inline'  theme tokens + dynamic badge/coverage fills
//	media-src  'self' blob:     MSE video preview (URL.createObjectURL(MediaSource))
const cspTemplate = "default-src 'self'; " +
	"script-src 'self' %s; " +
	"style-src 'self' 'unsafe-inline'; " +
	"media-src 'self' blob:"

// cspInlineScriptFiles are the embedded HTML entrypoints whose inline <head>
// scripts the CSP must allow. Both currently ship the identical theme-init
// snippet; hashing each independently keeps the policy correct if they
// diverge.
var cspInlineScriptFiles = []string{indexHTML, loginHTML}

// inlineScriptHashes returns the unique CSP-quoted sha256 tokens for the
// inline <script> blocks across the named files, in first-seen order, hashed
// via webhttp.InlineScriptHashes (byte-precise, quote-aware — the exact bytes
// a browser hashes for a script-src token). Each entrypoint carries exactly
// ONE inline script (the anti-FOUC theme-init, pinned to the
// @cplieger/ui-primitives snippet by the drift-guard test); the exactly-one
// assertion preserves the old targeted extraction's strictness — zero means
// the required block is missing (an authoring/build bug: fail startup rather
// than serve a CSP that would block it), and more than one means an
// unreviewed inline script was added (update this check consciously instead
// of silently granting it CSP allowance).
func inlineScriptHashes(staticFS fs.FS, files []string) (string, error) {
	seen := make(map[string]struct{}, len(files))
	tokens := make([]string, 0, len(files))
	for _, name := range files {
		html, err := fs.ReadFile(staticFS, name)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		hashes := webhttp.InlineScriptHashes(html)
		if len(hashes) != 1 {
			return "", fmt.Errorf("expected exactly one inline script in %s, found %d (a new inline script must be reviewed and this check updated)", name, len(hashes))
		}
		token := hashes[0]
		if _, dup := seen[token]; dup {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return strings.Join(tokens, " "), nil
}

// buildCSPPolicy assembles the full CSP from the embedded HTML entrypoints'
// inline anti-FOUC theme-init scripts. Called once at startup.
func buildCSPPolicy(staticFS fs.FS) (string, error) {
	themeInit, err := inlineScriptHashes(staticFS, cspInlineScriptFiles)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(cspTemplate, themeInit), nil
}

// mustBuildCSPPolicy is buildCSPPolicy-or-panic. The theme-init snippet is an
// embedded compile-time constant, so a failure is a build error that must
// surface immediately (mirrors mustSub for the embed itself).
func mustBuildCSPPolicy(staticFS fs.FS) string {
	policy, err := buildCSPPolicy(staticFS)
	if err != nil {
		panic("server: build CSP: " + err.Error())
	}
	return policy
}

// cspPolicy is the Content-Security-Policy header value, computed once
// from the embedded HTML. securityHeadersMW passes it via webhttp.WithCSP so it
// is set on every response.
var cspPolicy = mustBuildCSPPolicy(staticSub)
