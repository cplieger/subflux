package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
)

const (
	pinP     = "1111111111111111111111111111111111111111"
	commitM  = "2222222222222222222222222222222222222222"
	commitH  = "3333333333333333333333333333333333333333"
	sonarrRp = "Sonarr/Sonarr"
	radarrRp = "Radarr/Radarr"
	sonarrCF = "docs/json/sonarr/cf"
	radarrCF = "docs/json/radarr/cf"
)

const watchParser = `namespace Arr
{
    public static class QualityParser
    {
        // the source table
        private static readonly Regex SourceRegex = new(@"\b(?:(?<webdl>WEB[-_. ]DL))\b",
            RegexOptions.Compiled | RegexOptions.IgnoreCase);

        public static string Parse(string name)
        {
            return name.Trim();
        }
    }
}
`

const watchEnum = `namespace Arr
{
    public enum QualitySource
    {
        Unknown,
        Web
    }
}
`

func watchManifest() map[string]any {
	enum := func(arr string) map[string]any {
		return map[string]any{
			"id": "source-enum/" + arr, "upstream": arr, "path": enumPath, "symbol": "QualitySource",
			"kind": "enum", "members": map[string][]string{"Unknown": {"SourceUnknown"}, "Web": {"WebDL"}},
		}
	}
	cf := func(dir, stem string) map[string]any {
		return map[string]any{"path": dir + "/" + stem + ".json", "trash_id": stem + "-id", "specs": []string{"Name"}}
	}
	return map[string]any{
		"version": 1,
		"upstreams": map[string]any{
			"trash":  map[string]any{"repo": trashRepo, "pin": "trashCommit"},
			"sonarr": map[string]any{"repo": sonarrRp, "pin": "sonarrCommit"},
			"radarr": map[string]any{"repo": radarrRp, "pin": "radarrCommit"},
		},
		"symbols": []any{
			map[string]any{
				"id": "source/episode", "upstream": "sonarr", "path": parserPath, "symbol": "SourceRegex",
				"kind": "regex", "options": []string{"IgnoreCase"}, "groups": map[string]string{"webdl": "WebDL"},
			},
			enum("sonarr"), enum("radarr"),
		},
		"custom_formats": []any{
			map[string]any{"id": "streaming/amzn", "label": "AMZN", "files": map[string]any{
				"sonarr": cf(sonarrCF, "amzn"), "radarr": cf(radarrCF, "amzn"),
			}},
			map[string]any{"id": "streaming/funi", "label": "FUNI", "files": map[string]any{
				"sonarr": cf(sonarrCF, "funi"),
			}},
		},
		"watch": map[string]any{"classes": []any{
			map[string]any{"key": "audio", "title": "audio", "reason": "audio does not move timing.", "stems": []string{"dts*", "aac"}},
		}},
	}
}

// cfJSON is a CF file with one ReleaseTitleSpecification.
func cfJSON(id, value string, score int) string {
	b, _ := json.Marshal(map[string]any{
		"trash_id": id, "trash_scores": map[string]int{"default": score}, "name": strings.ToUpper(id),
		"specifications": []any{map[string]any{
			"name": "Name", "implementation": "ReleaseTitleSpecification", "negate": false, "required": true,
			"fields": map[string]any{"value": value},
		}},
	})
	return string(b)
}

// watchUpstream is a fake GitHub over commits P, M and H.
type watchUpstream struct {
	mu      sync.Mutex
	heads   map[string]string // repo -> head
	files   map[string]string // repo@ref/path -> body
	apiAuth []string
	rawAuth []string
}

func newWatchUpstream() *watchUpstream {
	u := &watchUpstream{
		heads: map[string]string{trashRepo: commitH, sonarrRp: commitH, radarrRp: commitH},
		files: map[string]string{},
	}
	for _, ref := range []string{pinP, commitM, commitH} {
		for _, repo := range []string{sonarrRp, radarrRp} {
			u.put(repo, ref, enumPath, watchEnum)
		}
		u.put(sonarrRp, ref, parserPath, watchParser)
		u.put(trashRepo, ref, sonarrCF+"/amzn.json", cfJSON("amzn-id", `\bamzn\b`, 1))
		u.put(trashRepo, ref, radarrCF+"/amzn.json", cfJSON("amzn-id", `\bamzn\b`, 1))
		u.put(trashRepo, ref, sonarrCF+"/funi.json", cfJSON("funi-id", `\bfuni\b`, 1))
		u.put(trashRepo, ref, sonarrCF+"/hfr.json", cfJSON("hfr-id", `\bHFR\b`, 1))
		u.put(trashRepo, ref, radarrCF+"/hfr.json", cfJSON("hfr-id", `\bHFR\b`, 1))
		u.put(trashRepo, ref, sonarrCF+"/dts-x.json", cfJSON("dtsx-id", `\bDTS-X\b`, 1))
		u.put(trashRepo, ref, sonarrCF+"/aac.json", cfJSON("aac-id", `\bAAC\b`, 1))
	}
	return u
}

