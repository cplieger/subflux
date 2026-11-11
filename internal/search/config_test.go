package search_test

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/search"
)

func TestSchemaFlatKeys(t *testing.T) {
	keys := make([]string, len(search.Schema.Fields))
	for i, f := range search.Schema.Fields {
		keys[i] = f.Key
	}
	if want := []string{"min_fit", "upgrade_window_days", "scan_delay"}; !slices.Equal(keys, want) {
		t.Errorf("Schema keys %v, want %v", keys, want)
	}
}

func TestParseConfig(t *testing.T) {
	t.Run("an absent section is design 8's defaults", func(t *testing.T) {
		got := defaults(t)
		if got.MinFit != rank.Other || got.UpgradeWindowDays != 7 || got.ScanDelay != 5*time.Second || got.TargetMinFit != nil {
			t.Errorf("defaults %+v, want other, 7 days, 5 s and no per-target fit", got)
		}
	})
	t.Run("every key is read", func(t *testing.T) {
		got, errs := search.ParseConfig(yamlNode(t, "min_fit: same_source\nmin_fit_by_target: {PT-br: exact, en: same_release}\nupgrade_window_days: 0\nscan_delay: 10m\n"), policy(t))
		if errs != nil {
			t.Fatal(errs)
		}
		ptBR, _ := language.Parse("pt-BR")
		en, _ := language.Parse("en")
		if want := map[language.Tag]rank.Fit{ptBR: rank.Exact, en: rank.SameRelease}; !maps.Equal(got.TargetMinFit, want) {
			t.Errorf("TargetMinFit %v, want %v", got.TargetMinFit, want)
		}
		if got.MinFit != rank.SameSource || got.UpgradeWindowDays != 0 || got.ScanDelay != 10*time.Minute {
			t.Errorf("config %+v, want same_source, 0 days and 10 minutes", got)
		}
	})
	t.Run("every scalar it reads is marked plain", func(t *testing.T) {
		n := yamlNode(t, "min_fit: exact\nmin_fit_by_target: {en: other}\nupgrade_window_days: 3\nscan_delay: 6s\n")
		if _, errs := search.ParseConfig(n, policy(t)); errs != nil {
			t.Fatal(errs)
		}
		tags := []string{n.Content[1].Tag, n.Content[3].Content[1].Tag, n.Content[5].Tag, n.Content[7].Tag}
		if want := slices.Repeat([]string{formschema.PlainTag}, 4); !slices.Equal(tags, want) {
			t.Errorf("value tags %v, want every one %s", tags, formschema.PlainTag)
		}
	})
}

func TestParseConfigRefusals(t *testing.T) {
	cases := []struct{ name, text, path string }{
		{"a section that is no mapping", "[min_fit]", "search"},
		{"an unknown key", "min_score: 10", "search.min_score"},
		{"conflict as the minimum", "min_fit: conflict", "search.min_fit"},
		{"a minimum that is no fit", "min_fit: good", "search.min_fit"},
		{"conflict for a target", "min_fit_by_target: {en: conflict}", "search.min_fit_by_target.en"},
		{"a per-target value that is no fit", "min_fit_by_target: {en: [exact]}", "search.min_fit_by_target.en"},
		{"a fit for a tag no target names", "min_fit_by_target: {fr: exact}", "search.min_fit_by_target.fr"},
		{"a fit for a close variety of a target", "min_fit_by_target: {pt: exact}", "search.min_fit_by_target.pt"},
		{"a key that is no tag", "min_fit_by_target: {'not a tag': exact}", "search.min_fit_by_target.not a tag"},
		{"one target spelled twice", "min_fit_by_target: {en: exact, EN: other}", "search.min_fit_by_target.EN"},
		{"min_fit_by_target set twice", "min_fit_by_target: {en: exact}\nmin_fit_by_target: {en: other}", "search.min_fit_by_target"},
		{"min_fit_by_target that is no mapping", "min_fit_by_target: [en]", "search.min_fit_by_target"},
		{"an upgrade window over a year", "upgrade_window_days: 366", "search.upgrade_window_days"},
		{"a negative upgrade window", "upgrade_window_days: -1", "search.upgrade_window_days"},
		{"a scan delay under 5 s", "scan_delay: 4999ms", "search.scan_delay"},
		{"a scan delay over 10 minutes", "scan_delay: 10m1s", "search.scan_delay"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, errs := search.ParseConfig(yamlNode(t, c.text), policy(t))
			paths := make([]string, len(errs))
			for i, e := range errs {
				paths[i] = e.Path
			}
			if !slices.Equal(paths, []string{c.path}) || got.MinFit != 0 {
				t.Errorf("ParseConfig = %+v, %v; want the zero Config and one error at %s", got, errs, c.path)
			}
		})
	}
	t.Run("a null min_fit_by_target is absent", func(t *testing.T) {
		if got, errs := search.ParseConfig(yamlNode(t, "min_fit_by_target: ~"), policy(t)); errs != nil || got.TargetMinFit != nil {
			t.Errorf("ParseConfig = %+v, %v; want no per-target fit", got, errs)
		}
	})
}
