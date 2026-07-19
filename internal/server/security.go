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
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
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

// themeInitRe captures the content of the inline anti-FOUC theme-init script
// (marked with the data-theme-init attribute). Its body is the verbatim output
// of @cplieger/ui-primitives' themeInitSnippet("subflux-theme"); hashing it lets
// script-src stay 'self' + specific hashes without 'unsafe-inline'. The
// theme-snippet drift-guard test pins the inline bytes to the library output, so
// an upstream snippet change fails the test rather than silently breaking the CSP.
var themeInitRe = regexp.MustCompile(`(?s)<script data-theme-init>(.*?)</script>`)

// cspInlineScriptFiles are the embedded HTML entrypoints whose inline <head>
// scripts the CSP must allow. Both currently ship the identical theme-init
// snippet; hashing each independently keeps the policy correct if they
// diverge.
var cspInlineScriptFiles = []string{indexHTML, loginHTML}

// inlineScriptHashes returns the unique CSP-quoted sha256 tokens for the inline
// <script> blocks matched by re (capturing the script body in group 1) across
// the named files, in first-seen order. It errors if any file is missing the
// block (an authoring/build bug), so startup fails loudly rather than serving a
// CSP that would silently block a required inline script.
func inlineScriptHashes(staticFS fs.FS, files []string, re *regexp.Regexp, what string) (string, error) {
	seen := make(map[string]struct{}, len(files))
	tokens := make([]string, 0, len(files))
	for _, name := range files {
		html, err := fs.ReadFile(staticFS, name)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		m := re.FindSubmatch(html)
		if m == nil {
			return "", fmt.Errorf("no inline %s script in %s", what, name)
		}
		sum := sha256.Sum256(m[1])
		token := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
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
	themeInit, err := inlineScriptHashes(staticFS, cspInlineScriptFiles, themeInitRe, "theme-init")
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
