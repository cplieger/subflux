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
	"ST-2", "ST-2b", "TS-2a", "TS-2b", "TS-2", "MS-2", "MR-1", "OW-6", "SE-2a",
	"SE-2", "SE-2p", "PA-2", "AP-1a", "AP-1b", "AP-1", "PA-2b", "MS-3", "SE-3",
	"WC-1a", "WC-1b", "WC-1", "UX-1b", "UX-1", "AUS-1",
}

// refPrefix is how deadset spells a symbol of this module; rows hold the rest.
const refPrefix = "go://github.com/cplieger/subflux/"

// retained holds one row per deadset.json root pattern: code with no
// production caller yet, kept live until the landing that calls, rebuilds or
// deletes it, which drops the pattern and the row together.
var retained = []struct{ pattern, until string }{
	{"internal/cache#*", "SE-2"},
	{"internal/embedded#*", "TS-2"},
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
	{"internal/language#FilenameTag", "TS-2"},
	{"internal/language#Flags.Serves", "TS-2"},
	{"internal/language#FromName", "MS-2"},
	{"internal/language#ISO3", "MS-2"},
	{"internal/language#MustProviderMap", "SE-2"},
	{"internal/language#Names", "AP-1"},
	{"internal/language#NewProviderMap", "SE-2"},
	{"internal/language#Policy.AutoQuery", "SE-2"},
	{"internal/language#Policy.Automatic", "SE-2"},
	{"internal/language#Policy.CloseVarieties", "AP-1"},
	{"internal/language#Policy.ManualQuery", "SE-2"},
	{"internal/language#Policy.Targets", "MS-2"},
	{"internal/language#ProviderMap.Tag", "SE-2"},
	{"internal/language#ProviderMap.Tags", "SE-2"},
	{"internal/language#ProviderMap.Wire", "SE-2"},
	{"internal/language#ReadFilename", "TS-2"},
	{"internal/language#RefineFromTitle", "TS-2"},
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
	{"internal/mediasource#Catalogue.Reordered", "MS-2"},
	{"internal/mediasource#Catalogue.Validate", "MS-2"},
	{"internal/mediasource#CheckSaved", "MS-2"},
	{"internal/mediasource#Config.Index", "MS-2"},
	{"internal/mediasource#Config.Values", "MS-2"},
	{"internal/mediasource#DefaultSchedule", "MS-2"},
	{"internal/mediasource#Deps.HTTP", "MS-2"},
	{"internal/mediasource#ErrAiredOrderUnavailable", "MS-2"},
	{"internal/mediasource#JoinQuery*", "MS-2"},
	{"internal/mediasource#Joined*", "MS-2"},
	{"internal/mediasource#Listed", "MS-2"},
	{"internal/mediasource#Listed.*", "MS-2"},
	{"internal/mediasource#ParseConfigs", "MS-2"},
	{"internal/mediasource#ParseSchedule", "MS-2"},
	{"internal/mediasource#PingEntry", "MS-2"},
	{"internal/mediasource#Schedule", "MS-2"},
	{"internal/mediasource#Schedule.Change", "MS-2"},
	{"internal/mediasource#Schedule.Whole", "MS-2"},
	{"internal/mediasource#ScheduleSchema", "MS-2"},
	{"internal/mediasource#Set.Inherit", "MS-2"},
	{"internal/mediasource#Set.LocalPath", "MS-2"},
	{"internal/mediasource#Set.MediaRoots", "MS-2"},
	{"internal/mediasource#Set.Sources", "MS-2"},
	{"internal/mediasource#TitleSet.Validate", "MS-2"},
	{"internal/mediasource/arr#*", "MS-2"},
	{"internal/mediasource/sourcetest#*", "MS-2"},
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
	{"internal/settings#Mode.UnmarshalText", "AP-1"},
	{"internal/settings#Server.AllowedHosts", "PA-2"},
	{"internal/settings#Server.Hosts", "AP-1"},
	{"internal/settings#Server.TrustedProxies", "PA-2"},
	{"internal/settings#Service.Merge", "AP-1"},
	{"internal/settings#Service.Save", "PA-2"},
	{"internal/settings#Snapshot.Document", "PA-2"},
	{"internal/skyhook#*", "MS-2"},
	{"internal/statedb#Coverage", "AP-1"},
	{"internal/statedb#Coverage.MarshalText", "AP-1"},
	{"internal/statedb#Coverage.UnmarshalText", "AP-1"},
	{"internal/statedb#CoverageCount", "AP-1b"},
	{"internal/statedb#CoverageCount.*", "AP-1b"},
	{"internal/statedb#CoverageRow", "SE-2"},
	{"internal/statedb#CoverageRow.*", "SE-2"},
	{"internal/statedb#CredentialDisable", "SE-2a"},
	{"internal/statedb#Delta", "MR-1"},
	{"internal/statedb#Delta.*", "MR-1"},
	{"internal/statedb#Download", "TS-2b"},
	{"internal/statedb#EmbeddedTrack", "TS-2"},
	{"internal/statedb#ErrCursorInvalid", "OW-6"},
	{"internal/statedb#ErrNotLockable", "OW-6"},
	{"internal/statedb#ErrPassUnknown", "OW-6"},
	{"internal/statedb#ErrSourceInactive", "MS-2"},
	{"internal/statedb#FileRecord", "TS-2b"},
	{"internal/statedb#FileRecord.*", "TS-2b"},
	{"internal/statedb#HistoryScope", "SE-2"},
	{"internal/statedb#Identity", "TS-2b"},
	{"internal/statedb#InspectOptions", "PA-2"},
	{"internal/statedb#Inspection", "PA-2"},
	{"internal/statedb#Inspection.*", "PA-2"},
	{"internal/statedb#Instance", "TS-2b"},
	{"internal/statedb#Instance.*", "TS-2b"},
	{"internal/statedb#InstanceID", "TS-2b"},
	{"internal/statedb#InstanceID.MarshalText", "AP-1"},
	{"internal/statedb#Intent", "TS-2b"},
	{"internal/statedb#Intent.*", "TS-2b"},
	{"internal/statedb#ItemHistory", "SE-2"},
	{"internal/statedb#ItemRow", "AP-1b"},
	{"internal/statedb#ItemRow.*", "AP-1b"},
	{"internal/statedb#ListCursor", "AP-1b"},
	{"internal/statedb#LocalFile", "TS-2b"},
	{"internal/statedb#MarkAccounts", "ST-2"},
	{"internal/statedb#MarkConfig", "AP-1b"},
	{"internal/statedb#MarkHistory", "AP-1b"},
	{"internal/statedb#MarkUnidentified", "AP-1b"},
	{"internal/statedb#Mismatch", "PA-2"},
	{"internal/statedb#Mismatch.*", "PA-2"},
	{"internal/statedb#NamespaceStat", "PA-2"},
	{"internal/statedb#NamespaceStat.*", "PA-2"},
	{"internal/statedb#NoResult", "SE-2"},
	{"internal/statedb#Op", "TS-2b"},
	{"internal/statedb#OrdinalKey", "TS-2b"},
	{"internal/statedb#ParseInstanceID", "AP-1b"},
	{"internal/statedb#PassID", "MS-2"},
	{"internal/statedb#PassScope", "MS-2"},
	{"internal/statedb#PendingDeliver", "TS-2b"},
	{"internal/statedb#Provenance", "TS-2b"},
	{"internal/statedb#Removal", "TS-2b"},
	{"internal/statedb#RootHistory", "AP-1"},
	{"internal/statedb#RootRow", "AP-1b"},
	{"internal/statedb#RootRow.*", "AP-1b"},
	{"internal/statedb#RootTarget", "MR-1"},
	{"internal/statedb#RunID", "TS-2b"},
	{"internal/statedb#Store.AckWork", "SE-2"},
	{"internal/statedb#Store.Backup", "PA-2"},
	{"internal/statedb#Store.Baseline", "TS-2b"},
	{"internal/statedb#Store.BeginDelete", "TS-2b"},
	{"internal/statedb#Store.BeginDeliver", "TS-2b"},
	{"internal/statedb#Store.BeginPass", "MS-2"},
	{"internal/statedb#Store.BeginRetime", "TS-2b"},
	{"internal/statedb#Store.Changed", "MR-1"},
	{"internal/statedb#Store.Coverage", "SE-2"},
	{"internal/statedb#Store.CredentialDisable", "SE-2a"},
	{"internal/statedb#Store.DeferWork", "SE-2"},
	{"internal/statedb#Store.DeleteCredentialDisable", "SE-2a"},
	{"internal/statedb#Store.Embedded", "TS-2"},
	{"internal/statedb#Store.EndDelete", "TS-2b"},
	{"internal/statedb#Store.EndDeliver", "TS-2b"},
	{"internal/statedb#Store.EndRetime", "TS-2b"},
	{"internal/statedb#Store.EnqueueWork", "SE-2"},
	{"internal/statedb#Store.Files", "TS-2b"},
	{"internal/statedb#Store.FinishPass", "MS-2"},
	{"internal/statedb#Store.History", "SE-2"},
	{"internal/statedb#Store.Inspect", "PA-2"},
	{"internal/statedb#Store.Instance", "TS-2b"},
	{"internal/statedb#Store.Item", "SE-2"},
	{"internal/statedb#Store.ItemRow", "AP-1b"},
	{"internal/statedb#Store.Items", "AP-1b"},
	{"internal/statedb#Store.NextWork", "SE-2"},
	{"internal/statedb#Store.NoResults", "SE-2"},
	{"internal/statedb#Store.NoteRefusal", "SE-2"},
	{"internal/statedb#Store.ObserveItems", "MS-2"},
	{"internal/statedb#Store.PendingDeliverOf", "TS-2b"},
	{"internal/statedb#Store.PendingWrites", "TS-2b"},
	{"internal/statedb#Store.Policy", "MS-2"},
	{"internal/statedb#Store.PutCredentialDisable", "SE-2a"},
	{"internal/statedb#Store.PutEmbedded", "TS-2"},
	{"internal/statedb#Store.PutExternal", "SE-2"},
	{"internal/statedb#Store.PutNoResults", "SE-2"},
	{"internal/statedb#Store.RecordAttempts", "TS-2b"},
	{"internal/statedb#Store.ReserveOrdinal", "TS-2b"},
	{"internal/statedb#Store.Root", "AP-1b"},
	{"internal/statedb#Store.RootEvidence", "MS-2"},
	{"internal/statedb#Store.RootTargets", "MR-1"},
	{"internal/statedb#Store.Roots", "AP-1b"},
	{"internal/statedb#Store.SetLock", "AP-1b"},
	{"internal/statedb#Store.Since", "MR-1"},
	{"internal/statedb#Store.SourceCursor", "MS-2"},
	{"internal/statedb#Store.Unidentified", "AP-1b"},
	{"internal/statedb#Store.WorkReady", "SE-2"},
	{"internal/statedb#Store.WriteHealth", "PA-2"},
	{"internal/statedb#TableStat", "PA-2"},
	{"internal/statedb#TableStat.*", "PA-2"},
	{"internal/statedb#UnidentifiedRow", "AP-1b"},
	{"internal/statedb#UnidentifiedRow.*", "AP-1b"},
	{"internal/statedb#VideoProbe", "TS-2"},
	{"internal/statedb#VideoStamp", "TS-2"},
	{"internal/statedb#Work", "SE-2"},
	{"internal/statedb#Work.*", "SE-2"},
	{"internal/statedb#WorkCause", "SE-2"},
	{"internal/statedb#WriteHealth", "PA-2"},
	{"internal/statedb#WriteHealth.*", "PA-2"},
	{"internal/status#AlertAdminSocket", "PA-2"},
	{"internal/status#AlertAuthDisabled", "PA-2"},
	{"internal/status#AlertBackup", "PA-2"},
	{"internal/status#AlertPendingWrites", "TS-2"},
	{"internal/status#AlertStore", "PA-2"},
	{"internal/status#ErrNotCancellable", "AP-1"},
	{"internal/status#ErrUnknownJob", "AP-1"},
	{"internal/status#Health.MarshalText", "AP-1"},
	{"internal/status#Health.UnmarshalText", "AP-1"},
	{"internal/status#JobID.MarshalText", "AP-1"},
	{"internal/status#Kind.MarshalText", "AP-1"},
	{"internal/status#Kind.UnmarshalText", "AP-1"},
	{"internal/status#MediaRootAlert", "TS-2"},
	{"internal/status#MediaServerAlert", "MS-3"},
	{"internal/status#Outcome.MarshalText", "AP-1"},
	{"internal/status#Outcome.UnmarshalText", "AP-1"},
	{"internal/status#OwnerOf", "AP-1"},
	{"internal/status#ProviderAlert", "SE-2"},
	{"internal/status#ProviderState", "SE-2"},
	{"internal/status#Snapshot.Boot", "AP-1"},
	{"internal/status#Snapshot.Jobs", "AP-1"},
	{"internal/status#Snapshot.Providers", "AP-1"},
	{"internal/status#SourceAlert", "MS-2"},
	{"internal/status#SourceOverlapAlert", "MS-2"},
	{"internal/status#StartCodes", "SE-2"},
	{"internal/status#State.MarshalText", "AP-1"},
	{"internal/status#State.UnmarshalText", "AP-1"},
	{"internal/status#Store.Begin", "MS-2"},
	{"internal/status#Store.Cancel", "AP-1"},
	{"internal/status#Store.Get", "AP-1"},
	{"internal/status#Store.Keyed", "MS-2"},
	{"internal/status#Store.RetainAlerts", "MS-2"},
	{"internal/status#Store.SetProvider", "SE-2"},
	{"internal/status#Store.Update", "MS-2"},
	{"internal/status#SystemOwner", "MS-2"},
	{"internal/subsync#*", "TS-2"},
	{"internal/subsync/*", "TS-2"},
	{"internal/subtitleenc#Encoding.Encode", "TS-2"},
	{"internal/subtitlefile#Build", "TS-2"},
	{"internal/subtitlefile#ExtOf", "TS-2"},
	{"internal/subtitlefile#IsVideo", "TS-2"},
	{"internal/subtitlefile#Owner", "TS-2"},
	{"internal/subtitlefile#Parse", "TS-2"},
	{"internal/subtitlefile#Stem", "TS-2"},
	{"internal/timing#Action.MarshalText", "TS-2"},
	{"internal/timing#Action.UnmarshalText", "TS-2"},
	{"internal/timing#Canonical", "TS-2"},
	{"internal/timing#Clean", "TS-2"},
	{"internal/timing#CleanOptions.*", "TS-2"},
	{"internal/timing#Codes", "AP-1"},
	{"internal/timing#Config", "TS-2"},
	{"internal/timing#Cues.*", "TS-2"},
	{"internal/timing#Event.*", "TS-2"},
	{"internal/timing#Format.MarshalText", "TS-2"},
	{"internal/timing#Format.UnmarshalText", "TS-2"},
	{"internal/timing#Method.MarshalText", "TS-2"},
	{"internal/timing#Method.UnmarshalText", "TS-2"},
	{"internal/timing#ParseConfig", "TS-2"},
	{"internal/timing#ParseCues", "SE-2"},
	{"internal/timing#Plan", "SE-2"},
	{"internal/timing#PreviewSpan", "TS-2"},
	{"internal/timing#Render", "TS-2"},
	{"internal/timing#RenderVTT", "TS-2"},
	{"internal/timing#Schema", "TS-2"},
	{"internal/timing#SkipReason.MarshalText", "TS-2"},
	{"internal/timing#SkipReason.UnmarshalText", "TS-2"},
	{"internal/timing#Verdict.MarshalText", "TS-2"},
	{"internal/timing#Verdict.UnmarshalText", "TS-2"},
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
		{"internal/a#*", "MS-2"},
		{"internal/a#T.*", "SE-2"},
		{"internal/a#T.M", "AP-1"},
	}
	for _, c := range []struct{ name, ref, pattern, until string }{
		{"its own row over its type's and package's", "internal/a#T.M", "internal/a#T.M", "AP-1"},
		{"its type's row over its package's", "internal/a#T.N", "internal/a#T.*", "SE-2"},
		{"its package's row alone", "internal/a#U", "internal/a#*", "MS-2"},
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
