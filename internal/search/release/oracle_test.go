package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .NET-oracle comparison (spec subflux-release-parse-fidelity R2): the
// committed testdata/dotnet_oracle.json holds the source patterns' behavior
// under the real .NET backtracking Regex engine (all matches in scan order,
// per-group participation, pinned culture and options; regenerated via
// testdata/oracle/regen.sh with a digest-pinned .NET 10 SDK image).
//
// The comparison gate runs at the OBSERVABLE boundary (R2.4): per-format
// match booleans (which fully determine MatchFirst labels) and the
// release-group pattern's consumed captures (m[1]/m[3] fallback). Every
// remaining divergence must be classified in testdata/oracle/contracts.json;
// an unclassified divergence fails the test. Staleness keys fail loudly
// when formats.go or the corpus change without regeneration.

// oraclePatternSpec is one exported pattern (testdata/oracle/patterns.json).
type oraclePatternSpec struct {
	ID    string `json:"id"`
	Regex string `json:"regex"`
}

// oraclePatterns enumerates the layer's full compiled pattern set with
// stable IDs: every Format table entry plus the release-group pattern.
func oraclePatterns() []oraclePatternSpec {
	var out []oraclePatternSpec
	tables := []struct {
		name    string
		formats []Format
	}{
		{"sources", SonarrSources},
		{"codecs", TrashVideoCodecs},
		{"hdr", TrashHDRFormats},
		{"streaming", TrashStreamingServices},
	}
	for _, tbl := range tables {
		for _, f := range tbl.formats {
			out = append(out, oraclePatternSpec{ID: tbl.name + "/" + f.Name, Regex: f.Regex})
		}
	}
	out = append(out, oraclePatternSpec{ID: "releasegroup", Regex: sonarrReleaseGroupRegex})
	return out
}

// patternsDigest computes the canonical pattern-set digest ("id\x00regex\n"
// lines), mirrored byte-for-byte by the C# runner.
func patternsDigest(specs []oraclePatternSpec) string {
	h := sha256.New()
	for _, s := range specs {
		fmt.Fprintf(h, "%s\x00%s\n", s.ID, s.Regex)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// corpusDigest computes the canonical corpus digest ("name\n" lines),
// mirrored by the C# runner.
func corpusDigest(names []string) string {
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s\n", n)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// loadCorpusNames reads the committed corpus.
func loadCorpusNames(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "oracle", "corpus.json"))
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var c struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	return c.Names
}

// TestOracleExportPatterns writes testdata/oracle/patterns.json from the
// live pattern tables when UPDATE_ORACLE_PATTERNS=1 (invoked by regen.sh);
// otherwise it verifies the committed export is current, so the file the
// runner consumed can never drift silently from formats.go.
func TestOracleExportPatterns(t *testing.T) {
	specs := oraclePatterns()
	seen := make(map[string]bool, len(specs))
	for _, s := range specs {
		if seen[s.ID] {
			t.Fatalf("duplicate oracle pattern id %q", s.ID)
		}
		seen[s.ID] = true
	}
	path := filepath.Join("testdata", "oracle", "patterns.json")
	blob, err := json.MarshalIndent(struct {
		Patterns []oraclePatternSpec `json:"patterns"`
	}{specs}, "", " ")
	if err != nil {
		t.Fatalf("marshal patterns: %v", err)
	}
	blob = append(blob, '\n')

	if os.Getenv("UPDATE_ORACLE_PATTERNS") == "1" {
		if err := os.WriteFile(path, blob, 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s (%d patterns)", path, len(specs))
		return
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		// Fail closed: the export is a committed artifact and its absence
		// must never turn the oracle gate green (F15).
		t.Fatalf("read committed %s: %v — restore it from version control or regenerate with testdata/oracle/regen.sh", path, err)
	}
	if string(existing) != string(blob) {
		t.Fatalf("testdata/oracle/patterns.json is stale against formats.go; run testdata/oracle/regen.sh")
	}
}

// --- committed oracle schema (subset the comparison consumes) ---

type oracleFile struct {
	RunnerVersion  string         `json:"runner_version"`
	RuntimeVersion string         `json:"runtime_version"`
	Culture        string         `json:"culture"`
	Engine         string         `json:"engine"`
	PatternsSha256 string         `json:"patterns_sha256"`
	CorpusSha256   string         `json:"corpus_sha256"`
	Results        []oracleResult `json:"results"`
	TimeoutSeconds float64        `json:"timeout_seconds"`
}

type oracleResult struct {
	CompileError *string          `json:"compile_error"`
	PatternID    string           `json:"pattern_id"`
	Options      string           `json:"options"`
	Names        []oracleNameHits `json:"names"`
}

type oracleNameHits struct {
	Error   *string       `json:"error"`
	Name    string        `json:"name"`
	Matches []oracleMatch `json:"matches"`
}

type oracleMatch struct {
	Value  string        `json:"value"`
	Groups []oracleGroup `json:"groups"`
	Index  int           `json:"index"`
	Length int           `json:"length"`
}

type oracleGroup struct {
	Value   *string `json:"value"`
	Index   int     `json:"index"`
	Length  int     `json:"length"`
	Success bool    `json:"success"`
}

// contractEntry is one classified, documented divergence
// (testdata/oracle/contracts.json): the accepted-contract allowlist keyed
// by pattern id + corpus name.
type contractEntry struct {
	PatternID string `json:"pattern_id"`
	Name      string `json:"name"`
	Class     string `json:"class"`  // divergence class (a)-(k)
	Reason    string `json:"reason"` // why this is an accepted contract
}

// loadOracle reads the committed oracle output. A missing artifact is a
// hard failure, never a skip: the comparison is the R2.4 completion gate,
// and deleting the committed file must not turn the gate green (F15).
func loadOracle(t *testing.T) *oracleFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "dotnet_oracle.json"))
	if err != nil {
		t.Fatalf("read committed testdata/dotnet_oracle.json: %v — restore it from version control or regenerate with internal/search/release/testdata/oracle/regen.sh", err)
	}
	var of oracleFile
	if err := json.Unmarshal(raw, &of); err != nil {
		t.Fatalf("parse dotnet_oracle.json: %v", err)
	}
	return &of
}

