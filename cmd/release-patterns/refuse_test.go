package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fixturePin  = "1111111111111111111111111111111111111111"
	fixtureRepo = "Arr/Arr"
	trashRepo   = "Trash/Guides"
	parserPath  = "src/Parser/QualityParser.cs"
	enumPath    = "src/Qualities/QualitySource.cs"
	cfPath      = "docs/json/sonarr/cf/amzn.json"
	cfTrashID   = "d660701077794679fd59e8bdf4ce3a29"
	fixtureTest = "src/Test/QualityParserFixture.cs"
)

// fixtureParser is a QualityParser.cs in the arrs' shape: a free-spacing
// SourceRegex, and a pattern with a backreference, which the executor does
// not run and the fixture's override covers.
const fixtureParser = `namespace Arr
{
    public static class QualityParser
    {
        // the source table
        private static readonly Regex SourceRegex = new(@"\b(?:
                (?<bluray>BluRay|Blu-Ray)|
                (?<webdl>WEB[-_. ]DL|WEBDL)
                )\b",
            RegexOptions.Compiled | RegexOptions.IgnoreCase | RegexOptions.IgnorePatternWhitespace);

        private static readonly Regex GroupRegex = new(@"-(?<releasegroup>\w+)-\k<releasegroup>$",
            RegexOptions.IgnoreCase | RegexOptions.Compiled);
    }
}
`

const fixtureEnum = `namespace Arr
{
    public enum QualitySource
    {
        Unknown,
        Web
    }
}
`

const fixtureCF = `{
  "trash_id": "d660701077794679fd59e8bdf4ce3a29",
  "name": "AMZN",
  "specifications": [
    {"name": "Amazon", "implementation": "ReleaseTitleSpecification", "negate": false, "required": true,
     "fields": {"value": "\\b(amzn|amazon(hd)?)\\b"}},
    {"name": "WEBDL", "implementation": "SourceSpecification", "negate": false, "required": false,
     "fields": {"value": 1}}
  ]
}
`

// fixtureTests is a parser test file in the arrs' NUnit shape.
const fixtureTests = `namespace Arr.Test
{
    public class QualityParserFixture
    {
        [TestCase("Show.S01E01.WEB-DL", false)]
        public void should_parse_webdl(string title, bool proper)
        {
            ParseAndVerifyQuality(title, Quality.WEBDL, proper);
        }
    }
}
`

// fixtureOverrides holds the GroupRegex override, written from its upstream
// text, and extra, which the "override unused" case names nowhere.
func fixtureOverrides(extra string) string {
	src := "package release\n\nvar overrideGroup = override{upstream: \"" + patternSHA256(`-(?<releasegroup>\w+)-\k<releasegroup>$`) + "\"}\n"
	if extra != "" {
		src += "\nvar " + extra + " = override{upstream: \"" + patternSHA256("gone") + "\"}\n"
	}
	return src
}

func fixtureManifest() map[string]any {
	return map[string]any{
		"version": 1,
		"upstreams": map[string]any{
			"trash":  map[string]any{"repo": trashRepo, "pin": "trashCommit"},
			"sonarr": map[string]any{"repo": fixtureRepo, "pin": "sonarrCommit"},
		},
		"symbols": []any{
			map[string]any{
				"id": "source/episode", "upstream": "sonarr", "path": parserPath, "symbol": "SourceRegex",
				"kind": "regex", "options": []string{"IgnoreCase", "IgnorePatternWhitespace"},
				"groups": map[string]string{"bluray": "BluRay", "webdl": "WebDL"},
			},
			map[string]any{
				"id": "group/release/episode", "upstream": "sonarr", "path": parserPath, "symbol": "GroupRegex",
				"kind": "regex", "options": []string{"IgnoreCase"}, "override": "overrideGroup",
			},
			map[string]any{
				"id": "source-enum/episode", "upstream": "sonarr", "path": enumPath, "symbol": "QualitySource",
				"kind": "enum", "members": map[string][]string{"Unknown": {"SourceUnknown"}, "Web": {"WebDL"}},
			},
		},
		"custom_formats": []any{
			map[string]any{"id": "streaming/amzn", "label": "AMZN", "files": map[string]any{
				"sonarr": map[string]any{"path": cfPath, "trash_id": cfTrashID, "specs": []string{"Amazon", "WEBDL"}},
			}},
		},
		"fixtures": []any{
			map[string]any{"id": "quality/episode", "upstream": "sonarr", "path": fixtureTest},
		},
	}
}

// fixture is one fake upstream and one package directory to generate into.
type fixture struct {
	dir      string
	files    map[string]string // "<repo>/<path>" -> body
	statuses map[string]int
	manifest map[string]any
	pins     map[string]string
	extra    string // an override no entry names
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{
		dir: t.TempDir(),
		files: map[string]string{
			fixtureRepo + "/" + parserPath:  fixtureParser,
			fixtureRepo + "/" + enumPath:    fixtureEnum,
			trashRepo + "/" + cfPath:        fixtureCF,
			fixtureRepo + "/" + fixtureTest: fixtureTests,
		},
		statuses: map[string]int{},
		manifest: fixtureManifest(),
		pins:     map[string]string{"trashCommit": fixturePin, "sonarrCommit": fixturePin},
	}
}

