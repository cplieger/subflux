package confighandlers

import (
	"bytes"
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/cplieger/atomicfile/v2"
)

// --- Secret management ---

// secretKeyNames lists YAML keys that typically contain secrets.
var secretKeyNames = []string{"api_key", "password", "passkey", "token", "secret", "client_key", "anidb_client_key", "client_secret"}

// SecretKeyNames returns the list of YAML keys treated as secrets.
// Exported for cross-package testing (providers_test.go verifies coverage).
func SecretKeyNames() []string { return secretKeyNames }

// secretKeyRe matches YAML keys that typically contain secrets.
var secretKeyRe = regexp.MustCompile(
	`(?im)^(\s*(?:` + strings.Join(secretKeyNames, "|") + `)\s*:\s*)(.+)$`)

// FindClosingQuote returns the index of the closing quote character in val.
func FindClosingQuote(val []byte, q byte) int {
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

// StripYAMLComment removes an inline YAML comment from a value.
func StripYAMLComment(val []byte) []byte {
	if len(val) == 0 {
		return val
	}
	if val[0] == '"' || val[0] == '\'' {
		ci := FindClosingQuote(val, val[0])
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

// RedactSecrets replaces secret values in YAML config with a placeholder.
func RedactSecrets(data []byte) []byte {
	return secretKeyRe.ReplaceAllFunc(data, func(match []byte) []byte {
		subs := secretKeyRe.FindSubmatch(match)
		if len(subs) < 3 {
			return match
		}
		val := bytes.TrimSpace(subs[2])
		val = StripYAMLComment(val)
		if len(val) == 0 || string(val) == `""` || string(val) == `''` {
			return match
		}
		return append(subs[1], []byte(`"********"`)...)
	})
}

// MergeSecrets fills empty secret values in newData from the existing config file.
func MergeSecrets(newData []byte, configPath string) []byte {
	existing, err := atomicfile.ReadBounded(context.Background(), configPath, 1<<20)
	if err != nil {
		return newData
	}

	oldSecrets := ExtractSecretValues(existing)
	if len(oldSecrets) == 0 {
		return newData
	}

	lines := bytes.Split(newData, []byte("\n"))
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		for _, key := range secretKeyNames {
			prefix := []byte(key + ": ")
			if !bytes.HasPrefix(trimmed, prefix) {
				continue
			}
			val := bytes.TrimSpace(trimmed[len(prefix):])
			stripped := bytes.Trim(val, `"'`)
			if len(stripped) != 0 && !IsRedactedPlaceholder(stripped) {
				break
			}
			ctxKey := SecretContextKey(lines, i, key)
			if oldVal, ok := oldSecrets[ctxKey]; ok {
				indent := len(line) - len(bytes.TrimLeft(line, " "))
				lines[i] = append(
					bytes.Repeat([]byte(" "), indent),
					[]byte(key+": "+oldVal)...)
			}
			break
		}
	}
	return bytes.Join(lines, []byte("\n"))
}

// ExtractSecretValues scans YAML lines and returns a map of context-qualified
// secret keys to their raw values.
func ExtractSecretValues(data []byte) map[string]string {
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
			val = string(StripYAMLComment([]byte(val)))
			stripped := strings.Trim(val, `"'`)
			if stripped == "" {
				break
			}
			ctxKey := SecretContextKey(lines, i, key)
			secrets[ctxKey] = val
			break
		}
	}
	return secrets
}

// IsRedactedPlaceholder returns true if the value is a redaction placeholder.
func IsRedactedPlaceholder(val []byte) bool {
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

// SecretContextKey builds a dot-separated path from parent YAML keys.
func SecretContextKey(lines [][]byte, lineIdx int, key string) string {
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