// loadContracts reads the classified-divergence allowlist.
func loadContracts(t *testing.T) map[string]contractEntry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "oracle", "contracts.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read contracts.json: %v", err)
	}
	var entries []contractEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("parse contracts.json: %v", err)
	}
	m := make(map[string]contractEntry, len(entries))
	for _, e := range entries {
		m[e.PatternID+"\x00"+e.Name] = e
	}
	return m
}

// TestDotNetOracleComparison is the R2.4 completion gate at the observable
// boundary: for every (pattern, corpus name) pair, the layer's match
// boolean must equal the oracle's, and for the release-group pattern the
// consumed capture observable (m[1], m[3] fallback, last match) must equal
// the oracle's last-match value. Divergences classified as contracts in
// contracts.json are reported but pass; anything unclassified fails.
func TestDotNetOracleComparison(t *testing.T) {
	oracle := loadOracle(t)
	names := loadCorpusNames(t)
	specs := oraclePatterns()
	contracts := loadContracts(t)

	// Staleness keys (R2.3): editing formats.go or the corpus without
	// regenerating the oracle fails loudly here.
	if got, want := patternsDigest(specs), oracle.PatternsSha256; got != want {
		t.Fatalf("pattern-set digest %s != oracle's %s: formats.go changed without oracle regeneration; run testdata/oracle/regen.sh", got, want)
	}
	if got, want := corpusDigest(names), oracle.CorpusSha256; got != want {
		t.Fatalf("corpus digest %s != oracle's %s: corpus.json changed without oracle regeneration; run testdata/oracle/regen.sh", got, want)
	}

	compiled := make(map[string]*Pattern, len(specs))
	for _, s := range specs {
		p, err := CompilePCRE(s.Regex)
		if err != nil {
			t.Fatalf("CompilePCRE(%s): %v", s.ID, err)
		}
		compiled[s.ID] = p
	}

	// One-to-one result-set check (F15): the header digests prove the
	// pattern set and corpus match, but not that the results array still
	// covers them — a truncated or hand-edited oracle would silently
	// shrink the gate. Every live pattern must have exactly one result
	// set, and every result set must belong to a live pattern.
	seenResults := make(map[string]bool, len(specs))

	divergences := 0
	classified := 0
	for _, res := range oracle.Results {
		if seenResults[res.PatternID] {
			t.Errorf("duplicate oracle result set for pattern %s (corrupt dotnet_oracle.json; run testdata/oracle/regen.sh)", res.PatternID)
			continue
		}
		seenResults[res.PatternID] = true
		if res.CompileError != nil {
			t.Errorf("oracle compile error for %s: %s", res.PatternID, *res.CompileError)
			continue
		}
		p := compiled[res.PatternID]
		if p == nil {
			t.Errorf("oracle result for unknown pattern %s (not in the live pattern set; run testdata/oracle/regen.sh)", res.PatternID)
			continue
		}
		// Oracle stores only names with >=1 match (or an error); rebuild
		// the full boolean map.
		oracleHits := make(map[string]*oracleNameHits, len(res.Names))
		for i := range res.Names {
			oracleHits[res.Names[i].Name] = &res.Names[i]
		}
		for _, name := range names {
			hit := oracleHits[name]
			if hit != nil && hit.Error != nil {
				t.Errorf("%s on %q: oracle error %s", res.PatternID, name, *hit.Error)
				continue
			}
			want := hit != nil && len(hit.Matches) > 0
			got := p.MatchString(name)
			if got != want {
				key := res.PatternID + "\x00" + name
				if c, ok := contracts[key]; ok {
					classified++
					t.Logf("classified divergence (%s, class %s): %s on %q: layer=%v oracle=%v (%s)",
						"contract", c.Class, res.PatternID, name, got, want, c.Reason)
					continue
				}
				divergences++
				t.Errorf("UNCLASSIFIED divergence: %s on %q: layer match=%v, oracle match=%v",
					res.PatternID, name, got, want)
				continue
			}
			if res.PatternID == "releasegroup" && want {
				compareReleaseGroupObservable(t, contracts, p, name, hit)
			}
		}
	}
	for _, s := range specs {
		if !seenResults[s.ID] {
			t.Errorf("oracle has no result set for pattern %s: the gate would silently skip it; run testdata/oracle/regen.sh", s.ID)
		}
	}
	t.Logf("oracle comparison: %d unclassified divergences, %d classified contracts", divergences, classified)
}

