// Package server — Content-Security-Policy construction.
//
// The CSP is computed once at startup from the embedded HTML entrypoints
// rather than hardcoded, so the inline <script type="importmap"> block is
// always allowed by an exact sha256 hash. Under default-src 'self' an
// inline script has no exception, so without a matching script-src hash
// the browser blocks the import map; every bare-specifier import
// (@cplieger/actions, @cplieger/reactive) then fails to resolve and the
// page never boots — exactly what breaks the login / first-boot wizard.
// Deriving the hash from the embedded file means an importmap edit (even
// a whitespace-only reformat) needs no hand-updated constant.
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
// placeholder for the inline-importmap sha256 token(s).
//
//	script-src 'self' <hash>    external app/theme modules + the hashed importmap
//	style-src  'unsafe-inline'  theme tokens + dynamic badge/coverage fills
//	media-src  'self' blob:     MSE video preview (URL.createObjectURL(MediaSource))
const cspTemplate = "default-src 'self'; " +
	"script-src 'self' %s; " +
	"style-src 'self' 'unsafe-inline'; " +
	"media-src 'self' blob:"

// importMapRe captures the content between the inline importmap script
// tags. (?s) so the JSON body may span multiple lines.
var importMapRe = regexp.MustCompile(`(?s)<script type="importmap">(.*?)</script>`)

// cspImportMapFiles are the embedded HTML entrypoints whose inline
// importmaps the CSP must allow. Both currently ship the identical map;
// hashing each independently keeps the policy correct if they diverge.
var cspImportMapFiles = []string{indexHTML, loginHTML}

// importMapHashes returns the unique CSP-quoted sha256 tokens for the
// inline importmap blocks of the named files, in first-seen order. It
// errors if any file is missing an importmap (an authoring/build bug).
func importMapHashes(staticFS fs.FS, files []string) (string, error) {
	seen := make(map[string]struct{}, len(files))
	tokens := make([]string, 0, len(files))
	for _, name := range files {
		html, err := fs.ReadFile(staticFS, name)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		m := importMapRe.FindSubmatch(html)
		if m == nil {
			return "", fmt.Errorf("no inline importmap script in %s", name)
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

// buildCSPPolicy assembles the full CSP from the embedded HTML
// entrypoints' inline importmaps. Called once at startup.
func buildCSPPolicy(staticFS fs.FS) (string, error) {
	hashes, err := importMapHashes(staticFS, cspImportMapFiles)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(cspTemplate, hashes), nil
}

// mustBuildCSPPolicy is buildCSPPolicy-or-panic. The importmap is an
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
// from the embedded HTML. securityHeaders sets it on every response.
var cspPolicy = mustBuildCSPPolicy(staticSub)
