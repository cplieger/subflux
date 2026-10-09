package confighandlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"

	"github.com/cplieger/atomicfile/v4"
)

// --- Secret management ---

// secretKeyNames lists YAML keys that typically contain secrets.
var secretKeyNames = []string{"api_key", "password", "passkey", "token", "secret", "client_key", "anidb_client_key", "client_secret"}

// SecretKeyNames returns the list of YAML keys treated as secrets. The
// provider registry test asserts every secret provider field has an entry.
//
//deadset:ignore DS1004 -- The provider registry test reads it to fail when a new secret provider field is missing from the redaction list.
func SecretKeyNames() []string { return secretKeyNames }

// secretKeyRe matches YAML keys that typically contain secrets.
var secretKeyRe = regexp.MustCompile(
	`(?im)^(\s*(?:` + strings.Join(secretKeyNames, "|") + `)\s*:\s*)(.+)$`,
)

// findClosingQuote returns the index of the closing quote character in val.
func findClosingQuote(val []byte, q byte) int {
	for i := 1; i < len(val); i++ {
		if val[i] == '\\' && q == '"' {
			i++
			continue
		}
		if val[i] == q {
			return i
		}
	}
	return -1
}

// stripYAMLComment removes an inline YAML comment from a value.
func stripYAMLComment(val []byte) []byte {
	if len(val) == 0 {
		return val
	}
	if val[0] == '"' || val[0] == '\'' {
		ci := findClosingQuote(val, val[0])
		if ci < 0 {
			return val
		}
		rest := val[ci+1:]
		if idx := bytes.Index(rest, []byte(" #")); idx >= 0 {
			return bytes.TrimSpace(val[:ci+1+idx])
		}
		return val
	}
	if before, _, found := bytes.Cut(val, []byte(" #")); found {
		return bytes.TrimSpace(before)
	}
	return val
}

// redactSecrets replaces secret values in YAML config with a placeholder.
func redactSecrets(data []byte) []byte {
	return secretKeyRe.ReplaceAllFunc(data, func(match []byte) []byte {
		subs := secretKeyRe.FindSubmatch(match)
		if len(subs) < 3 {
			return match
		}
		val := bytes.TrimSpace(subs[2])
		val = stripYAMLComment(val)
		if len(val) == 0 || string(val) == `""` || string(val) == `''` {
			return match
		}
		return append(subs[1], []byte(`"********"`)...)
	})
}

// mergeSecrets fills empty secret values in newData from the existing config
// file. An empty or redacted incoming secret means "keep what I have", which
// makes the baseline's readability a correctness input (the raw-path twin of
// the structured path's mergeExistingSecrets contract): when newData relies
// on keep semantics and the existing file cannot be read for any reason other
// than not existing, silently skipping the merge would persist the empty or
// placeholder value literally — deleting the secret. Those reads fail closed
// via errBaselineUnavailable — no save, no activation. A missing file is a
// true empty baseline (first save), and a payload with no keep-semantics
// secrets never needs the baseline at all — which also lets a complete
// payload overwrite, and thereby repair, an unreadable config file.
func mergeSecrets(newData []byte, configPath string) ([]byte, error) {
	existing, err := atomicfile.ReadBounded(context.Background(), configPath, 1<<20)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || !hasKeepSecretLines(newData) {
			return newData, nil
		}
		return nil, fmt.Errorf("%w: read existing config: %w", errBaselineUnavailable, err)
	}

	oldSecrets := extractSecretValues(existing)
	if len(oldSecrets) == 0 {
		return newData, nil
	}

	lines := bytes.Split(newData, []byte("\n"))
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		key, val, ok := secretLineValue(trimmed)
		if !ok {
			continue
		}
		stripped := bytes.Trim(val, `"'`)
		if len(stripped) != 0 && !isRedactedPlaceholder(stripped) {
			continue
		}
		ctxKey := secretContextKey(lines, i, key)
		if oldVal, ok := oldSecrets[ctxKey]; ok {
			indent := len(line) - len(bytes.TrimLeft(line, " "))
			lines[i] = append(
				bytes.Repeat([]byte(" "), indent),
				[]byte(key+": "+oldVal)...,
			)
		}
	}
	return bytes.Join(lines, []byte("\n")), nil
}

// secretLineValue matches one trimmed YAML line against the secret key
// names: the first matching key returns with the line's raw (space-trimmed)
// value. The shared line classifier for mergeSecrets and hasKeepSecretLines.
func secretLineValue(trimmed []byte) (key string, val []byte, ok bool) {
	for _, k := range secretKeyNames {
		prefix := []byte(k + ": ")
		if bytes.HasPrefix(trimmed, prefix) {
			return k, bytes.TrimSpace(trimmed[len(prefix):]), true
		}
	}
	return "", nil, false
}

// hasKeepSecretLines reports whether newData carries at least one secret key
// line with keep semantics — an empty or redaction-placeholder value, the two
// forms mergeSecrets fills from the baseline. Only such payloads depend on
// the baseline's readability.
func hasKeepSecretLines(newData []byte) bool {
	for line := range bytes.SplitSeq(newData, []byte("\n")) {
		_, val, ok := secretLineValue(bytes.TrimSpace(line))
		if !ok {
			continue
		}
		stripped := bytes.Trim(val, `"'`)
		if len(stripped) == 0 || isRedactedPlaceholder(stripped) {
			return true
		}
	}
	return false
}

// extractSecretValues scans YAML lines and returns a map of context-qualified
// secret keys to their raw values.
func extractSecretValues(data []byte) map[string]string {
	secrets := make(map[string]string)
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		for _, key := range secretKeyNames {
			prefix := []byte(key + ": ")
			if !bytes.HasPrefix(trimmed, prefix) {
				continue
			}
			val := string(bytes.TrimSpace(trimmed[len(prefix):]))
			val = string(stripYAMLComment([]byte(val)))
			stripped := strings.Trim(val, `"'`)
			if stripped == "" {
				break
			}
			ctxKey := secretContextKey(lines, i, key)
			secrets[ctxKey] = val
			break
		}
	}
	return secrets
}

// isRedactedPlaceholder returns true if the value is a redaction placeholder.
func isRedactedPlaceholder(val []byte) bool {
	if len(val) == 0 {
		return false
	}
	allStars := true
	for _, b := range val {
		if b != '*' {
			allStars = false
			break
		}
	}
	if allStars {
		return true
	}
	return string(val) == "[REDACTED]"
}

// secretContextKey builds a dot-separated path from parent YAML keys.
func secretContextKey(lines [][]byte, lineIdx int, key string) string {
	indent := len(lines[lineIdx]) - len(bytes.TrimLeft(lines[lineIdx], " "))
	var parents []string
	for i := lineIdx - 1; i >= 0; i-- {
		li := lines[i]
		trimmed := bytes.TrimSpace(li)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}
		liIndent := len(li) - len(bytes.TrimLeft(li, " "))
		if liIndent < indent {
			colonIdx := bytes.IndexByte(trimmed, ':')
			if colonIdx > 0 {
				parents = append(parents, string(trimmed[:colonIdx]))
			}
			indent = liIndent
		}
	}
	slices.Reverse(parents)
	return strings.Join(append(parents, key), ".")
}
