package testrig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// rollouts are the subflux landings a retained row may wait for. Library
// landings are absent: only a subflux landing gives subflux code a caller.
var rollouts = []string{
	"LG-1", "MS-1", "SE-1", "TS-1", "ST-1", "PAS-1", "MS-2", "TS-2", "MR-1",
	"ST-2", "PA-2", "SE-2", "AP-1", "SE-3", "WC-1", "UX-1", "MS-3",
}

// refPrefix is how deadset spells a symbol of this module; rows hold the rest.
const refPrefix = "go://github.com/cplieger/subflux/"

// retained holds one row per deadset.json root pattern: code with no
// production caller yet, kept live until the landing that calls, rebuilds or
// deletes it, which drops the pattern and the row together.
var retained = []struct{ pattern, until string }{
	{"internal/cache#*", "SE-2"},
	{"internal/embedded#*", "TS-2"},
	{"internal/epmarker#*", "SE-2"},
	{"internal/formschema#Field.Help", "PA-2"},
	{"internal/formschema#Field.Label", "PA-2"},
	{"internal/formschema#Kind.MarshalText", "AP-1"},
	{"internal/formschema#Kind.UnmarshalText", "AP-1"},
	{"internal/formschema#Values.Bool", "PA-2"},
	{"internal/formschema#Values.Duration", "PA-2"},
	{"internal/formschema#Values.Int", "PA-2"},
	{"internal/formschema#Values.Secret", "PA-2"},
	{"internal/httpwire#*", "SE-2"},
	{"internal/langcode#*", "SE-2"},
	{"internal/language#Class.MarshalText", "AP-1"},
	{"internal/language#Class.UnmarshalText", "AP-1"},
	{"internal/language#Closeness.MarshalText", "AP-1"},
	{"internal/language#Closeness.UnmarshalText", "AP-1"},
	{"internal/language#EnglishName", "AP-1"},
	{"internal/language#FilenameTag", "TS-2"},
	{"internal/language#Flags.Serves", "TS-1"},
	{"internal/language#FormatVersion", "ST-1"},
	{"internal/language#FromName", "MS-2"},
	{"internal/language#ISO3", "MS-2"},
	{"internal/language#MustProviderMap", "SE-2"},
	{"internal/language#Names", "AP-1"},
	{"internal/language#NewProviderMap", "SE-2"},
	{"internal/language#Policy.AutoQuery", "SE-2"},
	{"internal/language#Policy.Automatic", "SE-2"},
	{"internal/language#Policy.CloseVarieties", "AP-1"},
	{"internal/language#Policy.Digest", "ST-1"},
	{"internal/language#Policy.Grade", "ST-1"},
	{"internal/language#Policy.ManualQuery", "SE-2"},
	{"internal/language#Policy.Targets", "MS-2"},
	{"internal/language#Policy.TargetsFor", "ST-1"},
	{"internal/language#ProviderMap.Tag", "SE-2"},
	{"internal/language#ProviderMap.Tags", "SE-2"},
	{"internal/language#ProviderMap.Wire", "SE-2"},
	{"internal/language#ReadFilename", "TS-2"},
	{"internal/language#RefineFromTitle", "TS-2"},
	{"internal/language#Tag.MarshalText", "MS-1"},
	{"internal/language#Tag.UnmarshalText", "MS-1"},
	{"internal/language#Variant.Flags", "TS-1"},
	{"internal/language#Variant.MarshalText", "AP-1"},
	{"internal/language#Variant.UnmarshalText", "AP-1"},
	{"internal/logsafe#*", "SE-2"},
	{"internal/obs#Metrics.Register", "AP-1"},
	{"internal/provider#*", "SE-2"},
	{"internal/provider/*", "SE-2"},
	{"internal/search/release#*", "SE-2"},
	{"internal/settings#Active.Languages", "ST-1"},
	{"internal/settings#Mode.UnmarshalText", "AP-1"},
	{"internal/settings#Server.AllowedHosts", "PA-2"},
	{"internal/settings#Server.Hosts", "AP-1"},
	{"internal/settings#Server.TrustedProxies", "PA-2"},
	{"internal/settings#Service.Diverged", "PAS-1"},
	{"internal/settings#Service.Merge", "AP-1"},
	{"internal/settings#Service.Save", "PA-2"},
	{"internal/settings#Snapshot.Document", "PA-2"},
	{"internal/status#*", "PAS-1"},
	{"internal/status#StartCodes", "SE-2"},
	{"internal/subflux#*", "SE-2"},
	{"internal/subsync#*", "TS-2"},
	{"internal/subsync/*", "TS-2"},
	{"internal/subtitleenc#*", "SE-1"},
	{"internal/subtitleext#*", "TS-1"},
	{"internal/subtitlefile#*", "TS-1"},
}

