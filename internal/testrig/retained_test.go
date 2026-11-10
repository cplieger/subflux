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
	"ST-2b", "SE-2", "SE-2p",
	"PA-2", "AP-1a", "AP-1b", "AP-1", "PA-2b", "MS-3", "SE-3", "WC-1a", "WC-1b",
	"WC-1", "UX-1b", "UX-1", "AUS-1",
}

// refPrefix is how deadset spells a symbol of this module; rows hold the rest.
const refPrefix = "go://github.com/cplieger/subflux/"

// retained holds one row per deadset.json root pattern: code with no
// production caller yet, kept live until the landing that calls, rebuilds or
// deletes it, which drops the pattern and the row together.
var retained = []struct{ pattern, until string }{
	{"internal/cache#*", "SE-2"},
	{"internal/embedded#New", "SE-2"},
	{"internal/embedded#Schema", "PA-2"},
	{"internal/episodematch#*", "SE-2"},
	{"internal/epmarker#*", "SE-2"},
	{"internal/formschema#Field.Help", "PA-2"},
	{"internal/formschema#Field.Label", "PA-2"},
	{"internal/formschema#Kind.MarshalText", "AP-1"},
	{"internal/formschema#Kind.UnmarshalText", "AP-1"},
	{"internal/formschema#Values.Bool", "PA-2"},
	{"internal/formschema#Values.Duration", "PA-2"},
	{"internal/formschema#Values.Int", "PA-2"},
	{"internal/formschema#Values.Secret", "PA-2"},
	{"internal/language#Class.MarshalText", "AP-1"},
	{"internal/language#Class.UnmarshalText", "AP-1"},
	{"internal/language#Closeness.MarshalText", "AP-1"},
	{"internal/language#Closeness.UnmarshalText", "AP-1"},
	{"internal/language#EnglishName", "AP-1"},
	{"internal/language#MustProviderMap", "SE-2"},
	{"internal/language#Names", "AP-1"},
	{"internal/language#NewProviderMap", "SE-2"},
	{"internal/language#Policy.AutoQuery", "SE-2"},
	{"internal/language#Policy.Automatic", "SE-2"},
	{"internal/language#Policy.CloseVarieties", "AP-1"},
	{"internal/language#Policy.ManualQuery", "SE-2"},
	{"internal/language#ProviderMap.Tag", "SE-2"},
	{"internal/language#ProviderMap.Tags", "SE-2"},
	{"internal/language#ProviderMap.Wires", "SE-2"},
	{"internal/language#Tag.MarshalText", "AP-1"},
	{"internal/language#Tag.UnmarshalText", "AP-1"},
	{"internal/language#Variant.Flags", "SE-2"},
	{"internal/language#Variant.MarshalText", "AP-1"},
	{"internal/language#Variant.UnmarshalText", "AP-1"},
	{"internal/media#Key.MarshalText", "AP-1"},
	{"internal/media#Kind.MarshalText", "AP-1"},
	{"internal/media#ParseRootKey", "AP-1"},
	{"internal/media#RootKey.MarshalText", "AP-1"},
	{"internal/media#Scheme.MarshalText", "AP-1"},
	{"internal/mediaserver#*", "AP-1"},
	{"internal/mediasource#Capabilities.Posters", "AP-1"},
	{"internal/mediasource#ScheduleSchema", "PA-2"},
	{"internal/mediasource#Set.LocalPath", "AP-1"},
	{"internal/mediasource#Set.Ping", "AP-1"},
	{"internal/mediasource#Set.Poster", "AP-1"},
	{"internal/mediawrite#DecodeRef", "AP-1"},
	{"internal/mediawrite#FolderState.*", "AP-1"},
	{"internal/mediawrite#Found.*", "SE-2"},
	{"internal/mediawrite#Inventory.*", "SE-2"},
	{"internal/mediawrite#RefOf", "SE-2"},
	{"internal/mediawrite#Timed.Timing", "AP-1"},
	{"internal/mediawrite#Writer.Delete", "AP-1"},
	{"internal/mediawrite#Writer.Deliver", "SE-2"},
	{"internal/mediawrite#Writer.FolderState", "AP-1"},
	{"internal/mediawrite#Writer.List", "SE-2"},
	{"internal/obs#Metrics.Register", "AP-1"},
	{"internal/provider#*", "SE-2"},
	{"internal/provider/*", "SE-2"},
	{"internal/providercalls#Automatic", "SE-2"},
	{"internal/providercalls#BackoffError", "SE-2"},
	{"internal/providercalls#Binding", "SE-2"},
	{"internal/providercalls#Binding.Call", "SE-2"},
	{"internal/providercalls#Binding.Release", "SE-2"},
	{"internal/providercalls#Class", "SE-2"},
	{"internal/providercalls#ErrCredentialDisabled", "SE-2"},
	{"internal/providercalls#ErrReleased", "SE-2"},
	{"internal/providercalls#Funnel", "SE-2"},
	{"internal/providercalls#Funnel.Bind", "SE-2"},
	{"internal/providercalls#Funnel.Collectors", "SE-2"},
	{"internal/providercalls#Funnel.Probe", "SE-2"},
	{"internal/providercalls#Interactive", "SE-2"},
	{"internal/providercalls#NewFunnel", "SE-2"},
	{"internal/providercalls#Op", "SE-2"},
	{"internal/rank#Compare", "SE-2"},
	{"internal/rank#Film", "SE-2"},
	{"internal/rank#Fit.MarshalText", "AP-1"},
	{"internal/rank#Fit.UnmarshalText", "AP-1"},
	{"internal/rank#FitOf", "SE-2"},
	{"internal/rank#MatchedBy.MarshalText", "AP-1"},
	{"internal/rank#MatchedBy.UnmarshalText", "AP-1"},
	{"internal/rank#PAL", "SE-2"},
	{"internal/rank#RateFamily", "SE-2"},
	{"internal/rank#RateFamilyOf", "SE-2"},
	{"internal/rank#RateTolerance", "SE-2"},
	{"internal/rank#RateUnknown", "SE-2"},
	{"internal/rank#Tri.MarshalText", "AP-1"},
	{"internal/rank#Tri.UnmarshalText", "AP-1"},
	{"internal/release#*", "SE-2"},
	{"internal/scan#Codes", "AP-1"},
	{"internal/scan#ErrScanCapacity", "AP-1"},
	{"internal/scan#Request", "AP-1"},
	{"internal/scan#Request.*", "AP-1"},
	{"internal/scan#Scanner.PassNow", "AP-1"},
	{"internal/scan#Scanner.Start", "AP-1"},
	{"internal/seriesorder#Service.Evidence", "SE-2"},
	{"internal/settings#Active.Timing", "SE-2"},
	{"internal/settings#Codes", "AP-1"},
	{"internal/settings#Mode.UnmarshalText", "AP-1"},
	{"internal/settings#Server.AllowedHosts", "PA-2"},
	{"internal/settings#Server.Hosts", "AP-1"},
	{"internal/settings#Server.TrustedProxies", "PA-2"},
	{"internal/settings#Service.Merge", "AP-1"},
	{"internal/settings#Service.Save", "PA-2"},
	{"internal/settings#Snapshot.Document", "PA-2"},
	{"internal/statedb#AccountStore", "PA-2"},
	{"internal/statedb#AccountStore.*", "PA-2"},
	{"internal/statedb#AuthSecrets", "PA-2"},
	{"internal/statedb#AuthSecrets.*", "PA-2"},
	{"internal/statedb#Codes", "AP-1"},
	{"internal/statedb#Coverage", "AP-1"},
	{"internal/statedb#Coverage.MarshalText", "AP-1"},
	{"internal/statedb#Coverage.UnmarshalText", "AP-1"},
	{"internal/statedb#CoverageCount", "AP-1b"},
	{"internal/statedb#CoverageCount.*", "AP-1b"},
	{"internal/statedb#CoverageRow", "SE-2"},
	{"internal/statedb#CoverageRow.*", "SE-2"},
	{"internal/statedb#ErrCursorInvalid", "AP-1"},
	{"internal/statedb#ErrNotLockable", "AP-1"},
	{"internal/statedb#ErrPassUnknown", "AP-1"},
	{"internal/statedb#ErrSourceInactive", "AP-1"},
	{"internal/statedb#HistoryScope", "SE-2"},
	{"internal/statedb#InspectOptions", "PA-2"},
	{"internal/statedb#Inspection", "PA-2"},
	{"internal/statedb#Inspection.*", "PA-2"},
	{"internal/statedb#Instance.*", "SE-2"},
	{"internal/statedb#InstanceID.MarshalText", "AP-1"},
	{"internal/statedb#ItemHistory", "SE-2"},
	{"internal/statedb#ItemRow", "AP-1b"},
	{"internal/statedb#ItemRow.*", "AP-1b"},
	{"internal/statedb#ListCursor", "AP-1b"},
	{"internal/statedb#MarkAccounts", "AP-1b"},
	{"internal/statedb#MarkConfig", "AP-1b"},
	{"internal/statedb#MarkHistory", "AP-1b"},
	{"internal/statedb#MarkUnidentified", "AP-1b"},
	{"internal/statedb#MethodStateOf", "PA-2"},
	{"internal/statedb#Mismatch", "PA-2"},
	{"internal/statedb#Mismatch.*", "PA-2"},
	{"internal/statedb#NamespaceStat", "PA-2"},
	{"internal/statedb#NamespaceStat.*", "PA-2"},
	{"internal/statedb#NoResult", "SE-2"},
	{"internal/statedb#ParseInstanceID", "AP-1b"},
	{"internal/statedb#RootHistory", "AP-1"},
	{"internal/statedb#RootRow", "AP-1b"},
	{"internal/statedb#RootRow.*", "AP-1b"},
	{"internal/statedb#Store.Accounts", "PA-2"},
	{"internal/statedb#Store.AckWork", "SE-2"},
	{"internal/statedb#Store.ActivateAuth", "PA-2"},
	{"internal/statedb#Store.AuthSecrets", "PA-2"},
	{"internal/statedb#Store.Backup", "PA-2"},
	{"internal/statedb#Store.Coverage", "SE-2"},
	{"internal/statedb#Store.DeferWork", "SE-2"},
	{"internal/statedb#Store.EnqueueWork", "SE-2"},
	{"internal/statedb#Store.History", "SE-2"},
	{"internal/statedb#Store.Inspect", "PA-2"},
	{"internal/statedb#Store.Item", "SE-2"},
	{"internal/statedb#Store.ItemRow", "AP-1b"},
	{"internal/statedb#Store.Items", "AP-1b"},
	{"internal/statedb#Store.NextWork", "SE-2"},
	{"internal/statedb#Store.NoResults", "SE-2"},
	{"internal/statedb#Store.NoteRefusal", "SE-2"},
	{"internal/statedb#Store.PutExternal", "SE-2"},
	{"internal/statedb#Store.PutNoResults", "SE-2"},
	{"internal/statedb#Store.Root", "AP-1b"},
	{"internal/statedb#Store.Roots", "AP-1b"},
	{"internal/statedb#Store.SetLock", "AP-1b"},
	{"internal/statedb#Store.Unidentified", "AP-1b"},
	{"internal/statedb#Store.WorkReady", "SE-2"},
	{"internal/statedb#Store.WriteHealth", "PA-2"},
	{"internal/statedb#TableStat", "PA-2"},
	{"internal/statedb#TableStat.*", "PA-2"},
	{"internal/statedb#UnidentifiedRow", "AP-1b"},
	{"internal/statedb#UnidentifiedRow.*", "AP-1b"},
	{"internal/statedb#Work", "SE-2"},
	{"internal/statedb#Work.*", "SE-2"},
	{"internal/statedb#WorkCause", "SE-2"},
	{"internal/statedb#WriteHealth", "PA-2"},
	{"internal/statedb#WriteHealth.*", "PA-2"},
	{"internal/status#AlertAdminSocket", "PA-2"},
	{"internal/status#AlertAuthDisabled", "PA-2"},
	{"internal/status#AlertBackup", "PA-2"},
	{"internal/status#AlertStore", "PA-2"},
	{"internal/status#Codes", "AP-1"},
	{"internal/status#ErrNotCancellable", "AP-1"},
	{"internal/status#ErrUnknownJob", "AP-1"},
	{"internal/status#Health.MarshalText", "AP-1"},
	{"internal/status#Health.UnmarshalText", "AP-1"},
	{"internal/status#JobID.MarshalText", "AP-1"},
	{"internal/status#Kind.MarshalText", "AP-1"},
	{"internal/status#Kind.UnmarshalText", "AP-1"},
	{"internal/status#MediaServerAlert", "MS-3"},
	{"internal/status#Outcome.MarshalText", "AP-1"},
	{"internal/status#Outcome.UnmarshalText", "AP-1"},
	{"internal/status#OwnerOf", "AP-1"},
	{"internal/status#ProviderAlert", "SE-2"},
	{"internal/status#ProviderState", "SE-2"},
	{"internal/status#Snapshot.Boot", "AP-1"},
	{"internal/status#Snapshot.Jobs", "AP-1"},
	{"internal/status#Snapshot.Providers", "AP-1"},
	{"internal/status#StartCodes", "SE-2"},
	{"internal/status#State.MarshalText", "AP-1"},
	{"internal/status#State.UnmarshalText", "AP-1"},
	{"internal/status#Store.Cancel", "AP-1"},
	{"internal/status#Store.Get", "AP-1"},
	{"internal/status#Store.RetainProviders", "AP-1"},
	{"internal/status#Store.SetProvider", "SE-2"},
	{"internal/syncsched#*", "AP-1"},
	{"internal/syncsched#Aligned*", "SE-2"},
	{"internal/syncsched#Aligner.Align", "SE-2"},
	{"internal/timing#Action.MarshalText", "AP-1"},
	{"internal/timing#Action.UnmarshalText", "AP-1"},
	{"internal/timing#Codes", "AP-1"},
	{"internal/timing#Format.MarshalText", "AP-1"},
	{"internal/timing#Format.UnmarshalText", "AP-1"},
	{"internal/timing#Method.MarshalText", "AP-1"},
	{"internal/timing#Method.UnmarshalText", "AP-1"},
	{"internal/timing#ParseCues", "SE-2"},
	{"internal/timing#Plan", "SE-2"},
	{"internal/timing#RenderVTT", "AP-1"},
	{"internal/timing#SkipReason.MarshalText", "AP-1"},
	{"internal/timing#SkipReason.UnmarshalText", "AP-1"},
	{"internal/timing#Verdict.MarshalText", "AP-1"},
	{"internal/timing#Verdict.UnmarshalText", "AP-1"},
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

// rowFor answers the narrowest of rows whose pattern matches ref, the one
// with the most characters outside a star, so a symbol a later landing calls
// first is adjudicated by its own row, not by its type's or package's.
func rowFor(rows []struct{ pattern, until string }, ref string) (pattern, until string, ok bool) {
	best := -1
	for _, r := range rows {
		if n := len(r.pattern) - strings.Count(r.pattern, "*"); matchRef(r.pattern, ref) && n > best {
			pattern, until, ok, best = r.pattern, r.until, true, n
		}
	}
	return pattern, until, ok
}

func TestRowForPrefersTheNarrowestPattern(t *testing.T) {
	rows := []struct{ pattern, until string }{
		{"internal/a#*", "MS-3"},
		{"internal/a#T.*", "SE-2"},
		{"internal/a#T.M", "AP-1"},
	}
	for _, c := range []struct{ name, ref, pattern, until string }{
		{"its own row over its type's and package's", "internal/a#T.M", "internal/a#T.M", "AP-1"},
		{"its type's row over its package's", "internal/a#T.N", "internal/a#T.*", "SE-2"},
		{"its package's row alone", "internal/a#U", "internal/a#*", "MS-3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if pattern, until, ok := rowFor(rows, c.ref); !ok || pattern != c.pattern || until != c.until {
				t.Errorf("rowFor(%q) = %q, %q, %v; want %q, %q", c.ref, pattern, until, ok, c.pattern, c.until)
			}
		})
	}
	if _, _, ok := rowFor(rows, "internal/b#T"); ok {
		t.Error("rowFor matched a symbol no row retains")
	}
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
		if pattern, until, ok := rowFor(retained, rel); ok {
			want = "Retained until " + until + " (" + pattern + "): "
		}
		switch {
		case want != "" && !strings.HasPrefix(e.Reason, want):
			t.Errorf("%s on %s: reason %q; want it to start %q", e.Code, e.Symbol, e.Reason, want)
		case want == "" && strings.HasPrefix(e.Reason, "Retained until "):
			t.Errorf("%s on %s names a retained row, but no row's pattern holds it; delete the entry with its row", e.Code, e.Symbol)
		}
	}
}