func (u *watchUpstream) put(repo, ref, p, body string) { u.files[repo+"@"+ref+"/"+p] = body }

// atHead sets a file at H, or deletes it for an empty body.
func (u *watchUpstream) atHead(repo, p, body string) {
	if body == "" {
		delete(u.files, repo+"@"+commitH+"/"+p)
		return
	}
	u.put(repo, commitH, p, body)
}

func (u *watchUpstream) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		if rest, ok := strings.CutPrefix(r.URL.Path, "/api/repos/"); ok {
			u.apiAuth = append(u.apiAuth, r.Header.Get("Authorization"))
			u.api(w, r, rest)
			return
		}
		u.rawAuth = append(u.rawAuth, r.Header.Get("Authorization"))
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/raw/"), "/", 4)
		if len(parts) < 4 {
			http.NotFound(w, r)
			return
		}
		body, ok := u.files[parts[0]+"/"+parts[1]+"@"+parts[2]+"/"+parts[3]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (u *watchUpstream) api(w http.ResponseWriter, r *http.Request, rest string) {
	if repo, _, ok := strings.Cut(rest, "/commits/"); ok {
		if r.Header.Get("Accept") != "application/vnd.github.sha" {
			http.Error(w, "accept", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(u.heads[repo]))
		return
	}
	repo, dir, _ := strings.Cut(rest, "/contents/")
	prefix := repo + "@" + r.URL.Query().Get("ref") + "/"
	entries := []dirEntry{}
	for key, body := range u.files {
		if p, ok := strings.CutPrefix(key, prefix); ok && path.Dir(p) == dir {
			sum := sha256.Sum256([]byte(body))
			entries = append(entries, dirEntry{Path: p, Type: "file", SHA: hex.EncodeToString(sum[:20])})
		}
	}
	_ = json.NewEncoder(w).Encode(entries)
}

// watchDir writes the manifest and patterns_source.go with every pin at P.
func watchDir(t *testing.T, manifest map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	src := "package release\n"
	for _, p := range []struct{ name, repo, branch string }{
		{"trashCommit", trashRepo, "master"}, {"sonarrCommit", sonarrRp, "develop"}, {"radarrCommit", radarrRp, "develop"},
	} {
		src += fmt.Sprintf("\n// renovate: datasource=git-refs depName=x packageName=https://github.com/%s branch=%s\nconst %s = %q\n", p.repo, p.branch, p.name, pinP)
	}
	for name, body := range map[string][]byte{manifestFile: b, sourceFile: []byte(src)} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runWatchPlan runs watch over issues and returns its plan.
func runWatchPlan(t *testing.T, u *watchUpstream, issues []watchIssue) []action {
	t.Helper()
	actions, err := tryWatch(t, u, watchManifest(), issues, "")
	if err != nil {
		t.Fatal(err)
	}
	return actions
}

func tryWatch(t *testing.T, u *watchUpstream, manifest map[string]any, issues []watchIssue, token string) ([]action, error) {
	t.Helper()
	out, err := watchPlanFile(t, u, manifest, issues, token)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var actions []action
	if err := json.Unmarshal(b, &actions); err != nil {
		t.Fatal(err)
	}
	return actions, nil
}

func watchPlanFile(t *testing.T, u *watchUpstream, manifest map[string]any, issues []watchIssue, token string) (string, error) {
	t.Helper()
	dir := watchDir(t, manifest)
	var lines bytes.Buffer
	for _, is := range issues {
		b, _ := json.Marshal(is)
		lines.Write(append(b, '\n'))
	}
	existing, out := filepath.Join(dir, "existing.jsonl"), filepath.Join(dir, "actions.json")
	if err := os.WriteFile(existing, lines.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := u.serve(t)
	up := newUpstream(srv.URL+"/raw", srv.URL+"/api", token)
	if err := watch(t.Context(), dir, existing, out, up, &bytes.Buffer{}); err != nil {
		return "", err
	}
	return out, nil
}

// applyIssues runs scripts/apply-issues.sh on plan with a gh that records
// its argv to the returned log and fails.
func applyIssues(t *testing.T, plan string) (output []byte, ghLog string, err error) {
	t.Helper()
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("apply-issues.sh needs %s: %v", tool, err)
		}
	}
	bin := t.TempDir()
	ghLog = filepath.Join(bin, "gh.log")
	stub := fmt.Sprintf("#!/usr/bin/env bash\nprintf '%%s\\n' \"$*\" >>%q\nexit 1\n", ghLog)
	syscall.ForkLock.RLock() // golang/go#22315: a concurrent fork holds the fd and exec fails with ETXTBSY
	err = os.WriteFile(filepath.Join(bin, "gh"), []byte(stub), 0o700)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/apply-issues.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "bash", script, plan)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GITHUB_REPOSITORY=owner/repo")
	output, err = cmd.CombinedOutput()
	return output, ghLog, err
}

func keyOfAction(a *action) string {
	body := strings.TrimRight(a.Body, "\n")
	m := markerRE.FindStringSubmatch(body[strings.LastIndexByte(body, '\n')+1:])
	if m == nil {
		return ""
	}
	return m[1] + " " + m[2] + " " + m[3]
}

func summary(actions []action) []string {
	out := make([]string, len(actions))
	for i := range actions {
		out[i] = fmt.Sprintf("%s %d %s", actions[i].Kind, actions[i].Number, keyOfAction(&actions[i]))
	}
	return out
}

func TestWatchPlansNothingWithoutAChange(t *testing.T) {
	t.Run("heads at the pins", func(t *testing.T) {
		u := newWatchUpstream()
		for repo := range u.heads {
			u.heads[repo] = pinP
		}
		if got := runWatchPlan(t, u, nil); len(got) != 0 {
			t.Errorf("got %v", summary(got))
		}
	})
	t.Run("heads moved, files the same", func(t *testing.T) {
		if got := runWatchPlan(t, newWatchUpstream(), nil); len(got) != 0 {
			t.Errorf("got %v", summary(got))
		}
	})
	t.Run("the plan is an empty array apply-issues.sh accepts", func(t *testing.T) {
		plan, err := watchPlanFile(t, newWatchUpstream(), watchManifest(), nil, "")
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(plan)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "[]\n" {
			t.Errorf("plan file %q, want %q", b, "[]\n")
		}
		out, ghLog, err := applyIssues(t, plan)
		if err != nil {
			t.Errorf("apply-issues.sh: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "applied 0 action(s)") {
			t.Errorf("apply-issues.sh output:\n%s", out)
		}
		if calls, err := os.ReadFile(ghLog); err == nil {
			t.Errorf("apply-issues.sh ran gh:\n%s", calls)
		}
	})
}

func TestWatchCandidateChange(t *testing.T) {
	u := newWatchUpstream()
	u.atHead(trashRepo, radarrCF+"/hfr.json", cfJSON("hfr-id", `\b(HFR|HighFrameRate)\b`, 1))
	got := runWatchPlan(t, u, nil)
	if want := []string{"create 0 trash:cf:hfr " + pinP + " " + commitH}; !slices.Equal(summary(got), want) {
		t.Fatalf("got %v, want %v", summary(got), want)
	}
	a := got[0]
	t.Run("title and labels", func(t *testing.T) {
		if a.Title != "TRaSH custom format hfr changed (not imported)" || !slices.Equal(a.Labels, []string{"upstream-watch", "upstream: trash", "candidate"}) {
			t.Errorf("got %q %v", a.Title, a.Labels)
		}
	})
	t.Run("body names the spec change and the radarr file only", func(t *testing.T) {
		want := "- `Name` (`ReleaseTitleSpecification`, required): value changed from `\\bHFR\\b` to `\\b(HFR|HighFrameRate)\\b`."
		if !strings.Contains(a.Body, want) || !strings.Contains(a.Body, "**Radarr**") || strings.Contains(a.Body, "**Sonarr**") {
			t.Errorf("body:\n%s", a.Body)
		}
	})
	t.Run("body links the compare", func(t *testing.T) {
		if !strings.Contains(a.Body, "https://github.com/"+trashRepo+"/compare/"+pinP+"..."+commitH) {
			t.Errorf("body:\n%s", a.Body)
		}
	})
}

func TestWatchIgnoresScoresAndImportedFormats(t *testing.T) {
	u := newWatchUpstream()
	u.atHead(trashRepo, sonarrCF+"/hfr.json", cfJSON("hfr-id", `\bHFR\b`, 900))
	u.atHead(trashRepo, sonarrCF+"/amzn.json", cfJSON("amzn-id", `\bamazon\b`, 1))
	if got := runWatchPlan(t, u, nil); len(got) != 0 {
		t.Errorf("got %v", summary(got))
	}
}

func TestWatchNewFileUnderAnImportedStem(t *testing.T) {
	u := newWatchUpstream()
	u.atHead(trashRepo, radarrCF+"/funi.json", cfJSON("funi-r-id", `\bfuni\b`, 1))
	u.atHead(trashRepo, sonarrCF+"/funi.json", cfJSON("funi-id", `\bfunimation\b`, 1))
	got := runWatchPlan(t, u, nil)
	if want := []string{"create 0 trash:new:funi " + pinP + " " + commitH}; !slices.Equal(summary(got), want) {
		t.Fatalf("got %v, want %v", summary(got), want)
	}
	t.Run("lists the new file", func(t *testing.T) {
		if !strings.Contains(got[0].Body, "- New file.\n- `Name` (`ReleaseTitleSpecification`, required): added, value `\\bfuni\\b`.") {
			t.Errorf("body:\n%s", got[0].Body)
		}
	})
	t.Run("leaves out the imported file's change", func(t *testing.T) {
		if strings.Contains(got[0].Body, "**Sonarr**") {
			t.Errorf("body:\n%s", got[0].Body)
		}
	})
}

func TestWatchClassDigest(t *testing.T) {
	u := newWatchUpstream()
	u.atHead(trashRepo, sonarrCF+"/dts-x.json", cfJSON("dtsx-id", `\bDTS[-.]?X\b`, 1))
	u.atHead(trashRepo, sonarrCF+"/aac.json", "")
	got := runWatchPlan(t, u, nil)
	if want := []string{"create 0 trash:class:audio " + pinP + " " + commitH}; !slices.Equal(summary(got), want) {
		t.Fatalf("got %v, want %v", summary(got), want)
	}
	a := got[0]
	t.Run("one issue lists both stems", func(t *testing.T) {
		if !strings.Contains(a.Body, "aac.json") || !strings.Contains(a.Body, "- Removed.") || !strings.Contains(a.Body, "dts-x.json") {
			t.Errorf("body:\n%s", a.Body)
		}
	})
	t.Run("labelled out of model", func(t *testing.T) {
		if a.Title != "TRaSH audio formats changed (digest)" || a.Labels[2] != "out-of-model" {
			t.Errorf("got %q %v", a.Title, a.Labels)
		}
	})
}

func TestWatchRenameIsOneMove(t *testing.T) {
	u := newWatchUpstream()
	u.atHead(trashRepo, sonarrCF+"/hfr.json", "")
	u.atHead(trashRepo, sonarrCF+"/high-frame-rate.json", cfJSON("hfr-id", `\bHFR\b`, 1))
	got := runWatchPlan(t, u, nil)
	if want := []string{"create 0 trash:cf:high-frame-rate " + pinP + " " + commitH}; !slices.Equal(summary(got), want) {
		t.Fatalf("got %v, want %v", summary(got), want)
	}
	if !strings.Contains(got[0].Body, "- Moved from ["+sonarrCF+"/hfr.json](") || strings.Contains(got[0].Body, "added") {
		t.Errorf("body:\n%s", got[0].Body)
	}
}

func TestWatchLogicOutsideImportedSymbols(t *testing.T) {
	t.Run("an imported pattern, a comment and indentation change nothing", func(t *testing.T) {
		u := newWatchUpstream()
		src := strings.Replace(watchParser, `(?<webdl>WEB[-_. ]DL)`, `(?<webdl>WEB[-_. ]?DL)`, 1)
		src = strings.Replace(src, "// the source table", "// the sources", 1)
		src = strings.Replace(src, "            return name.Trim();", "                return  name.Trim();", 1)
		u.atHead(sonarrRp, parserPath, src)
		if got := runWatchPlan(t, u, nil); len(got) != 0 {
			t.Errorf("got %v", summary(got))
		}
	})
	t.Run("code outside them is a candidate", func(t *testing.T) {
		u := newWatchUpstream()
		u.atHead(sonarrRp, parserPath, strings.Replace(watchParser, "return name.Trim();", "return name.Trim().ToLower();", 1))
		got := runWatchPlan(t, u, nil)
		if want := []string{"create 0 sonarr:QualityParser.cs:logic " + pinP + " " + commitH}; !slices.Equal(summary(got), want) {
			t.Fatalf("got %v, want %v", summary(got), want)
		}
		a := got[0]
		if !strings.Contains(a.Body, "```diff\n- return name.Trim();\n+ return name.Trim().ToLower();\n```") {
			t.Errorf("body:\n%s", a.Body)
		}
		if a.Title != "Sonarr QualityParser.cs logic changed outside the imported patterns" || !slices.Equal(a.Labels, []string{"upstream-watch", "upstream: sonarr", "candidate"}) {
			t.Errorf("got %q %v", a.Title, a.Labels)
		}
	})
	t.Run("a removed file is reported", func(t *testing.T) {
		u := newWatchUpstream()
		u.atHead(sonarrRp, parserPath, "")
		got := runWatchPlan(t, u, nil)
		if len(got) != 1 || !strings.Contains(got[0].Body, "The file is gone at 3333333: removed or moved.") {
			t.Errorf("got %v", summary(got))
		}
	})
}

func hfrIssue(number int, state, since, through string) watchIssue {
	return watchIssue{Number: number, State: state, Title: "t", Body: "old\n\n" + marker("trash:cf:hfr", since, through) + "\n"}
}

func TestWatchIssueLifecycle(t *testing.T) {
	changed := func() *watchUpstream {
		u := newWatchUpstream()
		u.atHead(trashRepo, radarrCF+"/hfr.json", cfJSON("hfr-id", `\bHFR2\b`, 1))
		return u
	}
	t.Run("an issue through the head is left alone", func(t *testing.T) {
		if got := runWatchPlan(t, changed(), []watchIssue{hfrIssue(7, "open", pinP, commitH)}); len(got) != 0 {
			t.Errorf("got %v", summary(got))
		}
	})
	t.Run("an open issue is edited from its since", func(t *testing.T) {
		got := runWatchPlan(t, changed(), []watchIssue{hfrIssue(7, "open", pinP, commitM)})
		if want := []string{"edit 7 trash:cf:hfr " + pinP + " " + commitH}; !slices.Equal(summary(got), want) {
			t.Errorf("got %v, want %v", summary(got), want)
		}
	})
	t.Run("a closed issue reopens from its through", func(t *testing.T) {
		got := runWatchPlan(t, changed(), []watchIssue{hfrIssue(7, "closed", pinP, commitM)})
		if want := []string{"reopen 7 trash:cf:hfr " + commitM + " " + commitH}; !slices.Equal(summary(got), want) {
			t.Errorf("got %v, want %v", summary(got), want)
		}
	})
	t.Run("nothing new since the through", func(t *testing.T) {
		u := changed()
		u.put(trashRepo, commitM, radarrCF+"/hfr.json", cfJSON("hfr-id", `\bHFR2\b`, 1))
		if got := runWatchPlan(t, u, []watchIssue{hfrIssue(7, "closed", pinP, commitM)}); len(got) != 0 {
			t.Errorf("got %v", summary(got))
		}
	})
	t.Run("an open issue outranks a newer closed one", func(t *testing.T) {
		got := runWatchPlan(t, changed(), []watchIssue{hfrIssue(9, "closed", pinP, commitM), hfrIssue(7, "open", pinP, commitM)})
		if want := []string{"edit 7 trash:cf:hfr " + pinP + " " + commitH}; !slices.Equal(summary(got), want) {
			t.Errorf("got %v, want %v", summary(got), want)
		}
	})
	t.Run("a stem only the issue's through had", func(t *testing.T) {
		u := newWatchUpstream()
		u.put(trashRepo, commitM, sonarrCF+"/gone.json", cfJSON("gone-id", "x", 1))
		is := watchIssue{Number: 4, State: "open", Body: marker("trash:cf:gone", pinP, commitM)}
		got := runWatchPlan(t, u, []watchIssue{is})
		if want := []string{"edit 4 trash:cf:gone " + pinP + " " + commitH}; !slices.Equal(summary(got), want) {
			t.Fatalf("got %v, want %v", summary(got), want)
		}
		if !strings.Contains(got[0].Body, "Net of these commits, nothing watched differs from 1111111.") {
			t.Errorf("body:\n%s", got[0].Body)
		}
	})
	t.Run("only the last line is a marker", func(t *testing.T) {
		quoted := hfrIssue(7, "open", pinP, commitH)
		quoted.Body += "a quoted line after it\n"
		got := runWatchPlan(t, changed(), []watchIssue{quoted})
		if want := []string{"create 0 trash:cf:hfr " + pinP + " " + commitH}; !slices.Equal(summary(got), want) {
			t.Errorf("got %v, want %v", summary(got), want)
		}
	})
}

func TestWatchFloodLimit(t *testing.T) {
	u := newWatchUpstream()
	for i := range maxOpens + 2 {
		u.atHead(trashRepo, sonarrCF+fmt.Sprintf("/new-%02d.json", i), cfJSON(fmt.Sprintf("n%d", i), "x", 1))
	}
	t.Run("ten open, the rest go to the bulk issue", func(t *testing.T) {
		got := runWatchPlan(t, u, nil)
		if len(got) != maxOpens+1 {
			t.Fatalf("got %v", summary(got))
		}
		bulk := got[maxOpens]
		if keyOfAction(&bulk) != "bulk  " || bulk.Kind != actCreate || !strings.Contains(bulk.Body, "new-10 changed") || !strings.Contains(bulk.Body, "new-11 changed") {
			t.Errorf("bulk: %+v", bulk)
		}
	})
	t.Run("an open bulk issue is edited", func(t *testing.T) {
		got := runWatchPlan(t, u, []watchIssue{{Number: 3, State: "open", Body: marker(bulkKey, "", "")}})
		if bulk := got[len(got)-1]; bulk.Kind != actEdit || bulk.Number != 3 || bulk.Labels != nil {
			t.Errorf("bulk: %+v", bulk)
		}
	})
}

func TestWatchTokenReachesTheAPIOnly(t *testing.T) {
	u := newWatchUpstream()
	u.atHead(trashRepo, radarrCF+"/hfr.json", cfJSON("hfr-id", "y", 1))
	if _, err := tryWatch(t, u, watchManifest(), nil, "s3cret"); err != nil {
		t.Fatal(err)
	}
	for _, h := range u.apiAuth {
		if h != "Bearer s3cret" {
			t.Errorf("API request with Authorization %q", h)
		}
	}
	for _, h := range u.rawAuth {
		if h != "" {
			t.Errorf("raw request with Authorization %q", h)
		}
	}
	if len(u.apiAuth) == 0 || len(u.rawAuth) == 0 {
		t.Errorf("api %d, raw %d requests", len(u.apiAuth), len(u.rawAuth))
	}
}

func TestWatchRefuses(t *testing.T) {
	t.Run("a head that is not a commit", func(t *testing.T) {
		u := newWatchUpstream()
		u.heads[sonarrRp] = "<html>"
		if _, err := tryWatch(t, u, watchManifest(), nil, ""); err == nil || !strings.Contains(err.Error(), "is not a 40-hex commit") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a stem outside the watched form", func(t *testing.T) {
		u := newWatchUpstream()
		u.atHead(trashRepo, sonarrCF+"/Bad Name.json", cfJSON("b", "x", 1))
		if _, err := tryWatch(t, u, watchManifest(), nil, ""); err == nil || !strings.Contains(err.Error(), "outside the watched stem form") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a class with a bad pattern", func(t *testing.T) {
		m := watchManifest()
		m["watch"] = map[string]any{"classes": []any{map[string]any{"key": "a", "title": "a", "reason": "r", "stems": []string{"["}}}}
		if _, err := tryWatch(t, newWatchUpstream(), m, nil, ""); err == nil || !strings.Contains(err.Error(), "syntax error in pattern") {
			t.Errorf("got %v", err)
		}
	})
}

func TestRunWatchUsage(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(t.Context(), []string{"watch", "-out", "x"}, &bytes.Buffer{}, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
}

func TestReportBodyIsBounded(t *testing.T) {
	w := &watcher{repos: map[string]string{"trash": trashRepo}}
	r := &report{key: watchKey{key: "trash:cf:x", upstream: "trash"}, intro: "i", footer: "f", sections: []string{strings.Repeat("a", maxBodyRunes)}}
	body := r.body(w, pinP, commitH)
	if n := len([]rune(body)); n > maxBodyRunes+200 || !strings.HasSuffix(body, marker("trash:cf:x", pinP, commitH)+"\n") {
		t.Errorf("%d runes, tail %q", n, body[len(body)-120:])
	}
}

func TestLineDiff(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b []string
		want []string
	}{
		{"equal", []string{"x", "y"}, []string{"x", "y"}, nil},
		{"replace in the middle", []string{"a", "b", "c"}, []string{"a", "B", "c"}, []string{"- b", "+ B"}},
		{"insert", []string{"a", "c"}, []string{"a", "b", "c"}, []string{"+ b"}},
		{"delete and keep order", []string{"a", "b", "c", "d"}, []string{"a", "d", "e"}, []string{"- b", "- c", "+ e"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := lineDiff(c.a, c.b); !slices.Equal(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestCodeSpanQuotesUpstreamText(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain", "`plain`"},
		{"a`b", "``a`b``"},
		{"`x`", "`` `x` ``"},
		{"@user\nline", "`@user line`"},
		{"", "(empty)"},
	} {
		t.Run(c.in, func(t *testing.T) {
			if got := codeSpan(c.in); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
	t.Run("a block fence outruns the text", func(t *testing.T) {
		if got := codeBlock("diff", []string{"+ ````"}, 10); !strings.HasPrefix(got, "`````diff\n") {
			t.Errorf("got %q", got)
		}
	})
}

func TestSpecLines(t *testing.T) {
	doc := func(name string, specs ...string) *cfDoc {
		return parseCF([]byte(`{"name": "` + name + `", "trash_scores": {"default": 5}, "specifications": [` + strings.Join(specs, ",") + `]}`))
	}
	const (
		title  = `{"name": "T", "implementation": "ReleaseTitleSpecification", "negate": false, "required": true, "fields": {"value": "a"}}`
		retold = `{"name": "T", "implementation": "ReleaseTitleSpecification", "negate": false, "required": true, "fields": {"value": "a"}, "trash_regex": "x"}`
		negate = `{"name": "T", "implementation": "ReleaseTitleSpecification", "negate": true, "required": false, "fields": {"value": "a"}}`
		size   = `{"name": "S", "implementation": "SizeSpecification", "negate": false, "required": false, "fields": {"min": 1, "max": 2}}`
		size2  = `{"name": "S", "implementation": "SizeSpecification", "negate": false, "required": false, "fields": {"max": 3, "min": 1}}`
		linked = `{"name": "T", "implementation": "www.example.com", "negate": false, "required": true, "fields": {"value": "a"}}`
	)
	for _, c := range []struct {
		name      string
		prev, cur *cfDoc
		want      []string
	}{
		{"unchanged but for scores and unread keys", doc("X", title), doc("X", retold), nil},
		{"name", doc("X", title), doc("Y", title), []string{"Name changed from `X` to `Y`."}},
		{"flags", doc("X", title), doc("X", negate), []string{"`T` (`ReleaseTitleSpecification`, negated): no longer required; now negated."}},
		{"fields other than value", doc("X", size), doc("X", size2), []string{"`S` (`SizeSpecification`): fields changed from `{\"max\":2,\"min\":1}` to `{\"max\":3,\"min\":1}`."}},
		{"implementation", doc("X", title), doc("X", linked), []string{"`T` (`www.example.com`, required): implementation changed from `ReleaseTitleSpecification` to `www.example.com`."}},
		{"removed", doc("X", title, size), doc("X", title), []string{"`S` (`SizeSpecification`): removed."}},
		{"unreadable", doc("X", title), parseCF([]byte("{")), []string{"The file is not a readable custom format: `unexpected end of JSON input`."}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := specLines(c.prev, c.cur); !slices.Equal(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
