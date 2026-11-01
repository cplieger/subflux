package testrig

import (
	"slices"
	"testing"
)

// retiredModules are modules no package of the build or its tests may reach.
// until names the landing that re-adds the module under the same path and
// deletes its row; "" when none does, its successor having another path.
var retiredModules = []struct{ module, until string }{
	{"github.com/cplieger/auth/v6", ""},
	{"github.com/cplieger/wiregen/v3", ""},
	{"github.com/cplieger/sse", "AP-1"},
	{"github.com/evanw/esbuild", "WC-1"},
}

func TestModulesRetired(t *testing.T) {
	reached := map[string]bool{}
	for _, p := range goList(t, absDir(t, moduleRoot), "./...") {
		if p.Module != nil {
			reached[p.Module.Path] = true
		}
	}
	for _, r := range retiredModules {
		if reached[r.module] {
			t.Errorf("%s is reached by the build or its tests; the landing allowed to re-add it: %s", r.module, untilText(r.until))
		}
	}
}

func TestRetiredUntilIsARollout(t *testing.T) {
	for _, r := range retiredModules {
		if r.until != "" && !slices.Contains(rollouts, r.until) {
			t.Errorf("%s: until %q is not a rollout id", r.module, r.until)
		}
	}
}

func untilText(until string) string {
	if until == "" {
		return "none"
	}
	return until
}
