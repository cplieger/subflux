package testrig

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/cplieger/go/measure"
	"github.com/cplieger/subflux/internal/app"
	"go.yaml.in/yaml/v4"
)

// promqlWords are the PromQL functions and keywords the shipped rules may
// use; any other identifier in an expr must be a series. The grouping
// modifiers (by, without, on, ignoring, group_left, group_right) are stripped
// with their label lists before this check.
var promqlWords = []string{
	"sum", "max", "min", "avg", "count", "increase", "rate", "irate", "absent", "time",
	"and", "or", "unless", "bool", "offset", "histogram_quantile",
	"avg_over_time", "min_over_time", "max_over_time", "sum_over_time", "count_over_time",
	"quantile_over_time", "stddev_over_time", "stdvar_over_time", "last_over_time",
	"present_over_time", "absent_over_time", "mad_over_time",
}

// targetLabels are the labels Prometheus adds to every scraped series.
var targetLabels = []string{"job", "instance"}

var (
	promqlSelector = regexp.MustCompile(`([a-zA-Z_:][a-zA-Z0-9_:]*)\s*\{([^}]*)\}`)
	promqlMatcher  = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:=~|!~|!=|=)\s*"(?:[^"\\]|\\.)*"`)
	promqlGrouping = regexp.MustCompile(`\b(?:by|without|on|ignoring|group_left|group_right)\s*\(([^)]*)\)`)
	promqlStrip    = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|\{[^}]*\}|\[[^\]]*\]|\b(?:by|without|on|ignoring|group_left|group_right)\s*\([^)]*\)`)
	promqlIdent    = regexp.MustCompile(`\b[a-zA-Z_:][a-zA-Z0-9_:]*`)
	promqlLabel    = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)
)

// family is one series name the binary exposes: sampled when the rig gave it
// a series, so its label names are known.
type family struct {
	labels  []string
	sampled bool
}

// exportedFamilies reads the running binary's registry through
// app.Config.Gather, histogram and summary suffixes included, plus
// Prometheus' own up. A Save switching log_format raises restart_pending, so
// alerts_active carries a series.
func exportedFamilies(t *testing.T) map[string]family {
	t.Helper()
	gathers := make(chan func(context.Context) ([]measure.Family, error), 1)
	r := Start(t, Options{
		Config: []byte("server:\n  log_format: text\nlanguages:\n  default: [{tag: en}]\n"),
		Mutate: func(c *app.Config) {
			c.Gather = func(g func(context.Context) ([]measure.Family, error)) { gathers <- g }
		},
	})
	var gather func(context.Context) ([]measure.Family, error)
	select {
	case gather = <-gathers:
	default:
		t.Fatal("app.Run never handed over its Gather")
	}
	save(t, r, "server:\n  log_format: json\nlanguages:\n  default: [{tag: en}]\n")
	var fams []measure.Family
	if !eventually(func() bool {
		var err error
		if fams, err = gather(t.Context()); err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(fams, func(f measure.Family) bool { return f.Name == "subflux_alerts_active" })
		return i >= 0 && len(fams[i].Samples) > 0
	}) {
		t.Fatal("restart_pending never reached subflux_alerts_active")
	}
	out := map[string]family{"up": {labels: targetLabels, sampled: true}}
	for _, f := range fams {
		var labels []string
		for _, s := range f.Samples {
			for _, l := range s.Labels {
				if !slices.Contains(labels, l.Name) {
					labels = append(labels, l.Name)
				}
			}
		}
		fam := family{labels: labels, sampled: len(f.Samples) > 0}
		out[f.Name] = fam
		switch f.Type {
		case measure.TypeHistogram:
			out[f.Name+"_bucket"] = family{labels: append(slices.Clone(labels), "le"), sampled: fam.sampled}
			out[f.Name+"_sum"], out[f.Name+"_count"] = fam, fam
		case measure.TypeSummary:
			out[f.Name] = family{labels: append(slices.Clone(labels), "quantile"), sampled: fam.sampled}
			out[f.Name+"_sum"], out[f.Name+"_count"] = fam, fam
		}
	}
	return out
}

func TestAlertRulesNameExportedSeries(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(moduleRoot, "alerts", "promql.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Groups []struct {
			Rules []struct{ Alert, Expr string }
		}
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	fams := exportedFamilies(t)
	rules := 0
	for _, g := range file.Groups {
		for _, r := range g.Rules {
			rules++
			for _, problem := range ruleProblems(r.Expr, fams) {
				t.Errorf("%s %s", r.Alert, problem)
			}
		}
	}
	if rules == 0 {
		t.Fatal("alerts/promql.yaml holds no rule")
	}
}

// ruleProblems lists each series expr reads that fams lacks, and each label
// it matches or groups by that no read family carries. A family the rig left
// without a series has unknown labels, so it passes every label check.
func ruleProblems(expr string, fams map[string]family) []string {
	var problems []string
	var read []family
	for _, id := range promqlIdent.FindAllString(promqlStrip.ReplaceAllString(expr, " "), -1) {
		if slices.Contains(promqlWords, id) {
			continue
		}
		f, ok := fams[id]
		if !ok {
			problems = append(problems, "reads "+id+", which subflux does not export")
			continue
		}
		read = append(read, f)
	}
	carries := func(f family, label string) bool {
		return !f.sampled || slices.Contains(f.labels, label) || slices.Contains(targetLabels, label)
	}
	for _, sel := range promqlSelector.FindAllStringSubmatch(expr, -1) {
		f, ok := fams[sel[1]]
		if !ok {
			continue
		}
		for _, m := range promqlMatcher.FindAllStringSubmatch(sel[2], -1) {
			if !carries(f, m[1]) {
				problems = append(problems, "matches "+sel[1]+"{"+m[1]+"}, a label it does not carry")
			}
		}
	}
	for _, g := range promqlGrouping.FindAllStringSubmatch(expr, -1) {
		for _, label := range promqlLabel.FindAllString(g[1], -1) {
			if !slices.ContainsFunc(read, func(f family) bool { return carries(f, label) }) {
				problems = append(problems, "groups by "+label+", which no series it reads carries")
			}
		}
	}
	return problems
}

func TestRuleProblems(t *testing.T) {
	fams := map[string]family{
		"up":                    {labels: targetLabels, sampled: true},
		"subflux_alerts_active": {labels: []string{"kind", "id"}, sampled: true},
		"subflux_unsampled":     {},
	}
	for _, c := range []struct {
		expr string
		want int
	}{
		{`subflux_alerts_active{kind="store"} == 1`, 0},
		{`subflux_alerts_active{kind="a"} unless on(job, instance) subflux_alerts_active{kind="b"}`, 0},
		{`subflux_alerts_active{kind="a"} * on() group_left() up`, 0},
		{`max_over_time(subflux_alerts_active[5m]) > 0`, 0},
		{`sum by (kind) (subflux_unsampled{whatever="x"})`, 0},
		{`subflux_alerts_active{type="store"}`, 1},
		{`max by (provider) (subflux_alerts_active)`, 1},
		{`subflux_gone == 1`, 1},
	} {
		if got := ruleProblems(c.expr, fams); len(got) != c.want {
			t.Errorf("ruleProblems(%q) = %q, want %d problems", c.expr, got, c.want)
		}
	}
}
