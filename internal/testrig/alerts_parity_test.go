package testrig

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.yaml.in/yaml/v4"
)

// v093Rules are the alert names alerts/promql.yaml shipped at v0.9.3.
var v093Rules = []string{
	"SubfluxTargetDown", "SubfluxTargetAbsent", "SubfluxScanStalled", "SubfluxHTTP5xx",
	"SubfluxBackupStale", "SubfluxProviderCredentialsRejected", "SubfluxMediaUnwritable",
	"SubfluxMediaUnavailable",
}

// parityRow says what became of one v0.9.3 rule; exactly one of successor
// and removal is set.
type parityRow struct {
	old string
	// successor is the shipped rule that covers it.
	successor string
	// removal is why nothing replaces it.
	removal string
	// dropped is a part of the old rule its successor leaves out, and why.
	dropped string
}

// alertParity records what became of each v0.9.3 rule.
var alertParity = []parityRow{
	{old: "SubfluxTargetDown", successor: "SubfluxTargetDown"},
	{old: "SubfluxTargetAbsent", successor: "SubfluxTargetAbsent"},
	{old: "SubfluxHTTP5xx", successor: "SubfluxHTTP5xx"},
	{old: "SubfluxProviderCredentialsRejected", successor: "SubfluxProviderUnusable", dropped: "the AniDB client-key setting arm: the setting is deleted"},
	{old: "SubfluxMediaUnavailable", successor: "SubfluxMediaUnavailable"},
	{old: "SubfluxMediaUnwritable", removal: "folder level: each video's write problem is reported on its own; the root level is SubfluxMediaUnavailable"},
	{old: "SubfluxScanStalled", successor: "SubfluxScanStalled"},
	{old: "SubfluxBackupStale", successor: "SubfluxBackupStale"},
}

func shippedRules(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot, "alerts", "promql.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Groups []struct {
			Rules []struct{ Alert string }
		}
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, g := range file.Groups {
		for _, r := range g.Rules {
			names = append(names, r.Alert)
		}
	}
	return names
}

func TestAlertParityTable(t *testing.T) {
	shipped := shippedRules(t)
	var olds []string
	for _, r := range alertParity {
		olds = append(olds, r.old)
		if (r.successor == "") == (r.removal == "") {
			t.Errorf("%s: set exactly one of successor and removal", r.old)
		}
		if r.successor != "" && !slices.Contains(shipped, r.successor) {
			t.Errorf("%s: successor %s is not a rule in alerts/promql.yaml", r.old, r.successor)
		}
		if r.dropped != "" && r.successor == "" {
			t.Errorf("%s: a dropped arm needs a successor", r.old)
		}
	}
	slices.Sort(olds)
	want := slices.Sorted(slices.Values(v093Rules))
	if !slices.Equal(olds, want) {
		t.Errorf("the table lists %q, want each of v0.9.3's rules once: %q", olds, want)
	}
}