func (f *fixture) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rest, ok := strings.CutPrefix(r.URL.Path, "/api/repos/"); ok {
			f.list(w, rest)
			return
		}
		key := strings.Replace(strings.TrimPrefix(r.URL.Path, "/raw/"), "/"+fixturePin+"/", "/", 1)
		if code := f.statuses[key]; code != 0 {
			w.WriteHeader(code)
			return
		}
		body, ok := f.files[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// list answers the contents API for one directory.
func (f *fixture) list(w http.ResponseWriter, rest string) {
	repo, dir, _ := strings.Cut(rest, "/contents/")
	type entry struct{ Path, Type string }
	var entries []entry
	for key := range f.files {
		p, ok := strings.CutPrefix(key, repo+"/")
		if ok && path.Dir(p) == dir {
			entries = append(entries, entry{Path: p, Type: "file"})
		}
	}
	_ = json.NewEncoder(w).Encode(entries)
}

func (f *fixture) write(t *testing.T) {
	t.Helper()
	b, err := json.Marshal(f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	src.WriteString("package release\n\n")
	for _, name := range []string{"trashCommit", "sonarrCommit"} {
		src.WriteString("const " + name + " = \"" + f.pins[name] + "\"\n")
	}
	files := map[string]string{
		manifestFile: string(b),
		sourceFile:   src.String(),
		typesFile:    "package release\n\ntype Source uint8\n\nconst (\n\tSourceUnknown Source = iota\n\tBluRay\n\tWebDL\n)\n",
		overrideFile: fixtureOverrides(f.extra),
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// generate runs the command against the fake upstream.
func (f *fixture) generate(t *testing.T) (code int, stderr string) {
	t.Helper()
	srv := f.serve(t)
	var out, errOut bytes.Buffer
	code = run(t.Context(), []string{"generate", "-dir", f.dir, "-raw", srv.URL + "/raw", "-api", srv.URL + "/api"}, &out, &errOut)
	return code, errOut.String()
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		files[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestGenerateWritesTheTable(t *testing.T) {
	f := newFixture(t)
	f.write(t)
	code, stderr := f.generate(t)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	gen, err := os.ReadFile(filepath.Join(f.dir, generated))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`id: "source/episode"`, `SourceRegex, as published`, `override: &overrideGroup`,
		`(?<bluray>BluRay|Blu-Ray)|\n                (?<webdl>WEB[-_. ]DL|WEBDL)\n`,
		`options: netregex.Options{IgnoreCase: true, IgnorePatternWhitespace: true}`,
		"pattern: `\\b(amzn|amazon(hd)?)\\b`,",
		`sources: []Source{WebDL}`, `// members Unknown=0, Web=1`,
	} {
		if !strings.Contains(string(gen), want) {
			t.Errorf("patterns_gen.go lacks %s:\n%s", want, gen)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, upstreamFile)); err != nil {
		t.Errorf("no upstream snapshot: %v", err)
	}
	fixtures, err := os.ReadFile(filepath.Join(f.dir, fixturesFile))
	if err != nil {
		t.Fatal(err)
	}
	const wantCase = `{"name": "should_parse_webdl", "body": ["ParseAndVerifyQuality","title","Quality.WEBDL","proper"], "cases": [
      ["Show.S01E01.WEB-DL",{"code":"false"}]`
	if !strings.Contains(string(fixtures), wantCase) {
		t.Errorf("fixtures.json lacks the test case:\n%s", fixtures)
	}
}

// TestImportRefusesDrift has one subtest per refusal: generate exits
// non-zero, names the reason and leaves every byte of the package directory
// as it was.
func TestImportRefusesDrift(t *testing.T) {
	setSymbol := func(f *fixture, i int, key string, v any) {
		f.manifest["symbols"].([]any)[i].(map[string]any)[key] = v
	}
	cfFile := func(f *fixture) map[string]any {
		formats := f.manifest["custom_formats"].([]any)
		return formats[0].(map[string]any)["files"].(map[string]any)["sonarr"].(map[string]any)
	}
	cases := []struct {
		name   string
		change func(*fixture)
		want   string
	}{
		{"pin not 40 hex", func(f *fixture) { f.pins["sonarrCommit"] = "develop" }, `pin "develop" is not a 40-hex commit`},
		{"fetch not 200", func(f *fixture) { f.statuses[fixtureRepo+"/"+enumPath] = http.StatusBadGateway }, "status 502"},
		{"cf path missing", func(f *fixture) { delete(f.files, trashRepo+"/"+cfPath) }, "streaming/amzn: " + cfPath + " missing"},
		{"cf path moved", func(f *fixture) {
			f.files[trashRepo+"/docs/json/sonarr/cf/amazon.json"] = f.files[trashRepo+"/"+cfPath]
			delete(f.files, trashRepo+"/"+cfPath)
		}, "missing: moved to docs/json/sonarr/cf/amazon.json"},
		{"cf replaced", func(f *fixture) { cfFile(f)["trash_id"] = "00000000000000000000000000000000" }, "replaced upstream"},
		{"cf composition changed", func(f *fixture) { cfFile(f)["specs"] = []string{"Amazon", "WEBDL", "WEBRIP"} }, "composition changed; review the CF and update the manifest"},
		{"source value outside enum", func(f *fixture) {
			f.files[trashRepo+"/"+cfPath] = strings.Replace(fixtureCF, `"value": 1}`, `"value": 7}`, 1)
		}, "Source value 7 is outside the arr's enum"},
		{"symbol renamed", func(f *fixture) {
			f.files[fixtureRepo+"/"+parserPath] = strings.Replace(fixtureParser, "SourceRegex", "SourceRegexV2", 1)
		}, "SourceRegex: renamed or removed"},
		{"source groups changed", func(f *fixture) {
			f.files[fixtureRepo+"/"+parserPath] = strings.Replace(fixtureParser, `(?<webdl>WEB[-_. ]DL|WEBDL)`, `(?<webdl>WEB[-_. ]DL|WEBDL)|(?<hdtv>HDTV)`, 1)
		}, "named groups changed: upstream [bluray hdtv webdl], manifest [bluray webdl]"},
		{"override hash changed", func(f *fixture) {
			f.files[fixtureRepo+"/"+parserPath] = strings.Replace(fixtureParser, `\w+)-`, `\w*)-`, 1)
		}, "update override overrideGroup"},
		{"override unused", func(f *fixture) { f.extra = "overrideGone" }, "overrideGone: serves no manifest entry"},
		{"override missing", func(f *fixture) { setSymbol(f, 1, "override", "overrideNone") }, "no override overrideNone in overrides.go"},
		{"override declares groups", func(f *fixture) { setSymbol(f, 1, "captures", []string{"releasegroup"}) }, "an override's groups are pinned by its hash, not declared"},
		{"unsupported pattern", func(f *fixture) {
			f.files[fixtureRepo+"/"+parserPath] = strings.Replace(fixtureParser, `(?<bluray>BluRay|Blu-Ray)`, `(?<bluray>(?>BluRay)|Blu-Ray)`, 1)
		}, "the executor does not run (?>B: add an override"},
		{"group in a loop that can match empty", func(f *fixture) {
			f.files[fixtureRepo+"/"+parserPath] = strings.Replace(fixtureParser, `(?<bluray>BluRay|Blu-Ray)`, `(?<bluray>BluRay|Blu-Ray|)+`, 1)
		}, "the executor does not run a named group in a loop that can match empty: add an override"},
		{"unsupported option", func(f *fixture) {
			f.files[fixtureRepo+"/"+parserPath] = strings.Replace(fixtureParser, "RegexOptions.IgnoreCase | RegexOptions.Compiled);", "RegexOptions.IgnoreCase | RegexOptions.Multiline);", 1)
		}, "RegexOptions.Multiline is not supported"},
		{"unsupported cf pattern", func(f *fixture) {
			f.files[trashRepo+"/"+cfPath] = strings.Replace(fixtureCF, `amazon(hd)?`, `amazon(hd)?\\1`, 1)
		}, "the executor does not run a backreference"},
		{"options changed", func(f *fixture) { setSymbol(f, 0, "options", []string{"IgnoreCase"}) }, "options changed"},
		{"fixture missing", func(f *fixture) { delete(f.files, fixtureRepo+"/"+fixtureTest) }, "quality/episode: fetch"},
		{"fixture has no test case", func(f *fixture) {
			f.files[fixtureRepo+"/"+fixtureTest] = strings.Replace(fixtureTests, "[TestCase(", "[Ignore(", 1)
		}, "no [TestCase] method: renamed or removed"},
		{"enum members changed", func(f *fixture) {
			f.files[fixtureRepo+"/"+enumPath] = strings.Replace(fixtureEnum, "Web\n", "Web,\n        WebRip\n", 1)
		}, "members changed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			c.change(f)
			f.write(t)
			before := snapshot(t, f.dir)
			code, stderr := f.generate(t)
			if code == 0 {
				t.Errorf("exit 0, want a refusal")
			}
			if !strings.Contains(stderr, c.want) {
				t.Errorf("stderr %q lacks %q", stderr, c.want)
			}
			if after := snapshot(t, f.dir); !maps.Equal(after, before) {
				t.Errorf("generate wrote files despite refusing")
			}
		})
	}
}
