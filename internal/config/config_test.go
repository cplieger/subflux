package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Test helpers ---

// minScanDelay is the minimum valid ScanDelay for test configs that go through validate().
var minScanDelay = Duration{D: 5 * time.Second}

func minimalValidYAML() string {
	return `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  opensubtitles:
    enabled: true
    settings:
      api_key: "test"
`
}

// writeConfig writes YAML data to a temp file and returns the path.
func writeConfig(t *testing.T, data string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	return path
}