// TestDotNetOracleMetadata pins the oracle's execution metadata to the
// values the spec requires (R2.2/R2.3): the 1.0.0 runner, a .NET 10
// runtime (the SDK major pinned alongside the image digest in regen.sh),
// the invariant culture, the real backtracking engine (NonBacktracking is
// never allowed — it disallows lookarounds and is not the emulated
// semantics), the explicit 5-second match timeout, and the
// IgnoreCase|CultureInvariant options on every pattern. Recording these
// without enforcement let a regeneration under the wrong engine or
// culture pass silently.
func TestDotNetOracleMetadata(t *testing.T) {
	oracle := loadOracle(t)
	if got, want := oracle.RunnerVersion, "1.0.0"; got != want {
		t.Errorf("runner_version = %q, want %q (testdata/oracle/oracle.cs RunnerVersion)", got, want)
	}
	if !strings.HasPrefix(oracle.RuntimeVersion, ".NET 10.") {
		t.Errorf("runtime_version = %q, want a .NET 10 runtime (R2.2 pins the SDK major in regen.sh)", oracle.RuntimeVersion)
	}
	wantCulture := "CultureInvariant (RegexOptions.CultureInvariant; CurrentCulture pinned to InvariantCulture)"
	if oracle.Culture != wantCulture {
		t.Errorf("culture = %q, want %q", oracle.Culture, wantCulture)
	}
	wantEngine := "backtracking (NonBacktracking never used; lookarounds required)"
	if oracle.Engine != wantEngine {
		t.Errorf("engine = %q, want %q", oracle.Engine, wantEngine)
	}
	if got, want := oracle.TimeoutSeconds, 5.0; got != want {
		t.Errorf("timeout_seconds = %v, want %v (explicit match timeout, R2.2)", got, want)
	}
	for _, res := range oracle.Results {
		if got, want := res.Options, "IgnoreCase, CultureInvariant"; got != want {
			t.Errorf("%s: options = %q, want %q (the RegexOptions every source pattern runs under)", res.PatternID, got, want)
		}
	}
}

// compareReleaseGroupObservable checks the consumed capture observable for
// the release-group pattern: the last match's m[1], falling back to m[3]
// (mirroring ParseReleaseGroup's consumption).
func compareReleaseGroupObservable(t *testing.T, contracts map[string]contractEntry, p *Pattern, name string, hit *oracleNameHits) {
	t.Helper()
	last := hit.Matches[len(hit.Matches)-1]
	wantVal := func(g int) string {
		if g < len(last.Groups) && last.Groups[g].Success && last.Groups[g].Value != nil {
			return *last.Groups[g].Value
		}
		return ""
	}
	want := wantVal(1)
	if want == "" {
		want = wantVal(3)
	}

	m := p.FindStringSubmatch(name)
	got := ""
	if m != nil {
		if m[1] != "" {
			got = m[1]
		} else if len(m) > 3 && m[3] != "" {
			got = m[3]
		}
	}
	if got != want {
		key := "releasegroup/captures\x00" + name
		if c, ok := contracts[key]; ok {
			t.Logf("classified capture divergence (class %s): %q: layer=%q oracle=%q (%s)",
				c.Class, name, got, want, c.Reason)
			return
		}
		t.Errorf("UNCLASSIFIED capture divergence: releasegroup on %q: layer group=%q, oracle group=%q",
			name, got, want)
	}
}