func TestRetainedSetMatchesDeadset(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(moduleRoot, "deadset.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Roots struct{ Patterns []string } `json:"roots"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("deadset.json: %v", err)
	}
	rows := map[string]bool{}
	for _, r := range retained {
		if rows[r.pattern] {
			t.Errorf("row %q is listed twice", r.pattern)
		}
		rows[r.pattern] = true
		if !slices.Contains(rollouts, r.until) {
			t.Errorf("row %q: until %q is not a rollout id", r.pattern, r.until)
		}
	}
	roots := map[string]bool{}
	for _, p := range cfg.Roots.Patterns {
		rel, ok := strings.CutPrefix(p, refPrefix)
		if !ok {
			t.Errorf("deadset.json root %q does not start with %s", p, refPrefix)
			continue
		}
		roots[rel] = true
		if !rows[rel] {
			t.Errorf("deadset.json root %q has no retained row naming its landing", p)
		}
	}
	for _, r := range retained {
		if !roots[r.pattern] {
			t.Errorf("retained row %q has no deadset.json root pattern", r.pattern)
		}
	}
}

// matchRef follows https://github.com/cplieger/deadset-spec/blob/main/contract/grammar/symbol-ref.md#patterns
func matchRef(pattern, ref string) bool {
	expr := strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(regexp.QuoteMeta(pattern))
	return regexp.MustCompile(`(?s)^` + expr + "$").MatchString(ref)
}

func TestMatchRef(t *testing.T) {
	cases := []struct {
		name, pattern, ref string
		want               bool
	}{
		{"star runs across solidus and hash", "internal/provider/*", "internal/provider/a/b#T.M", true},
		{"star stands for nothing", "internal/cache#*", "internal/cache#", true},
		{"question mark is one character", "internal/route-v?#T", "internal/route-v2#T", true},
		{"question mark is one code point", "internal/route-v?#T", "internal/route-v\U0001F600#T", true},
		{"question mark is not two characters", "internal/route-v?#T", "internal/route-v22#T", false},
		{"question mark is not zero characters", "internal/route-v?#T", "internal/route-v#T", false},
		{"dot is itself", "internal/a.b#T", "internal/aXb#T", false},
		{"no wildcard matches exactly", "internal/cache#T", "internal/cache#TT", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchRef(c.pattern, c.ref); got != c.want {
				t.Errorf("matchRef(%q, %q) = %v; want %v", c.pattern, c.ref, got, c.want)
			}
		})
	}
}

func TestRetainedAdjudicationsNameTheirRow(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(moduleRoot, "deadset-ignore.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Ignore []struct{ Code, Symbol, Reason string } `json:"ignore"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("deadset-ignore.json: %v", err)
	}
	for _, e := range cfg.Ignore {
		rel, _ := strings.CutPrefix(e.Symbol, refPrefix)
		want := ""
		for _, r := range retained {
			if matchRef(r.pattern, rel) {
				want = "Retained until " + r.until + " (" + r.pattern + "): "
				break
			}
		}
		switch {
		case want != "" && !strings.HasPrefix(e.Reason, want):
			t.Errorf("%s on %s: reason %q; want it to start %q", e.Code, e.Symbol, e.Reason, want)
		case want == "" && strings.HasPrefix(e.Reason, "Retained until "):
			t.Errorf("%s on %s names a retained row, but no row's pattern holds it; delete the entry with its row", e.Code, e.Symbol)
		}
	}
}
