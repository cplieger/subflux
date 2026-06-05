package scanning

import (
	"strings"
	"testing"

	"subflux/internal/api"
)

// FuzzExtractAltTitlesNoDuplicates verifies that ExtractAltTitles never
// returns duplicate titles (case-insensitive) and never includes the primary.
func FuzzExtractAltTitlesNoDuplicates(f *testing.F) {
	f.Add("Primary", "Alt One", "Alt Two", "Primary")
	f.Add("", "a", "b", "c")
	f.Add("SAME", "same", "Same", "SAME")
	f.Add("X", "", "", "")

	f.Fuzz(func(t *testing.T, primary, a1, a2, a3 string) {
		alts := []api.AlternateTitle{
			{Title: a1},
			{Title: a2},
			{Title: a3},
		}
		result := ExtractAltTitles(alts, primary)

		// Invariant 1: primary title (case-insensitive) must not be in output.
		primaryLower := strings.ToLower(primary)
		for _, title := range result {
			if strings.ToLower(title) == primaryLower {
				t.Fatalf("result contains primary %q (case-insensitive)", title)
			}
		}

		// Invariant 2: no duplicates (case-insensitive).
		seen := make(map[string]bool, len(result))
		for _, title := range result {
			lower := strings.ToLower(title)
			if seen[lower] {
				t.Fatalf("duplicate in result: %q", title)
			}
			seen[lower] = true
		}

		// Invariant 3: all output titles are non-empty.
		for _, title := range result {
			if title == "" {
				t.Fatal("empty string in result")
			}
		}
	})
}

// FuzzSceneOrPathNeverEmptyWhenInputGiven verifies that SceneOrPath returns
// a non-empty string when at least one input is non-empty.
func FuzzSceneOrPathNeverEmptyWhenInputGiven(f *testing.F) {
	f.Add("scene.name", "/path/to/file.mkv")
	f.Add("", "/path/to/file.mkv")
	f.Add("scene", "")
	f.Add("", "")

	f.Fuzz(func(t *testing.T, scene, path string) {
		result := SceneOrPath(scene, path)
		if scene != "" && result != scene {
			t.Fatalf("SceneOrPath(%q, %q) = %q, want scene", scene, path, result)
		}
		if scene == "" && result != path {
			t.Fatalf("SceneOrPath(%q, %q) = %q, want path", scene, path, result)
		}
	})
}
